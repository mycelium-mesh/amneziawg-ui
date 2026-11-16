package manager

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"amneziawg-web-ui/internal/store"
	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// What GET /api/default-i-settings reports must be what a restart loads, and
// overlapping updates with a failing persist must not break that: every
// update either lands in memory and on disk together or rolls back together,
// so at quiescence the two agree. The failing persist is injected the
// smallest way an in-package test can - by pointing the manager at a store
// whose path has no parent directory, so every save fails - which needs no
// production seam at all.
//
// Both shapes that used to assign under the lock, release it, then save and
// roll back against a `previous` captured earlier take part: the default
// I-settings save and the server-endpoint update. The old shape let a
// rollback resurrect a stale value a later update had already replaced while
// the file held yet another one; this run of overlapping updates drives that
// interleaving hard enough to surface it.
func TestOverlappingUpdatesKeepMemoryAndFileTogether(t *testing.T) {
	m, _ := newTestManager(t)
	working := m.store // the store whose path exists; used to seed and re-read the file

	// Seed a state the file really holds, through the normal update paths.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>"}); err != nil {
		t.Fatalf("seed standard: %v", err)
	}
	if _, err := m.UpdateServerEndpoint("s1", "seed.example.com"); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	// Break persistence: the path's parent directory does not exist, so
	// atomicfile.Write fails before touching anything. The original store
	// still reads the file as a restart would.
	m.store = store.New(filepath.Join(t.TempDir(), "no", "such", "dir", "web_config.json"))

	const workers = 8
	const rounds = 25
	var (
		wg       sync.WaitGroup
		failures atomic.Int64
		start    = make(chan struct{})
	)
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				body := api.ISettings{"i1": fmt.Sprintf("<r %d>", 10+w*rounds+r)}
				if _, err := m.UpdateDefaultISettings(body); err != nil {
					failures.Add(1)
				}
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				if _, err := m.UpdateServerEndpoint("s1", fmt.Sprintf("h%d.example.com", w*rounds+r)); err != nil {
					failures.Add(1)
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()

	if failures.Load() == 0 {
		t.Fatal("no update saw the failing persist - the test would not exercise the rollback")
	}

	// Persisting works again; read the file back the way a restart does and
	// require memory and disk to say the same thing.
	m.store = working
	loaded := working.Load()

	if got, file := m.DefaultISettings(), wgconf.MergeISettings(loaded.DefaultISettings); !sameISettings(got, file) {
		t.Errorf("after the failed updates GET reports %v, a restart loads %v - memory and file diverged", got, file)
	}

	mem, _ := m.Server("s1")
	if len(loaded.Servers) != 1 {
		t.Fatalf("the file holds %d servers, want the seeded one", len(loaded.Servers))
	}
	if mem.Endpoint != loaded.Servers[0].Endpoint {
		t.Errorf("endpoint in memory = %q, in the file = %q - memory and file diverged", mem.Endpoint, loaded.Servers[0].Endpoint)
	}
}

// sameISettings compares two effective sets key by key.
func sameISettings(a, b api.ISettings) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
