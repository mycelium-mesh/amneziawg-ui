package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

// TestCreateServerRejectsAPortAnotherServerAlreadyUses covers the check that
// runs before anything is written: two interfaces on one UDP port would only
// fail much later, when the second one cannot bind.
func TestCreateServerRejectsAPortAnotherServerAlreadyUses(t *testing.T) {
	m, _ := newTestManager(t) // its one server, "srv", listens on 54844

	_, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54844, MTU: 1420})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), `"srv"`) {
		t.Fatalf("error should name the server holding the port, got %v", err)
	}
	if got := len(m.cfg.Servers); got != 1 {
		t.Fatalf("nothing should have been created, got %d servers", got)
	}
}

// The panel's own port counts as taken too: publishing one number for both
// the web UI and a tunnel is a trap even though TCP and UDP would coexist.
func TestCreateServerRejectsTheWebUIsOwnPort(t *testing.T) {
	m, _ := newTestManager(t)

	_, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54845, MTU: 1420})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "web UI") {
		t.Fatalf("error should say what holds the port, got %v", err)
	}
}

// A port awg-quick cannot bind is a bad request: accepted, it would be saved
// and then fail only when the server is started.
func TestCreateServerRejectsAPortOutOfRange(t *testing.T) {
	m, _ := newTestManager(t)

	for _, port := range []int{-5, api.MaxPort + 1} {
		_, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: port, MTU: 1420})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("port %d: want ErrInvalid, got %v", port, err)
		}
	}
	if got := len(m.cfg.Servers); got != 1 {
		t.Fatalf("nothing should have been created, got %d servers", got)
	}
}

// A subnet sharing addresses with another server's is a conflict, whether it
// is the same block or one that contains or sits inside it.
func TestCreateServerRejectsAnOverlappingSubnet(t *testing.T) {
	m, _ := newTestManager(t) // "srv" holds 10.0.1.0/24

	for _, subnet := range []string{"10.0.1.0/24", "10.0.0.0/16", "10.0.1.128/25"} {
		_, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54846, Subnet: subnet, MTU: 1420})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("subnet %s: want ErrConflict, got %v", subnet, err)
		}
		if !strings.Contains(err.Error(), `"srv"`) {
			t.Fatalf("subnet %s: error should name the server holding it, got %v", subnet, err)
		}
	}
	if got := len(m.cfg.Servers); got != 1 {
		t.Fatalf("nothing should have been created, got %d servers", got)
	}
}

// A subnet that is not an IPv4 block with room for a client is a bad request.
func TestCreateServerRejectsAMalformedSubnet(t *testing.T) {
	m, _ := newTestManager(t)

	for _, subnet := range []string{"abc", "10.0.2.0", "10.0.2.0/31", "fd00::/64"} {
		_, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54846, Subnet: subnet, MTU: 1420})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("subnet %q: want ErrInvalid, got %v", subnet, err)
		}
	}
}

// A server name lands in the archive's file name, so a name that cannot be
// a file name on the desktops people unpack an archive on is refused at the
// input: the file would not carry the name the user gave it.
func TestCreateServerRefusesANameThatCannotBeAFileName(t *testing.T) {
	m, _ := newTestManager(t)

	for _, bad := range []string{
		"a<b", "a>b", "a:b", `a"b`, "a|b", "a?b", "a*b", "a/b", `a\b`,
		"name.", "name ",
	} {
		_, err := m.CreateServer(api.CreateServerRequest{Name: bad, Port: 54846, Subnet: "10.0.2.0/24"})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateServer(%q): error = %v, want ErrInvalid", bad, err)
		}
	}
	if got := len(m.cfg.Servers); got != 1 {
		t.Fatalf("nothing should have been created, got %d servers", got)
	}

	if _, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54846, Subnet: "10.0.2.0/24"}); err != nil {
		t.Fatalf("a usable name was refused: %v", err)
	}
}

func TestPortInUse(t *testing.T) {
	m, _ := newTestManager(t)

	if holder, ok := m.portInUse(54844); !ok || holder != `server "srv"` {
		t.Errorf(`portInUse(54844) = %q, %v; want server "srv", true`, holder, ok)
	}
	if holder, ok := m.portInUse(54845); !ok || holder != "the web UI" {
		t.Errorf(`portInUse(54845) = %q, %v; want the web UI, true`, holder, ok)
	}
	if holder, ok := m.portInUse(54846); ok {
		t.Errorf("portInUse(54846) = %q, %v; want the port to be free", holder, ok)
	}
}

// A server created without an explicit endpoint stays on the auto-detected
// public IP: its Endpoint must remain empty, or endpointFor would pin every
// client config to the address the host had at creation time. One created
// with an endpoint keeps it, and the detected IP lands only in PublicIP -
// never in place of the manual value.
func TestCreateServerEndpointStaysAutoUnlessAsked(t *testing.T) {
	m, _ := newTestManager(t)
	m.setPublicIP("203.0.113.7")

	auto, err := m.CreateServer(api.CreateServerRequest{Name: "auto", Port: 54846, Subnet: "10.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if auto.PublicIP != "203.0.113.7" {
		t.Errorf("PublicIP = %q, want the detected one", auto.PublicIP)
	}
	if auto.Endpoint != "" {
		t.Errorf("Endpoint = %q, want it empty so configs follow PublicIP", auto.Endpoint)
	}

	m.setPublicIP("203.0.113.8")
	manual, err := m.CreateServer(api.CreateServerRequest{Name: "manual", Port: 54847, Subnet: "10.0.3.0/24", Endpoint: "vpn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if manual.Endpoint != "vpn.example.com" {
		t.Errorf("Endpoint = %q, want the requested one", manual.Endpoint)
	}
	if manual.PublicIP != "203.0.113.8" {
		t.Errorf("PublicIP = %q, want the detected one, not the manual endpoint", manual.PublicIP)
	}
}

// Setting an endpoint pins client configs to it; clearing sends them back to
// the public IP stamped on the server, which is what RefreshPublicIP keeps
// current. Editing the endpoint never moves PublicIP, whitespace-only input
// counts as empty, not as a hostname, and a server nothing ever stamped
// falls back to the manager-wide detected address.
func TestUpdateServerEndpointSetsAndClears(t *testing.T) {
	m, _ := newTestManager(t)
	m.setPublicIP("203.0.113.7")
	m.cfg.Servers[0].PublicIP = "198.51.100.2"

	if _, err := m.UpdateServerEndpoint("s1", "vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	srv, ok := m.Server("s1")
	if !ok || srv.Endpoint != "vpn.example.com" {
		t.Fatalf("Endpoint = %q, %v; want vpn.example.com", srv.Endpoint, ok)
	}
	if got := m.endpointFor(&srv); got != "vpn.example.com" {
		t.Errorf("endpointFor = %q, want the manual one", got)
	}

	if _, err := m.UpdateServerEndpoint("s1", "   "); err != nil {
		t.Fatal(err)
	}
	srv, _ = m.Server("s1")
	if srv.Endpoint != "" {
		t.Errorf("Endpoint = %q, want it cleared", srv.Endpoint)
	}
	if got := m.endpointFor(&srv); got != "198.51.100.2" {
		t.Errorf("endpointFor = %q, want the stamped PublicIP", got)
	}
	if srv.PublicIP != "198.51.100.2" {
		t.Errorf("PublicIP = %q, want it untouched by the endpoint edit", srv.PublicIP)
	}

	// A server whose PublicIP was never stamped: the last detected address
	// the manager holds is the next fallback.
	if got := m.endpointFor(&api.Server{ID: "s2"}); got != "203.0.113.7" {
		t.Errorf("endpointFor without a stamp = %q, want the manager-wide IP", got)
	}

	if _, err := m.UpdateServerEndpoint("nope", "vpn.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown server: want ErrNotFound, got %v", err)
	}
}

// An endpoint that is not a host is refused: it lands in the generated .conf
// and in the vpn:// link as it is, so a newline or a port inside it breaks
// the config the client imports.
func TestUpdateServerEndpointRefusesABrokenHost(t *testing.T) {
	m, _ := newTestManager(t)

	for _, bad := range []string{
		"h\nPostUp = x",
		"vpn.example.com:51820",
		"2001:db8::1",
		"host name",
		"https://vpn.example.com",
	} {
		if _, err := m.UpdateServerEndpoint("s1", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("endpoint %q: want ErrInvalid, got %v", bad, err)
		}
	}
	srv, _ := m.Server("s1")
	if srv.Endpoint != "" {
		t.Errorf("stored Endpoint = %q, want it untouched", srv.Endpoint)
	}
}

// A server created with an endpoint that is not a host is refused, the same
// way the update route refuses one: the value would land in the client .conf
// as it is.
func TestCreateServerRefusesABrokenEndpoint(t *testing.T) {
	m, _ := newTestManager(t)
	m.setPublicIP("203.0.113.7")

	for _, bad := range []string{
		"h\nPostUp = x",
		"vpn.example.com:51820",
		"2001:db8::1",
		"host name",
	} {
		_, err := m.CreateServer(api.CreateServerRequest{
			Name: "bad", Port: 54846, Subnet: "10.0.2.0/24", Endpoint: bad,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("endpoint %q: want ErrInvalid, got %v", bad, err)
		}
	}
}

// A change the operator asked for is not reported as done when it did not
// reach the disk: the error goes to the caller and the in-memory config is
// put back, so the UI and the config agree about what is stored.
func TestUpdateServerEndpointReportsAFailedSave(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.UpdateServerEndpoint("s1", "vpn.example.com"); err != nil {
		t.Fatal(err)
	}

	// Taking the config file's directory away makes the write fail.
	if err := os.RemoveAll(filepath.Dir(m.settings.ConfigFile)); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateServerEndpoint("s1", "other.example.com"); err == nil {
		t.Fatal("a failed save must be reported")
	}
	srv, _ := m.Server("s1")
	if srv.Endpoint != "vpn.example.com" {
		t.Errorf("stored Endpoint = %q, want the value from before the failed save", srv.Endpoint)
	}
}

// A boot under a changed address has to reach the servers, not only the
// manager: endpointFor reads the server's own copy first, so a server left
// holding the address the host had at some earlier boot keeps handing that
// address to its clients.
func TestSetPublicIPStampsEveryServer(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.Servers[0].PublicIP = "198.51.100.4"

	m.setPublicIP("198.51.100.9")

	srv, _ := m.Server("s1")
	if srv.PublicIP != "198.51.100.9" {
		t.Errorf("PublicIP = %q, want the freshly detected one", srv.PublicIP)
	}
	if got := m.endpointFor(&srv); got != "198.51.100.9" {
		t.Errorf("endpointFor = %q, want the fresh address", got)
	}
}

// With no address anywhere, the config says so in words instead of rendering
// a host-less "Endpoint = :54844".
func TestEndpointForWithoutAnyAddress(t *testing.T) {
	m, _ := newTestManager(t)
	m.publicIP = ""

	if got := m.endpointFor(&api.Server{ID: "s2"}); got != api.EndpointUnknown {
		t.Errorf("endpointFor = %q, want the placeholder", got)
	}
}

// A created server gets a .conf whose [Interface] carries the same
// obfuscation lines its clients will, and is brought up when asked.
func TestCreateServerWritesTheConfAndAutoStarts(t *testing.T) {
	m, run := newTestManager(t)
	run.Stub("awg genkey", "SPRIV").Stub("echo 'SPRIV' | awg pubkey", "SPUB")
	run.Stub("/usr/bin/awg-quick up wg-", "")
	yes := true

	srv, err := m.CreateServer(api.CreateServerRequest{Name: "second", Port: 54846, Subnet: "10.0.2.0/24", AutoStart: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ServerPublicKey != "SPUB" || srv.ServerIP != "10.0.2.1" || !srv.ObfuscationEnabled {
		t.Errorf("server = %+v", srv)
	}

	conf, err := os.ReadFile(srv.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"PrivateKey = SPRIV", "Address = 10.0.2.1/24", "ListenPort = 54846", "HeaderProtectionKey = "} {
		if !strings.Contains(string(conf), line) {
			t.Errorf("conf missing %q:\n%s", line, conf)
		}
	}

	if !run.Ran("/usr/bin/awg-quick up " + srv.Interface) {
		t.Errorf("the server was not brought up: %v", run.Commands)
	}
	if got := m.ServerStatus(srv.ID); got != "running" {
		t.Errorf("status after auto-start = %q", got)
	}
}

func TestDeleteServerStopsARunningInterfaceFirst(t *testing.T) {
	m, run := newTestManager(t)
	run.Stub("ip link show wg-test-absent", "wg-test-absent: state UNKNOWN")
	run.Stub("/usr/bin/awg-quick down wg-test-absent", "")
	confPath := m.cfg.Servers[0].ConfigPath

	if err := m.DeleteServer("s1"); err != nil {
		t.Fatal(err)
	}
	if !run.Ran("/usr/bin/awg-quick down wg-test-absent") {
		t.Errorf("the interface was left up: %v", run.Commands)
	}
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		t.Errorf("the .conf was left behind (err %v)", err)
	}
	if _, ok := m.Server("s1"); ok {
		t.Error("the server is still listed")
	}
}
