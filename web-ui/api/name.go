package api

import "strings"

// The name rule: a client or a server name has to be usable as a file name
// on the desktops people unpack an archive on. It lives here, next to the
// wire types, because both sides need it: the create form checks a name
// before the request goes out, the backend checks it again before it stores
// anything. A rule stated twice is a rule that drifts, and the two copies
// disagreeing is exactly the bug the user would see as "the form let me,
// the server did not".

// NameForbiddenChars are the characters a name may not hold: the ones
// Windows refuses in a file name, plus the path separators - a name that
// carries one writes outside the directory it is unpacked into.
const NameForbiddenChars = `<>:"|?*/\`

// NameForbiddenList is the same set written out for a human to read: the
// hint under the name field names the characters and the problem message
// repeats them. The test beside them keeps the two lists the same.
const NameForbiddenList = `< > : " | ? * / \`

// The problems the rule answers with. The messages are fixed - they do not
// name the offending character - so both sides can translate them.
const (
	// NameProblemForbidden is what a name carrying one of the forbidden
	// characters is told.
	NameProblemForbidden = "Name must not contain any of these characters: " + NameForbiddenList
	// NameProblemTrailing is what a name ending in a dot or a space is
	// told: Windows drops both, so the file would not carry the name.
	NameProblemTrailing = "Name must not end with a dot or a space"
)

// ValidateName reports what is wrong with a client or a server name, or ""
// when the name is usable.
//
// The name lands in a file name - an entry of the archive, the name of the
// single download - so a name Windows would reshape is refused here instead
// of being reshaped on the way out: the file the user gets is called what
// they asked for, on every desktop.
//
// An empty name is not a problem here: the caller has a fallback of its own
// for it.
func ValidateName(name string) string {
	if strings.ContainsAny(name, NameForbiddenChars) {
		return NameProblemForbidden
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return NameProblemTrailing
	}
	return ""
}
