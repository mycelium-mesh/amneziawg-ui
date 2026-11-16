package manager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// SystemStatus is the backend's own health: what is installed, how many
// servers and clients there are, and the defaults it started with.
func (m *Manager) SystemStatus() api.SystemStatus {
	servers := m.Servers()
	active := 0
	for _, s := range servers {
		if s.Status == "running" {
			active++
		}
	}

	return api.SystemStatus{
		AWGAvailable:  m.tools.Available(),
		PublicIP:      m.PublicIP(),
		TotalServers:  len(servers),
		TotalClients:  m.ClientCount(),
		ActiveServers: active,
		UptimeSeconds: time.Since(m.started).Seconds(),
		Timestamp:     float64(time.Now().Unix()),
		Environment: api.SystemEnvironment{
			WebUIPort:        strconv.Itoa(m.settings.WebUIPort),
			AutoStartServers: m.settings.AutoStart,
			DefaultMTU:       m.settings.DefaultMTU,
			DefaultSubnet:    m.settings.DefaultSubnet,
			DefaultPort:      m.settings.DefaultPort,
			DefaultDNS:       strings.Join(m.settings.DNSServers, ","),
		},
	}
}

// IPTablesCheck reports which of a server's firewall rules are in place.
func (m *Manager) IPTablesCheck(serverID string) (api.IptablesTest, error) {
	srv, ok := m.Server(serverID)
	if !ok {
		return api.IptablesTest{}, serverNotFound(serverID)
	}
	return api.IptablesTest{
		ServerID:      serverID,
		ServerName:    srv.Name,
		Interface:     srv.Interface,
		Subnet:        srv.Subnet,
		IptablesCheck: m.tools.CheckIPTables(srv.Interface, srv.Subnet),
	}, nil
}

// DefaultISettings returns the effective standard I1-I5 set: the shipped
// wgconf defaults overlaid with the operator's saved values, non-empty
// saved values winning per key. This is the one definition of the standard
// set - the standalone endpoint, ServerInfo and the base a new client with
// apply_i_settings starts from all take it from here, so they cannot
// disagree.
func (m *Manager) DefaultISettings() api.ISettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return wgconf.MergeISettings(m.cfg.DefaultISettings)
}

// UpdateDefaultISettings replaces the operator's saved standard I1-I5 set;
// it does not merge into the previous one. Values go through the same
// shared rules the form and the client-update path use, so a typo is
// rejected here as an invalid request before it reaches any client config.
// An empty or absent set clears the override and restores the shipped
// defaults. Only the five keys holding a non-empty value are kept: an empty
// value is "no opinion" - exactly what layering already means - and a
// stray key would otherwise travel into the effective set.
//
// It returns the effective set as it now stands.
func (m *Manager) UpdateDefaultISettings(settings api.ISettings) (api.ISettings, error) {
	if err := validateISettings(settings); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	saved := api.ISettings{}
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("i%d", i)
		if strings.TrimSpace(settings[key]) != "" {
			saved[key] = settings[key]
		}
	}

	m.mu.Lock()
	previous := m.cfg.DefaultISettings
	m.cfg.DefaultISettings = saved
	m.mu.Unlock()

	// A save failure is reported, and the in-memory value put back, the
	// way the endpoint update does it: this is a change the operator asked
	// for, and a silent failure would show the new set in GET while the old
	// one came back on the next restart.
	if err := m.SaveConfig(); err != nil {
		m.mu.Lock()
		m.cfg.DefaultISettings = previous
		m.mu.Unlock()
		return nil, fmt.Errorf("saving the default I-settings: %w", err)
	}
	return wgconf.MergeISettings(saved), nil
}
