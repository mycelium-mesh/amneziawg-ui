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
// stamped one in client configs forever. Such an endpoint is
// indistinguishable from the auto-copy, so the migration clears it and the
// config follows PublicIP again. A genuinely manual endpoint, and one on a
// server whose PublicIP was never stamped, are left alone.
func TestMigrationClearsAnEndpointEqualToThePublicIP(t *testing.T) {
	cfg := &AppConfig{SchemaVersion: 4, Servers: []api.Server{{
		ID: "auto", PublicIP: "198.51.100.4", Endpoint: "198.51.100.4",
	}, {
		ID: "manual", PublicIP: "198.51.100.4", Endpoint: "vpn.example.com",
	}, {
		ID: "nostamp", Endpoint: "203.0.113.9",
	}}}

	if !Migrate(cfg) {
		t.Error("a config that needs the migration must be reported as changed")
	}
	if got := cfg.Servers[0].Endpoint; got != "" {
		t.Errorf("auto-copied endpoint = %q, want it cleared", got)
	}
	if got := cfg.Servers[0].PublicIP; got != "198.51.100.4" {
		t.Errorf("PublicIP = %q, want it kept", got)
	}
	if got := cfg.Servers[1].Endpoint; got != "vpn.example.com" {
		t.Errorf("manual endpoint = %q, want it kept", got)
	}
	if got := cfg.Servers[2].Endpoint; got != "203.0.113.9" {
		t.Errorf("endpoint without a stamped PublicIP = %q, want it kept", got)
	}
	if got := cfg.SchemaVersion; got != SchemaVersion {
		t.Errorf("schema version = %d, want %d", got, SchemaVersion)
	}
}
