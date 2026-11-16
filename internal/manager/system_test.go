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

// An update merges into the saved standard key by key: only the keys it
// fills in change, a key it leaves out or sends empty keeps its saved state,
// and a key nobody ever saved stays "no override" so the shipped value stays
// live underneath. This is the partial-save guard: typing only I2 used to
// wipe the saved I1 back to the shipped default. A set the shared rules
// reject and a request with nothing filled in both change nothing; the one
// explicit full clear is ClearDefaultISettings.
func TestUpdateDefaultISettingsMergesIntoTheSavedStandard(t *testing.T) {
	m, _ := newTestManager(t)

	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>"}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// The dialog's partial save: only I2 is typed this time. The saved I1
	// must survive it, and I3 - never saved by anyone - must stay "no
	// override": the effective set keeps showing the shipped value there.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i2": "<r 20>"}); err != nil {
		t.Fatalf("partial save: %v", err)
	}
	got := m.DefaultISettings()
	if got["i1"] != "<b 0xaabb>" {
		t.Errorf("i1 = %q, want the earlier saved value kept by the partial save", got["i1"])
	}
	if got["i2"] != "<r 20>" {
		t.Errorf("i2 = %q, want the newly saved value", got["i2"])
	}
	if got["i3"] != wgconf.DefaultI3 {
		t.Errorf("i3 = %q, want the shipped %q: a key nobody saved keeps no override", got["i3"], wgconf.DefaultI3)
	}
	m.mu.RLock()
	_, i3Saved := m.cfg.DefaultISettings["i3"]
	m.mu.RUnlock()
	if i3Saved {
		t.Error("i3 landed in the saved set although no save filled it in")
	}

	// The same shared rules the form and the client-update path use: a
	// malformed packet is an invalid request, and the saved set is left
	// exactly as it stood.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i2": "<b 0xabc>"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed set: want ErrInvalid, got %v", err)
	}
	if got := m.DefaultISettings(); got["i1"] != "<b 0xaabb>" || got["i2"] != "<r 20>" {
		t.Errorf("after the rejection i1 = %q, i2 = %q, want both saved values untouched", got["i1"], got["i2"])
	}

	// A stray key is not one of I1-I5 and never reaches the stored set, but
	// the valid keys next to it still save.
	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xccdd>", "i6": "<r 20>"}); err != nil {
		t.Fatalf("stray key: %v", err)
	}
	m.mu.RLock()
	_, i6Saved := m.cfg.DefaultISettings["i6"]
	m.mu.RUnlock()
	if i6Saved {
		t.Error("stray key i6 reached the saved set")
	}
	if got := m.DefaultISettings(); got["i1"] != "<b 0xccdd>" || len(got) != 5 {
		t.Errorf("effective set = %v, want the five I1-I5 keys with the saved i1", got)
	}

	// An empty value is "no opinion": a whitespace-only value and an
	// all-empty request both leave the saved set exactly as it stood -
	// neither is a way to drop a saved override.
	for _, empty := range []struct {
		name string
		body api.ISettings
	}{
		{"whitespace-only", api.ISettings{"i1": "   "}},
		{"all-empty", api.ISettings{}},
	} {
		if _, err := m.UpdateDefaultISettings(empty.body); err != nil {
			t.Fatalf("%s: %v", empty.name, err)
		}
		if got := m.DefaultISettings(); got["i1"] != "<b 0xccdd>" || got["i2"] != "<r 20>" {
			t.Errorf("after the %s request i1 = %q, i2 = %q, want both saved values kept", empty.name, got["i1"], got["i2"])
		}
	}
}

// The one explicit full clear: the saved standard leaves and the shipped
// defaults stand again - across a restart as well, since the clear is
// persisted like any other change. Clearing an already-clear set changes
// nothing.
func TestClearDefaultISettingsRestoresTheShippedOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web_config.json")
	st := store.New(path)
	settings := config.Settings{ConfigFile: path}
	m := New(settings, st, awg.New(awgtest.New()))

	if _, err := m.UpdateDefaultISettings(api.ISettings{"i1": "<b 0xaabb>", "i2": "<r 20>"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := m.ClearDefaultISettings(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	want := shippedISettings()
	checkShipped := func(where string, got api.ISettings) {
		t.Helper()
		for key := range want {
			if got[key] != want[key] {
				t.Errorf("%s: %s = %q, want the shipped %q", where, key, got[key], want[key])
			}
		}
	}
	checkShipped("after the clear", m.DefaultISettings())
	checkShipped("after a restart", New(settings, st, awg.New(awgtest.New())).DefaultISettings())

	if saved := st.Load().DefaultISettings; len(saved) != 0 {
		t.Errorf("saved set on disk = %v, want it empty after the clear", saved)
	}
	if _, err := m.ClearDefaultISettings(); err != nil {
		t.Errorf("clearing an already-clear set: %v", err)
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
