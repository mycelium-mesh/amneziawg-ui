package manager

import (
	"amneziawg-web-ui/internal/publicip"
	"amneziawg-web-ui/web-ui/api"
)

// PublicIP returns the address every generated config points clients at.
func (m *Manager) PublicIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.publicIP
}

// setPublicIP records the address detection produced, on the manager and on
// every server - the same stamp RefreshPublicIP makes, without detecting. It
// is the manager's own entry point at boot and the tests' way to fix the
// address without reaching the network.
func (m *Manager) setPublicIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicIP = ip
	m.stampPublicIPLocked(ip)
}

// RefreshPublicIP re-detects the public IP and stamps it on every server.
func (m *Manager) RefreshPublicIP() string {
	ip := publicip.Detect(m.tools.RouteSourceIP)

	m.mu.Lock()
	m.publicIP = ip
	m.stampPublicIPLocked(ip)
	m.mu.Unlock()

	m.saveOrLog("public IP")
	return ip
}

// stampPublicIPLocked records the detected address on every server. The
// caller holds mu; the caller persists. endpointFor prefers a server's
// stamped PublicIP over the manager-wide one, so a server left holding the
// address the host had at some earlier boot keeps handing that address to
// clients - which is exactly what a restart looks like when the host's IP has
// changed since, and why detection has to reach the servers and not only the
// manager.
func (m *Manager) stampPublicIPLocked(ip string) {
	for i := range m.cfg.Servers {
		m.cfg.Servers[i].PublicIP = ip
	}
}

// endpointFor is the host a client of srv connects to: the endpoint the
// server was created with, else the public IP stamped on it, else whatever
// the backend detected last - and the placeholder when none of those is
// known, so the config says so instead of naming no host at all.
func (m *Manager) endpointFor(srv *api.Server) string {
	if endpoint := srv.EffectiveEndpoint(); endpoint != "" {
		return endpoint
	}
	if ip := m.PublicIP(); ip != "" {
		return ip
	}
	// Nothing is known yet: the placeholder says so in the config, where
	// the operator sees it, instead of an empty host that would render as
	// "Endpoint = :54844".
	return api.EndpointUnknown
}
