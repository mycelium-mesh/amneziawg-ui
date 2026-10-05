package amnezialink

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

func fixture(p *api.ObfuscationParams) (*api.Server, *api.Client) {
	srv := &api.Server{
		ID: "abc123", Name: "srv", Interface: "wg-abc123", Port: 54844,
		Subnet: "10.0.1.0/24", ServerIP: "10.0.1.1", MTU: 1280,
		Endpoint: "vpn.example", ServerPublicKey: "SRVPUB",
		ObfuscationEnabled: true, ObfuscationParams: p,
		DNS: []string{"8.8.8.8", "1.1.1.1"},
	}
	cl := &api.Client{
		ID: "c1", Name: "client", ServerID: srv.ID, ClientIP: "10.0.1.2",
		ClientPrivateKey: "CPRIV", ClientPublicKey: "CPUB", PresharedKey: "PSK",
		ObfuscationEnabled: true, ObfuscationParams: p,
	}
	return srv, cl
}

func decode(t *testing.T, link string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "vpn://"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAwg31FlagsOnInLink(t *testing.T) {
	p := &api.ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280,
		HeaderProtectionKey: "KEY", RandomTrailers: true, DisableCookies: true}
	srv, cl := fixture(p)

	link, err := Build(srv, cl, "vpn.example")
	if err != nil {
		t.Fatal(err)
	}
	root := decode(t, link)
	awg := root["containers"].([]any)[0].(map[string]any)["awg"].(map[string]any)
	if awg["protocol_version"] != "3.1" {
		t.Fatalf("protocol_version = %v, want 3.1", awg["protocol_version"])
	}
	if awg["RandomTrailers"] != "on" || awg["DisableCookies"] != "on" {
		t.Fatalf("link missing 3.1 switches: %v", awg)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(awg["last_config"].(string)), &last); err != nil {
		t.Fatal(err)
	}
	if last["RandomTrailers"] != "on" || last["hostName"] != "vpn.example" {
		t.Fatalf("last_config wrong: %v", last)
	}
	if root["dns1"] != "8.8.8.8" || root["dns2"] != "1.1.1.1" {
		t.Errorf("dns keys = %v / %v", root["dns1"], root["dns2"])
	}
}

func TestAwg31FlagsOffAreOmitted(t *testing.T) {
	p := &api.ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280, HeaderProtectionKey: "KEY"}
	srv, cl := fixture(p)

	link, _ := Build(srv, cl, "vpn.example")
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "vpn://"))
	if strings.Contains(string(raw), "RandomTrailers") || strings.Contains(string(raw), "DisableCookies") {
		t.Fatalf("off flags leaked into link:\n%s", raw)
	}
}

// The link follows the app's own client export: the .conf text under
// "config" and no third-party flag, which would hide the connection settings
// page where the user can correct the MTU the app resets on import.
func TestLinkMatchesNativeExport(t *testing.T) {
	srv, cl := fixture(&api.ObfuscationParams{S1: 50, S2: 60, MTU: 1280})

	link, err := Build(srv, cl, "vpn.example")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "vpn://"))
	if strings.Contains(string(raw), "isThirdPartyConfig") {
		t.Fatalf("link carries isThirdPartyConfig:\n%s", raw)
	}

	awg := decode(t, link)["containers"].([]any)[0].(map[string]any)["awg"].(map[string]any)
	var last map[string]any
	if err := json.Unmarshal([]byte(awg["last_config"].(string)), &last); err != nil {
		t.Fatal(err)
	}
	if last["mtu"] != "1280" {
		t.Errorf("mtu = %v, want 1280", last["mtu"])
	}
	conf, _ := last["config"].(string)
	for _, want := range []string{"[Interface]", "MTU = 1280", "PrivateKey = CPRIV", "Endpoint = vpn.example:54844"} {
		if !strings.Contains(conf, want) {
			t.Errorf("config missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "#") {
		t.Errorf("config carries comments:\n%s", conf)
	}
}
