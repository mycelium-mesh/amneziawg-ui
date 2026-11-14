// Package filename turns a name into something that can be a file name on
// the desktops people unpack an archive on.
//
// The rule is one rule for the whole app: a name that survives it can be
// written on Windows, macOS and Linux alike. Windows is the strict one -
// it refuses a set of characters, drops trailing dots and spaces, and keeps
// a few names for its own devices - and macOS and Linux agree on the case:
// two names differing only in case are two files here and one file there.
package filename

import (
	"strings"
)

// invalid are the characters a Windows file name may not hold. The path
// separators join them: a name that carries one writes outside the
// directory it is unpacked into.
const invalid = `<>:"|?*/\`

// reserved are the device names Windows keeps for itself. A file called
// CON cannot be created there, not even with an extension - so the check
// happens on the name without its extension.
var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Safe returns name with everything a file name may not hold replaced, and
// an empty name replaced by fallback.
//
// What it replaces: the characters Windows refuses, the path separators, the
// control characters; the trailing dots and spaces Windows drops on its own
// (which would make two different names one file); and the device names it
// keeps for itself, which get an underscore in front.
func Safe(name, fallback string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(invalid, r) {
			return '_'
		}
		return r
	}, name)

	// Windows drops trailing dots and spaces, so "a." and "a" are one file
	// there - and in an archive they would be two entries of one name.
	cleaned = strings.TrimRight(cleaned, ". ")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		cleaned = fallback
	}
	if reserved[strings.ToUpper(cleaned)] {
		cleaned = "_" + cleaned
	}
	return cleaned
}
