package manager

import (
	"os"
	"path/filepath"
	"testing"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/awg/awgtest"
	"amneziawg-web-ui/internal/config"
	"amneziawg-web-ui/internal/store"
	"amneziawg-web-ui/internal/sysinfo"
	"amneziawg-web-ui/web-ui/api"
)

// newTestManager builds a manager whose single server has a real .conf on
// disk and whose host commands all fail, as on a machine without
// amneziawg-tools: key generation falls back to random keys, every interface
// is down. Tests stub the runner for anything else.
func newTestManager(t *testing.T) (*Manager, *awgtest.Runner) {
	t.Helper()
	run := awgtest.New()
	return newTestManagerWith(t, run), run
}

// newTestManagerWith is newTestManager with a runner of the caller's
// choosing, for the tests that need the host commands to behave in a
// particular way.
func newTestManagerWith(t *testing.T, run awg.Runner) *Manager {
	t.Helper()
	dir := t.TempDir()
	confPath := filepath.Join(dir, "wg-s1.conf")
	if err := os.WriteFile(confPath, []byte("[Interface]\nPrivateKey = PRIV\nS1 = 50\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := config.Settings{
		WebUIPort: 54845, DefaultMTU: 1280, DefaultSubnet: "10.0.0.0/24", DefaultPort: 54844,
		DNSServers: []string{"8.8.8.8"}, EnableObfuscation: true,
		ConfigFile: filepath.Join(dir, "web_config.json"), WireguardConfigDir: dir,
	}
	m := &Manager{
		settings: settings,
		store:    store.New(settings.ConfigFile),
		tools:    awg.New(run),
		host:     sysinfo.Default(dir),
		statuses: map[string]statusObservation{},
		cfg: &store.AppConfig{SchemaVersion: store.SchemaVersion, Servers: []api.Server{{
			ID: "s1", Name: "srv", Interface: "wg-test-absent", ConfigPath: confPath,
			Subnet: "10.0.1.0/24", ServerIP: "10.0.1.1", MTU: 1280, Port: 54844,
			Status: "stopped", ObfuscationEnabled: true,
			ObfuscationParams: &api.ObfuscationParams{S1: 50, S2: 60, S3: 20, S4: 16, MTU: 1280},
			Clients:           []api.Client{},
		}}},
	}
	m.tools.ScriptsDir = filepath.Join(dir, "no-scripts")
	return m
}

// New brings an older config up to date and writes the result back, so the
// migration does not run again on the next start.
func TestNewMigratesAndSavesAnOldConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web_config.json")
	st := store.New(path)
	if err := st.Save(&store.AppConfig{SchemaVersion: 3, Servers: []api.Server{{
		ID: "s1", Clients: []api.Client{{ID: "c1", ServerID: "tatus5"}},
	}}}); err != nil {
		t.Fatal(err)
	}

	m := New(config.Settings{ConfigFile: path}, st, awg.New(awgtest.New()))

	client, ok := m.Client("s1", "c1")
	if !ok || client.ServerID != "s1" {
		t.Errorf("client after migration = %+v, %v", client, ok)
	}
	if saved := st.Load(); saved.SchemaVersion != store.SchemaVersion {
		t.Errorf("saved schema version = %d, want %d", saved.SchemaVersion, store.SchemaVersion)
	}
}

func TestNewLeavesACurrentConfigAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web_config.json")
	st := store.New(path)
	if err := st.Save(&store.AppConfig{SchemaVersion: store.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)

	New(config.Settings{ConfigFile: path}, st, awg.New(awgtest.New()))

	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("a config that needs nothing was rewritten")
	}
}
