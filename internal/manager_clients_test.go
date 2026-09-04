package internal

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// newClientManager builds a manager whose single server has a real .conf on
// disk, which is all AddClient needs: key generation falls back to
// crypto-free random keys when awg is not installed, so this runs anywhere.
func newClientManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	confPath := dir + "/wg-s1.conf"
	if err := os.WriteFile(confPath, []byte("[Interface]\nPrivateKey = PRIV\nS1 = 50\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Manager{
		configFile: dir + "/web_config.json",
		Config: &AppConfig{Servers: []Server{{
			ID: "s1", Name: "srv", Interface: "wg-test-absent", ConfigPath: confPath,
			Subnet: "10.0.1.0/24", ServerIP: "10.0.1.1", MTU: 1280, Port: 54844,
			Status: "stopped", ObfuscationEnabled: true,
			ObfuscationParams: &ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280},
			Clients:           []Client{},
		}}},
	}
}

func TestAddClientDetachesObfuscationParamsFromTheServer(t *testing.T) {
	m := newClientManager(t)

	client, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if client.ObfuscationParams == nil {
		t.Fatal("client got no obfuscation params")
	}

	// Retuning the server must not rewrite what an already-issued client was
	// handed: both ends have to keep agreeing on the values in its config.
	m.Config.Servers[0].ObfuscationParams.S1 = 999

	stored, ok := m.getClientInServer("s1", client.ID)
	if !ok {
		t.Fatal("client vanished from its server")
	}
	if stored.ObfuscationParams.S1 != 50 {
		t.Errorf("server edit leaked into the stored client: S1 = %d, want 50", stored.ObfuscationParams.S1)
	}
	if client.ObfuscationParams.S1 != 50 {
		t.Errorf("server edit leaked into the returned client: S1 = %d, want 50", client.ObfuscationParams.S1)
	}
}

func TestClientLivesOnlyInItsServer(t *testing.T) {
	m := newClientManager(t)

	client, _, err := m.AddClient("s1", "alice", true, map[string]string{"i2": "custom"}, "")
	if err != nil {
		t.Fatal(err)
	}

	if got := m.clientCount(); got != 1 {
		t.Errorf("clientCount = %d, want 1", got)
	}
	all := m.GetClientConfigs("")
	perServer := m.GetClientConfigs("s1")
	if len(all) != 1 || len(perServer) != 1 {
		t.Fatalf("client listed %d time(s) globally and %d time(s) per server, want 1 and 1", len(all), len(perServer))
	}
	if all[0].ID != client.ID || perServer[0].ID != client.ID {
		t.Errorf("listings disagree on the client: %q / %q, want %q", all[0].ID, perServer[0].ID, client.ID)
	}

	// A returned client is a copy: writing to it must not reach the config.
	all[0].Name = "mutated"
	all[0].ISettings["i2"] = "mutated"
	stored, _ := m.getClientInServer("s1", client.ID)
	if stored.Name != "alice" || stored.ISettings["i2"] != "custom" {
		t.Errorf("caller's edits leaked into the config: %+v", stored)
	}

	if err := m.DeleteClient("s1", client.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if got := m.clientCount(); got != 0 {
		t.Errorf("clientCount after delete = %d, want 0", got)
	}
	if got := len(m.GetClientConfigs("")); got != 0 {
		t.Errorf("deleted client still listed %d time(s)", got)
	}
}

func TestSuspendAndActivateKeepASingleClientRecord(t *testing.T) {
	m := newClientManager(t)

	client, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.SuspendClient("s1", client.ID); err != nil {
		t.Fatalf("SuspendClient: %v", err)
	}
	listed := m.GetClientConfigs("")
	if len(listed) != 1 || listed[0].Status != "suspended" {
		t.Fatalf("after suspend: %+v", listed)
	}

	conf, _ := os.ReadFile(m.Config.Servers[0].ConfigPath)
	if strings.Contains(string(conf), client.ClientPublicKey) {
		t.Errorf("suspended peer still in the server conf:\n%s", conf)
	}

	if _, err := m.ActivateClient("s1", client.ID); err != nil {
		t.Fatalf("ActivateClient: %v", err)
	}
	listed = m.GetClientConfigs("")
	if len(listed) != 1 || listed[0].Status != "active" {
		t.Fatalf("after activate: %+v", listed)
	}
}

// Before peer blocks carried the client ID, deleting one of two clients that
// share a name took both peers out of the server .conf.
func TestDeletingOneOfTwoNamesakesKeepsTheOther(t *testing.T) {
	m := newClientManager(t)

	first, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteClient("s1", first.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}

	conf, err := os.ReadFile(m.Config.Servers[0].ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(conf), first.ClientPublicKey) {
		t.Errorf("deleted peer still in the conf:\n%s", conf)
	}
	if !strings.Contains(string(conf), second.ClientPublicKey) {
		t.Errorf("the namesake's peer was removed too:\n%s", conf)
	}
	if !strings.Contains(string(conf), "S1 = 50") {
		t.Errorf("[Interface] section damaged:\n%s", conf)
	}
	if got := m.clientCount(); got != 1 {
		t.Errorf("clientCount = %d, want 1", got)
	}
}

// A name is free text; it must not be able to inject config directives into
// the peer comment it lands in.
func TestClientNameCannotInjectConfigLines(t *testing.T) {
	m := newClientManager(t)

	client, _, err := m.AddClient("s1", "evil\n[Peer]\nPublicKey = INJECTED", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(client.Name, "\n\r") {
		t.Errorf("stored name still has line breaks: %q", client.Name)
	}

	conf, _ := os.ReadFile(m.Config.Servers[0].ConfigPath)
	if strings.Contains(string(conf), "INJECTED\n") && !strings.Contains(string(conf), "# Client:") {
		t.Errorf("name broke out of its comment:\n%s", conf)
	}
	if got := strings.Count(string(conf), "[Peer]"); got != 1 {
		t.Errorf("[Peer] blocks = %d, want 1:\n%s", got, conf)
	}
}

// Concurrent reads and writes must not race, and every one of them must be
// accounted for. Meaningful under -race.
func TestConcurrentClientChurnIsRaceFree(t *testing.T) {
	m := newClientManager(t)

	const n = 12
	added := make(chan string, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client, _, err := m.AddClient("s1", fmt.Sprintf("client-%d", i), false, nil, "")
			if err != nil {
				t.Errorf("AddClient: %v", err)
				return
			}
			added <- client.ID
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.GetClientConfigs("")
			m.GetClientConfigs("s1")
			m.GetServersWithStatus()
			m.clientCount()
		}()
	}
	wg.Wait()
	close(added)

	if got := m.clientCount(); got != n {
		t.Fatalf("clientCount = %d, want %d", got, n)
	}

	// Every client must have its own address and its own peer block.
	conf, _ := os.ReadFile(m.Config.Servers[0].ConfigPath)
	if got := strings.Count(string(conf), "[Peer]"); got != n {
		t.Errorf("[Peer] blocks = %d, want %d", got, n)
	}
	ips := map[string]bool{}
	for _, c := range m.GetClientConfigs("s1") {
		if ips[c.ClientIP] {
			t.Errorf("duplicate client IP %s", c.ClientIP)
		}
		ips[c.ClientIP] = true
	}

	for id := range added {
		if _, ok := m.getClientInServer("s1", id); !ok {
			t.Errorf("client %s was lost", id)
		}
	}
}
