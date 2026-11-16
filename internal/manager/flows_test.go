package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// flowRunner is the host behind the flow tests. It keeps the two facts the
// per-interface critical section exists for - is the interface up, do the
// firewall rules exist - and answers every command from them the way the
// host would: `ip link show` only while the interface is up, syncconf only
// while it is up. The first command matching pauseOn blocks until released,
// which is how a test holds one flow open mid-flight while the rest of the
// world moves; an answer is computed when the command returns, so a flow
// paused on its status check sees the state everything else just made.
type flowRunner struct {
	mu       sync.Mutex
	up       bool
	rules    bool
	commands []string

	pauseOn string
	entered chan struct{}
	release chan struct{}
	paused  sync.Once
}

func newFlowRunner(pauseOn string) *flowRunner {
	return &flowRunner{
		pauseOn: pauseOn,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}, 1),
	}
}

func (r *flowRunner) Run(command string) (string, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	pausing := r.pauseOn != "" && strings.Contains(command, r.pauseOn)
	r.mu.Unlock()

	if pausing {
		first := false
		r.paused.Do(func() { first = true; close(r.entered) })
		if first {
			<-r.release
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case strings.Contains(command, "awg-quick up "):
		r.up = true
	case strings.Contains(command, "awg-quick down"):
		r.up = false
	case strings.Contains(command, "setup_iptables.sh"):
		r.rules = true
	case strings.Contains(command, "cleanup_iptables.sh"):
		r.rules = false
	case strings.HasPrefix(command, "ip link show "):
		if r.up {
			return strings.TrimPrefix(command, "ip link show ") + ": state UNKNOWN", nil
		}
		return "", errors.New("ip link show: exit status 1")
	case strings.Contains(command, "awg syncconf "):
		if r.up {
			return "", nil
		}
		return "", errors.New("awg syncconf: exit status 1")
	}
	return "", nil
}

func (r *flowRunner) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.commands...)
}

func (r *flowRunner) state() (up, rules bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.up, r.rules
}

// deployScripts gives the flow tests firewall commands to record; without
// them SetupIPTables and CleanupIPTables run nothing and the flows lose the
// second half the invariant is about.
func deployScripts(t *testing.T, m *Manager) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"setup_iptables.sh", "cleanup_iptables.sh"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.tools.ScriptsDir = dir
}

// The delete is the same critical section as the rest of issue #31's family:
// its running-check decision, the stop that may follow and the removal are
// one flow on the interface, so a start racing a delete runs entirely before
// or after it. The hazard this pins: a start completing behind a delete that
// already decided "stopped" must never leave a running interface with the
// firewall rules of a server that no longer exists.
func TestDeleteDoesNotInterleaveWithAStartOnOneServer(t *testing.T) {
	run := newFlowRunner("ip link show")
	run.up = false // the interface is down: the delete's decision would skip the stop
	m := newTestManagerWith(t, run)
	deployScripts(t, m)

	startDone := make(chan error, 1)
	go func() { startDone <- m.StartServer("s1") }()
	select {
	case <-run.entered: // the start is mid-flow, inside its status check
	case <-time.After(2 * time.Second):
		t.Fatal("the start never reached its status check")
	}

	deleteDone := make(chan error, 1)
	deleteRan := false
	go func() { deleteDone <- m.DeleteServer("s1") }()
	select {
	case err := <-deleteDone:
		deleteRan = true
		t.Errorf("the delete ran its whole decision and removal while the start was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	run.release <- struct{}{}
	select {
	case err := <-startDone:
		if err != nil {
			t.Errorf("StartServer = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the start never finished")
	}
	if !deleteRan {
		select {
		case err := <-deleteDone:
			if err != nil {
				t.Fatalf("DeleteServer = %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the delete never finished after the start")
		}
	}

	if _, ok := m.Server("s1"); ok {
		t.Error("the server is still listed after the delete")
	}
	if up, rules := run.state(); up && rules {
		t.Errorf("running interface with the firewall rules of a deleted server: commands=%v", run.recorded())
	}
}

// The store, not awg-quick's luck, decides whether a start may proceed: a
// delete that ran to completion first must leave a later start failing
// cleanly - not found, not one host command - even though the start had
// already read the server before the delete finished. The delete's .conf
// removal is best-effort (removeServerLocked ignores os.Remove's error), so
// only the store re-check under the same flow lock closes the window where
// a leftover .conf could resurrect the interface of a server that is gone.
func TestStartAfterAFinishedDeleteFindsNoServer(t *testing.T) {
	run := newFlowRunner("ip link show")
	run.up = false
	m := newTestManagerWith(t, run)
	deployScripts(t, m)

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteServer("s1") }()
	select {
	case <-run.entered: // the delete holds the flow, mid-decision
	case <-time.After(2 * time.Second):
		t.Fatal("the delete never reached its status check")
	}
	before := len(run.recorded())

	// The start resolves the server while it still exists, then queues on
	// the same flow lock behind the delete. The sleep gives the goroutine
	// time to get that far - entry check and lock acquisition are pure
	// in-memory work, so a scheduling delay here would only let the entry
	// check see the already-deleted server and miss the window this test
	// exists for.
	startDone := make(chan error, 1)
	go func() { startDone <- m.StartServer("s1") }()
	time.Sleep(100 * time.Millisecond)

	run.release <- struct{}{}
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteServer = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the delete never finished")
	}

	select {
	case err := <-startDone:
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("StartServer = %v, want a not-found error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the start never finished after the delete")
	}
	if cmds := run.recorded(); len(cmds) != before {
		t.Errorf("the start ran host commands for a deleted server: %v", cmds[before:])
	}
}

// The stop and the live apply are each a critical section on one server: a
// stop arriving while the apply is in flight must not run a single host
// command until the apply has finished - no cleanup under an apply that is
// about to report success, no syncconf against an interface a stop is
// tearing down. A status read, meanwhile, must not wait behind either.
func TestApplyAndStopDoNotInterleaveOnOneServer(t *testing.T) {
	run := newFlowRunner("ip link show")
	run.up = true // the interface is up: the apply will apply, the stop will stop
	m := newTestManagerWith(t, run)
	deployScripts(t, m)
	iface := m.cfg.Servers[0].Interface

	applyDone := make(chan struct{})
	go func() {
		m.syncLiveConfig(iface)
		close(applyDone)
	}()
	select {
	case <-run.entered: // the apply is mid-flight, inside its status check
	case <-time.After(2 * time.Second):
		t.Fatal("the apply never reached its status check")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- m.StopServer("s1") }()
	select {
	case err := <-stopDone:
		t.Fatalf("the stop ran its whole flow while the apply was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Reads stay free of the flow lock: a dashboard status poll must be
	// answered while a flow is mid-flight, not queued behind it.
	statusDone := make(chan string, 1)
	go func() { statusDone <- m.ServerStatus("s1") }()
	select {
	case <-statusDone:
	case <-time.After(2 * time.Second):
		t.Fatal("a status read waited behind a flow in flight")
	}

	during := run.recorded()
	for _, c := range during {
		if strings.Contains(c, "cleanup_iptables.sh") || strings.Contains(c, "awg-quick down") ||
			strings.Contains(c, "awg syncconf") {
			t.Fatalf("a host command ran while the apply was in flight: %v", during)
		}
	}

	run.release <- struct{}{}
	select {
	case <-applyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the apply never finished")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopServer = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stop never ran after the apply finished")
	}

	// Order: the whole apply, then the whole stop - end state consistent.
	got := run.recorded()
	syncIdx, cleanupIdx, downIdx := -1, -1, -1
	for i, c := range got {
		switch {
		case strings.Contains(c, "awg syncconf"):
			syncIdx = i
		case strings.Contains(c, "cleanup_iptables.sh"):
			cleanupIdx = i
		case strings.Contains(c, "awg-quick down"):
			downIdx = i
		}
	}
	if syncIdx < 0 || cleanupIdx < syncIdx || downIdx < cleanupIdx {
		t.Errorf("commands = %v, want the apply strictly before the stop's cleanup and down", got)
	}
	if up, rules := run.state(); up || rules {
		t.Errorf("end state: up=%v rules=%v, want both false", up, rules)
	}
}

// The same critical section with the other pair: a start arriving while the
// apply is in flight must not bring the interface up or lay down firewall
// rules until the apply is done - and once it is, the start completes.
func TestApplyAndStartDoNotInterleaveOnOneServer(t *testing.T) {
	run := newFlowRunner("ip link show")
	run.up = false // the interface is down: the apply will skip, the start will start
	m := newTestManagerWith(t, run)
	deployScripts(t, m)
	iface := m.cfg.Servers[0].Interface

	applyDone := make(chan struct{})
	go func() {
		m.syncLiveConfig(iface)
		close(applyDone)
	}()
	select {
	case <-run.entered: // the apply is mid-flight, inside its status check
	case <-time.After(2 * time.Second):
		t.Fatal("the apply never reached its status check")
	}

	startDone := make(chan error, 1)
	go func() { startDone <- m.StartServer("s1") }()
	select {
	case err := <-startDone:
		t.Fatalf("the start ran its whole flow while the apply was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	during := run.recorded()
	for _, c := range during {
		if strings.Contains(c, "awg-quick up") || strings.Contains(c, "setup_iptables.sh") {
			t.Fatalf("a host command ran while the apply was in flight: %v", during)
		}
	}

	run.release <- struct{}{}
	select {
	case <-applyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the apply never finished")
	}
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("StartServer = %v, want nil after the apply", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the start never ran after the apply finished")
	}

	got := run.recorded()
	sawUp, sawSetup := false, false
	for _, c := range got {
		sawUp = sawUp || strings.Contains(c, "awg-quick up")
		sawSetup = sawSetup || strings.Contains(c, "setup_iptables.sh")
	}
	if !sawUp || !sawSetup {
		t.Errorf("commands = %v, want the start's up and firewall setup after the apply", got)
	}
	if up, rules := run.state(); !up || !rules {
		t.Errorf("end state: up=%v rules=%v, want both true", up, rules)
	}
}
