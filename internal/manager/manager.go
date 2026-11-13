// Package manager holds the backend's state - the servers, their clients and
// what the kernel currently says about them - and every operation on it. It
// is the only package that takes the config lock, and it never shells out
// itself: the host is reached through awg.Tools, the disk through store and
// wgconf.
package manager

import (
	"fmt"
	"sync"
	"time"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/config"
	"amneziawg-web-ui/internal/publicip"
	"amneziawg-web-ui/internal/store"
	"amneziawg-web-ui/internal/sysinfo"
)

// Manager orchestrates all AmneziaWG operations.
type Manager struct {
	settings config.Settings
	store    *store.Store
	tools    *awg.Tools
	host     sysinfo.Host

	// started is when this process came up; the panel's uptime counts
	// from it.
	started time.Time

	// cfg is the live config. Every read and write goes through mu; nothing
	// handed out of this package points into it.
	cfg *store.AppConfig

	// publicIP is re-detected on demand from an HTTP handler while other
	// requests are generating configs from it, so it lives behind mu rather
	// than as a bare field.
	publicIP string

	mu sync.RWMutex

	// statuses caches observed interface states, keyed by interface name;
	// see serverStatus. Guarded by statusMu, not mu, so a status lookup
	// never waits on a config write.
	statuses map[string]statusObservation
	statusMu sync.Mutex
}

// New loads the config from st and brings it up to the current schema. It
// does not touch the host: see Start for that.
func New(settings config.Settings, st *store.Store, tools *awg.Tools) *Manager {
	m := &Manager{
		settings: settings,
		store:    st,
		tools:    tools,
		host:     sysinfo.Default(settings.WireguardConfigDir),
		started:  time.Now(),
		statuses: map[string]statusObservation{},
	}

	m.cfg = st.Load()
	if store.Migrate(m.cfg) {
		m.saveOrLog("schema migration")
	}
	return m
}

// Start does what a booting backend does once the config is in memory:
// detects the public address, brings auto-start servers up and launches the
// scheduled-suspension checker. It returns once the servers are up; the
// checker keeps running in the background for the life of the process.
func (m *Manager) Start() {
	// Servers carry their own copy of the address (endpointFor reads it there
	// first), so a restart under a changed IP has to restamp them as well -
	// otherwise every config keeps pointing at the address the host had at
	// some earlier boot until someone presses Refresh IP.
	m.setPublicIP(publicip.Detect(m.tools.RouteSourceIP))

	if m.settings.AutoStart {
		m.autoStartServers()
	}

	go m.runSuspender()

	fmt.Printf("=== Environment Configuration ===\n")
	fmt.Printf("WEB_UI_PORT: %d\n", m.settings.WebUIPort)
	fmt.Printf("AUTO_START: %v\n", m.settings.AutoStart)
	fmt.Printf("DEFAULT_MTU: %d\n", m.settings.DefaultMTU)
	fmt.Printf("DEFAULT_SUBNET: %s\n", m.settings.DefaultSubnet)
	fmt.Printf("DEFAULT_PORT: %d\n", m.settings.DefaultPort)
	fmt.Printf("DNS_SERVERS: %v\n", m.settings.DNSServers)
	fmt.Printf("Detected public IP: %s\n", m.PublicIP())
}

// Settings are the defaults the manager was started with.
func (m *Manager) Settings() config.Settings {
	return m.settings
}

// SaveConfig writes the config to disk. It takes the read lock itself, so it
// must not be called with mu held.
func (m *Manager) SaveConfig() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.Save(m.cfg)
}

// saveOrLog saves the config where the caller has no way to report a failure.
// what names the operation whose result was about to be persisted, so the log
// line says which change is at risk of being lost on the next restart.
func (m *Manager) saveOrLog(what string) {
	if err := m.SaveConfig(); err != nil {
		fmt.Printf("Failed to persist %s: %v\n", what, err)
	}
}
