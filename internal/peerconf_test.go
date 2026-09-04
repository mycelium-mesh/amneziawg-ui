package internal

import (
	"strings"
	"testing"
)

func TestParsePeerMarker(t *testing.T) {
	cases := []struct {
		line, name, id string
		ok             bool
	}{
		{line: "# Client: alice [id:ab12cd]", name: "alice", id: "ab12cd", ok: true},
		{line: "  # Client: alice [id:ab12cd]  ", name: "alice", id: "ab12cd", ok: true},
		{line: "# Client: alice", name: "alice", ok: true},
		{line: "# Client: two words [id:x1]", name: "two words", id: "x1", ok: true},
		{line: "[Peer]"},
		{line: "PublicKey = ABC"},
		{line: "# something else"},
	}
	for _, c := range cases {
		name, id, ok := parsePeerMarker(c.line)
		if ok != c.ok || name != c.name || id != c.id {
			t.Errorf("parsePeerMarker(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.line, name, id, ok, c.name, c.id, c.ok)
		}
	}
}

// Two clients sharing a name is the case the ID exists for: matching by name
// alone removed both blocks at once.
func TestSplitPeerBlockPicksTheRightOneOfTwoNamesakes(t *testing.T) {
	conf := []string{
		"[Interface]",
		"PrivateKey = PRIV",
		"",
		"# Client: alice [id:aaa]",
		"[Peer]",
		"PublicKey = FIRST",
		"",
		"# Client: alice [id:bbb]",
		"[Peer]",
		"PublicKey = SECOND",
	}

	rest, block, found := splitPeerBlock(conf, &Client{ID: "bbb", Name: "alice"})
	if !found {
		t.Fatal("block not found")
	}
	if strings.Join(block, "\n") != "# Client: alice [id:bbb]\n[Peer]\nPublicKey = SECOND" {
		t.Errorf("wrong block extracted:\n%s", strings.Join(block, "\n"))
	}
	joined := strings.Join(rest, "\n")
	if !strings.Contains(joined, "FIRST") {
		t.Errorf("the namesake's block was removed too:\n%s", joined)
	}
	if strings.Contains(joined, "SECOND") {
		t.Errorf("the block is still there:\n%s", joined)
	}
}

// A marker with no ID belongs to no client - it was not written here - but it
// still ends the block above it, so removing that peer leaves it in place.
func TestSplitPeerBlockIgnoresAnUntaggedMarker(t *testing.T) {
	conf := []string{
		"[Interface]",
		"",
		"# Client: alice [id:aaa]",
		"[Peer]",
		"PublicKey = MINE",
		"",
		"# Client: alice",
		"[Peer]",
		"PublicKey = FOREIGN",
	}

	if _, _, found := splitPeerBlock(conf, &Client{ID: "", Name: "alice"}); found {
		t.Error("an untagged marker must not match a client")
	}

	rest, block, found := splitPeerBlock(conf, &Client{ID: "aaa", Name: "alice"})
	if !found || len(block) != 3 {
		t.Fatalf("found = %v, block = %v", found, block)
	}
	joined := strings.Join(rest, "\n")
	if strings.Contains(joined, "MINE") {
		t.Errorf("block not removed:\n%s", joined)
	}
	if !strings.Contains(joined, "FOREIGN") {
		t.Errorf("the untagged block was swallowed:\n%s", joined)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice", "alice"},
		{"  alice  ", "alice"},
		{"", "fb"},
		{"   ", "fb"},
		// A newline would end the comment line and turn the rest into config.
		{"alice\n[Peer]\nPublicKey = EVIL", "alice Peer PublicKey = EVIL"},
		// Brackets would let a name forge an ID tag.
		{"alice [id:other]", "alice id:other"},
		{`quote" name`, "quote name"},
		{strings.Repeat("x", 100), strings.Repeat("x", maxNameRunes)},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in, "fb"); got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The marker a sanitised name produces must survive a round trip, or a
// crafted name could still detach a peer block from its client.
func TestSanitizedNameCannotForgeAMarker(t *testing.T) {
	name := sanitizeName("evil [id:victim]", "client")
	line := peerMarker(name, "mine12")

	gotName, gotID, ok := parsePeerMarker(line)
	if !ok || gotID != "mine12" || gotName != name {
		t.Fatalf("parsePeerMarker(%q) = (%q, %q, %v)", line, gotName, gotID, ok)
	}
	if markerMatches(line, &Client{ID: "victim", Name: name}) {
		t.Errorf("marker %q matched the forged ID", line)
	}
}
