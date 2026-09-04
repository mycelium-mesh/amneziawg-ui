package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicWriteFileLeavesNoTemporariesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web_config.json")

	for _, body := range []string{"first", "second, rather longer"} {
		if err := atomicWriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Errorf("content = %q, want %q", got, body)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 - the config holds private keys", info.Mode().Perm())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

// A failed write must not destroy what is already on disk - that file is the
// only copy of every server and client private key.
func TestAtomicWriteFileKeepsTheOldFileWhenTheWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web_config.json")
	if err := atomicWriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A directory that does not exist makes the temporary file fail.
	missing := filepath.Join(dir, "gone", "web_config.json")
	if err := atomicWriteFile(missing, []byte("nope"), 0o600); err == nil {
		t.Error("expected an error writing into a missing directory")
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Errorf("original file damaged: %q, %v", got, err)
	}
}

func TestConcurrentSavesLeaveAValidConfig(t *testing.T) {
	m := newClientManager(t)
	if _, _, err := m.AddClient("s1", "alice", false, nil, ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.SaveConfig(); err != nil {
				t.Errorf("SaveConfig: %v", err)
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(m.configPath())
	if err != nil {
		t.Fatal(err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not valid JSON after concurrent saves: %v\n%s", err, data)
	}
	if len(cfg.Servers) != 1 || len(cfg.Servers[0].Clients) != 1 {
		t.Errorf("config lost data: %+v", cfg)
	}
}

// GET /api/servers is polled by the dashboard. It reports what the kernel
// says, and it never rewrites the file of private keys to record that.
func TestListingServersReportsLiveStatusWithoutSaving(t *testing.T) {
	m := newClientManager(t)

	// The config claims the interface is up; it does not exist.
	m.Config.Servers[0].Status = "running"

	servers := m.GetServersWithStatus()
	if len(servers) != 1 || servers[0].Status != "stopped" {
		t.Fatalf("status = %+v, want the observed \"stopped\"", servers)
	}
	if _, err := os.Stat(m.configPath()); !os.IsNotExist(err) {
		t.Errorf("a dashboard poll wrote the config file (err %v)", err)
	}
}
