package store

import (
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

// The stamp is the part every migration shares: a config that predates the
// field is brought up to the current revision and reported as changed, and
// one that is already current is left alone.
func TestMigrateStampsAnUnversionedConfig(t *testing.T) {
	cfg := &AppConfig{Servers: []api.Server{{ID: "s1"}}}
	if !Migrate(cfg) {
		t.Error("an unversioned config must be reported as changed")
	}
	if got := cfg.SchemaVersion; got != SchemaVersion {
		t.Errorf("schema version = %d, want %d", got, SchemaVersion)
	}
}

func TestMigrateLeavesACurrentConfigAlone(t *testing.T) {
	cfg := &AppConfig{SchemaVersion: SchemaVersion}
	if Migrate(cfg) {
		t.Error("a config that needs nothing must not be reported as changed")
	}
}

// Configs written before 3.1.5 can hold a client ServerID that decayed into a
// slice of an unrelated request path, because fiber recycles the buffer a
// route parameter points into. The server a client is stored under is what
// binds them, so the migration rewrites the copy from it.
func TestMigrationRepairsClientServerIDs(t *testing.T) {
	cfg := &AppConfig{SchemaVersion: 3, Servers: []api.Server{{
		ID: "s1",
		Clients: []api.Client{
			{ID: "c1", ServerID: "tatus5"},
			{ID: "c2", ServerID: "s1"},
		},
	}, {
		ID:      "s2",
		Clients: []api.Client{{ID: "c3", ServerID: "i-sett"}},
	}}}

	Migrate(cfg)

	for _, srv := range cfg.Servers {
		for _, c := range srv.Clients {
			if c.ServerID != srv.ID {
				t.Errorf("client %s: ServerID = %q, want %q", c.ID, c.ServerID, srv.ID)
			}
		}
	}
	if got := cfg.SchemaVersion; got != SchemaVersion {
		t.Errorf("schema version = %d, want %d", got, SchemaVersion)
	}
}

// An Endpoint equal to the server's PublicIP is what releases up to 3.1.9
// wrote for every server created without an explicit endpoint: the detected
// IP was copied into both fields, and the stored copy then won over the
// stamped one in client configs forever. Which stored endpoints are that copy
// is decided by content: detection only ever produces an IP literal, so an
// endpoint that is not one is the operator's and stays; one that is, is the
// copy and is cleared - including one left frozen on an old address by a
// Refresh IP after the host moved. A typed endpoint copied into PublicIP by
// the same old code is moved back to Endpoint, leaving PublicIP for detection.
func TestMigrationClearsAutoDetectedEndpoints(t *testing.T) {
	cfg := &AppConfig{SchemaVersion: 4, Servers: []api.Server{
		{ID: "auto", PublicIP: "198.51.100.4", Endpoint: "198.51.100.4"},
		{ID: "refreshed", PublicIP: "198.51.100.9", Endpoint: "198.51.100.4"},
		{ID: "manualhost", PublicIP: "vpn.example.com", Endpoint: "vpn.example.com"},
		{ID: "manualip", PublicIP: "203.0.113.5", Endpoint: "203.0.113.5"},
		{ID: "placeholder", PublicIP: "YOUR_SERVER_IP", Endpoint: "YOUR_SERVER_IP"},
		{ID: "placeholderonly", PublicIP: "YOUR_SERVER_IP"},
	}}

	if !Migrate(cfg) {
		t.Error("a config that needs the migration must be reported as changed")
	}
	byID := map[string]api.Server{}
	for _, srv := range cfg.Servers {
		byID[srv.ID] = srv
	}

	if got := byID["auto"].Endpoint; got != "" {
		t.Errorf("auto-copied endpoint = %q, want it cleared", got)
	}
	if got := byID["auto"].PublicIP; got != "198.51.100.4" {
		t.Errorf("PublicIP = %q, want it kept", got)
	}
	if got := byID["refreshed"].Endpoint; got != "" {
		t.Errorf("frozen endpoint after Refresh IP = %q, want it cleared", got)
	}
	if got := byID["refreshed"].PublicIP; got != "198.51.100.9" {
		t.Errorf("refreshed PublicIP = %q, want it kept", got)
	}
	if got := byID["manualhost"].Endpoint; got != "vpn.example.com" {
		t.Errorf("manual hostname endpoint = %q, want it kept", got)
	}
	if got := byID["manualhost"].PublicIP; got != "" {
		t.Errorf("PublicIP copied from a hostname = %q, want it cleared", got)
	}
	if got := byID["manualip"].Endpoint; got != "" {
		t.Errorf("manually typed IP endpoint = %q, want it cleared (indistinguishable from the copy)", got)
	}
	// The placeholder the app wrote when detection failed is not a host the
	// operator chose, so it must not pin the server forever.
	if got := byID["placeholder"].Endpoint; got != "" {
		t.Errorf("placeholder endpoint = %q, want it cleared", got)
	}
	if got := byID["placeholder"].PublicIP; got != "" {
		t.Errorf("placeholder PublicIP = %q, want it cleared", got)
	}
	if got := byID["placeholderonly"].PublicIP; got != "" {
		t.Errorf("placeholder-only PublicIP = %q, want it cleared", got)
	}
	if got := byID["placeholderonly"].Endpoint; got != "" {
		t.Errorf("placeholder-only endpoint = %q, want it empty", got)
	}

	if got := cfg.SchemaVersion; got != SchemaVersion {
		t.Errorf("schema version = %d, want %d", got, SchemaVersion)
	}
}
