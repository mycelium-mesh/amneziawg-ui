package manager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"amneziawg-web-ui/internal/store"
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

// UpdateDefaultISettings merges the operator's update into the saved standard
// I1-I5 set: only the keys holding a non-empty value change, and every key
// the request leaves out or sends empty keeps its current state in the saved
// set - including "no saved override", where the shipped default stays live.
// That is the layering rule wgconf.MergeISettings already states ("an empty
// value in a layer does not clear the key") applied to the save itself, so a
// partially-filled save can never silently drop a value the operator saved
// earlier. A request with nothing filled in changes nothing. Values go
// through the same shared rules the form and the client-update path use, so
// a typo is rejected here as an invalid request before it reaches any client
// config. Only the five keys holding a non-empty value are kept: a stray key
// would otherwise travel into the effective set.
//
// It returns the effective set as it now stands.
func (m *Manager) UpdateDefaultISettings(settings api.ISettings) (api.ISettings, error) {
	if err := validateISettings(settings); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	updates := api.ISettings{}
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("i%d", i)
		if value := strings.TrimSpace(settings[key]); value != "" {
			updates[key] = value
		}
	}
	if len(updates) == 0 {
		// Nothing was filled in: no opinion, no change, no write.
		return m.DefaultISettings(), nil
	}

	err := m.updateConfig("the default I-settings", func(cfg *store.AppConfig) (func(), error) {
		previous := cfg.DefaultISettings
		merged := make(api.ISettings, len(previous)+len(updates))
		for key, value := range previous {
			merged[key] = value
		}
		for key, value := range updates {
			merged[key] = value
		}
		cfg.DefaultISettings = merged
		return func() { cfg.DefaultISettings = previous }, nil
	})
	if err != nil {
		return nil, err
	}
	return m.DefaultISettings(), nil
}

// ClearDefaultISettings drops the operator's saved standard: the one explicit
// full clear, and the only way a saved override leaves the set. The shipped
// defaults stand again. Clearing an already-clear set changes nothing.
//
// It returns the effective set as it now stands.
func (m *Manager) ClearDefaultISettings() (api.ISettings, error) {
	err := m.updateConfig("the default I-settings", func(cfg *store.AppConfig) (func(), error) {
		if len(cfg.DefaultISettings) == 0 {
			return nil, nil
		}
		previous := cfg.DefaultISettings
		cfg.DefaultISettings = api.ISettings{}
		return func() { cfg.DefaultISettings = previous }, nil
	})
	if err != nil {
		return nil, err
	}
	return m.DefaultISettings(), nil
}
