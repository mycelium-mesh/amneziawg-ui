package internal

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func newSmokeManager(p *ObfuscationParams) (*Manager, *Client) {
	srv := Server{
		ID: "abc123", Name: "srv", Interface: "wg-abc123", Port: 54844,
		Subnet: "10.0.1.0/24", ServerIP: "10.0.1.1", MTU: 1280,
		Endpoint: "vpn.example", ServerPublicKey: "SRVPUB",
		ObfuscationEnabled: true, ObfuscationParams: p,
		DNS: []string{"8.8.8.8", "1.1.1.1"},
	}
	cl := &Client{
		ID: "c1", Name: "client", ServerID: srv.ID, ClientIP: "10.0.1.2",
		ClientPrivateKey: "CPRIV", ClientPublicKey: "CPUB", PresharedKey: "PSK",
		ObfuscationEnabled: true, ObfuscationParams: p,
	}
	srv.Clients = []Client{*cl}
	return &Manager{Config: &AppConfig{Servers: []Server{srv}}}, cl
}

func TestAwg31FlagsOnInClientConfigAndLink(t *testing.T) {
	p := &ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280,
		HeaderProtectionKey: "KEY", RandomTrailers: true, DisableCookies: true}
	m, cl := newSmokeManager(p)

	conf := m.GenerateClientConfig("abc123", cl, false)
	if !strings.Contains(conf, "RandomTrailers = on\n") || !strings.Contains(conf, "DisableCookies = on\n") {
		t.Fatalf("client conf missing 3.1 switches:\n%s", conf)
	}

	link, err := m.GenerateAmneziaVpnURL("abc123", cl)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "vpn://"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
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
	if last["RandomTrailers"] != "on" {
		t.Fatalf("last_config missing RandomTrailers: %v", last)
	}
}

func TestAwg31FlagsOffAreOmitted(t *testing.T) {
	p := &ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280, HeaderProtectionKey: "KEY"}
	m, cl := newSmokeManager(p)

	conf := m.GenerateClientConfig("abc123", cl, false)
	if strings.Contains(conf, "RandomTrailers") || strings.Contains(conf, "DisableCookies") {
		t.Fatalf("off flags leaked into conf:\n%s", conf)
	}

	link, _ := m.GenerateAmneziaVpnURL("abc123", cl)
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "vpn://"))
	if strings.Contains(string(raw), "RandomTrailers") || strings.Contains(string(raw), "DisableCookies") {
		t.Fatalf("off flags leaked into link:\n%s", raw)
	}
}

func TestGeneratedParamsEnable31(t *testing.T) {
	m := &Manager{Config: &AppConfig{}}
	p := m.generateObfuscationParams(1280)
	if !p.RandomTrailers || !p.DisableCookies {
		t.Fatalf("generated params should enable 3.1 switches: %+v", p)
	}
	if err := validateObfuscationParams(&p); err != nil {
		t.Fatalf("generated params invalid: %v", err)
	}
}
