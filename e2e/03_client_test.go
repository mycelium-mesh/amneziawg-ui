package e2e

import (
	"strings"
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

func TestAddClient(t *testing.T) {
	a := startApp(t)

	servers := getJSON[[]api.Server](t, "/api/servers")
	if len(servers) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	server := servers[0]
	clientsPath := "/api/servers/" + server.ID + "/clients"

	// "Add client" on the server card.
	a.click(225, 267)
	time.Sleep(1200 * time.Millisecond)
	a.shot("06-client-dialog")

	a.click(800, 233)
	a.typeText("laptop")
	a.click(807, 751)

	eventually(t, 60*time.Second, "the client never showed up in "+clientsPath, func() bool {
		return len(getJSON[[]api.Client](t, clientsPath)) == 1
	})

	client := getJSON[[]api.Client](t, clientsPath)[0]
	if client.Name != "laptop" {
		t.Errorf("name = %q, want %q", client.Name, "laptop")
	}
	if !strings.HasPrefix(client.ClientIP, "10.0.0.") {
		t.Errorf("client_ip = %q, want one in 10.0.0.0/24", client.ClientIP)
	}

	time.Sleep(4 * time.Second)
	a.shot("07-client-added")
	a.noPageErrors()
}
