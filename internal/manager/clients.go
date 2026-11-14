package manager

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"amneziawg-web-ui/internal/amnezialink"
	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/netutil"
	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// Clients returns detached copies of every client, optionally narrowed to
// one server.
func (m *Manager) Clients(serverID string) []api.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := m.cfg.Servers
	if serverID != "" {
		srv := m.findServer(serverID)
		if srv == nil {
			return []api.Client{}
		}
		servers = []api.Server{*srv}
	}

	clients := []api.Client{}
	for i := range servers {
		for j := range servers[i].Clients {
			c := cloneClient(&servers[i].Clients[j])
			// Fields a config written before they existed may still be
			// missing; the UI expects both to be set.
			if c.Status == "" {
				c.Status = "active"
			}
			if c.ISettings == nil {
				c.ISettings = map[string]string{}
			}
			clients = append(clients, c)
		}
	}
	return clients
}

// ClientConfig renders the .conf a client imports, with or without the
// header comments.
func (m *Manager) ClientConfig(serverID, clientID string, includeComments bool) (string, error) {
	srv, client, err := m.serverAndClient(serverID, clientID)
	if err != nil {
		return "", err
	}
	return wgconf.ClientConf(&srv, &client, m.endpointFor(&srv), includeComments), nil
}

// ClientConfigFile is one rendered client .conf: the name it goes by inside
// an archive and the content that goes with it.
type ClientConfigFile struct {
	Name    string
	Content string
}

// ClientConfigs renders the .conf of every client of one server, suspended
// ones included: a suspended client's config is still valid, it only stops
// being served until the client is activated again.
//
// The whole set comes out of one snapshot of the server, so the configs in
// an archive agree with each other, and each of them is rendered by the same
// wgconf.ClientConf a single-client download uses, so the two cannot drift
// apart.
func (m *Manager) ClientConfigs(serverID string) ([]ClientConfigFile, error) {
	srv, ok := m.serverSnapshot(serverID)
	if !ok {
		return nil, serverNotFound(serverID)
	}

	endpoint := m.endpointFor(&srv)

	files := make([]ClientConfigFile, 0, len(srv.Clients))
	taken := make(map[string]bool, len(srv.Clients))
	for i := range srv.Clients {
		client := &srv.Clients[i]
		files = append(files, ClientConfigFile{
			Name:    clientConfigFileName(*client, taken),
			Content: wgconf.ClientConf(&srv, client, endpoint, true),
		})
	}
	return files, nil
}

// clientConfigFileName is the name one client's .conf goes by inside the
// archive. Two clients may share a name, so the second one carries the
// client's ID, which is unique.
func clientConfigFileName(client api.Client, taken map[string]bool) string {
	base := archiveEntryBase(client.Name)

	name := base + ".conf"
	if taken[name] {
		name = fmt.Sprintf("%s_%s.conf", base, client.ID)
		for n := 2; taken[name]; n++ {
			name = fmt.Sprintf("%s_%s_%d.conf", base, client.ID, n)
		}
	}
	taken[name] = true
	return name
}

// archiveEntryBase is the file name a client's .conf is built from. The
// name is sanitised on the way in, but an archive is unpacked by tools that
// take its paths literally: a client named "a/b" would write outside the
// directory it is unpacked into, so the separators are flattened here.
func archiveEntryBase(name string) string {
	base := strings.NewReplacer("/", "_", "\\", "_").Replace(wgconf.SanitizeName(name, "client"))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." {
		return "client"
	}
	return base
}

// ClientLink renders the vpn:// link for a client.
func (m *Manager) ClientLink(serverID, clientID string) (string, error) {
	srv, client, err := m.serverAndClient(serverID, clientID)
	if err != nil {
		return "", err
	}
	return amnezialink.Build(&srv, &client, m.endpointFor(&srv))
}

// serverAndClient snapshots both halves of a client export.
func (m *Manager) serverAndClient(serverID, clientID string) (api.Server, api.Client, error) {
	srv, ok := m.Server(serverID)
	if !ok {
		return api.Server{}, api.Client{}, serverNotFound(serverID)
	}
	client, ok := m.Client(serverID, clientID)
	if !ok {
		return api.Server{}, api.Client{}, clientNotFound(clientID)
	}
	return srv, client, nil
}

// clientConfigOf renders the config for a client the caller already holds a
// copy of - the one an update just produced.
func (m *Manager) clientConfigOf(serverID string, client *api.Client) string {
	srv, ok := m.Server(serverID)
	if !ok {
		return ""
	}
	return wgconf.ClientConf(&srv, client, m.endpointFor(&srv), true)
}

// AddClient adds a WireGuard peer to a server and returns the client with
// its rendered config.
func (m *Manager) AddClient(serverID string, req api.AddClientRequest) (*api.Client, string, error) {
	// The name lands in a .conf comment, in the peer marker and in a
	// Content-Disposition filename, so it is cleaned before anything stores
	// it - not at each of those points.
	name := wgconf.SanitizeName(req.Name, "client")

	// Checked before any key is generated: a malformed signature packet would
	// otherwise reach the client's .conf and only surface there, as a tunnel
	// that will not start.
	if req.ApplyISettings {
		if err := validateISettings(req.ISettings); err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	// Key generation shells out to awg three times. Doing that under the
	// write lock would stall every other request, including plain reads, for
	// the duration - and the keys do not depend on any config state.
	keys := m.tools.GenerateKeyPair()
	psk := m.tools.GeneratePresharedKey()

	client, ifaceName, err := m.addClientLocked(serverID, name, req, keys, psk)
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("new client")
	m.syncLiveConfig(ifaceName)

	fmt.Printf("Client %s added with AllowedIPs: %s\n", name, client.AllowedIPs)
	return client, m.clientConfigOf(serverID, client), nil
}

// addClientLocked builds the client, appends it to the server's peer config
// file and to the in-memory config, all under a single write lock.
func (m *Manager) addClientLocked(serverID, name string, req api.AddClientRequest, keys awg.KeyPair, psk string) (client *api.Client, ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, "", serverNotFound(serverID)
	}

	clientIP := m.nextClientIP(srv)
	if clientIP == "" {
		return nil, "", fmt.Errorf("subnet is full: %w", ErrConflict)
	}

	iSettings := map[string]string{}
	if req.ApplyISettings {
		iSettings = wgconf.MergeISettings(req.ISettings)
	}

	newClient := api.Client{
		ID:                 uuid.New().String()[:6],
		Name:               name,
		ServerID:           serverID,
		ServerName:         srv.Name,
		Status:             "active",
		CreatedAt:          float64(time.Now().Unix()),
		ClientPrivateKey:   keys.Private,
		ClientPublicKey:    keys.Public,
		PresharedKey:       psk,
		ClientIP:           clientIP,
		ObfuscationEnabled: srv.ObfuscationEnabled,
		ObfuscationParams:  cloneObfuscationParams(srv.ObfuscationParams),
		ApplyISettings:     req.ApplyISettings,
		ISettings:          iSettings,
		AllowedIPs:         wgconf.AllowedIPsOrDefault(req.AllowedIPs),
	}

	// The server side routes only the client's own address to it, whatever
	// the client itself sends through the tunnel.
	if err := wgconf.AppendPeer(srv.ConfigPath, &newClient, clientIP+"/32"); err != nil {
		return nil, "", fmt.Errorf("failed to write client to server config: %w", err)
	}

	srv.Clients = append(srv.Clients, newClient)

	// newClient is already a detached copy; the caller gets it rather than a
	// pointer into srv.Clients, which the next append could move.
	return &newClient, srv.Interface, nil
}

// nextClientIP hands out an address in the server's subnet, reusing one a
// deleted client gave back before allocating a fresh one. Caller holds mu.
func (m *Manager) nextClientIP(srv *api.Server) string {
	if len(srv.UnboundNATIPs) > 0 {
		ip := srv.UnboundNATIPs[0]
		srv.UnboundNATIPs = srv.UnboundNATIPs[1:]
		return ip
	}

	used := map[string]bool{srv.ServerIP: true}
	for _, c := range srv.Clients {
		used[c.ClientIP] = true
	}
	return netutil.NextFreeIP(srv.Subnet, used)
}

// DeleteClient removes a peer from a server.
func (m *Manager) DeleteClient(serverID, clientID string) error {
	serverName, clientCopy, ifaceName, err := m.deleteClientLocked(serverID, clientID)
	if err != nil {
		return err
	}

	m.saveOrLog("client removal")
	m.syncLiveConfig(ifaceName)

	fmt.Printf("Client %s:%s removed\n", serverName, clientCopy.Name)
	return nil
}

// deleteClientLocked drops the client from the server's peer list and from
// its .conf under a single write lock, so the file and the config cannot
// disagree about which peers exist. It returns copies only - no pointer into
// the config outlives the lock.
func (m *Manager) deleteClientLocked(serverID, clientID string) (serverName string, clientCopy api.Client, ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, target := m.findClient(serverID, clientID)
	if srv == nil {
		return "", api.Client{}, "", serverNotFound(serverID)
	}
	if target == nil {
		return "", api.Client{}, "", clientNotFound(clientID)
	}

	clientCopy = cloneClient(target)
	if _, err := wgconf.RemovePeer(srv.ConfigPath, target); err != nil {
		return "", api.Client{}, "", fmt.Errorf("removing the peer from %s: %w", srv.ConfigPath, err)
	}

	newClients := make([]api.Client, 0, len(srv.Clients)-1)
	for _, c := range srv.Clients {
		if c.ID != clientID {
			newClients = append(newClients, c)
		}
	}
	srv.Clients = newClients
	srv.UnboundNATIPs = append(srv.UnboundNATIPs, clientCopy.ClientIP)

	return srv.Name, clientCopy, srv.Interface, nil
}

// UpdateClientAllowedIPs changes the AllowedIPs field for a client.
func (m *Manager) UpdateClientAllowedIPs(serverID, clientID, allowedIPs string) (*api.Client, string, error) {
	clientCopy, err := m.updateClientLocked(serverID, clientID, func(client *api.Client) {
		client.AllowedIPs = wgconf.AllowedIPsOrDefault(allowedIPs)
	})
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client update")
	return clientCopy, m.clientConfigOf(serverID, clientCopy), nil
}

// UpdateClientISettings updates the I1-I5 settings for a client.
func (m *Manager) UpdateClientISettings(serverID, clientID string, applyI *bool, iSettings map[string]string) (*api.Client, string, error) {
	if err := validateISettings(iSettings); err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	clientCopy, err := m.updateClientLocked(serverID, clientID, func(client *api.Client) {
		if applyI != nil {
			client.ApplyISettings = *applyI
		}
		if iSettings == nil {
			return
		}
		if client.ApplyISettings {
			client.ISettings = wgconf.MergeISettings(client.ISettings, iSettings)
		} else {
			client.ISettings = map[string]string{}
		}
	})
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client update")
	return clientCopy, m.clientConfigOf(serverID, clientCopy), nil
}

// updateClientLocked applies edit to the client under a single write lock
// and returns a copy of the result.
func (m *Manager) updateClientLocked(serverID, clientID string, edit func(*api.Client)) (*api.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return nil, serverNotFound(serverID)
	}
	if client == nil {
		return nil, clientNotFound(clientID)
	}

	edit(client)

	clientCopy := cloneClient(client)
	return &clientCopy, nil
}
