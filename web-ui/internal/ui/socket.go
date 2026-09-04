package ui

import (
	"encoding/json"
	"time"

	"fyne.io/fyne/v2"

	"github.com/zishang520/socket.io/clients/engine/v3/transports"
	sio "github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"

	"amneziawg-web-ui/web-ui/api"
)

// connectSocket subscribes to the backend's Socket.IO feed. Traffic counters
// and server state changes arrive as events, so the UI does not have to poll.
func (u *UI) connectSocket() {
	opts := sio.DefaultOptions()

	// A wasm build has no TCP stack of its own, so the websocket transport -
	// which dials a socket directly - can never come up. Pin the client to
	// HTTP long polling, which the browser serves through fetch and which
	// carries the page's basic-auth credentials automatically.
	opts.SetTransports(types.NewSet(transports.Polling))
	opts.SetUpgrade(false)
	opts.SetPath("/socket.io")
	opts.SetTimeout(20 * time.Second)
	opts.SetReconnection(true)
	opts.SetReconnectionDelay(1000)
	opts.SetReconnectionDelayMax(5000)

	client, err := sio.Connect(baseURL(), opts)
	if err != nil {
		fyne.LogError("socket.io connect failed", err)
		u.setTransport("polling", colWarning)
		return
	}
	u.socket = client

	client.On("connect", func(...any) {
		u.socketLive.Store(true)
		u.setTransport("live", colSuccess)
		go u.reloadServers()
	})

	client.On("disconnect", func(...any) {
		u.socketLive.Store(false)
		u.setTransport("reconnecting…", colWarning)
	})

	client.On("connect_error", func(...any) {
		u.socketLive.Store(false)
		u.setTransport("polling", colWarning)
	})

	client.On("status", func(args ...any) {
		var status api.StatusEvent
		if !decodeEvent(args, &status) {
			return
		}
		if status.PublicIP != "" {
			u.setPublicIP(status.PublicIP)
		}
	})

	// The backend emits this whenever an interface goes up or down, including
	// changes made from another browser tab.
	client.On("server_status", func(...any) {
		go u.reloadServers()
	})

	client.On("traffic_update", func(args ...any) {
		var update api.TrafficEvent
		if !decodeEvent(args, &update) {
			return
		}
		u.applyTraffic(update.ServerTraffic, update.ClientTraffic)
	})
}

// decodeEvent re-marshals a Socket.IO argument (a decoded JSON value) into a
// typed struct, which is cheaper to maintain than walking map[string]any.
func decodeEvent(args []any, out any) bool {
	if len(args) == 0 || args[0] == nil {
		return false
	}
	data, err := json.Marshal(args[0])
	if err != nil {
		fyne.LogError("could not re-encode socket payload", err)
		return false
	}
	if err := json.Unmarshal(data, out); err != nil {
		fyne.LogError("could not decode socket payload", err)
		return false
	}
	return true
}
