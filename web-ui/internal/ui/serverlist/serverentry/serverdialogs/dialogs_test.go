package serverdialogs

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"amneziawg-web-ui/web-ui/api"
	"amneziawg-web-ui/web-ui/internal/ui/widgets"
)

func TestMain(m *testing.M) {
	// Widgets build against the theme; the test driver needs no display.
	test.NewApp()
	os.Exit(m.Run())
}

// The save decision is the promise of the endpoint editor: what "Save" sends,
// judged against the server as it is now rather than against the copy the
// card rendered. A re-render racing behind the dialog can leave that copy
// stale, and judging against it dropped a real edit in silence - the dialog
// closed without sending anything. These cases pin the decision from both
// sides: what must not be sent, what must be, and what must keep the dialog
// open. They run against a counted stub, and "the dialog closes" is the bool
// endpointConfirm reports - the contract dialogs.ShowForm acts on.

// saveStub stands in for the backend with every call counted: it holds what
// the server has now and records what the dialog tried to send.
type saveStub struct {
	mu      sync.Mutex
	current []api.Server
	err     error // when set, the current value cannot be read
	updates []sent
	fetched chan struct{} // fires when Servers is called
	sentCh  chan sent
}

// sent is one UpdateServerEndpoint call.
type sent struct{ serverID, endpoint string }

func newSaveStub(current ...api.Server) *saveStub {
	return &saveStub{
		current: current,
		fetched: make(chan struct{}, 1),
		sentCh:  make(chan sent, 1),
	}
}

func (s *saveStub) Servers() ([]api.Server, error) {
	s.mu.Lock()
	current, err := append([]api.Server(nil), s.current...), s.err
	s.mu.Unlock()
	select {
	case s.fetched <- struct{}{}:
	default:
	}
	return current, err
}

func (s *saveStub) UpdateServerEndpoint(serverID, endpoint string) error {
	got := sent{serverID, endpoint}
	s.mu.Lock()
	s.updates = append(s.updates, got)
	s.mu.Unlock()
	select {
	case s.sentCh <- got:
	default:
	}
	return nil
}

func (s *saveStub) sentSoFar() []sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sent(nil), s.updates...)
}

// notifyStub records what the dialog would have told the operator.
type notifyStub struct {
	mu   sync.Mutex
	ok   []string
	fail []error
}

func (n *notifyStub) OK(message string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ok = append(n.ok, message)
}

func (n *notifyStub) Warn(message string) {} // this dialog never warns

func (n *notifyStub) Fail(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fail = append(n.fail, err)
}

func (n *notifyStub) successes() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.ok...)
}

func (n *notifyStub) failures() []error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]error(nil), n.fail...)
}

// reloadStub counts the "fetch the server list again" hook.
type reloadStub struct {
	mu sync.Mutex
	n  int
}

func (r *reloadStub) call() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
}

func (r *reloadStub) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// confirm plays "Save" in the endpoint editor: the entry gets the typed text
// and the returned bool is what the dialog acts on - true closes it, false
// leaves it open over the input.
func confirm(t *testing.T, stub *saveStub, notes *notifyStub, reload *reloadStub, srv api.Server, typed string) bool {
	t.Helper()
	endpoint := widgets.NewEntry()
	endpoint.SetText(typed)
	return endpointConfirm(stub, notes, reload.call, srv, endpoint)()
}

// waitSent reports what the save sent, or fails when nothing went out.
func waitSent(t *testing.T, stub *saveStub) sent {
	t.Helper()
	select {
	case got := <-stub.sentCh:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent, want UpdateServerEndpoint to be called")
		return sent{}
	}
}

// waitFor gives the save goroutine a moment to finish the work after its
// send - the notification and the reload happen past the call itself.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A value the server already holds is not a change: nothing is sent and the
// dialog closes. The snapshot in the test is deliberately the stale one the
// card rendered - against it the same value would look like an edit.
func TestEndpointSaveSendsNothingWhenTheServerAlreadyHasTheValue(t *testing.T) {
	stub := newSaveStub(api.Server{ID: "srv-1", Endpoint: "vpn.example.com"})
	notes := &notifyStub{}
	reload := &reloadStub{}
	srv := api.Server{ID: "srv-1", Endpoint: "stale.example.com"} // the render-time copy

	if !confirm(t, stub, notes, reload, srv, "vpn.example.com") {
		t.Error("the dialog should close over a save that changes nothing")
	}

	// The no-op is decided after reading the server, so let the goroutine
	// reach that decision before judging that it sent nothing.
	select {
	case <-stub.fetched:
	case <-time.After(5 * time.Second):
		t.Fatal("the server's current value was never read")
	}
	select {
	case got := <-stub.sentCh:
		t.Errorf("UpdateServerEndpoint(%q, %q) was called, want none", got.serverID, got.endpoint)
	case <-time.After(200 * time.Millisecond):
	}
	if len(notes.failures()) != 0 || len(notes.successes()) != 0 {
		t.Errorf("a silent no-op should report nothing, got ok=%v fail=%v", notes.successes(), notes.failures())
	}
}

// An unchanged value that fails api.ValidateEndpoint is still a mistake in
// the field: the dialog reports the problem and stays open rather than
// closing over "nothing changed". Validation runs before the request, so a
// value the server may already hold is checked all the same.
func TestEndpointSaveRejectsAnUnchangedInvalidValue(t *testing.T) {
	// "host_name" fails ValidateEndpoint: an underscore is not a DNS label.
	stub := newSaveStub(api.Server{ID: "srv-1", Endpoint: "host_name"})
	notes := &notifyStub{}
	reload := &reloadStub{}
	srv := api.Server{ID: "srv-1", Endpoint: "host_name"}

	if confirm(t, stub, notes, reload, srv, "host_name") {
		t.Error("the dialog must stay open over a value that does not validate")
	}

	fails := notes.failures()
	if len(fails) != 1 || !strings.Contains(fails[0].Error(), "IP address or a hostname") {
		t.Errorf("want the validation problem reported once, got %v", fails)
	}
	if got := stub.sentSoFar(); len(got) != 0 {
		t.Errorf("UpdateServerEndpoint was called %v, want none", got)
	}
}

// The motivating race: the dialog holds a snapshot naming the old value, the
// server has moved on since, and the operator types that old value back. It
// equals the stale snapshot and differs from the server now - a real edit
// that must go out instead of being dropped as "nothing changed".
func TestEndpointSaveSendsWhatTheRenderSnapshotCalledUnchanged(t *testing.T) {
	stub := newSaveStub(api.Server{ID: "srv-1", Endpoint: "other.example.com"})
	notes := &notifyStub{}
	reload := &reloadStub{}
	srv := api.Server{ID: "srv-1", Endpoint: "vpn.example.com"} // the render-time copy

	if !confirm(t, stub, notes, reload, srv, "vpn.example.com") {
		t.Fatal("the dialog should close on a save that went out")
	}

	if got := waitSent(t, stub); got != (sent{"srv-1", "vpn.example.com"}) {
		t.Errorf("sent %v, want the typed value for srv-1", got)
	}
	// The work after the send is unchanged: report it and re-render.
	waitFor(t, "the success notification", func() bool { return len(notes.successes()) == 1 })
	waitFor(t, "the reload hook", func() bool { return reload.calls() == 1 })
}

// Clearing is the one way back to the auto-detected public IP, and it is a
// change like any other: empty passes ValidateEndpoint and is sent while the
// server still carries a value. The typed text is trimmed first, so a field
// left whitespace still counts as cleared.
func TestEndpointSaveClearsTheEndpoint(t *testing.T) {
	stub := newSaveStub(api.Server{ID: "srv-1", Endpoint: "vpn.example.com"})
	notes := &notifyStub{}
	reload := &reloadStub{}
	srv := api.Server{ID: "srv-1", Endpoint: "vpn.example.com"}

	if !confirm(t, stub, notes, reload, srv, "  ") {
		t.Fatal("the dialog should close on a save that went out")
	}

	if got := waitSent(t, stub); got != (sent{"srv-1", ""}) {
		t.Errorf("sent %v, want the cleared endpoint for srv-1", got)
	}
}

// When the server's current endpoint cannot be read, "nothing changed" cannot
// be established, so the save goes out rather than being dropped in silence -
// the error must never eat an edit the way the stale snapshot did.
func TestEndpointSaveStillSendsWhenTheCurrentValueCannotBeRead(t *testing.T) {
	stub := newSaveStub()
	stub.err = errors.New("backend unreachable")
	notes := &notifyStub{}
	reload := &reloadStub{}
	srv := api.Server{ID: "srv-1", Endpoint: "vpn.example.com"}

	if !confirm(t, stub, notes, reload, srv, "vpn.example.com") {
		t.Fatal("the dialog should close on a save that went out")
	}

	if got := waitSent(t, stub); got != (sent{"srv-1", "vpn.example.com"}) {
		t.Errorf("sent %v, want the typed value for srv-1", got)
	}
}
