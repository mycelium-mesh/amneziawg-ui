package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/netutil"
	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// Servers returns all servers with their current live status.
//
// Status is filled in from the kernel rather than read out of the config: the
// stored field is only the last thing this process observed, and the
// interface can go up or down without it. The lookups happen between the two
// locks, not under one - this is what every dashboard poll calls, and holding
// the write lock across a shell command per server would stall every other
// request for as long as that takes.
func (m *Manager) Servers() []api.Server {
	servers := m.copyServers()

	for i := range servers {
		servers[i].Status = m.serverStatus(servers[i].Interface)
	}

	m.storeServerStatuses(servers)
	return servers
}

// storeServerStatuses keeps the config's echo of the status current, in
// memory only. It is not persisted: the kernel is asked on every read anyway,
// so writing an observation to disk would rewrite a file full of private keys
// on a plain dashboard poll and still tell the next reader nothing it can
// trust.
func (m *Manager) storeServerStatuses(observed []api.Server) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range observed {
		if srv := m.findServer(observed[i].ID); srv != nil {
			srv.Status = observed[i].Status
		}
	}
}

// ServerInfo is the detailed view of one server: its fields, its clients and
// the head of its .conf.
func (m *Manager) ServerInfo(id string) (api.ServerInfo, error) {
	srv, ok := m.Server(id)
	if !ok {
		return api.ServerInfo{}, serverNotFound(id)
	}

	status := m.serverStatus(srv.Interface)
	clients := m.Clients(id)

	preview := ""
	if data, err := os.ReadFile(srv.ConfigPath); err == nil {
		lines := strings.Split(string(data), "\n")
		if len(lines) > 10 {
			lines = lines[:10]
		}
		preview = strings.Join(lines, "\n")
	}

	return api.ServerInfo{
		ID:                 srv.ID,
		Name:               srv.Name,
		Protocol:           srv.Protocol,
		Port:               srv.Port,
		Status:             status,
		Interface:          srv.Interface,
		ConfigPath:         srv.ConfigPath,
		PublicIP:           srv.PublicIP,
		ServerIP:           srv.ServerIP,
		Subnet:             srv.Subnet,
		MTU:                srv.MTU,
		ObfuscationEnabled: srv.ObfuscationEnabled,
		ObfuscationParams:  srv.ObfuscationParams,
		ClientsCount:       len(clients),
		Clients:            clients,
		CreatedAt:          srv.CreatedAt,
		ConfigPreview:      preview,
		PublicKey:          srv.ServerPublicKey,
		DNS:                srv.DNS,
		DefaultISettings:   wgconf.DefaultISettings(),
	}, nil
}

// ServerConfig is a server's .conf as it sits on disk, with its identity.
func (m *Manager) ServerConfig(id string) (api.ServerConfig, error) {
	srv, ok := m.Server(id)
	if !ok {
		return api.ServerConfig{}, serverNotFound(id)
	}
	data, err := os.ReadFile(srv.ConfigPath)
	if err != nil {
		return api.ServerConfig{}, fmt.Errorf("config file %s: %w", srv.ConfigPath, ErrNotFound)
	}
	return api.ServerConfig{
		ServerID:      id,
		ServerName:    srv.Name,
		ConfigPath:    srv.ConfigPath,
		ConfigContent: string(data),
		Interface:     srv.Interface,
		PublicKey:     srv.ServerPublicKey,
	}, nil
}

// CreateServer creates a new WireGuard server configuration.
func (m *Manager) CreateServer(req api.CreateServerRequest) (*api.Server, error) {
	name := wgconf.SanitizeName(req.Name, "New Server")
	port := req.Port
	if port == 0 {
		port = m.settings.DefaultPort
	}
	if port < api.MinPort || port > api.MaxPort {
		return nil, fmt.Errorf("port must be between %d and %d, got %d: %w", api.MinPort, api.MaxPort, port, ErrInvalid)
	}
	if holder, ok := m.portInUse(port); ok {
		return nil, fmt.Errorf("port %d is already used by %s: %w", port, holder, ErrConflict)
	}
	subnet := strings.TrimSpace(req.Subnet)
	if subnet == "" {
		subnet = m.settings.DefaultSubnet
	}
	parsed, err := api.ParseSubnet(subnet)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	subnet = parsed.String()
	if holder, ok := m.subnetInUse(subnet); ok {
		return nil, fmt.Errorf("subnet %s overlaps %s: %w", subnet, holder, ErrConflict)
	}
	mtu := req.MTU
	if mtu == 0 {
		mtu = m.settings.DefaultMTU
	}
	if mtu < api.MinMTU || mtu > api.MaxMTU {
		return nil, fmt.Errorf("MTU must be between %d and %d, got %d: %w", api.MinMTU, api.MaxMTU, mtu, ErrInvalid)
	}

	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		endpoint = m.PublicIP()
	}

	dnsServers := parseDNS(req.DNS)
	if len(dnsServers) == 0 {
		dnsServers = m.settings.DNSServers
	}
	for _, dns := range dnsServers {
		if !netutil.IsIPv4(dns) {
			return nil, fmt.Errorf("invalid DNS server IP %s: %w", dns, ErrInvalid)
		}
	}

	enableObfuscation := m.settings.EnableObfuscation
	if req.Obfuscation != nil {
		enableObfuscation = *req.Obfuscation
	}

	autoStart := m.settings.AutoStart
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	}

	// AmneziaWG 1.0/1.5/2.0-only modes are no longer supported: enabling
	// obfuscation always means the full AmneziaWG 3.x parameter set,
	// including mandatory header protection.
	var obfParams *api.ObfuscationParams
	if enableObfuscation {
		if req.ObfuscationParams != nil {
			obfParams = req.ObfuscationParams
		} else {
			p := generateObfuscationParams(mtu)
			obfParams = &p
		}
		if obfParams.HeaderProtectionKey == "" {
			obfParams.HeaderProtectionKey = awg.RandomKey()
		}
		if err := validateObfuscationParams(obfParams, mtu); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	serverID := uuid.New().String()[:6]
	ifaceName := "wg-" + serverID
	configPath := filepath.Join(m.settings.WireguardConfigDir, ifaceName+".conf")

	keys := m.tools.GenerateKeyPair()

	network, prefix := netutil.SplitCIDR(subnet)
	serverIP := netutil.ServerIP(network)

	err = wgconf.WriteServerConf(configPath, wgconf.ServerInterface{
		PrivateKey:  keys.Private,
		Address:     serverIP + "/" + prefix,
		ListenPort:  port,
		MTU:         mtu,
		Obfuscation: obfParams,
	})
	if err != nil {
		return nil, err
	}

	srv := api.Server{
		ID:                 serverID,
		Name:               name,
		Protocol:           "wireguard",
		Port:               port,
		Status:             "stopped",
		Interface:          ifaceName,
		ConfigPath:         configPath,
		ServerPublicKey:    keys.Public,
		ServerPrivateKey:   keys.Private,
		Subnet:             subnet,
		ServerIP:           serverIP,
		MTU:                mtu,
		PublicIP:           endpoint,
		Endpoint:           endpoint,
		ObfuscationEnabled: enableObfuscation,
		ObfuscationParams:  obfParams,
		AutoStart:          autoStart,
		DNS:                dnsServers,
		Clients:            []api.Client{},
		UnboundNATIPs:      []string{},
		CreatedAt:          float64(time.Now().Unix()),
	}

	m.mu.Lock()
	m.cfg.Servers = append(m.cfg.Servers, srv)
	m.mu.Unlock()
	m.saveOrLog("new server")

	if autoStart {
		fmt.Printf("Auto-starting new server: %s\n", name)
		if err := m.StartServer(serverID); err != nil {
			fmt.Printf("Auto-start failed for %s: %v\n", name, err)
		}
	}

	return &srv, nil
}

// parseDNS reads the DNS field of a create request, which the form sends as
// a comma separated string and an API caller may send as a list.
func parseDNS(raw interface{}) []string {
	var out []string
	switch v := raw.(type) {
	case string:
		for _, d := range strings.Split(v, ",") {
			if s := strings.TrimSpace(d); s != "" {
				out = append(out, s)
			}
		}
	case []interface{}:
		for _, d := range v {
			if s, ok := d.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// portInUse names whatever already holds this port - another server, or the
// web UI's own listener - if anything does. Two interfaces cannot share a
// port: the second one comes up only to have awg-quick fail on bind, long
// after the create request was answered, so the clash is worth catching up
// front. The panel's port is reserved along with them: it is TCP rather than
// UDP and would technically coexist, but a deployment that publishes one
// number for two different things is a trap, not a feature.
func (m *Manager) portInUse(port int) (string, bool) {
	if m.settings.WebUIPort == port {
		return "the web UI", true
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.cfg.Servers {
		if m.cfg.Servers[i].Port == port {
			return fmt.Sprintf("server %q", m.cfg.Servers[i].Name), true
		}
	}
	return "", false
}

// subnetInUse names the server whose subnet shares addresses with this one,
// if any does. Overlapping subnets would route one client address to two
// interfaces, and the kernel settles that by sending it to only one of them.
func (m *Manager) subnetInUse(subnet string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.cfg.Servers {
		if api.SubnetsOverlap(subnet, m.cfg.Servers[i].Subnet) {
			return fmt.Sprintf("%s of server %q", m.cfg.Servers[i].Subnet, m.cfg.Servers[i].Name), true
		}
	}
	return "", false
}

// DeleteServer stops and removes a server and its clients.
func (m *Manager) DeleteServer(serverID string) error {
	srv, ok := m.Server(serverID)
	if !ok {
		return serverNotFound(serverID)
	}

	// The stored Status is only the last observation; the interface may have
	// been brought up or down since - including by a restart of this process
	// - and deleting a server whose interface is still up would leave it and
	// its iptables rules behind.
	if m.serverStatus(srv.Interface) == "running" {
		if err := m.StopServer(serverID); err != nil {
			return fmt.Errorf("stopping the server first: %w", err)
		}
	}

	if !m.removeServerLocked(serverID) {
		return serverNotFound(serverID)
	}
	m.forgetServerStatus(srv.Interface)
	m.saveOrLog("server removal")
	return nil
}

// removeServerLocked removes the server config file and all associated data.
func (m *Manager) removeServerLocked(serverID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, s := range m.cfg.Servers {
		if s.ID == serverID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}

	os.Remove(m.cfg.Servers[idx].ConfigPath)

	// The server owns its clients, so dropping it drops them with it.
	m.cfg.Servers = append(m.cfg.Servers[:idx], m.cfg.Servers[idx+1:]...)
	return true
}

// StartServer brings up a WireGuard interface.
func (m *Manager) StartServer(serverID string) error {
	srv, ok := m.Server(serverID)
	if !ok {
		return serverNotFound(serverID)
	}

	// awg-quick has nothing to do on an interface that is already up: it
	// fails with "File exists", which is a conflict, not a broken server.
	if m.serverStatus(srv.Interface) == "running" {
		return fmt.Errorf("server %s is already running: %w", serverID, ErrConflict)
	}

	if err := m.tools.QuickUp(srv.Interface); err != nil {
		fmt.Printf("Failed to start server %s: %v\n", srv.Name, err)
		return err
	}

	m.tools.SetupIPTables(srv.Interface, srv.Subnet)
	m.setServerStatus(serverID, "running")
	m.noteServerStatus(srv.Interface, "running")
	m.saveOrLog("server start")

	fmt.Printf("Server %s started\n", srv.Name)
	return nil
}

// StopServer tears down a WireGuard interface.
func (m *Manager) StopServer(serverID string) error {
	srv, ok := m.Server(serverID)
	if !ok {
		return serverNotFound(serverID)
	}

	if m.serverStatus(srv.Interface) != "running" {
		return fmt.Errorf("server %s is not running: %w", serverID, ErrConflict)
	}

	m.tools.CleanupIPTables(srv.Interface, srv.Subnet)

	if err := m.tools.QuickDown(srv.Interface); err != nil {
		fmt.Printf("Failed to stop server %s: %v\n", srv.Name, err)
		return err
	}

	m.setServerStatus(serverID, "stopped")
	m.noteServerStatus(srv.Interface, "stopped")
	m.saveOrLog("server stop")

	fmt.Printf("Server %s stopped\n", srv.Name)
	return nil
}

// setServerStatus locks and updates the status field of a server.
func (m *Manager) setServerStatus(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if srv := m.findServer(id); srv != nil {
		srv.Status = status
	}
}

// autoStartServers brings up every server marked for it whose interface is
// not already up - after a restart of this process the kernel may still
// have them.
func (m *Manager) autoStartServers() {
	fmt.Println("Checking for existing servers to auto-start...")
	for _, srv := range m.copyServers() {
		if !srv.AutoStart {
			continue
		}
		if _, err := os.Stat(srv.ConfigPath); err != nil {
			continue
		}
		if m.serverStatus(srv.Interface) != "stopped" {
			continue
		}
		fmt.Printf("Auto-starting server: %s\n", srv.Name)
		if err := m.StartServer(srv.ID); err != nil {
			fmt.Printf("Auto-start failed for %s: %v\n", srv.Name, err)
		}
	}
}
