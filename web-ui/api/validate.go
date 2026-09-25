package api

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file and generate.go hold the AmneziaWG rules both sides of the app
// need: the create form checks them before a request goes out, the backend
// checks them again before it writes a config file. They live here with the
// wire types, for the same reason those do - a rule stated twice is a rule
// that drifts, and the two copies disagreeing is exactly the bug the user
// would see as "the form let me, the server did not".
//
// The limits are what the engine accepts, not what the docs advise: the
// recommended values belong in the form's captions, and a deliberate
// departure from them is a legitimate config nothing here should block.
// Sources are the AmneziaWG 3.1 parameter reference
// (docs.amnezia.org/documentation/amnezia-wg) and amneziawg-go itself.
const (
	// MinPort and MaxPort bound a server's UDP listen port; awg-quick fails
	// on anything outside them, but only once the server is started.
	MinPort = 1
	MaxPort = 65535

	// MinMTU and MaxMTU bound a server's tunnel MTU.
	MinMTU = 1280
	MaxMTU = 1440

	// MaxUint16 caps Jc, Jmin, Jmax, S1-S4 and both ends of every range-typed
	// timing knob: that is the width awg(8) stores them in.
	MaxUint16 = 65535

	// MinPadding is what AmneziaWG 3.x header protection needs from each of
	// S1-S4: the cipher takes its 12-byte nonce from the start of that
	// padding.
	MinPadding = 12

	// MinHeader and MaxHeader bound H1-H4, which are uint32 and must differ
	// from each other. 1-4 are the plain WireGuard message types, which
	// AmneziaWG reads as "custom headers off" - what the 3.1 docs recommend
	// leaving them at when header protection is on, so they are accepted
	// rather than rejected.
	MinHeader = 1
	MaxHeader = 4294967295

	// MaxCPSCount is the ceiling the docs put on <r>, <rc> and <rd>.
	MaxCPSCount = 1000
)

// Validate reports every rule the parameter set breaks at the given MTU, so a
// form can show them together rather than one per attempt. An empty result
// means the engine will accept it.
func (p *ObfuscationParams) Validate(mtu int) []string {
	if p == nil {
		return []string{"obfuscation parameters are required"}
	}

	var problems []string

	for _, field := range []struct {
		name  string
		value int
		lo    int
	}{
		{"Jc", p.Jc, 0}, {"Jmin", p.Jmin, 1}, {"Jmax", p.Jmax, 1},
		{"S1", p.S1, MinPadding}, {"S2", p.S2, MinPadding},
		{"S3", p.S3, MinPadding}, {"S4", p.S4, MinPadding},
	} {
		if field.value < field.lo || field.value > MaxUint16 {
			problems = append(problems, fmt.Sprintf("%s (%d) must be in [%d, %d]", field.name, field.value, field.lo, MaxUint16))
		}
	}

	if !(p.Jmin < p.Jmax && p.Jmax <= mtu) {
		problems = append(problems, fmt.Sprintf("Jmin (%d) must be less than Jmax (%d), and Jmax <= MTU (%d)", p.Jmin, p.Jmax, mtu))
	}
	if p.S1 > mtu-148 {
		problems = append(problems, fmt.Sprintf("S1 (%d) must fit MTU-148 (%d)", p.S1, mtu-148))
	}
	if p.S2 > mtu-92 {
		problems = append(problems, fmt.Sprintf("S2 (%d) must fit MTU-92 (%d)", p.S2, mtu-92))
	}
	// The one combination the engine cannot tell apart: S2+92 would land on
	// S1+148.
	if p.S1+56 == p.S2 {
		problems = append(problems, fmt.Sprintf("S1 + 56 (%d) must not equal S2 (%d)", p.S1+56, p.S2))
	}

	// H1-H4 replace the message type field of every packet type, and the
	// engine refuses two that overlap.
	seen := map[int]string{}
	for _, header := range []struct {
		name  string
		value int
	}{{"H1", p.H1}, {"H2", p.H2}, {"H3", p.H3}, {"H4", p.H4}} {
		switch other, clash := seen[header.value]; {
		case header.value < MinHeader || header.value > MaxHeader:
			problems = append(problems, fmt.Sprintf("%s (%d) must be in [%d, %d]", header.name, header.value, MinHeader, MaxHeader))
		case clash:
			problems = append(problems, fmt.Sprintf("%s (%d) must differ from %s", header.name, header.value, other))
		default:
			seen[header.value] = header.name
		}
	}

	for _, knob := range [][2]string{
		{"ContentPaddingAddition", p.ContentPaddingAddition},
		{"RekeyAfterTime", p.RekeyAfterTime},
		{"RekeyTimeout", p.RekeyTimeout},
		{"RejectAfterTime", p.RejectAfterTime},
		{"KeepaliveTimeout", p.KeepaliveTimeout},
		{"MaxHandshakeAttempts", p.MaxHandshakeAttempts},
		{"PersistentKeepalive", p.PersistentKeepalive},
	} {
		if knob[1] == "" {
			continue // empty means "use the engine default"
		}
		if problem := ValidateUintRange(knob[0], knob[1]); problem != "" {
			problems = append(problems, problem)
		}
	}

	return problems
}

var uintRangePattern = regexp.MustCompile(`^(\d+)(?:-(\d+))?$`)

// ValidateUintRange checks one of the optional timing knobs against what
// awg(8) stores it in - a pair of uint16s - and returns an empty string when
// the value is fine. The pattern alone would let "70000" or "64-0" through.
func ValidateUintRange(key, value string) string {
	match := uintRangePattern.FindStringSubmatch(value)
	if match == nil {
		return fmt.Sprintf("%s (%s) must be an integer or an \"a-b\" range", key, value)
	}

	lo, err := strconv.Atoi(match[1])
	if err != nil || lo > MaxUint16 {
		return fmt.Sprintf("%s (%s) must be in [0, %d]", key, value, MaxUint16)
	}
	if match[2] == "" {
		return ""
	}

	hi, err := strconv.Atoi(match[2])
	if err != nil || hi > MaxUint16 {
		return fmt.Sprintf("%s (%s) must be in [0, %d]", key, value, MaxUint16)
	}
	if hi < lo {
		return fmt.Sprintf("%s (%s) is a reversed range: the second bound must not be smaller than the first", key, value)
	}
	return ""
}

// ── Custom signature packets (I1-I5) ─────────────────────────────────────────

// cpsTags is the tag set amneziawg-go's obfBuilders map holds, and the value
// is whether the tag takes a byte count as its argument. b/t/r/rc/rd are the
// five the AmneziaWG docs describe; d, ds and dz are in the same map but copy
// or measure a source buffer, and an I-packet is built with no source, so they
// contribute nothing there. They are accepted anyway - the engine accepts
// them, and refusing a config it would have run is not this app's job.
var cpsTags = map[string]bool{
	"b": false, "t": false, "d": false, "ds": false,
	"r": true, "rc": true, "rd": true, "dz": true,
}

// ValidateISettings reports the problems in a whole I1-I5 set. An empty entry
// means "skip this packet", which is how I2-I5 ship by default.
func ValidateISettings(settings ISettings) []string {
	var problems []string
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("i%d", i)
		if spec := strings.TrimSpace(settings[key]); spec != "" {
			if problem := ValidateCPS(strings.ToUpper(key), spec); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

// ValidateCPS checks one I1-I5 value against the grammar amneziawg-go parses
// in newObfChain, so a typo is caught before it reaches a client .conf and
// surfaces there as a tunnel that will not start. It returns an empty string
// when the value is fine, and prefixes any problem with key.
//
// It is stricter than the engine on exactly one point: a non-empty value that
// holds no tags at all parses cleanly upstream and quietly produces no packet,
// which can only ever be a mistake.
func ValidateCPS(key, spec string) string {
	fail := func(format string, args ...any) string {
		return fmt.Sprintf("%s: %s", key, fmt.Sprintf(format, args...))
	}

	tags := 0
	for rest := spec; ; {
		open := strings.IndexByte(rest, '<')
		if open == -1 {
			break
		}
		closing := strings.IndexByte(rest[open:], '>')
		if closing == -1 {
			return fail("tag %q is missing its closing \">\"", rest[open:])
		}

		fields := strings.Fields(rest[open+1 : open+closing])
		rest = rest[open+closing+1:]
		if len(fields) == 0 {
			return fail("empty tag \"<>\"")
		}

		name := fields[0]
		takesCount, known := cpsTags[name]
		if !known {
			return fail("unknown tag \"<%s>\"; the tags are <b 0x...>, <t>, <r N>, <rc N> and <rd N>", name)
		}

		switch {
		case name == "b":
			if problem := checkHexTag(fields); problem != "" {
				return fail("%s", problem)
			}
		case takesCount:
			if problem := checkCountTag(name, fields); problem != "" {
				return fail("%s", problem)
			}
		}
		tags++
	}

	if tags == 0 {
		return fail("no tags - a value like \"<b 0x00ff><r 20>\" is what builds the packet, and text outside tags is ignored")
	}
	return ""
}

func checkHexTag(fields []string) string {
	if len(fields) < 2 {
		return "<b> needs a hex sequence, e.g. <b 0xf6ab3267fa>"
	}
	digits := strings.TrimPrefix(fields[1], "0x")
	switch {
	case digits == "":
		return "<b> needs a hex sequence, e.g. <b 0xf6ab3267fa>"
	case len(digits)%2 != 0:
		return fmt.Sprintf("<b %s> has an odd number of hex digits - one byte is two of them", fields[1])
	}
	if _, err := hex.DecodeString(digits); err != nil {
		return fmt.Sprintf("<b %s> is not hexadecimal", fields[1])
	}
	return ""
}

func checkCountTag(name string, fields []string) string {
	if len(fields) < 2 {
		return fmt.Sprintf("<%s> needs a byte count, e.g. <%s 20>", name, name)
	}
	count, err := strconv.Atoi(fields[1])
	if err != nil {
		return fmt.Sprintf("<%s %s> needs a byte count", name, fields[1])
	}
	if count < 0 || count > MaxCPSCount {
		return fmt.Sprintf("<%s %d> must be in [0, %d]", name, count, MaxCPSCount)
	}
	return ""
}
