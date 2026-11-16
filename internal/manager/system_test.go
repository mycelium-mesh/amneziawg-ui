package manager

import (
	"errors"
	"path/filepath"
	"testing"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/awg/awgtest"
	"amneziawg-web-ui/internal/config"
	"amneziawg-web-ui/internal/store"
	"amneziawg-web-ui/internal/wgconf"
	"amneziawg-web-ui/web-ui/api"
)

// shippedISettings is the shipped standard set, for whole-set comparisons.
func shippedISettings() api.ISettings {
	return wgconf.DefaultISettings()
}

// Without a saved set the effective standard set is the shipped one; with
// one, only its non-empty values overlay the shipped set - an empty saved
// value holds no opinion and cannot blank the shipped value behind it.
func TestDefaultISettingsLayersTheSavedSetOverTheShippedOne(t *testing.T) {
	m, _ := newTestManager(t)

	got := m.DefaultISettings()
	want := shippedISettings()
	for key := range want {
		if got[key] != want[key] {
			t.Errorf("%s = %q, want the shipped %q with nothing saved", key, got[key], want[key])
		}
	}

	m.mu.Lock()
	m.cfg.DefaultISettings = api.ISettings{"i1": "<b 0xaabb>", "i2": ""}
	m.mu.Unlock()

	got = m.DefaultISettings()
	if got["i1"] != "<b 0xaabb>" {
		t.Errorf("i1 = %q, want the saved value", got["i1"])
	}
	if got["i2"] != want["i2"] {
		t.Errorf("i2 = %q, want the shipped %q: an empty saved value holds no opinion", got["i2"], want["i2"])
	}
	if got["i3"] != want["i3"] {
		t.Errorf("i3 = %q, want the shipped %q: keys the saved set leaves out stay shipped", got["i3"], want["i3"])
	}
}

// An update replaces the saved set rather than merging into it, a set the
// shared rules reject changes nothing, and an empty set clears back to the
// shipped defaults.
func TestUpdateDefaultISettingsReplacesRejectsAndClears(t *testing.T) {
	m, _ := newTestManager(t)

	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i2": "<r 20>"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got := m.DefaultISettings()
	if got["i1"] != wgconf.DefaultI1 {
		t.Errorf("i1 = %q, want the shipped value back: the update replaces, not merges", got["i1"])
	}
	if got["i2"] != "<r 20>" {
		t.Errorf("i2 = %q, want the newly saved value", got["i2"])
	}

	// The same shared rules the form and the client-update path use: a
	// malformed packet is an invalid request, and the saved set is left
	// exactly as it stood.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i2": "<b 0xabc>"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed set: want ErrInvalid, got %v", err)
	}
	if got := m.DefaultISettings(); got["i2"] != "<r 20>" {
		t.Errorf("after the rejection i2 = %q, want the saved value untouched", got["i2"])
	}

	// A stray key is not one of I1-I5 and never reaches the stored set, and
	// a whitespace-only value is the emptiness the shared rules already
	// treat it as - neither can end up as an override.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>", "i6": "<r 20>"}); err != nil {
		t.Fatalf("stray key: %v", err)
	}
	got = m.DefaultISettings()
	if got["i1"] != "<b 0xaabb>" || len(got) != 5 {
		t.Errorf("effective set = %v, want the five I1-I5 keys with the saved i1", got)
	}
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "   "}); err != nil {
		t.Fatalf("whitespace-only: %v", err)
	}
	if got := m.DefaultISettings(); got["i1"] != wgconf.DefaultI1 {
		t.Errorf("whitespace-only i1 = %q, want it treated as empty and cleared", got["i1"])
	}

	// Empty clears: the shipped defaults stand again.
	if _, err := m.UpdateDefaultISettings(api.ISettings{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got = m.DefaultISettings()
	want := shippedISettings()
	for key := range want {
		if got[key] != want[key] {
			t.Errorf("after clear %s = %q, want the shipped %q", key, got[key], want[key])
		}
	}
}

// The saved set lives in web_config.json, so a restarted backend comes up
// with the same effective set.
func TestDefaultISettingsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web_config.json")
	st := store.New(path)
	settings := config.Settings{ConfigFile: path}

	first := New(settings, st, awg.New(awgtest.New()))
	if _, err := first.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>"}); err != nil {
		t.Fatal(err)
	}

	second := New(settings, st, awg.New(awgtest.New()))
	got := second.DefaultISettings()
	if got["i1"] != "<b 0xaabb>" {
		t.Errorf("after restart i1 = %q, want the saved value", got["i1"])
	}
	if got["i3"] != wgconf.DefaultI3 {
		t.Errorf("after restart i3 = %q, want the shipped %q", got["i3"], wgconf.DefaultI3)
	}
}

// A new client with apply_i_settings starts from the effective standard set:
// the shipped values under the operator's saved ones, and the client's own
// non-empty values over both. An empty client value never blanks the
// standard behind it, and without apply_i_settings nothing is written.
func TestAddClientStartsFromTheSavedStandardSet(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>", "i2": "<r 20>"}); err != nil {
		t.Fatal(err)
	}

	plain, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice", ApplyISettings: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.ISettings["i1"] != "<b 0xaabb>" {
		t.Errorf("alice i1 = %q, want the saved standard value", plain.ISettings["i1"])
	}
	if plain.ISettings["i2"] != "<r 20>" {
		t.Errorf("alice i2 = %q, want the saved standard value", plain.ISettings["i2"])
	}
	if plain.ISettings["i3"] != wgconf.DefaultI3 {
		t.Errorf("alice i3 = %q, want the shipped %q", plain.ISettings["i3"], wgconf.DefaultI3)
	}

	own, _, err := m.AddClient("s1", api.AddClientRequest{
		Name: "bob", ApplyISettings: true,
		ISettings: map[string]string{"i1": "<b 0xccdd>", "i2": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if own.ISettings["i1"] != "<b 0xccdd>" {
		t.Errorf("bob i1 = %q, want bob's own value: the client's non-empty value wins", own.ISettings["i1"])
	}
	if own.ISettings["i2"] != "<r 20>" {
		t.Errorf("bob i2 = %q, want the standard %q: an empty client value holds no opinion", own.ISettings["i2"], "<r 20>")
	}

	off, _, err := m.AddClient("s1", api.AddClientRequest{Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.ISettings) != 0 || off.ApplyISettings {
		t.Errorf("carol I-settings = %v (apply %v), want none", off.ISettings, off.ApplyISettings)
	}
}
