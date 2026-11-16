package awg_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amneziawg-web-ui/internal/awg"
)

// scriptedRunner records every command, fails the one matching failOn, and
// blocks the first command matching blockOn until released - the mid-flight
// pause the interleaving tests hold an operation open with.
type scriptedRunner struct {
	mu       sync.Mutex
	commands []string
	blockOn  string
	failOn   string
	entered  chan string
	release  chan struct{}
}

func (r *scriptedRunner) Run(command string) (string, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if r.entered != nil && strings.Contains(command, r.blockOn) {
		r.entered <- command
		<-r.release
	}
	if r.failOn != "" && strings.Contains(command, r.failOn) {
		return "", &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: invalid argument"}
	}
	return "", nil
}

func (r *scriptedRunner) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.commands...)
}

func await(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}

// The stop-vs-restart race from the review, and its general form: while one
// operation is paused mid-sequence, a second on the SAME interface must not
// have run a single command yet - its commands appear only after the first
// completes.
func TestOperationsOnOneInterfaceDoNotInterleave(t *testing.T) {
	run := &scriptedRunner{
		blockOn: "awg-quick up wg-1",
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
	}
	tools := awg.New(run)

	upDone := make(chan error, 1)
	go func() { upDone <- tools.QuickUp("wg-1") }()
	select {
	case <-run.entered: // the start is mid-sequence, inside awg-quick up
	case <-time.After(2 * time.Second):
		t.Fatal("the start never reached its command")
	}

	// A stop arriving now - SyncConf's restart is the same shape - must wait
	// out the whole operation rather than delete the interface mid-restart.
	stopDone := make(chan error, 1)
	go func() { stopDone <- tools.QuickDown("wg-1") }()
	select {
	case err := <-stopDone:
		t.Fatalf("the stop ran while the start was still in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	run.release <- struct{}{}
	if err := await(t, upDone); err != nil {
		t.Fatalf("start = %v, want nil", err)
	}
	if err := await(t, stopDone); err != nil {
		t.Fatalf("stop = %v, want nil", err)
	}
	got := run.recorded()
	want := []string{"/usr/bin/awg-quick up wg-1", "ip link show wg-1", "/usr/bin/awg-quick down wg-1"}
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The review's chain made explicit: a down arriving while SyncConf's
// restart is in flight must run strictly after it, never deleting the
// interface the restart is recreating.
func TestStopWaitsForAnInFlightRestart(t *testing.T) {
	run := &scriptedRunner{
		blockOn: "awg-quick up wg-1",
		failOn:  "awg syncconf",
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
	}
	tools := awg.New(run)

	syncDone := make(chan error, 1)
	go func() { syncDone <- tools.SyncConf("wg-1") }()
	select {
	case <-run.entered: // syncconf failed, down ran, the restart's up is open
	case <-time.After(2 * time.Second):
		t.Fatal("the restart never reached its up")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- tools.QuickDown("wg-1") }()
	select {
	case err := <-stopDone:
		t.Fatalf("the stop ran mid-restart: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	downs := 0
	for _, c := range run.recorded() {
		if c == "/usr/bin/awg-quick down wg-1" {
			downs++
		}
	}
	if downs != 1 {
		t.Fatalf("downs recorded mid-restart = %d, want the restart's own only", downs)
	}

	run.release <- struct{}{}
	if err := await(t, syncDone); err != nil {
		t.Fatalf("SyncConf = %v, want nil after the restart", err)
	}
	if err := await(t, stopDone); err != nil {
		t.Fatalf("stop = %v, want nil", err)
	}
	recorded := run.recorded()
	if last := recorded[len(recorded)-1]; last != "/usr/bin/awg-quick down wg-1" {
		t.Errorf("last command = %q, want the stop strictly after the restart: %v", last, recorded)
	}
}

// Preservation: a start paused on one interface holds nothing over another -
// no global serialization, no deadlock.
func TestOperationsOnDifferentInterfacesDoNotBlock(t *testing.T) {
	run := &scriptedRunner{
		blockOn: "awg-quick up wg-1",
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
	}
	tools := awg.New(run)

	oneDone := make(chan error, 1)
	go func() { oneDone <- tools.QuickUp("wg-1") }()
	select {
	case <-run.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first start never reached its command")
	}

	twoDone := make(chan error, 1)
	go func() { twoDone <- tools.QuickUp("wg-2") }()
	if err := await(t, twoDone); err != nil {
		t.Fatalf("second interface start = %v, want nil", err)
	}

	run.release <- struct{}{}
	if err := await(t, oneDone); err != nil {
		t.Fatalf("first interface start = %v, want nil", err)
	}
}

// Proposition 5's apply-vs-apply case: two SyncConf restarts overlapping on
// one interface interleave nowhere - each one's whole sequence (apply,
// down, up, check) runs strictly after the other's - so no restart can find
// the interface stranded between another restart's halves.
func TestOverlappingAppliesDoNotInterleaveTheirRestarts(t *testing.T) {
	run := &scriptedRunner{
		blockOn: "awg-quick up wg-1",
		failOn:  "awg syncconf",
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
	}
	tools := awg.New(run)

	firstDone := make(chan error, 1)
	go func() { firstDone <- tools.SyncConf("wg-1") }()
	select {
	case <-run.entered: // the first restart's up is open
	case <-time.After(2 * time.Second):
		t.Fatal("the first apply never reached its restart")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- tools.SyncConf("wg-1") }()
	select {
	case err := <-secondDone:
		t.Fatalf("the second apply completed while the first was mid-restart: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	applies := 0
	for _, c := range run.recorded() {
		if strings.Contains(c, "awg syncconf") {
			applies++
		}
	}
	if applies != 1 {
		t.Fatalf("applies recorded mid-restart = %d, want the first's own only", applies)
	}

	run.release <- struct{}{} // the first restart's up proceeds
	if err := await(t, firstDone); err != nil {
		t.Fatalf("first SyncConf = %v, want nil after its restart", err)
	}
	run.release <- struct{}{} // the second's, once it gets there
	if err := await(t, secondDone); err != nil {
		t.Fatalf("second SyncConf = %v, want nil after its restart", err)
	}

	got := run.recorded()
	apply, down, up, check := applyCmd("wg-1"), "/usr/bin/awg-quick down wg-1",
		"/usr/bin/awg-quick up wg-1", "ip link show wg-1"
	want := []string{apply, down, up, check, apply, down, up, check}
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want each restart whole and strictly after the other: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The stop's firewall cleanup arriving during an in-flight restart must run
// after the whole syncconf -> down -> up sequence, never inside it: rules
// stripped from under a restarting interface leave it running without a
// firewall, silently.
func TestFirewallCleanupWaitsForAnInFlightRestart(t *testing.T) {
	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "cleanup_iptables.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &scriptedRunner{
		blockOn: "awg-quick up wg-1",
		failOn:  "awg syncconf",
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
	}
	tools := awg.New(run)
	tools.ScriptsDir = scripts

	syncDone := make(chan error, 1)
	go func() { syncDone <- tools.SyncConf("wg-1") }()
	select {
	case <-run.entered: // syncconf failed, down ran, the restart's up is open
	case <-time.After(2 * time.Second):
		t.Fatal("the restart never reached its up")
	}

	cleanupDone := make(chan struct{}, 1)
	go func() { tools.CleanupIPTables("wg-1", "10.0.0.0/24"); cleanupDone <- struct{}{} }()
	select {
	case <-cleanupDone:
		t.Fatal("the firewall cleanup ran inside the restart sequence")
	case <-time.After(50 * time.Millisecond):
	}
	for _, c := range run.recorded() {
		if strings.Contains(c, "cleanup_iptables.sh") {
			t.Fatalf("cleanup recorded mid-restart: %v", run.recorded())
		}
	}

	run.release <- struct{}{}
	if err := await(t, syncDone); err != nil {
		t.Fatalf("SyncConf = %v, want nil after the restart", err)
	}
	select {
	case <-cleanupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the firewall cleanup never ran")
	}
	recorded := run.recorded()
	if last := recorded[len(recorded)-1]; !strings.Contains(last, "cleanup_iptables.sh") {
		t.Errorf("last command = %q, want the cleanup strictly after the restart: %v", last, recorded)
	}
}
