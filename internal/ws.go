package internal

import (
	"fmt"
	"time"

	socket "github.com/zishang520/socket.io/servers/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

// SocketHub manages Socket.IO connections and broadcasting.
type SocketHub struct {
	mgr *Manager
	io  *socket.Server
}

// NewHub creates and returns a new SocketHub.
func NewHub(mgr *Manager) *SocketHub {
	opts := socket.DefaultServerOptions()
	opts.SetCors(&types.Cors{Origin: "*", Credentials: true})
	sio := socket.NewServer(nil, opts)
	h := &SocketHub{mgr: mgr, io: sio}
	h.registerConnectionHandlers()
	return h
}

// Server returns the underlying socket.io server for mounting in main.go.
func (h *SocketHub) Server() *socket.Server {
	return h.io
}

// BroadcastServerStatus satisfies the HubBroadcaster interface.
func (h *SocketHub) BroadcastServerStatus(serverID, status string) {
	h.io.Emit("server_status", ServerStatusEvent{
		ServerID: serverID,
		Status:   status,
	}) //nolint:errcheck
}

// registerConnectionHandlers sets up socket.io connection event handlers.
func (h *SocketHub) registerConnectionHandlers() {
	h.io.On("connection", func(clients ...interface{}) {
		if len(clients) == 0 {
			return
		}
		client, ok := clients[0].(*socket.Socket)
		if !ok {
			return
		}

		// Send initial status to the connecting client.
		client.Emit("status", StatusEvent{ //nolint:errcheck
			Message:  "Connected to AmneziaWG Web UI",
			PublicIP: h.mgr.PublicIP(),
			Port:     h.mgr.WebUIPort,
		})

		// Send current traffic snapshot.
		if data := h.buildTrafficData(); data != nil {
			client.Emit("traffic_update", data) //nolint:errcheck
		}

		client.On("disconnect", func(...interface{}) {
			fmt.Printf("Socket.IO client disconnected: %s\n", client.Id())
		})
	})
}

// RunBroadcaster is kept for API compatibility; setup is done in NewHub.
func (h *SocketHub) RunBroadcaster() {}

// StartTrafficUpdates periodically broadcasts traffic data to all connected clients.
func (h *SocketHub) StartTrafficUpdates() {
	ticker := time.NewTicker(time.Duration(h.mgr.TrafficUpdateInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if data := h.buildTrafficData(); data != nil {
			h.io.Emit("traffic_update", data) //nolint:errcheck
		}
	}
}

// buildTrafficData returns the periodic snapshot, or nil when there is
// nothing to report.
func (h *SocketHub) buildTrafficData() *TrafficEvent {
	servers := h.mgr.copyServers()

	clientTraffic := map[string]map[string]ClientTraffic{}
	for _, srv := range servers {
		if t := h.mgr.GetPeerTrafficForServer(srv.ID); len(t) > 0 {
			clientTraffic[srv.ID] = t
		}
	}
	serverTraffic := h.mgr.GetAllServersTraffic()

	if len(clientTraffic) == 0 && len(serverTraffic) == 0 {
		return nil
	}

	return &TrafficEvent{
		Timestamp:     float64(time.Now().Unix()),
		ClientTraffic: clientTraffic,
		ServerTraffic: serverTraffic,
	}
}
