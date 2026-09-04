package internal

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The interface the fixture names does not exist, so "stopped" is the truth
// and whatever the config says about it is not.
func TestStatusComesFromTheKernelNotTheConfig(t *testing.T) {
	m := newClientManager(t)
	m.Config.Servers[0].Status = "running"

	if got := m.GetServerStatus("s1"); got != "stopped" {
		t.Errorf("GetServerStatus = %q, want %q", got, "stopped")
	}
	if got := m.GetServerStatus("nope"); got != "not_found" {
		t.Errorf("unknown server = %q, want not_found", got)
	}
}

// One dashboard refresh asks about every server, and the traffic broadcaster
// asks again seconds later; the shell command behind that is answered once.
func TestServerStatusIsCachedBriefly(t *testing.T) {
	m := newClientManager(t)
	iface := m.Config.Servers[0].Interface

	if got := m.serverStatus(iface); got != "stopped" {
		t.Fatalf("first lookup = %q", got)
	}

	// A cached observation is returned as-is, even one the kernel would
	// contradict.
	m.noteServerStatus(iface, "running")
	if got := m.serverStatus(iface); got != "running" {
		t.Errorf("cached lookup = %q, want the noted %q", got, "running")
	}

	// ...but only until it expires.
	m.statusMu.Lock()
	m.statuses[iface] = statusObservation{status: "running", at: time.Now().Add(-2 * statusTTL)}
	m.statusMu.Unlock()
	if got := m.serverStatus(iface); got != "stopped" {
		t.Errorf("expired lookup = %q, want a fresh %q", got, "stopped")
	}
}

// A peer added while the config still says "stopped" but the interface is up
// must be pushed onto the live interface, and vice versa: the stored field
// must not decide this.
func TestLiveSyncFollowsTheInterfaceNotTheStoredStatus(t *testing.T) {
	m := newClientManager(t)
	iface := m.Config.Servers[0].Interface

	m.Config.Servers[0].Status = "running" // stale: the interface is absent
	client, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// The peer is in the file either way; what must not happen is the
	// bookkeeping claiming the interface was synced.
	conf, _ := os.ReadFile(m.Config.Servers[0].ConfigPath)
	if !strings.Contains(string(conf), client.ClientPublicKey) {
		t.Errorf("peer missing from the conf:\n%s", conf)
	}
	if got := m.serverStatus(iface); got != "stopped" {
		t.Errorf("status = %q: a stale config field decided it", got)
	}
}

// Start and stop record what they just did, so the next reader sees it
// without waiting for the cache to expire or racing the kernel.
func TestStartAndStopRecordTheirOwnObservation(t *testing.T) {
	m := newClientManager(t)
	iface := m.Config.Servers[0].Interface

	m.noteServerStatus(iface, "running")
	if got := m.GetServerStatus("s1"); got != "running" {
		t.Errorf("noted status = %q, want running", got)
	}

	m.forgetServerStatus(iface)
	if got := m.GetServerStatus("s1"); got != "stopped" {
		t.Errorf("after forgetting, status = %q, want a fresh stopped", got)
	}
}
