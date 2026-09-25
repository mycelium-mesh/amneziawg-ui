package e2e

import (
	"strings"
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

func TestClientConfig(t *testing.T) {
	a := startApp(t)

	servers := getJSON[[]api.Server](t, "/api/servers")
	if len(servers) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	server := servers[0]
	clients := getJSON[[]api.Client](t, "/api/servers/"+server.ID+"/clients")
	if len(clients) == 0 {
		t.Fatal("TestAddClient must run first")
	}
	clientPath := "/api/servers/" + server.ID + "/clients/" + clients[0].ID

	// The backend has to offer both views the dialog renders, and the download
	// has to carry the full config with the obfuscation parameters.
	configs := getJSON[api.ClientConfigs](t, clientPath+"/config-both")
	if !strings.Contains(configs.CleanConfig, "[Interface]") {
		t.Errorf("clean_config has no [Interface]:\n%s", configs.CleanConfig)
	}
	link := getJSON[api.AmneziaLink](t, clientPath+"/link")
	if !strings.HasPrefix(link.VPNURL, "vpn://") {
		t.Errorf("vpn_url = %q, want a vpn:// link", link.VPNURL)
	}
	if download := string(apiGet(t, clientPath+"/config")); !strings.Contains(download, "Jc =") {
		t.Errorf("the downloaded config has no Jc:\n%s", download)
	}

	// "QR / config" on the client row. The dialog opens on the .conf tab,
	// which is the one that gets a QR code.
	a.click(1240, 340)
	time.Sleep(2500 * time.Millisecond)
	a.shot("08-qr-conf")

	// Switch to the AmneziaVPN link tab.
	a.click(407, 205)
	time.Sleep(1500 * time.Millisecond)
	a.shot("09-qr-link")

	// Close, then open the server configuration dialog.
	a.click(749, 800)
	time.Sleep(time.Second)
	a.click(346, 267)
	time.Sleep(2 * time.Second)
	a.shot("11-server-config")

	a.noPageErrors()
}
