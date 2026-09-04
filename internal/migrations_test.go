package internal

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// There is nothing to migrate yet, so the whole contract is the stamp: a
// config that predates the field is brought up to the current revision and
// written back, and one that is already current is left alone.
func TestRunMigrationsStampsAnUnversionedConfig(t *testing.T) {
	path := t.TempDir() + "/web_config.json"
	m := &Manager{configFile: path, Config: &AppConfig{Servers: []Server{{ID: "s1"}}}}
	m.runMigrations()

	if got := m.Config.SchemaVersion; got != awgConfigSchemaVersion {
		t.Errorf("schema version = %d, want %d", got, awgConfigSchemaVersion)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), fmt.Sprintf(`"schema_version": %d`, awgConfigSchemaVersion)) {
		t.Errorf("schema version not stamped:\n%s", saved)
	}
}

func TestRunMigrationsLeavesACurrentConfigAlone(t *testing.T) {
	path := t.TempDir() + "/web_config.json"
	m := &Manager{configFile: path, Config: &AppConfig{SchemaVersion: awgConfigSchemaVersion}}
	m.runMigrations()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a config that needs nothing must not be rewritten (err %v)", err)
	}
}
