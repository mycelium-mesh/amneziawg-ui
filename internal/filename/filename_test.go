package filename

import (
	"strings"
	"testing"
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
		if strings.ContainsAny(got, `<>:"|?*/\`) {
			t.Errorf("Safe(%q) = %q, want no forbidden character left", bad, got)
		}
		if strings.ContainsRune(got, 0x01) {
			t.Errorf("Safe(%q) = %q, want no control character left", bad, got)
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
