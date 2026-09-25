package manager

import (
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
)

// The suspender suspends exactly the active clients whose time has passed.
func TestSuspendDueSuspendsOnlyOverdueActiveClients(t *testing.T) {
	m, _ := newTestManager(t)
	past := float64(time.Now().Add(-time.Minute).Unix())
	future := float64(time.Now().Add(time.Hour).Unix())

	overdue, _, _ := m.AddClient("s1", api.AddClientRequest{Name: "overdue"})
	later, _, _ := m.AddClient("s1", api.AddClientRequest{Name: "later"})
	already, _, _ := m.AddClient("s1", api.AddClientRequest{Name: "already"})
	for _, c := range []struct {
		id string
		at *float64
	}{{overdue.ID, &past}, {later.ID, &future}, {already.ID, &past}} {
		if _, _, err := m.UpdateClientSuspendTime("s1", c.id, c.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SuspendClient("s1", already.ID); err != nil {
		t.Fatal(err)
	}

	m.suspendDue(time.Now())

	want := map[string]string{overdue.ID: "suspended", later.ID: "active", already.ID: "suspended"}
	for _, c := range m.Clients("s1") {
		if c.Status != want[c.ID] {
			t.Errorf("client %s: status = %q, want %q", c.Name, c.Status, want[c.ID])
		}
	}
}

// A scheduled time fires once: it is cleared when it suspends the client, so
// switching the client back on by hand is not undone on the next tick.
func TestSuspendDueFiresOnce(t *testing.T) {
	m, _ := newTestManager(t)
	past := float64(time.Now().Add(-time.Minute).Unix())

	client, _, _ := m.AddClient("s1", api.AddClientRequest{Name: "c"})
	if _, _, err := m.UpdateClientSuspendTime("s1", client.ID, &past); err != nil {
		t.Fatal(err)
	}

	m.suspendDue(time.Now())
	c := m.Clients("s1")[0]
	if c.Status != "suspended" || c.SuspendAt != nil {
		t.Fatalf("after the time: status = %q, suspend_at = %v, want suspended and cleared", c.Status, c.SuspendAt)
	}

	if _, err := m.ActivateClient("s1", client.ID); err != nil {
		t.Fatal(err)
	}
	m.suspendDue(time.Now())
	if c := m.Clients("s1")[0]; c.Status != "active" {
		t.Errorf("after activating by hand: status = %q, want active", c.Status)
	}
}
