package internal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager orchestrates all AmneziaWG operations.
type Manager struct {
	Config *AppConfig
	hub    HubBroadcaster

	// publicIP is re-detected on demand from an HTTP handler while other
	// requests are generating configs from it, so it lives behind mu rather
	// than as a bare exported field.
	publicIP string

	// Traffic update interval in seconds
	TrafficUpdateInterval int
	SuspendUpdateInterval int

	// Environment-driven defaults
	WebUIPort         string
	AutoStart         bool
	DefaultMTU        int
	DefaultSubnet     string
	DefaultPort       int
	DNSServers        []string
	EnableObfuscation bool

	// configFile is where the config is loaded from and saved to. Empty means
	// ConfigFile; tests point it at a temp file so they never touch /etc.
	configFile string

	mu sync.RWMutex

	// saveMu orders writes to the config file; see SaveConfig.
	saveMu sync.Mutex

	// statuses caches observed interface states, keyed by interface name;
	// see serverStatus. Guarded by statusMu, not mu, so a status lookup
	// never waits on a config write.
	statuses map[string]statusObservation
	statusMu sync.Mutex
}

// configPath returns the file the config lives in.
func (m *Manager) configPath() string {
	if m.configFile != "" {
		return m.configFile
	}
	return ConfigFile
}

// HubBroadcaster is a minimal interface the Manager uses to send events.
type HubBroadcaster interface {
	BroadcastServerStatus(serverID, status string)
}

func getenv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// NewManager creates and initialises a Manager from environment variables.
func NewManager() *Manager {
	m := &Manager{
		TrafficUpdateInterval: 5,
		SuspendUpdateInterval: 60,
		EnableObfuscation:     true,
		statuses:              map[string]statusObservation{},
	}

	m.WebUIPort = getenv("WEB_UI_PORT", "54845")
	m.AutoStart = strings.EqualFold(getenv("AUTO_START_SERVERS", "true"), "true")
	m.DefaultMTU = atoiDefault(getenv("DEFAULT_MTU", "1280"), 1280)
	m.DefaultSubnet = getenv("DEFAULT_SUBNET", "10.0.0.0/24")
	m.DefaultPort = atoiDefault(getenv("DEFAULT_PORT", "54844"), 54844)

	defaultDNS := getenv("DEFAULT_DNS", "8.8.8.8,1.1.1.1")
	for _, dns := range strings.Split(defaultDNS, ",") {
		if s := strings.TrimSpace(dns); s != "" {
			m.DNSServers = append(m.DNSServers, s)
		}
	}

	m.Config = m.loadConfig()
	m.ensureDirectories()
	m.runMigrations()
	m.setPublicIP(m.detectPublicIP())

	if m.AutoStart {
		m.autoStartServers()
	}

	go m.startSuspensionChecker()

	fmt.Printf("=== Environment Configuration ===\n")
	fmt.Printf("WEB_UI_PORT: %s\n", m.WebUIPort)
	fmt.Printf("AUTO_START: %v\n", m.AutoStart)
	fmt.Printf("DEFAULT_MTU: %d\n", m.DefaultMTU)
	fmt.Printf("DEFAULT_SUBNET: %s\n", m.DefaultSubnet)
	fmt.Printf("DEFAULT_PORT: %d\n", m.DefaultPort)
	fmt.Printf("DNS_SERVERS: %v\n", m.DNSServers)
	fmt.Printf("Detected public IP: %s\n", m.PublicIP())

	return m
}

// SetHub injects the WebSocket hub so the manager can broadcast events.
func (m *Manager) SetHub(h HubBroadcaster) {
	m.hub = h
}

func (m *Manager) ensureDirectories() {
	os.MkdirAll(ConfigDir, 0o755)
	os.MkdirAll(WireguardConfigDir, 0o755)
	os.MkdirAll("/var/log/amnezia", 0o755)
}

func (m *Manager) detectPublicIP() string {
	services := []string{
		"http://ifconfig.me",
		"https://api.ipify.org",
		"https://ident.me",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, svc := range services {
		resp, err := client.Get(svc)
		if err != nil {
			continue
		}
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		ip := strings.TrimSpace(string(buf[:n]))
		if isValidIPv4(ip) {
			return ip
		}
	}
	// Fallback: local routing table
	out, err := execCommand("ip route get 1 | awk '{print $7}' | head -1")
	if err == nil && isValidIPv4(out) {
		return out
	}
	return "YOUR_SERVER_IP"
}

func isValidIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func (m *Manager) autoStartServers() {
	fmt.Println("Checking for existing servers to auto-start...")
	for i := range m.Config.Servers {
		srv := &m.Config.Servers[i]
		if _, err := os.Stat(srv.ConfigPath); err == nil {
			if m.GetServerStatus(srv.ID) == "stopped" && srv.AutoStart {
				fmt.Printf("Auto-starting server: %s\n", srv.Name)
				if err := m.StartServer(srv.ID); err != nil {
					fmt.Printf("Auto-start failed for %s: %v\n", srv.Name, err)
				}
			}
		}
	}
}

func (m *Manager) loadConfig() *AppConfig {
	data, err := os.ReadFile(m.configPath())
	if err == nil {
		var cfg AppConfig
		if err = json.Unmarshal(data, &cfg); err == nil {
			for i := range cfg.Servers {
				if cfg.Servers[i].Clients == nil {
					cfg.Servers[i].Clients = []Client{}
				}
				if cfg.Servers[i].UnboundNATIPs == nil {
					cfg.Servers[i].UnboundNATIPs = []string{}
				}
			}
			return &cfg
		} else {
			fmt.Printf("Error unmarshaling config: %v\n", err)
		}
	}
	return &AppConfig{Servers: []Server{}}
}

// SaveConfig writes the config to disk. It must not be called while m.mu is
// held - it takes the read lock itself to snapshot the config.
//
// saveMu serialises the whole snapshot-and-write, so two concurrent saves
// cannot reorder into the older one landing last.
func (m *Manager) SaveConfig() error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.RLock()
	data, err := json.MarshalIndent(m.Config, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := atomicWriteFile(m.configPath(), data, 0o600); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}

// saveOrLog saves the config where the caller has no way to report a failure.
// what names the operation whose result was about to be persisted, so the log
// line says which change is at risk of being lost on the next restart.
func (m *Manager) saveOrLog(what string) {
	if err := m.SaveConfig(); err != nil {
		fmt.Printf("Failed to persist %s: %v\n", what, err)
	}
}

// atomicWriteFile writes data to a temporary file in the target directory and
// renames it over path. A plain write truncates the file first, so an
// interrupted one leaves the config - which holds every server and client
// private key - truncated or half-written, with no copy left to recover from.
// The rename is atomic, so a reader sees either the old file or the new one.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Durability before visibility: rename can otherwise become visible while
	// the contents are still only in the page cache.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	// Persist the rename itself. Nothing depends on the result - the data is
	// already durable - so a directory that cannot be opened is not an error.
	if d, err := os.Open(dir); err == nil {
		d.Sync() //nolint:errcheck
		d.Close()
	}
	return nil
}

func execCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) generateWireguardKeys() (privKey, pubKey string, err error) {
	priv, err := execCommand("awg genkey")
	if err != nil {
		return "", "", err
	}
	pub, err := execCommand(fmt.Sprintf("echo '%s' | awg pubkey", priv))
	if err != nil {
		return "", "", err
	}
	return priv, pub, nil
}

func (m *Manager) generatePresharedKey() string {
	key, err := execCommand("awg genpsk")
	if err != nil {
		b := make([]byte, 32)
		rand.Read(b) //nolint:gosec
		return base64.StdEncoding.EncodeToString(b)
	}
	return key
}

func randomBase64Key() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:gosec
	return base64.StdEncoding.EncodeToString(b)
}

// awg3MinPadding is the minimum value AmneziaWG 3.0 requires for S1-S4 when
// header protection is enabled: the cipher's 12-byte nonce is taken from the
// start of the padding, so anything smaller can't fit it.
const awg3MinPadding = 12

// generateObfuscationParams generates a full set of AmneziaWG 3.1 obfuscation
// parameters, including a header protection key. Obfuscation in this app is
// always AmneziaWG 3.x - there's no more separate 1.0/1.5/2.0 mode.
func (m *Manager) generateObfuscationParams(mtu int) ObfuscationParams {
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	s1Max := mtu - 148
	if s1Max > 150 {
		s1Max = 150
	}
	s1 := rng.Intn(s1Max-15+1) + 15

	s2Max := mtu - 92
	if s2Max > 150 {
		s2Max = 150
	}
	var s2Candidates []int
	for s := 15; s <= s2Max; s++ {
		if s != s1+56 {
			s2Candidates = append(s2Candidates, s)
		}
	}
	s2 := s2Candidates[rng.Intn(len(s2Candidates))]

	jmin := rng.Intn(mtu-2-4+1) + 4
	jmax := jmin + 1 + rng.Intn(mtu-jmin)

	return ObfuscationParams{
		Jc:                  rng.Intn(9) + 4,
		Jmin:                jmin,
		Jmax:                jmax,
		S1:                  s1,
		S2:                  s2,
		S3:                  rng.Intn(256-awg3MinPadding+1) + awg3MinPadding,
		S4:                  rng.Intn(32-awg3MinPadding+1) + awg3MinPadding,
		H1:                  rng.Intn(90001) + 10000,
		H2:                  rng.Intn(100001) + 100000,
		H3:                  rng.Intn(100001) + 200000,
		H4:                  rng.Intn(100001) + 300000,
		MTU:                 mtu,
		HeaderProtectionKey: randomBase64Key(),
		RandomTrailers:      true,
		DisableCookies:      true,
	}
}

// validateObfuscationParams checks the AmneziaWG 3.0 header protection
// requirement: S1-S4 must all be at least awg3MinPadding, since the cipher's
// nonce is taken from the start of that padding. It also validates the
// format of the optional client-side range tuning knobs, if any were
// provided.
func validateObfuscationParams(p *ObfuscationParams) error {
	if p == nil {
		return fmt.Errorf("obfuscation parameters are required")
	}
	for name, v := range map[string]int{"S1": p.S1, "S2": p.S2, "S3": p.S3, "S4": p.S4} {
		if v < awg3MinPadding {
			return fmt.Errorf("%s must be at least %d for AmneziaWG 3.0 header protection, got %d", name, awg3MinPadding, v)
		}
	}
	for name, v := range map[string]string{
		"ContentPaddingAddition": p.ContentPaddingAddition,
		"RekeyAfterTime":         p.RekeyAfterTime,
		"RekeyTimeout":           p.RekeyTimeout,
		"RejectAfterTime":        p.RejectAfterTime,
		"KeepaliveTimeout":       p.KeepaliveTimeout,
		"MaxHandshakeAttempts":   p.MaxHandshakeAttempts,
		"PersistentKeepalive":    p.PersistentKeepalive,
	} {
		if v != "" && !isValidUintRange(v) {
			return fmt.Errorf("%s must be an integer or an \"a-b\" range, got %q", name, v)
		}
	}
	return nil
}

// isValidUintRange reports whether s is a plain non-negative integer or an
// "a-b" range of them, the format amneziawg-tools expects for its AWG 3.0
// range-typed config keys.
func isValidUintRange(s string) bool {
	return uintRangeRe.MatchString(s)
}

var uintRangeRe = regexp.MustCompile(`^\d+(-\d+)?$`)

// writeIfSet writes "key = value\n" to sb only if value is non-empty.
func writeIfSet(sb *strings.Builder, key, value string) {
	if value != "" {
		fmt.Fprintf(sb, "%s = %s\n", key, value)
	}
}

// writeAwgBoolIfOn writes an AmneziaWG 3.1 "key = on" line, and writes
// nothing when the flag is off. amneziawg-tools accepts on/off and 0/1, but
// an omitted key is the only form every pre-3.1 parser tolerates - clients
// still on AmneziaWG 3.0 reject an unknown key outright and would fail to
// import the config, so "off" is expressed by silence.
func writeAwgBoolIfOn(sb *strings.Builder, key string, enabled bool) {
	if enabled {
		fmt.Fprintf(sb, "%s = on\n", key)
	}
}

// awgProtocolVersion is the "protocol_version" value the AmneziaVPN app uses
// for the AmneziaWG 3 generation (protocols::awg::awgV3 in amnezia-client).
const awgProtocolVersion = "3.1"

// awgBoolOrEmpty renders an AmneziaWG 3.1 toggle the way the AmneziaVPN app's
// JSON expects it, or "" for off so the key can be dropped entirely.
func awgBoolOrEmpty(enabled bool) string {
	if enabled {
		return "on"
	}
	return ""
}

func getServerIP(network string) string {
	parts := strings.Split(network, ".")
	if len(parts) == 4 {
		return fmt.Sprintf("%s.%s.%s.1", parts[0], parts[1], parts[2])
	}
	return "10.0.0.1"
}

// CreateServer creates a new WireGuard server configuration.
func (m *Manager) CreateServer(req CreateServerRequest) (*Server, error) {
	name := sanitizeName(req.Name, "New Server")
	port := req.Port
	if port == 0 {
		port = m.DefaultPort
	}
	subnet := req.Subnet
	if subnet == "" {
		subnet = m.DefaultSubnet
	}
	mtu := req.MTU
	if mtu == 0 {
		mtu = m.DefaultMTU
	}
	if mtu < 1280 || mtu > 1440 {
		return nil, fmt.Errorf("MTU must be between 1280 and 1440, got %d", mtu)
	}

	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		endpoint = m.PublicIP()
	}

	// Parse DNS
	var dnsServers []string
	if req.DNS != nil {
		switch v := req.DNS.(type) {
		case string:
			for _, d := range strings.Split(v, ",") {
				if s := strings.TrimSpace(d); s != "" {
					dnsServers = append(dnsServers, s)
				}
			}
		case []interface{}:
			for _, d := range v {
				if s, ok := d.(string); ok {
					if t := strings.TrimSpace(s); t != "" {
						dnsServers = append(dnsServers, t)
					}
				}
			}
		}
	}
	if len(dnsServers) == 0 {
		dnsServers = m.DNSServers
	}
	for _, dns := range dnsServers {
		if !isValidIPv4(dns) {
			return nil, fmt.Errorf("invalid DNS server IP: %s", dns)
		}
	}

	enableObfuscation := m.EnableObfuscation
	if req.Obfuscation != nil {
		enableObfuscation = *req.Obfuscation
	}

	autoStart := m.AutoStart
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	}

	serverID := uuid.New().String()[:6]
	ifaceName := "wg-" + serverID
	configPath := filepath.Join(WireguardConfigDir, ifaceName+".conf")

	privKey, pubKey, err := m.generateWireguardKeys()
	if err != nil {
		privKey = randomBase64Key()
		pubKey = randomBase64Key()
	}

	// AmneziaWG 1.0/1.5/2.0-only modes are no longer supported: enabling
	// obfuscation always means the full AmneziaWG 3.x parameter set,
	// including mandatory header protection.
	var obfParams *ObfuscationParams
	if enableObfuscation {
		if req.ObfuscationParams != nil {
			obfParams = req.ObfuscationParams
		} else {
			p := m.generateObfuscationParams(mtu)
			obfParams = &p
		}
		if obfParams.HeaderProtectionKey == "" {
			obfParams.HeaderProtectionKey = randomBase64Key()
		}
		if err := validateObfuscationParams(obfParams); err != nil {
			return nil, err
		}
	}

	parts := strings.SplitN(subnet, "/", 2)
	network := parts[0]
	prefix := "24"
	if len(parts) > 1 {
		prefix = parts[1]
	}
	serverIP := getServerIP(network)

	// Build config file content
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Interface]\n")
	fmt.Fprintf(&sb, "PrivateKey = %s\n", privKey)
	fmt.Fprintf(&sb, "Address = %s/%s\n", serverIP, prefix)
	fmt.Fprintf(&sb, "ListenPort = %d\n", port)
	fmt.Fprintf(&sb, "SaveConfig = false\n")
	fmt.Fprintf(&sb, "MTU = %d\n", mtu)

	if enableObfuscation && obfParams != nil {
		fmt.Fprintf(&sb, "Jc = %d\n", obfParams.Jc)
		fmt.Fprintf(&sb, "Jmin = %d\n", obfParams.Jmin)
		fmt.Fprintf(&sb, "Jmax = %d\n", obfParams.Jmax)
		fmt.Fprintf(&sb, "S1 = %d\n", obfParams.S1)
		fmt.Fprintf(&sb, "S2 = %d\n", obfParams.S2)
		fmt.Fprintf(&sb, "S3 = %d\n", obfParams.S3)
		fmt.Fprintf(&sb, "S4 = %d\n", obfParams.S4)
		fmt.Fprintf(&sb, "H1 = %d\n", obfParams.H1)
		fmt.Fprintf(&sb, "H2 = %d\n", obfParams.H2)
		fmt.Fprintf(&sb, "H3 = %d\n", obfParams.H3)
		fmt.Fprintf(&sb, "H4 = %d\n", obfParams.H4)
		if obfParams.HeaderProtectionKey != "" {
			fmt.Fprintf(&sb, "HeaderProtectionKey = %s\n", obfParams.HeaderProtectionKey)
		}
		writeIfSet(&sb, "ContentPaddingAddition", obfParams.ContentPaddingAddition)
		writeIfSet(&sb, "RekeyAfterTime", obfParams.RekeyAfterTime)
		writeIfSet(&sb, "RekeyTimeout", obfParams.RekeyTimeout)
		writeIfSet(&sb, "RejectAfterTime", obfParams.RejectAfterTime)
		writeIfSet(&sb, "KeepaliveTimeout", obfParams.KeepaliveTimeout)
		writeIfSet(&sb, "MaxHandshakeAttempts", obfParams.MaxHandshakeAttempts)
		writeAwgBoolIfOn(&sb, "RandomTrailers", obfParams.RandomTrailers)
		writeAwgBoolIfOn(&sb, "DisableCookies", obfParams.DisableCookies)
	}

	if err := os.WriteFile(configPath, []byte(sb.String()), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write config: %w", err)
	}

	srv := Server{
		ID:                 serverID,
		Name:               name,
		Protocol:           "wireguard",
		Port:               port,
		Status:             "stopped",
		Interface:          ifaceName,
		ConfigPath:         configPath,
		ServerPublicKey:    pubKey,
		ServerPrivateKey:   privKey,
		Subnet:             subnet,
		ServerIP:           serverIP,
		MTU:                mtu,
		PublicIP:           endpoint,
		Endpoint:           endpoint,
		ObfuscationEnabled: enableObfuscation,
		ObfuscationParams:  obfParams,
		AutoStart:          autoStart,
		DNS:                dnsServers,
		Clients:            []Client{},
		UnboundNATIPs:      []string{},
		CreatedAt:          float64(time.Now().Unix()),
	}

	m.addServer(srv)
	m.saveOrLog("new server")

	if autoStart {
		fmt.Printf("Auto-starting new server: %s\n", name)
		if err := m.StartServer(serverID); err != nil {
			fmt.Printf("Auto-start failed for %s: %v\n", name, err)
		}
	}

	return &srv, nil
}

// DeleteServer stops and removes a server and its clients.
func (m *Manager) DeleteServer(serverID string) error {
	srv, ok := m.getServer(serverID)
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
	for i, s := range m.Config.Servers {
		if s.ID == serverID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	srv := m.Config.Servers[idx]

	os.Remove(srv.ConfigPath)

	// The server owns its clients, so dropping it drops them with it.
	m.Config.Servers = append(m.Config.Servers[:idx], m.Config.Servers[idx+1:]...)

	return true
}

func (m *Manager) findServer(id string) *Server {
	for i := range m.Config.Servers {
		if m.Config.Servers[i].ID == id {
			return &m.Config.Servers[i]
		}
	}
	return nil
}

// getServer locks and returns a detached snapshot of the server, without its
// clients - use GetClientConfigs for those. A pointer into m.Config.Servers
// would stop being safe the moment the lock is released: another request can
// mutate the fields, and appending a server can move the whole slice.
func (m *Manager) getServer(id string) (Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv := m.findServer(id)
	if srv == nil {
		return Server{}, false
	}
	snapshot := cloneServer(srv)
	snapshot.Clients = nil
	return snapshot, true
}

// findClient returns pointers to the server with the given ID and to the
// client within its Clients list, or (nil, nil) if either is missing.
// Membership in srv.Clients is what makes a client belong to a server - the
// client's own ServerID field is a denormalised copy and is never consulted
// for the lookup.
//
// Both pointers are into the live config, so the caller must hold m.mu for
// as long as it uses them.
func (m *Manager) findClient(serverID, clientID string) (*Server, *Client) {
	srv := m.findServer(serverID)
	if srv == nil {
		return nil, nil
	}
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			return srv, &srv.Clients[i]
		}
	}
	return srv, nil
}

// getClientInServer locks and returns a detached copy of a client.
func (m *Manager) getClientInServer(serverID, clientID string) (Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, client := m.findClient(serverID, clientID)
	if client == nil {
		return Client{}, false
	}
	return cloneClient(client), true
}

// cloneClient copies a client along with the two fields a plain assignment
// would only alias: the obfuscation parameters behind the pointer and the
// I-settings map. Without this a client handed out to a caller - or copied
// from its server at creation time - keeps writing through to the original.
func cloneClient(c *Client) Client {
	clone := *c
	clone.ObfuscationParams = cloneObfuscationParams(c.ObfuscationParams)
	if c.ISettings != nil {
		clone.ISettings = make(map[string]string, len(c.ISettings))
		for k, v := range c.ISettings {
			clone.ISettings[k] = v
		}
	}
	return clone
}

// cloneObfuscationParams detaches a parameter set from whoever else points at
// it. A server's parameters must not be shared with its clients: editing the
// server would silently rewrite configs that were already handed out, and the
// obfuscation only works when both ends agree on the values they were issued
// with.
func cloneObfuscationParams(p *ObfuscationParams) *ObfuscationParams {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

// clientCount locks and returns the total number of clients.
func (m *Manager) clientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for i := range m.Config.Servers {
		n += len(m.Config.Servers[i].Clients)
	}
	return n
}

// copyServers returns a detached copy of the current server list.
func (m *Manager) copyServers() []Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneServers(m.Config.Servers)
}

// cloneServers detaches a server list from the config: a plain copy would
// still share the Clients slice and the obfuscation parameters, which the
// next request is free to mutate while the result is being serialised.
func cloneServers(servers []Server) []Server {
	out := make([]Server, len(servers))
	for i := range servers {
		out[i] = cloneServer(&servers[i])
	}
	return out
}

func cloneServer(srv *Server) Server {
	clone := *srv
	clone.ObfuscationParams = cloneObfuscationParams(srv.ObfuscationParams)
	clone.DNS = slices.Clone(srv.DNS)
	clone.UnboundNATIPs = slices.Clone(srv.UnboundNATIPs)
	clone.Clients = make([]Client, len(srv.Clients))
	for i := range srv.Clients {
		clone.Clients[i] = cloneClient(&srv.Clients[i])
	}
	return clone
}

// addServer locks and appends a new server to the config.
func (m *Manager) addServer(srv Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Config.Servers = append(m.Config.Servers, srv)
}

// setServerStatus locks and updates the status field of a server.
func (m *Manager) setServerStatus(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if srv := m.findServer(id); srv != nil {
		srv.Status = status
	}
}

// setServersPublicIP locks and updates the public IP on every server.
func (m *Manager) setServersPublicIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Config.Servers {
		m.Config.Servers[i].PublicIP = ip
	}
}

// StartServer brings up a WireGuard interface.
func (m *Manager) StartServer(serverID string) error {
	srv, ok := m.getServer(serverID)
	if !ok {
		return serverNotFound(serverID)
	}

	// awg-quick has nothing to do on an interface that is already up: it
	// fails with "File exists", which is a conflict, not a broken server.
	if m.serverStatus(srv.Interface) == "running" {
		return fmt.Errorf("server %s is already running: %w", serverID, ErrConflict)
	}

	if _, err := execCommand(fmt.Sprintf("/usr/bin/awg-quick up %s", srv.Interface)); err != nil {
		fmt.Printf("Failed to start server %s: %v\n", srv.Name, err)
		return fmt.Errorf("awg-quick up %s: %w", srv.Interface, err)
	}

	m.setupIPTables(srv.Interface, srv.Subnet)
	m.setServerStatus(serverID, "running")
	m.noteServerStatus(srv.Interface, "running")
	m.saveOrLog("server start")

	fmt.Printf("Server %s started\n", srv.Name)
	if m.hub != nil {
		go func() {
			time.Sleep(2 * time.Second)
			m.hub.BroadcastServerStatus(serverID, "running")
		}()
	}
	return nil
}

// StopServer tears down a WireGuard interface.
func (m *Manager) StopServer(serverID string) error {
	srv, ok := m.getServer(serverID)
	if !ok {
		return serverNotFound(serverID)
	}

	if m.serverStatus(srv.Interface) != "running" {
		return fmt.Errorf("server %s is not running: %w", serverID, ErrConflict)
	}

	m.cleanupIPTables(srv.Interface, srv.Subnet)

	if _, err := execCommand(fmt.Sprintf("/usr/bin/awg-quick down %s", srv.Interface)); err != nil {
		fmt.Printf("Failed to stop server %s: %v\n", srv.Name, err)
		return fmt.Errorf("awg-quick down %s: %w", srv.Interface, err)
	}

	m.setServerStatus(serverID, "stopped")
	m.noteServerStatus(srv.Interface, "stopped")
	m.saveOrLog("server stop")

	fmt.Printf("Server %s stopped\n", srv.Name)
	if m.hub != nil {
		go func() {
			time.Sleep(2 * time.Second)
			m.hub.BroadcastServerStatus(serverID, "stopped")
		}()
	}
	return nil
}

// GetServerStatus checks the real interface state.
func (m *Manager) GetServerStatus(serverID string) string {
	srv, ok := m.getServer(serverID)
	if !ok {
		return "not_found"
	}
	return m.serverStatus(srv.Interface)
}

// serverStatus reports whether the server's interface is up, from a short
// lived cache. The kernel is the only authority on this - the Status field in
// the config is a stale echo of the last observation - but every dashboard
// poll asks about every server, and the traffic broadcaster asks again every
// few seconds, so the answer is reused for statusTTL rather than shelling out
// per caller.
func (m *Manager) serverStatus(iface string) string {
	if iface == "" {
		return "stopped"
	}
	if status, ok := m.cachedStatus(iface); ok {
		return status
	}

	status := interfaceStatus(iface)
	m.statusMu.Lock()
	m.statuses[iface] = statusObservation{status: status, at: time.Now()}
	m.statusMu.Unlock()
	return status
}

func (m *Manager) cachedStatus(iface string) (string, bool) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.statuses == nil {
		m.statuses = map[string]statusObservation{}
	}
	seen, ok := m.statuses[iface]
	if !ok || time.Since(seen.at) > statusTTL {
		return "", false
	}
	return seen.status, true
}

// noteServerStatus records a status this code just caused, so the next reader
// does not have to wait out the cache or race the kernel.
func (m *Manager) noteServerStatus(iface, status string) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.statuses == nil {
		m.statuses = map[string]statusObservation{}
	}
	m.statuses[iface] = statusObservation{status: status, at: time.Now()}
}

// forgetServerStatus drops a cached observation, for an interface that no
// longer exists.
func (m *Manager) forgetServerStatus(iface string) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	delete(m.statuses, iface)
}

// statusTTL is how long an observed interface state is reused. Long enough to
// collapse the burst of lookups one dashboard refresh makes, short enough that
// an interface going down is noticed within a poll or two.
const statusTTL = 2 * time.Second

type statusObservation struct {
	status string
	at     time.Time
}

// interfaceStatus reports whether the kernel has the interface. It shells out,
// so it must never be called with m.mu held.
func interfaceStatus(iface string) string {
	if iface == "" {
		return "stopped"
	}
	result, err := execCommand(fmt.Sprintf("ip link show %s", iface))
	if err != nil {
		return "stopped"
	}
	if strings.Contains(result, "state UNKNOWN") || strings.Contains(result, iface) {
		return "running"
	}
	return "stopped"
}

// AddClient adds a WireGuard peer to a server.
func (m *Manager) AddClient(serverID, clientName string, applyI bool, iSettings map[string]string, allowedIPs string) (*Client, string, error) {
	// The name lands in a .conf comment, in the peer marker and in a
	// Content-Disposition filename, so it is cleaned before anything stores
	// it - not at each of those points.
	clientName = sanitizeName(clientName, "client")

	// Key generation shells out to awg three times. Doing that under the
	// write lock would stall every other request, including plain reads, for
	// the duration - and the keys do not depend on any config state.
	keys := m.generateClientKeys()

	client, ifaceName, err := m.addClientLocked(serverID, clientName, applyI, iSettings, allowedIPs, keys)
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("new client")
	m.syncLiveConfig(ifaceName)

	configContent := m.GenerateClientConfig(serverID, client, true)
	fmt.Printf("Client %s added with AllowedIPs: %s\n", clientName, client.AllowedIPs)
	return client, configContent, nil
}

// clientKeys is one client's freshly generated key material.
type clientKeys struct {
	priv, pub, psk string
}

// generateClientKeys produces the key material for a new client. It shells
// out and must not be called with m.mu held.
func (m *Manager) generateClientKeys() clientKeys {
	priv, pub, err := m.generateWireguardKeys()
	if err != nil {
		priv = randomBase64Key()
		pub = randomBase64Key()
	}
	return clientKeys{priv: priv, pub: pub, psk: m.generatePresharedKey()}
}

// addClientLocked builds the client, appends it to the server's peer config
// file and to the in-memory config, all under a single write lock.
func (m *Manager) addClientLocked(serverID, clientName string, applyI bool, iSettings map[string]string, allowedIPs string, keys clientKeys) (client *Client, ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, "", serverNotFound(serverID)
	}

	clientID := uuid.New().String()[:6]
	privKey, pubKey, psk := keys.priv, keys.pub, keys.psk

	clientIP := m.getNewClientIP(srv)
	if clientIP == "" {
		return nil, "", fmt.Errorf("subnet is full")
	}

	serverPeerAllowedIPs := clientIP + "/32"
	clientAllowedIPs := strings.TrimSpace(allowedIPs)
	if clientAllowedIPs == "" {
		clientAllowedIPs = "0.0.0.0/0, ::/0"
	}

	clientISettings := map[string]string{}
	if applyI {
		defaults := map[string]string{
			"i1": DefaultI1, "i2": DefaultI2,
			"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
		}
		for k, v := range defaults {
			clientISettings[k] = v
		}
		for k, v := range iSettings {
			if v != "" {
				clientISettings[k] = v
			}
		}
	}

	newClient := Client{
		ID:                 clientID,
		Name:               clientName,
		ServerID:           serverID,
		ServerName:         srv.Name,
		Status:             "active",
		CreatedAt:          float64(time.Now().Unix()),
		ClientPrivateKey:   privKey,
		ClientPublicKey:    pubKey,
		PresharedKey:       psk,
		ClientIP:           clientIP,
		ObfuscationEnabled: srv.ObfuscationEnabled,
		ObfuscationParams:  cloneObfuscationParams(srv.ObfuscationParams),
		ApplyISettings:     applyI,
		ISettings:          clientISettings,
		AllowedIPs:         clientAllowedIPs,
	}

	peerConf := fmt.Sprintf("\n%s\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s\n",
		peerMarker(clientName, clientID), pubKey, psk, serverPeerAllowedIPs)

	f, openErr := os.OpenFile(srv.ConfigPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if openErr != nil {
		return nil, "", fmt.Errorf("failed to write client to server config: %w", openErr)
	}
	f.WriteString(peerConf)
	f.Close()

	srv.Clients = append(srv.Clients, newClient)

	// newClient is already a detached copy; the caller gets it rather than a
	// pointer into srv.Clients, which the next append could move.
	return &newClient, srv.Interface, nil
}

func (m *Manager) getNewClientIP(srv *Server) string {
	if len(srv.UnboundNATIPs) > 0 {
		ip := srv.UnboundNATIPs[0]
		srv.UnboundNATIPs = srv.UnboundNATIPs[1:]
		return ip
	}

	usedIPs := map[string]bool{srv.ServerIP: true}
	for _, c := range srv.Clients {
		usedIPs[c.ClientIP] = true
	}

	parts := strings.SplitN(srv.Subnet, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	_, ipNet, err := net.ParseCIDR(srv.Subnet)
	if err != nil {
		return ""
	}

	// Iterate over host IPs in the subnet
	for ip := cloneIP(ipNet.IP); ipNet.Contains(ip); incrementIP(ip) {
		s := ip.String()
		if s == ipNet.IP.String() {
			continue // network address
		}
		// broadcast: last address
		if isBroadcast(ip, ipNet) {
			continue
		}
		if !usedIPs[s] {
			return s
		}
	}
	return ""
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(ip net.IP, ipNet *net.IPNet) bool {
	mask := ipNet.Mask
	broadcast := make(net.IP, len(ip))
	for i := range ip {
		broadcast[i] = ipNet.IP[i] | ^mask[i]
	}
	return ip.Equal(broadcast)
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
func (m *Manager) deleteClientLocked(serverID, clientID string) (serverName string, clientCopy Client, ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, target := m.findClient(serverID, clientID)
	if srv == nil {
		return "", Client{}, "", serverNotFound(serverID)
	}
	if target == nil {
		return "", Client{}, "", clientNotFound(clientID)
	}

	clientCopy = cloneClient(target)
	if _, err := removePeerFromServerConf(srv.ConfigPath, target); err != nil {
		return "", Client{}, "", fmt.Errorf("removing the peer from %s: %w", srv.ConfigPath, err)
	}

	newClients := make([]Client, 0, len(srv.Clients)-1)
	for _, c := range srv.Clients {
		if c.ID != clientID {
			newClients = append(newClients, c)
		}
	}
	srv.Clients = newClients
	srv.UnboundNATIPs = append(srv.UnboundNATIPs, clientCopy.ClientIP)

	return srv.Name, clientCopy, srv.Interface, nil
}

// removePeerFromServerConf deletes the client's peer block from the server's
// .conf and returns the block it removed, so a caller that means to park it
// (suspend) does not have to parse the file a second time.
func removePeerFromServerConf(configPath string, client *Client) (block []string, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	rest, block, found := splitPeerBlock(strings.Split(string(data), "\n"), client)
	if !found {
		return nil, nil
	}
	if err := atomicWriteFile(configPath, []byte(strings.Join(rest, "\n")+"\n"), 0o600); err != nil {
		return nil, err
	}
	return block, nil
}

// GenerateClientConfig produces a WireGuard client .conf string.
func (m *Manager) GenerateClientConfig(serverID string, client *Client, includeComments bool) string {
	srv, ok := m.getServer(serverID)
	if !ok {
		return ""
	}

	endpoint := srv.Endpoint
	if endpoint == "" {
		endpoint = srv.PublicIP
	}
	if endpoint == "" {
		endpoint = m.PublicIP()
	}

	var sb strings.Builder

	if includeComments {
		fmt.Fprintf(&sb, "# AmneziaWG Client Configuration\n")
		fmt.Fprintf(&sb, "# Server: %s\n", srv.Name)
		fmt.Fprintf(&sb, "# Client: %s\n", client.Name)
		fmt.Fprintf(&sb, "# Generated: %s\n", time.Unix(int64(client.CreatedAt), 0).UTC().String())
		fmt.Fprintf(&sb, "# Server Endpoint: %s:%d\n", endpoint, srv.Port)
	}

	fmt.Fprintf(&sb, "[Interface]\n")
	fmt.Fprintf(&sb, "PrivateKey = %s\n", client.ClientPrivateKey)
	fmt.Fprintf(&sb, "Address = %s/32\n", client.ClientIP)
	fmt.Fprintf(&sb, "DNS = %s\n", strings.Join(srv.DNS, ", "))
	fmt.Fprintf(&sb, "MTU = %d\n", srv.MTU)

	if client.ObfuscationEnabled && client.ObfuscationParams != nil {
		p := client.ObfuscationParams
		fmt.Fprintf(&sb, "Jc = %d\n", p.Jc)
		fmt.Fprintf(&sb, "Jmin = %d\n", p.Jmin)
		fmt.Fprintf(&sb, "Jmax = %d\n", p.Jmax)
		fmt.Fprintf(&sb, "S1 = %d\n", p.S1)
		fmt.Fprintf(&sb, "S2 = %d\n", p.S2)
		fmt.Fprintf(&sb, "S3 = %d\n", p.S3)
		fmt.Fprintf(&sb, "S4 = %d\n", p.S4)
		fmt.Fprintf(&sb, "H1 = %d\n", p.H1)
		fmt.Fprintf(&sb, "H2 = %d\n", p.H2)
		fmt.Fprintf(&sb, "H3 = %d\n", p.H3)
		fmt.Fprintf(&sb, "H4 = %d\n", p.H4)
		if p.HeaderProtectionKey != "" {
			fmt.Fprintf(&sb, "HeaderProtectionKey = %s\n", p.HeaderProtectionKey)
		}
		writeIfSet(&sb, "ContentPaddingAddition", p.ContentPaddingAddition)
		writeIfSet(&sb, "RekeyAfterTime", p.RekeyAfterTime)
		writeIfSet(&sb, "RekeyTimeout", p.RekeyTimeout)
		writeIfSet(&sb, "RejectAfterTime", p.RejectAfterTime)
		writeIfSet(&sb, "KeepaliveTimeout", p.KeepaliveTimeout)
		writeIfSet(&sb, "MaxHandshakeAttempts", p.MaxHandshakeAttempts)
		writeAwgBoolIfOn(&sb, "RandomTrailers", p.RandomTrailers)
		writeAwgBoolIfOn(&sb, "DisableCookies", p.DisableCookies)
	}

	if client.ApplyISettings {
		if i1 := client.ISettings["i1"]; i1 != "" {
			for n := 1; n <= 5; n++ {
				key := fmt.Sprintf("i%d", n)
				if v := client.ISettings[key]; v != "" {
					fmt.Fprintf(&sb, "I%d = %s\n", n, v)
				}
			}
		}
	}

	allowedIPs := client.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}

	fmt.Fprintf(&sb, "\n[Peer]\n")
	fmt.Fprintf(&sb, "PublicKey = %s\n", srv.ServerPublicKey)
	fmt.Fprintf(&sb, "PresharedKey = %s\n", client.PresharedKey)
	fmt.Fprintf(&sb, "Endpoint = %s:%d\n", endpoint, srv.Port)
	fmt.Fprintf(&sb, "AllowedIPs = %s\n", allowedIPs)
	persistentKeepalive := "25"
	if client.ObfuscationParams != nil && client.ObfuscationParams.PersistentKeepalive != "" {
		persistentKeepalive = client.ObfuscationParams.PersistentKeepalive
	}
	fmt.Fprintf(&sb, "PersistentKeepalive = %s\n", persistentKeepalive)

	return sb.String()
}

// GenerateAmneziaVpnURL builds a "vpn://" link in AmneziaVPN's own native
// config format (base64 of the same JSON its app exports/imports).
//
// It sits alongside the plain .conf export rather than replacing it: the link
// is a single string, so it survives being pasted into a chat and the app
// opens it directly, and the JSON has room for what a .conf cannot express -
// the container name, the protocol version the app matches against, a
// readable server description and the DNS servers as their own keys.
func (m *Manager) GenerateAmneziaVpnURL(serverID string, client *Client) (string, error) {
	srv, ok := m.getServer(serverID)
	if !ok {
		return "", serverNotFound(serverID)
	}

	endpoint := srv.Endpoint
	if endpoint == "" {
		endpoint = srv.PublicIP
	}
	if endpoint == "" {
		endpoint = m.PublicIP()
	}

	subnetCidr := "24"
	if parts := strings.SplitN(srv.Subnet, "/", 2); len(parts) > 1 {
		subnetCidr = parts[1]
	}

	allowedIPs := client.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}
	var allowedIPList []string
	for _, ip := range strings.Split(allowedIPs, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			allowedIPList = append(allowedIPList, ip)
		}
	}

	awgObj := map[string]interface{}{
		"port":            fmt.Sprintf("%d", srv.Port),
		"transport_proto": "udp",
		// The app compares this against its own protocols::awg::awgV3
		// constant, which is the literal string "3.1" in every release that
		// knows about AmneziaWG 3 (5.0.1.5 onwards; no shipped client ever
		// used a bare "3"). A mismatch makes the app render the server with
		// the pre-3.0 settings page and flag it as an outdated container.
		"protocol_version":   awgProtocolVersion,
		"subnet_address":     srv.ServerIP,
		"subnet_cidr":        subnetCidr,
		"isThirdPartyConfig": true,
	}

	persistentKeepalive := "25"
	clientObj := map[string]interface{}{
		"hostName":        endpoint,
		"port":            srv.Port,
		"client_ip":       client.ClientIP,
		"client_priv_key": client.ClientPrivateKey,
		"client_pub_key":  client.ClientPublicKey,
		"server_pub_key":  srv.ServerPublicKey,
		"psk_key":         client.PresharedKey,
		"clientId":        client.ClientPublicKey,
		"allowed_ips":     allowedIPList,
		"mtu":             fmt.Sprintf("%d", srv.MTU),
	}

	if client.ObfuscationEnabled && client.ObfuscationParams != nil {
		p := client.ObfuscationParams
		for k, v := range map[string]string{
			"Jc": fmt.Sprintf("%d", p.Jc), "Jmin": fmt.Sprintf("%d", p.Jmin), "Jmax": fmt.Sprintf("%d", p.Jmax),
			"S1": fmt.Sprintf("%d", p.S1), "S2": fmt.Sprintf("%d", p.S2),
			"S3": fmt.Sprintf("%d", p.S3), "S4": fmt.Sprintf("%d", p.S4),
			"H1": fmt.Sprintf("%d", p.H1), "H2": fmt.Sprintf("%d", p.H2),
			"H3": fmt.Sprintf("%d", p.H3), "H4": fmt.Sprintf("%d", p.H4),
		} {
			awgObj[k] = v
			clientObj[k] = v
		}
		if p.HeaderProtectionKey != "" {
			awgObj["HeaderProtectionKey"] = p.HeaderProtectionKey
			clientObj["HeaderProtectionKey"] = p.HeaderProtectionKey
		}
		for k, v := range map[string]string{
			"ContentPaddingAddition": p.ContentPaddingAddition,
			"RekeyAfterTime":         p.RekeyAfterTime,
			"RekeyTimeout":           p.RekeyTimeout,
			"RejectAfterTime":        p.RejectAfterTime,
			"KeepaliveTimeout":       p.KeepaliveTimeout,
			"MaxHandshakeAttempts":   p.MaxHandshakeAttempts,
			// The app stores these as the strings "on"/"off" (awgBoolOn /
			// awgBoolOff) and treats an absent key as off, so only the "on"
			// case needs to travel.
			"RandomTrailers": awgBoolOrEmpty(p.RandomTrailers),
			"DisableCookies": awgBoolOrEmpty(p.DisableCookies),
		} {
			if v != "" {
				awgObj[k] = v
				clientObj[k] = v
			}
		}
		if p.PersistentKeepalive != "" {
			persistentKeepalive = p.PersistentKeepalive
		}
	}
	clientObj["persistent_keep_alive"] = persistentKeepalive

	if client.ApplyISettings {
		for n := 1; n <= 5; n++ {
			if v := client.ISettings[fmt.Sprintf("i%d", n)]; v != "" {
				iKey := fmt.Sprintf("I%d", n)
				awgObj[iKey] = v
				clientObj[iKey] = v
			}
		}
	}

	clientJSON, err := json.Marshal(clientObj)
	if err != nil {
		return "", err
	}
	awgObj["last_config"] = string(clientJSON)

	root := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-awg2",
				"awg":       awgObj,
			},
		},
		"defaultContainer": "amnezia-awg2",
		"description":      fmt.Sprintf("%s - %s", srv.Name, client.Name),
		"hostName":         endpoint,
	}
	if len(srv.DNS) > 0 {
		root["dns1"] = srv.DNS[0]
		if len(srv.DNS) > 1 {
			root["dns2"] = srv.DNS[1]
		}
	}

	rootJSON, err := json.Marshal(root)
	if err != nil {
		return "", err
	}

	return "vpn://" + base64.RawURLEncoding.EncodeToString(rootJSON), nil
}

// UpdateClientAllowedIPs changes the AllowedIPs field for a client.
func (m *Manager) UpdateClientAllowedIPs(serverID, clientID, allowedIPs string) (*Client, string, error) {
	clientCopy, err := m.updateClientAllowedIPsLocked(serverID, clientID, allowedIPs)
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client update")
	cfg := m.GenerateClientConfig(serverID, clientCopy, true)
	return clientCopy, cfg, nil
}

// updateClientAllowedIPsLocked updates the client's AllowedIPs under a
// single write lock and returns a copy of the updated client.
func (m *Manager) updateClientAllowedIPsLocked(serverID, clientID, allowedIPs string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return nil, serverNotFound(serverID)
	}
	if client == nil {
		return nil, clientNotFound(clientID)
	}

	if strings.TrimSpace(allowedIPs) == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}
	client.AllowedIPs = strings.TrimSpace(allowedIPs)
	clientCopy := cloneClient(client)
	return &clientCopy, nil
}

// UpdateClientISettings updates the I1-I5 settings for a client.
func (m *Manager) UpdateClientISettings(serverID, clientID string, applyI *bool, iSettings map[string]string) (*Client, string, error) {
	clientCopy, err := m.updateClientISettingsLocked(serverID, clientID, applyI, iSettings)
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client update")
	cfg := m.GenerateClientConfig(serverID, clientCopy, true)
	return clientCopy, cfg, nil
}

// updateClientISettingsLocked updates the client's I1-I5 settings under a
// single write lock and returns a copy of the updated client.
func (m *Manager) updateClientISettingsLocked(serverID, clientID string, applyI *bool, iSettings map[string]string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return nil, serverNotFound(serverID)
	}
	if client == nil {
		return nil, clientNotFound(clientID)
	}

	if applyI != nil {
		client.ApplyISettings = *applyI
	}

	if iSettings != nil {
		if client.ApplyISettings {
			newSettings := map[string]string{
				"i1": DefaultI1, "i2": DefaultI2,
				"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
			}
			for k, v := range client.ISettings {
				newSettings[k] = v
			}
			for k, v := range iSettings {
				if v != "" {
					newSettings[k] = v
				}
			}
			client.ISettings = newSettings
		} else {
			client.ISettings = map[string]string{}
		}
	}

	clientCopy := cloneClient(client)
	return &clientCopy, nil
}

// suspendedDirFor is where a suspended peer block is parked while it is out
// of the server's live .conf. It sits next to that .conf rather than under a
// fixed path, so the two always move together.
func suspendedDirFor(srv *Server) string {
	return filepath.Join(filepath.Dir(srv.ConfigPath), "suspended")
}

// SuspendClient removes the client peer block from the active server config.
func (m *Manager) SuspendClient(serverID, clientID string) (string, error) {
	ifaceName, err := m.suspendClientLocked(serverID, clientID)
	if err != nil {
		return "", err
	}

	m.saveOrLog("client suspend")
	m.syncLiveConfig(ifaceName)
	return "client suspended", nil
}

// suspendClientLocked moves the client's peer block out of the live server
// config and marks it suspended, all under a single write lock.
func (m *Manager) suspendClientLocked(serverID, clientID string) (ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return "", serverNotFound(serverID)
	}
	if client == nil {
		return "", clientNotFound(clientID)
	}

	// Park the peer block next to the server conf, then take it out of the
	// live one. Both halves happen under this lock, so the file and the
	// in-memory status cannot disagree.
	peerBlock, err := removePeerFromServerConf(srv.ConfigPath, client)
	if err != nil {
		return "", fmt.Errorf("rewriting %s: %w", srv.ConfigPath, err)
	}
	if len(peerBlock) > 0 {
		suspendedDir := suspendedDirFor(srv)
		if err := os.MkdirAll(suspendedDir, 0o755); err != nil {
			return "", fmt.Errorf("storing the suspended peer: %w", err)
		}
		suspendedPath := filepath.Join(suspendedDir, clientID+".conf")
		if err := atomicWriteFile(suspendedPath, []byte(strings.Join(peerBlock, "\n")+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("storing the suspended peer: %w", err)
		}
	}

	client.Status = "suspended"

	return srv.Interface, nil
}

// ActivateClient restores a suspended client peer block.
func (m *Manager) ActivateClient(serverID, clientID string) (string, error) {
	ifaceName, err := m.activateClientLocked(serverID, clientID)
	if err != nil {
		return "", err
	}

	m.saveOrLog("client activation")
	m.syncLiveConfig(ifaceName)
	return "client activated", nil
}

// activateClientLocked restores the client's peer block into the live server
// config and marks it active, all under a single write lock.
func (m *Manager) activateClientLocked(serverID, clientID string) (ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return "", serverNotFound(serverID)
	}
	if client == nil {
		return "", clientNotFound(clientID)
	}
	if client.Status != "suspended" {
		return "", fmt.Errorf("client %s is not suspended: %w", clientID, ErrConflict)
	}

	suspendedPath := filepath.Join(suspendedDirFor(srv), clientID+".conf")
	suspended, err := os.ReadFile(suspendedPath)
	if err != nil {
		return "", fmt.Errorf("reading the suspended peer of %s: %w", clientID, err)
	}

	// The client may have been renamed while it was parked, so the marker is
	// rewritten from the client as it is now.
	block := retagPeerBlock(strings.Split(strings.TrimRight(string(suspended), "\n"), "\n"), client)

	f, err := os.OpenFile(srv.ConfigPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("reopening %s: %w", srv.ConfigPath, err)
	}
	if _, err := fmt.Fprintf(f, "\n%s\n", strings.Join(block, "\n")); err != nil {
		f.Close()
		return "", fmt.Errorf("appending the peer to %s: %w", srv.ConfigPath, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", srv.ConfigPath, err)
	}
	os.Remove(suspendedPath)

	client.Status = "active"

	return srv.Interface, nil
}

// UpdateClientSuspendTime sets or clears the scheduled auto-suspend time.
func (m *Manager) UpdateClientSuspendTime(serverID, clientID string, suspendAt *float64) (*Client, string, error) {
	clientCopy, err := m.updateClientSuspendTimeLocked(serverID, clientID, suspendAt)
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client suspend time")
	return clientCopy, "suspension time updated", nil
}

// updateClientSuspendTimeLocked sets the scheduled suspend time under a
// single write lock and returns a copy of the updated client.
func (m *Manager) updateClientSuspendTimeLocked(serverID, clientID string, suspendAt *float64) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil {
		return nil, serverNotFound(serverID)
	}
	if client == nil {
		return nil, clientNotFound(clientID)
	}

	client.SuspendAt = suspendAt

	clientCopy := cloneClient(client)
	return &clientCopy, nil
}

// pendingSuspension identifies a client that has reached its scheduled
// auto-suspend time.
type pendingSuspension struct {
	serverID, clientID string
}

func (m *Manager) startSuspensionChecker() {
	ticker := time.NewTicker(time.Duration(m.SuspendUpdateInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := float64(time.Now().Unix())
		for _, it := range m.findClientsPastSuspendTime(now) {
			if _, err := m.SuspendClient(it.serverID, it.clientID); err != nil {
				fmt.Printf("Auto-suspend failed for client %s: %v\n", it.clientID, err)
				continue
			}
			fmt.Printf("Auto-suspended client %s at %s\n", it.clientID, time.Now().Format(time.RFC1123))
		}
	}
}

// findClientsPastSuspendTime locks and returns all active clients whose
// scheduled suspend time has passed.
func (m *Manager) findClientsPastSuspendTime(now float64) []pendingSuspension {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []pendingSuspension
	for _, srv := range m.Config.Servers {
		for _, c := range srv.Clients {
			if c.SuspendAt != nil && c.Status == "active" && now >= *c.SuspendAt {
				items = append(items, pendingSuspension{srv.ID, c.ID})
			}
		}
	}
	return items
}

// syncLiveConfig pushes the .conf onto the interface when it is actually up.
// The check is deliberately made here, after the config change and outside
// the lock, rather than from the stored Status: a peer added while the stored
// value says "stopped" but the interface is up would silently not take effect
// until the next restart.
func (m *Manager) syncLiveConfig(iface string) {
	if m.serverStatus(iface) == "running" {
		m.applyLiveConfig(iface)
	}
}

// applyLiveConfig syncs the running WireGuard interface.
func (m *Manager) applyLiveConfig(iface string) bool {
	cmd := fmt.Sprintf("bash -c 'awg syncconf %s <(awg-quick strip %s)'", iface, iface)
	if _, err := execCommand(cmd); err != nil {
		fmt.Printf("Failed to apply live config to %s: %v\n", iface, err)
		return false
	}
	fmt.Printf("Live config applied to %s\n", iface)
	return true
}

func (m *Manager) setupIPTables(iface, subnet string) {
	script := "/app/scripts/setup_iptables.sh"
	if _, err := os.Stat(script); err == nil {
		execCommand(fmt.Sprintf("%s %s %s", script, iface, subnet))
	}
}

func (m *Manager) cleanupIPTables(iface, subnet string) {
	script := "/app/scripts/cleanup_iptables.sh"
	if _, err := os.Stat(script); err == nil {
		execCommand(fmt.Sprintf("%s %s %s", script, iface, subnet))
	}
}

// GetPeerTrafficForServer parses `awg show` output.
func (m *Manager) GetPeerTrafficForServer(serverID string) map[string]ClientTraffic {
	srv, ok := m.getServer(serverID)
	if !ok {
		return nil
	}

	output, err := execCommand(fmt.Sprintf("/usr/bin/awg show %s", srv.Interface))
	if err != nil || output == "" {
		return nil
	}

	peerData := map[string]map[string]string{}
	currentPeer := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "peer:") {
			currentPeer = strings.TrimSpace(strings.TrimPrefix(line, "peer:"))
			peerData[currentPeer] = map[string]string{
				"received": "0 B", "sent": "0 B",
				"last_handshake": "Never", "endpoint": "",
			}
		} else if strings.HasPrefix(line, "transfer:") && currentPeer != "" {
			parts := strings.SplitN(strings.TrimPrefix(line, "transfer:"), ",", 2)
			if len(parts) == 2 {
				peerData[currentPeer]["received"] = strings.TrimSpace(parts[0])
				// "X sent" -> strip " sent"
				sent := strings.TrimSpace(parts[1])
				sent = strings.TrimSuffix(sent, " sent")
				peerData[currentPeer]["sent"] = strings.TrimSpace(sent)
			}
		} else if strings.HasPrefix(line, "endpoint:") && currentPeer != "" {
			peerData[currentPeer]["endpoint"] = strings.TrimSpace(strings.TrimPrefix(line, "endpoint:"))
		} else if strings.HasPrefix(line, "latest handshake:") && currentPeer != "" {
			peerData[currentPeer]["last_handshake"] = strings.TrimSpace(strings.TrimPrefix(line, "latest handshake:"))
		}
	}

	return m.buildClientTraffic(serverID, peerData)
}

// buildClientTraffic locks and joins parsed `awg show` peer data with the
// clients the server owns.
func (m *Manager) buildClientTraffic(serverID string, peerData map[string]map[string]string) map[string]ClientTraffic {
	m.mu.RLock()
	defer m.mu.RUnlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil
	}

	result := map[string]ClientTraffic{}
	for _, c := range srv.Clients {
		if pd, ok := peerData[c.ClientPublicKey]; ok {
			result[c.ID] = ClientTraffic{
				Received:      pd["received"],
				Sent:          pd["sent"],
				LastHandshake: pd["last_handshake"],
				Endpoint:      pd["endpoint"],
			}
		} else {
			result[c.ID] = ClientTraffic{Received: "0 B", Sent: "0 B", LastHandshake: "Never"}
		}
	}
	return result
}

// GetServerInterfaceTraffic parses `ifconfig` for RX/TX on an interface.
func (m *Manager) GetServerInterfaceTraffic(iface string) InterfaceTraffic {
	output, err := execCommand(fmt.Sprintf("ifconfig %s", iface))
	if err != nil || output == "" {
		return nil
	}
	rx, tx := "0 B", "0 B"
	if m := rxRe.FindStringSubmatch(output); len(m) > 1 {
		rx = m[1]
	}
	if m := txRe.FindStringSubmatch(output); len(m) > 1 {
		tx = m[1]
	}
	return InterfaceTraffic{"rx": rx, "tx": tx}
}

// Compiled once: the traffic broadcast calls GetServerInterfaceTraffic for
// every server on every tick, and compiling these two on each call costs more
// than the matching itself.
var (
	rxRe = regexp.MustCompile(`RX bytes:\d+\s+\(([^)]+)\)`)
	txRe = regexp.MustCompile(`TX bytes:\d+\s+\(([^)]+)\)`)
)

// GetAllServersTraffic returns interface traffic for every server.
func (m *Manager) GetAllServersTraffic() map[string]InterfaceTraffic {
	servers := m.copyServers()

	result := map[string]InterfaceTraffic{}
	for _, srv := range servers {
		if t := m.GetServerInterfaceTraffic(srv.Interface); t != nil {
			result[srv.ID] = t
		}
	}
	return result
}

// PublicIP returns the address every generated config points clients at.
func (m *Manager) PublicIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.publicIP
}

func (m *Manager) setPublicIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicIP = ip
}

// RefreshPublicIP re-detects and updates the stored public IP.
func (m *Manager) RefreshPublicIP() string {
	ip := m.detectPublicIP()
	m.setPublicIP(ip)
	m.setServersPublicIP(ip)
	m.saveOrLog("public IP")
	return ip
}

// GetClientConfigs returns detached copies of every client, optionally
// narrowed to one server. Copies, because the result outlives the lock: it
// travels straight into a JSON response while other requests keep mutating
// the config.
func (m *Manager) GetClientConfigs(serverID string) []Client {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := m.Config.Servers
	if serverID != "" {
		srv := m.findServer(serverID)
		if srv == nil {
			return []Client{}
		}
		servers = []Server{*srv}
	}

	clients := []Client{}
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

// GetServersWithStatus returns all servers with their current live status.
//
// Status is filled in from the kernel rather than read out of the config: the
// stored field is only the last thing this process observed, and the
// interface can go up or down without it. The lookups happen between the two
// locks, not under one - this is what every dashboard poll calls, and holding
// the write lock across a shell command per server would stall every other
// request for as long as that takes.
func (m *Manager) GetServersWithStatus() []Server {
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
func (m *Manager) storeServerStatuses(observed []Server) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range observed {
		if srv := m.findServer(observed[i].ID); srv != nil {
			srv.Status = observed[i].Status
		}
	}
}
