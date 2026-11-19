package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

func TestLoadOfAMissingFileIsAnEmptyConfig(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "web_config.json"))
	cfg := s.Load()
	if cfg.Servers == nil || len(cfg.Servers) != 0 {
		t.Errorf("Load = %+v, want an empty server list", cfg)
	}
}

// A file written before Clients and UnboundNATIPs existed leaves them nil;
// the loaded config must not, or JSON renders null and appends need guards.
func TestLoadFillsInMissingSlices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web_config.json")
	if err := os.WriteFile(path, []byte(`{"servers":[{"id":"s1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := New(path).Load()
	if len(cfg.Servers) != 1 || cfg.Servers[0].Clients == nil || cfg.Servers[0].UnboundNATIPs == nil {
		t.Errorf("Load = %+v", cfg)
	}
}

func TestSaveRoundTrips(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "web_config.json"))
	cfg := &AppConfig{SchemaVersion: SchemaVersion, Servers: []api.Server{{
		ID: "s1", Clients: []api.Client{{ID: "c1", ServerID: "s1"}},
	}}}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}

	got := s.Load()
	if got.SchemaVersion != SchemaVersion || len(got.Servers) != 1 || len(got.Servers[0].Clients) != 1 {
		t.Errorf("Load after Save = %+v", got)
	}
}

// The operator's own standard I1-I5 set exists to outlive the process:
// that is the whole point of saving it instead of keeping it in a notepad.
func TestSaveRoundTripsTheDefaultISettings(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "web_config.json"))
	cfg := &AppConfig{
		SchemaVersion:    SchemaVersion,
		DefaultISettings: api.ISettings{"i1": "<b 0xaabb>", "i3": "<r 20>"},
		Servers:          []api.Server{},
	}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}

	got := s.Load()
	if got.DefaultISettings["i1"] != "<b 0xaabb>" || got.DefaultISettings["i3"] != "<r 20>" {
		t.Errorf("DefaultISettings after Save = %v, want the saved set", got.DefaultISettings)
	}
}

func TestConcurrentSavesLeaveAValidConfig(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "web_config.json"))
	cfg := &AppConfig{Servers: []api.Server{{ID: "s1", Clients: []api.Client{{ID: "c1"}}}}}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Save(cfg); err != nil {
				t.Errorf("Save: %v", err)
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var got AppConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config is not valid JSON after concurrent saves: %v\n%s", err, data)
	}
	if len(got.Servers) != 1 || len(got.Servers[0].Clients) != 1 {
		t.Errorf("config lost data: %+v", got)
	}
}
