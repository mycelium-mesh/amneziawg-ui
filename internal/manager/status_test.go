package manager

import (
	"os"
	"strings"
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

// The interface the fixture names does not exist, so "stopped" is the truth
// and whatever the config says about it is not.
func TestStatusComesFromTheKernelNotTheConfig(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.Servers[0].Status = "running"

	if got := m.ServerStatus("s1"); got != "stopped" {
		t.Errorf("ServerStatus = %q, want %q", got, "stopped")
	}
	if got := m.ServerStatus("nope"); got != "not_found" {
		t.Errorf("unknown server = %q, want not_found", got)
	}
}

// One dashboard refresh asks about every server, and the traffic poll asks
// again seconds later; the shell command behind that is answered once.
func TestServerStatusIsCachedBriefly(t *testing.T) {
	m, run := newTestManager(t)
	iface := m.cfg.Servers[0].Interface

	if got := m.serverStatus(iface); got != "stopped" {
		t.Fatalf("first lookup = %q", got)
	}
	asked := len(run.Commands)
	m.serverStatus(iface)
	if len(run.Commands) != asked {
		t.Errorf("the kernel was asked again within the TTL: %v", run.Commands)
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
	m, run := newTestManager(t)
	iface := m.cfg.Servers[0].Interface

	m.cfg.Servers[0].Status = "running" // stale: the interface is absent
	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	// The peer is in the file either way; what must not happen is the
	// bookkeeping claiming the interface was synced.
	conf, _ := os.ReadFile(m.cfg.Servers[0].ConfigPath)
	if !strings.Contains(string(conf), client.ClientPublicKey) {
		t.Errorf("peer missing from the conf:\n%s", conf)
	}
	if run.Ran("bash -c 'strip=$(mktemp)") {
		t.Errorf("syncconf was attempted on a down interface: %v", run.Commands)
	}

	// Now the kernel has the interface, the config says otherwise, and the
	// next change must reach the interface.
	m.forgetServerStatus(iface)
	run.Stub("ip link show "+iface, iface+": state UNKNOWN")
	run.Stub("bash -c 'strip=$(mktemp)", "")
	m.cfg.Servers[0].Status = "stopped"
	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "bob"}); err != nil {
		t.Fatal(err)
	}
	synced := false
	for _, c := range run.Commands {
		if strings.Contains(c, "awg syncconf "+iface) {
			synced = true
		}
	}
	if !synced {
		t.Errorf("syncconf was not run on the live interface: %v", run.Commands)
	}
}

// Start and stop record what they just did, so the next reader sees it
// without waiting for the cache to expire or racing the kernel.
func TestStartAndStopRecordTheirOwnObservation(t *testing.T) {
	m, _ := newTestManager(t)
	iface := m.cfg.Servers[0].Interface

	m.noteServerStatus(iface, "running")
	if got := m.ServerStatus("s1"); got != "running" {
		t.Errorf("noted status = %q, want running", got)
	}

	m.forgetServerStatus(iface)
	if got := m.ServerStatus("s1"); got != "stopped" {
		t.Errorf("after forgetting, status = %q, want a fresh stopped", got)
	}
}

// GET /api/servers is polled by the dashboard. It reports what the kernel
// says, and it never rewrites the file of private keys to record that.
func TestListingServersReportsLiveStatusWithoutSaving(t *testing.T) {
	m, _ := newTestManager(t)

	// The config claims the interface is up; it does not exist.
	m.cfg.Servers[0].Status = "running"

	servers := m.Servers()
	if len(servers) != 1 || servers[0].Status != "stopped" {
		t.Fatalf("status = %+v, want the observed \"stopped\"", servers)
	}
	if _, err := os.Stat(m.store.Path()); !os.IsNotExist(err) {
		t.Errorf("a dashboard poll wrote the config file (err %v)", err)
	}
}
