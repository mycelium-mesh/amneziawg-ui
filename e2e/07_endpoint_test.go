package e2e

import (
	"strings"
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

// The coordinates of the endpoint editor, on the card and in the dialog.
const (
	cardActionsY    = 267
	endpointButtonX = 480
	endpointFieldX  = 780
	endpointFieldY  = 443
	dialogSaveX     = 793
	dialogSaveY     = 541
)

// The endpoint a client config carries is editable on the server card: the
// dialog sets it, and clearing it puts the server back on the detected public
// IP. The canvas shows the value as text with no field to read, so it is
// checked through the API.
func TestEditServerEndpoint(t *testing.T) {
	a := startApp(t)

	servers := getJSON[[]api.Server](t, "/api/servers")
	if len(servers) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	server := servers[0]
	if server.Endpoint != "" {
		t.Fatalf("endpoint = %q, want it empty before the test", server.Endpoint)
	}

	// "Edit endpoint" on the server card.
	a.click(endpointButtonX, cardActionsY)
	time.Sleep(1500 * time.Millisecond)
	a.shot("12-endpoint-dialog")

	// The endpoint field, then the value.
	a.click(endpointFieldX, endpointFieldY)
	a.typeText("vpn.example.com")
	a.shot("13-endpoint-typed")

	// Save.
	a.click(dialogSaveX, dialogSaveY)

	eventually(t, 30*time.Second, "the endpoint never changed", func() bool {
		return serverEndpoint(t, server.ID) == "vpn.example.com"
	})

	// The config of a client now points at the host the operator set.
	clients := getJSON[[]api.Client](t, "/api/servers/"+server.ID+"/clients")
	if len(clients) == 0 {
		t.Fatal("TestAddClient must run first")
	}
	config := string(apiGet(t, "/api/servers/"+server.ID+"/clients/"+clients[0].ID+"/config"))
	if !strings.Contains(config, "Endpoint = vpn.example.com:") {
		t.Errorf("the client config does not carry the endpoint:\n%s", config)
	}

	// Clearing it puts the server back on the detected public IP.
	a.click(endpointButtonX, cardActionsY)
	time.Sleep(1500 * time.Millisecond)
	a.click(endpointFieldX, endpointFieldY)
	a.press("Control+a")
	a.press("Delete")
	a.click(dialogSaveX, dialogSaveY)

	eventually(t, 30*time.Second, "the endpoint was never cleared", func() bool {
		return serverEndpoint(t, server.ID) == ""
	})

	// With the endpoint cleared, the config follows the detected public IP
	// again - the promise the whole feature rests on.
	servers = getJSON[[]api.Server](t, "/api/servers")
	publicIP := servers[0].PublicIP
	if publicIP == "" {
		t.Fatal("the server has no public IP")
	}
	config = string(apiGet(t, "/api/servers/"+server.ID+"/clients/"+clients[0].ID+"/config"))
	if !strings.Contains(config, "Endpoint = "+publicIP+":") {
		t.Errorf("the client config does not follow the public IP %s:\n%s", publicIP, config)
	}

	a.noPageErrors()
}

// serverEndpoint reads one server's endpoint from the server list.
func serverEndpoint(t *testing.T, serverID string) string {
	t.Helper()
	for _, srv := range getJSON[[]api.Server](t, "/api/servers") {
		if srv.ID == serverID {
			return srv.Endpoint
		}
	}
	t.Fatalf("server %s is gone", serverID)
	return ""
}
