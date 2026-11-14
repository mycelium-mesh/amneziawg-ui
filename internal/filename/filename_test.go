package filename

import (
	"strings"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

func TestSafeKeepsNamesAnyDesktopAccepts(t *testing.T) {
	for _, ok := range []string{
		"laptop", "Alice", "my-server", "client_1", "vpn.example.com",
	} {
		if got := Safe(ok, "client"); got != ok {
			t.Errorf("Safe(%q) = %q, want it unchanged", ok, got)
		}
	}
}

func TestSafeReplacesWhatAFileNameMayNotHold(t *testing.T) {
	for _, bad := range []string{
		"a:b*c?", "a/b", "a\\b", `a"b`, "a<b>c", "a|b", "control\x01char",
	} {
		got := Safe(bad, "client")
		if strings.ContainsAny(got, api.NameForbiddenChars) {
			t.Errorf("Safe(%q) = %q, want no forbidden character left", bad, got)
		}
		if strings.ContainsRune(got, 0x01) {
			t.Errorf("Safe(%q) = %q, want no control character left", bad, got)
		}
	}
}

// Every character the shared name rule refuses has to be replaced here too:
// the two are one rule, and a character that survives one and not the other
// is a name the archive would carry into a file Windows refuses.
func TestSafeReplacesEveryCharacterTheNameRuleRefuses(t *testing.T) {
	for _, r := range api.NameForbiddenChars {
		got := Safe("a"+string(r)+"b", "client")
		if strings.ContainsRune(got, r) {
			t.Errorf("Safe(%q) = %q, want %q replaced", string(r), got, string(r))
		}
	}
}

func TestSafeDropsWhatWindowsWouldDrop(t *testing.T) {
	if got := Safe("name.", "client"); got != "name" {
		t.Errorf("Safe(%q) = %q, want the trailing dot dropped", "name.", got)
	}
	if got := Safe("name ", "client"); got != "name" {
		t.Errorf("Safe(%q) = %q, want the trailing space dropped", "name ", got)
	}
	if got := Safe("..", "client"); got != "client" {
		t.Errorf("Safe(%q) = %q, want the fallback", "..", got)
	}
}

func TestSafeKeepsTheDeviceNamesForWindowsItself(t *testing.T) {
	for _, reserved := range []string{"CON", "con", "NUL", "COM1", "LPT9"} {
		got := Safe(reserved, "client")
		if got == reserved {
			t.Errorf("Safe(%q) = %q, want it renamed", reserved, got)
		}
		if got != "_"+reserved {
			t.Errorf("Safe(%q) = %q, want an underscore in front", reserved, got)
		}
	}
}

func TestSafeFallsBackOnAnEmptyName(t *testing.T) {
	if got := Safe("", "client"); got != "client" {
		t.Errorf("Safe(\"\") = %q, want the fallback", got)
	}
}
