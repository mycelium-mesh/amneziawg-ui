package internal

import (
	"fmt"
	"strings"
)

// Peer blocks in a server's .conf are introduced by a comment that names the
// client they belong to, and that comment is how this code finds a block
// again when the client is removed or suspended. The client's name is not an
// identity: two clients may share one, and a name is free text that can
// contain anything the operator typed. So the marker carries the client ID as
// well, and every lookup matches on that.
//
//	# Client: alice [id:ab12cd]
//	[Peer]
//	PublicKey = ...
//
// A comment without an ID - hand-written, or left by something else - still
// delimits a block, so removing the peer above it cannot swallow it, but it
// never matches a client.

const peerMarkerPrefix = "# Client:"

// peerMarker renders the comment introducing a client's peer block.
func peerMarker(name, clientID string) string {
	return fmt.Sprintf("%s %s [id:%s]", peerMarkerPrefix, name, clientID)
}

// parsePeerMarker splits a marker line into the client name and ID. ok is
// false for any other line; id is empty for a marker that carries none.
func parsePeerMarker(line string) (name, id string, ok bool) {
	trimmed := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(trimmed, peerMarkerPrefix)
	if !ok {
		return "", "", false
	}
	rest = strings.TrimSpace(rest)

	if strings.HasSuffix(rest, "]") {
		if head, tail, found := strings.Cut(rest[:len(rest)-1], "[id:"); found {
			return strings.TrimSpace(head), strings.TrimSpace(tail), true
		}
	}
	return rest, "", true
}

// markerMatches reports whether a marker line introduces this client's block.
// Only the ID decides: a name is neither unique nor trusted input.
func markerMatches(line string, client *Client) bool {
	_, id, ok := parsePeerMarker(line)
	return ok && id != "" && id == client.ID
}

// splitPeerBlock finds the client's peer block and returns the config lines
// with the block removed, plus the block itself. The block runs from its
// marker to the line before the next marker, trailing blank lines excluded,
// so removing it cannot swallow the peer that follows.
func splitPeerBlock(lines []string, client *Client) (rest, block []string, found bool) {
	start := -1
	for i, line := range lines {
		if markerMatches(line, client) {
			start = i
			break
		}
	}
	if start < 0 {
		return lines, nil, false
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if _, _, isMarker := parsePeerMarker(lines[i]); isMarker {
			end = i
			break
		}
	}

	block = trimTrailingBlank(lines[start:end])
	rest = append(append([]string{}, lines[:start]...), lines[end:]...)
	return trimTrailingBlank(rest), block, true
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// retagPeerBlock rewrites a stored block's marker from the client as it is
// now, so a client renamed while suspended comes back under its current name.
func retagPeerBlock(block []string, client *Client) []string {
	out := append([]string{}, block...)
	for i, line := range out {
		if _, _, ok := parsePeerMarker(line); ok {
			out[i] = peerMarker(client.Name, client.ID)
			break
		}
	}
	return out
}

// sanitizeName strips what must never reach a .conf comment, a Content
// Disposition header or a peer marker: a newline would split the comment and
// turn the rest of the name into config directives, and "[id:" would forge a
// marker. Control characters collapse into spaces rather than being rejected,
// so a name pasted with a stray tab still works.
func sanitizeName(name string, fallback string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteByte(' ')
		case r == '[' || r == ']' || r == '"':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := strings.Join(strings.Fields(b.String()), " ")
	if len([]rune(cleaned)) > maxNameRunes {
		cleaned = strings.TrimSpace(string([]rune(cleaned)[:maxNameRunes]))
	}
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

// maxNameRunes bounds a name so one client cannot bloat every generated
// config and filename.
const maxNameRunes = 64
