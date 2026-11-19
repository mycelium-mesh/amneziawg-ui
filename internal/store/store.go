// Package store persists web_config.json - the one file that holds every
// server and client, private keys included - and brings an older copy of it
// up to the current schema.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"amneziawg-web-ui/internal/atomicfile"
	"amneziawg-web-ui/web-ui/api"
)

// AppConfig is the top-level config stored on disk.
type AppConfig struct {
	// SchemaVersion records which one-shot migrations have already been
	// applied to this file, so they don't re-run on every start and undo a
	// choice the operator made afterwards. A file written before this field
	// existed unmarshals to 0.
	SchemaVersion int `json:"schema_version"`

	// Servers owns every client: a client exists exactly once, inside the
	// Clients slice of the server it belongs to. There is no second copy to
	// keep in sync.
	Servers []api.Server `json:"servers"`

	// DefaultISettings is the operator's own standard I1-I5 set: the values
	// layered over the shipped wgconf defaults wherever the standard set is
	// used. Empty - or absent in a file written before this field existed -
	// means no override, and the shipped defaults stand. Updates merge key
	// by key (an empty request value is "no opinion" and leaves the stored
	// state alone); the whole set leaves only through the explicit clear.
	DefaultISettings api.ISettings `json:"default_i_settings"`
}

// Store reads and writes the config file at one path.
type Store struct {
	path string

	// mu orders writes to the file: two concurrent saves cannot reorder
	// into the older one landing last, because each holds mu from the
	// moment it serialises the config until the rename is done.
	mu sync.Mutex
}

// New returns a store over path. Nothing is read until Load.
func New(path string) *Store {
	return &Store{path: path}
}

// Path is the file the store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// Load reads the config, or returns an empty one when the file is missing or
// unreadable - a fresh install has no file yet. Slices a file written before
// they existed leaves nil are made empty, so JSON renders them as [] and the
// rest of the code can append without a nil check.
func (s *Store) Load() *AppConfig {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return &AppConfig{Servers: []api.Server{}}
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("Error unmarshaling config: %v\n", err)
		return &AppConfig{Servers: []api.Server{}}
	}
	if cfg.Servers == nil {
		cfg.Servers = []api.Server{}
	}
	for i := range cfg.Servers {
		if cfg.Servers[i].Clients == nil {
			cfg.Servers[i].Clients = []api.Client{}
		}
		if cfg.Servers[i].UnboundNATIPs == nil {
			cfg.Servers[i].UnboundNATIPs = []string{}
		}
	}
	return &cfg
}

// Save writes cfg to disk atomically. The caller must keep cfg from changing
// for the duration - the manager holds its config lock across the call - since
// the snapshot is taken here, under mu, so that concurrent saves land in the
// order they serialised. A failed write leaves the previous file contents in
// place: the new bytes only ever arrive by rename.
func (s *Store) Save(cfg *AppConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := atomicfile.Write(s.path, data, 0o600); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	return nil
}
