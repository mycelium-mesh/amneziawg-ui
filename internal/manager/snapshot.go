package manager

import (
	"slices"

	"amneziawg-web-ui/web-ui/api"
)

// Lookups into the live config, and the copies that leave it. The find*
// functions return pointers into cfg and need mu held for as long as the
// pointer is used; everything exported returns a detached copy, because the
// result outlives the lock and travels straight into a JSON response while
// other requests keep mutating the config.

func (m *Manager) findServer(id string) *api.Server {
	for i := range m.cfg.Servers {
		if m.cfg.Servers[i].ID == id {
			return &m.cfg.Servers[i]
		}
	}
	return nil
}

// findClient returns pointers to the server with the given ID and to the
// client within its Clients list, or (nil, nil) if either is missing.
// Membership in srv.Clients is what makes a client belong to a server - the
// client's own ServerID field is a denormalised copy and is never consulted
// for the lookup.
func (m *Manager) findClient(serverID, clientID string) (*api.Server, *api.Client) {
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

// Server returns a detached snapshot of the server, without its clients -
// use Clients for those. A pointer into cfg.Servers would stop being safe
// the moment the lock is released: another request can mutate the fields,
// and appending a server can move the whole slice.
func (m *Manager) Server(id string) (api.Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv := m.findServer(id)
	if srv == nil {
		return api.Server{}, false
	}
	snapshot := cloneServer(srv)
	snapshot.Clients = nil
	return snapshot, true
}

// Client returns a detached copy of one client.
func (m *Manager) Client(serverID, clientID string) (api.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, client := m.findClient(serverID, clientID)
	if client == nil {
		return api.Client{}, false
	}
	return cloneClient(client), true
}

// serverSnapshot is one server with its clients, copied out of the config in
// a single read: a caller that needs both halves of a server gets them from
// the same moment, not from two lookups a write could come between.
func (m *Manager) serverSnapshot(id string) (api.Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv := m.findServer(id)
	if srv == nil {
		return api.Server{}, false
	}
	return cloneServer(srv), true
}

// ClientCount is the total number of clients across every server.
func (m *Manager) ClientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for i := range m.cfg.Servers {
		n += len(m.cfg.Servers[i].Clients)
	}
	return n
}

// copyServers returns a detached copy of the current server list.
func (m *Manager) copyServers() []api.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]api.Server, len(m.cfg.Servers))
	for i := range m.cfg.Servers {
		out[i] = cloneServer(&m.cfg.Servers[i])
	}
	return out
}

// cloneServer detaches a server from the config: a plain copy would still
// share the Clients slice and the obfuscation parameters, which the next
// request is free to mutate while the result is being serialised.
func cloneServer(srv *api.Server) api.Server {
	clone := *srv
	clone.ObfuscationParams = cloneObfuscationParams(srv.ObfuscationParams)
	clone.DNS = slices.Clone(srv.DNS)
	clone.UnboundNATIPs = slices.Clone(srv.UnboundNATIPs)
	clone.Clients = make([]api.Client, len(srv.Clients))
	for i := range srv.Clients {
		clone.Clients[i] = cloneClient(&srv.Clients[i])
	}
	return clone
}

// cloneClient copies a client along with the two fields a plain assignment
// would only alias: the obfuscation parameters behind the pointer and the
// I-settings map. Without this a client handed out to a caller - or copied
// from its server at creation time - keeps writing through to the original.
func cloneClient(c *api.Client) api.Client {
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
func cloneObfuscationParams(p *api.ObfuscationParams) *api.ObfuscationParams {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}
