package api

import (
	"strings"
	"testing"
)

// A name every desktop accepts as a file name is accepted as it is. The
// device names Windows keeps for itself are among them: a client may carry
// one, and the file is renamed at download time.
func TestValidateNameAcceptsUsableNames(t *testing.T) {
	for _, ok := range []string{
		"", "alice", "My VPN Server", "client-1", "a.b", "CON", "имя",
	} {
		if problem := ValidateName(ok); problem != "" {
			t.Errorf("ValidateName(%q) = %q, want no problem", ok, problem)
		}
	}
}

// The characters Windows refuses in a file name, and the path separators,
// are refused by name - whatever else the name holds.
func TestValidateNameRefusesTheForbiddenCharacters(t *testing.T) {
	for _, bad := range []string{
		"a<b", "a>b", "a:b", `a"b`, "a|b", "a?b", "a*b", "a/b", `a\b`,
		`<:>|?*/\`,
	} {
		if problem := ValidateName(bad); problem != NameProblemForbidden {
			t.Errorf("ValidateName(%q) = %q, want the forbidden-characters problem", bad, problem)
		}
	}
}

// A name ending in a dot or a space is refused: Windows drops both, so the
// file would not carry the name it was given.
func TestValidateNameRefusesATrailingDotOrSpace(t *testing.T) {
	for _, bad := range []string{"name.", "name ", "name. ", "name ."} {
		if problem := ValidateName(bad); problem != NameProblemTrailing {
			t.Errorf("ValidateName(%q) = %q, want the trailing problem", bad, problem)
		}
	}
}

// The messages are fixed: they do not name the offending character, so the
// UI can translate them. Two names that break the rule the same way get the
// same message.
func TestTheNameProblemsAreFixedMessages(t *testing.T) {
	if NameProblemForbidden == NameProblemTrailing {
		t.Error("the two problems share one message")
	}
	if got := ValidateName("a:b"); got != ValidateName("c/d") {
		t.Errorf("the same problem got two messages: %q and %q", got, ValidateName("c/d"))
	}
	if got := ValidateName("name."); got != ValidateName("other ") {
		t.Errorf("the same problem got two messages: %q and %q", got, ValidateName("other "))
	}
	// A name that breaks both rules is told about the characters first.
	if got := ValidateName("a:b."); got != NameProblemForbidden {
		t.Errorf("ValidateName(%q) = %q, want the forbidden-characters problem", "a:b.", got)
	}
}

// The list the UI names and the set the rule refuses are the same
// characters: one is written out for a human, the other is what the rule
// checks.
func TestTheNamedCharactersAreTheForbiddenOnes(t *testing.T) {
	if got := strings.Join(strings.Fields(NameForbiddenList), ""); got != NameForbiddenChars {
		t.Errorf("the named characters %q are not the forbidden set %q", got, NameForbiddenChars)
	}
}
