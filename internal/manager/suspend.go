package manager

import (
	"fmt"
	"time"

	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

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
// config and marks it suspended, all under a single write lock, so the file
// and the in-memory status cannot disagree.
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

	if err := wgconf.ParkPeer(srv.ConfigPath, client); err != nil {
		return "", err
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

	if err := wgconf.RestorePeer(srv.ConfigPath, client); err != nil {
		return "", err
	}
	client.Status = "active"

	return srv.Interface, nil
}

// UpdateClientSuspendTime sets or clears the scheduled auto-suspend time.
func (m *Manager) UpdateClientSuspendTime(serverID, clientID string, suspendAt *float64) (*api.Client, string, error) {
	clientCopy, err := m.updateClientLocked(serverID, clientID, func(client *api.Client) {
		client.SuspendAt = suspendAt
	})
	if err != nil {
		return nil, "", err
	}

	m.saveOrLog("client suspend time")
	return clientCopy, "suspension time updated", nil
}

// pendingSuspension identifies a client that has reached its scheduled
// auto-suspend time.
type pendingSuspension struct {
	serverID, clientID string
}

// runSuspender suspends clients as their scheduled time passes. It runs for
// the life of the process.
func (m *Manager) runSuspender() {
	ticker := time.NewTicker(m.settings.SuspendCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.suspendDue(time.Now())
	}
}

// suspendDue is one pass of the suspender.
func (m *Manager) suspendDue(now time.Time) {
	for _, it := range m.findClientsPastSuspendTime(float64(now.Unix())) {
		ifaceName, err := m.autoSuspendLocked(it.serverID, it.clientID, float64(now.Unix()))
		if err != nil {
			fmt.Printf("Auto-suspend failed for client %s: %v\n", it.clientID, err)
			continue
		}
		if ifaceName == "" {
			continue
		}
		m.saveOrLog("client auto-suspend")
		m.syncLiveConfig(ifaceName)
		fmt.Printf("Auto-suspended client %s at %s\n", it.clientID, now.Format(time.RFC1123))
	}
}

// autoSuspendLocked suspends a client whose scheduled time has passed and
// clears the schedule, so the time fires once: a client switched back on by
// hand afterwards stays on instead of being suspended again on the next tick.
// The schedule is checked again under the write lock, since it may have been
// moved or cleared since findClientsPastSuspendTime read it; an empty
// ifaceName means there was nothing to do.
func (m *Manager) autoSuspendLocked(serverID, clientID string, now float64) (ifaceName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv, client := m.findClient(serverID, clientID)
	if srv == nil || client == nil || client.Status != "active" || client.SuspendAt == nil || now < *client.SuspendAt {
		return "", nil
	}

	if err := wgconf.ParkPeer(srv.ConfigPath, client); err != nil {
		return "", err
	}
	client.Status = "suspended"
	client.SuspendAt = nil

	return srv.Interface, nil
}

// findClientsPastSuspendTime locks and returns all active clients whose
// scheduled suspend time has passed.
func (m *Manager) findClientsPastSuspendTime(now float64) []pendingSuspension {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []pendingSuspension
	for _, srv := range m.cfg.Servers {
		for _, c := range srv.Clients {
			if c.SuspendAt != nil && c.Status == "active" && now >= *c.SuspendAt {
				items = append(items, pendingSuspension{srv.ID, c.ID})
			}
		}
	}
	return items
}
