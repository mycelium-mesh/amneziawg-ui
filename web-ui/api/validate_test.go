package api

import (
	"strings"
	"testing"
)

// validParams is a set that passes every check, so each case below can break
// exactly one rule.
func validParams() *ObfuscationParams {
	return &ObfuscationParams{
		Jc: 8, Jmin: 8, Jmax: 80,
		S1: 50, S2: 50, S3: 50, S4: 50,
		H1: 1, H2: 2, H3: 3, H4: 4,
	}
}

func TestValidateAcceptsAValidSet(t *testing.T) {
	if problems := validParams().Validate(MinMTU); len(problems) > 0 {
		t.Fatalf("valid parameters rejected:\n%s", strings.Join(problems, "\n"))
	}
}

// Validation rejects what the engine would refuse, and nothing beyond that:
// the recommended values live in the form's captions, and a config that
// departs from them on purpose still has to go through.
func TestValidateEnforcesTheEnginesBounds(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ObfuscationParams)
		wantErr string
	}{
		{"Jc past uint16", func(p *ObfuscationParams) { p.Jc = 65536 }, "Jc (65536) must be in [0, 65535]"},
		{"an empty junk packet", func(p *ObfuscationParams) { p.Jmin = 0 }, "Jmin (0) must be in [1, 65535]"},
		{"Jmax below Jmin", func(p *ObfuscationParams) { p.Jmax = 4 }, "must be less than Jmax"},
		{"Jmax past the MTU", func(p *ObfuscationParams) { p.Jmax = MinMTU + 1 }, "Jmax <= MTU"},
		{"S1 below the header protection floor", func(p *ObfuscationParams) { p.S1 = 11 }, "S1 (11) must be in [12, 65535]"},
		{"S3 past uint16", func(p *ObfuscationParams) { p.S3 = 65536 }, "S3 (65536) must be in [12, 65535]"},
		{"S1 past the MTU headroom", func(p *ObfuscationParams) { p.S1 = MinMTU - 147 }, "must fit MTU-148"},
		{"S2 past the MTU headroom", func(p *ObfuscationParams) { p.S2 = MinMTU - 91 }, "must fit MTU-92"},
		{"the one indistinguishable pair", func(p *ObfuscationParams) { p.S2 = p.S1 + 56 }, "must not equal S2"},
		{"a header past uint32", func(p *ObfuscationParams) { p.H2 = 4294967296 }, "H2 (4294967296) must be in [1, 4294967295]"},
		{"a header below 1", func(p *ObfuscationParams) { p.H2 = 0 }, "H2 (0) must be in [1, 4294967295]"},
		{"two identical headers", func(p *ObfuscationParams) { p.H4 = p.H1 }, "must differ"},
		{"a timing knob past uint16", func(p *ObfuscationParams) { p.RekeyAfterTime = "70000" }, "RekeyAfterTime (70000) must be in [0, 65535]"},
		{"a reversed timing range", func(p *ObfuscationParams) { p.ContentPaddingAddition = "64-0" }, "reversed range"},
		{"a timing knob that is not a range", func(p *ObfuscationParams) { p.RekeyTimeout = "soon" }, "must be an integer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validParams()
			tc.mutate(p)

			problems := p.Validate(MinMTU)
			if len(problems) == 0 {
				t.Fatalf("want a problem mentioning %q, got none", tc.wantErr)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.wantErr) {
				t.Fatalf("want a problem mentioning %q, got:\n%s", tc.wantErr, strings.Join(problems, "\n"))
			}
		})
	}
}

// With header protection on - which is the only mode this app has - the
// AmneziaWG docs recommend leaving H1-H4 at the standard message types and
// giving all four paddings the same value. Validation used to refuse both.
func TestValidateAcceptsTheRecommendedShape(t *testing.T) {
	if problems := validParams().Validate(MinMTU); len(problems) > 0 {
		t.Fatalf("the shape the docs recommend was rejected:\n%s", strings.Join(problems, "\n"))
	}
}

// Custom headers stay available for anyone who wants them, which is what the
// pre-3.1 advice called for.
func TestValidateAcceptsCustomHeaders(t *testing.T) {
	p := validParams()
	p.H1, p.H2, p.H3, p.H4 = 1000, 2000, 3000, 2147483647

	if problems := p.Validate(MinMTU); len(problems) > 0 {
		t.Fatalf("custom headers rejected:\n%s", strings.Join(problems, "\n"))
	}
}

// Every problem at once, so a form can show them together instead of making
// the user fix one per attempt.
func TestValidateReportsEveryProblem(t *testing.T) {
	p := validParams()
	p.Jc, p.S1, p.H2 = 65536, 11, 0

	if problems := p.Validate(MinMTU); len(problems) < 3 {
		t.Fatalf("want a problem per broken rule, got %d:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

// An empty knob means "use the engine default" and must not be read as a
// malformed range.
func TestValidateAcceptsEmptyAndRangedKnobs(t *testing.T) {
	p := validParams()
	p.ContentPaddingAddition = "0-64"
	p.RekeyAfterTime = "120"
	p.PersistentKeepalive = "22-30"
	p.RekeyTimeout = ""

	if problems := p.Validate(MinMTU); len(problems) > 0 {
		t.Fatalf("valid knobs rejected:\n%s", strings.Join(problems, "\n"))
	}
}

// Empty entries mean "skip this packet", which is how I2-I5 are shipped by
// default; only what is actually set gets parsed.
func TestValidateISettingsSkipsEmptyEntries(t *testing.T) {
	if problems := ValidateISettings(ISettings{"i1": "<t>", "i2": "", "i3": "   "}); len(problems) > 0 {
		t.Fatalf("empty entries should be skipped, got %v", problems)
	}

	problems := ValidateISettings(ISettings{"i4": "<r 2000>"})
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "I4: ") {
		t.Fatalf("want one problem naming I4, got %v", problems)
	}
}

func TestValidateEndpointAcceptsHostsRejectsInjection(t *testing.T) {
	for _, ok := range []string{
		"203.0.113.5", "vpn.example.com", "a.io", "x.y-z.example",
		"xn--b1aew.xn--p1ai", "[2001:db8::1]",
	} {
		if problem := ValidateEndpoint(ok); problem != "" {
			t.Errorf("ValidateEndpoint(%q) = %q, want empty", ok, problem)
		}
	}
	for _, bad := range []string{
		"vpn.example.com:51820", "203.0.113.5\n\n[Peer]",
		"vpn.example.com PublicKey = x", "https://vpn.example.com",
		"host name", "host_name", "-lead.example", "trail-.example",
		"192.168.1.1:80", "2001:db8::1",
	} {
		if problem := ValidateEndpoint(bad); problem == "" {
			t.Errorf("ValidateEndpoint(%q) = empty, want a problem", bad)
		}
	}
}

// The effective endpoint is the one the operator set, else the address the
// backend stamped on the server - the same rule the backend renders configs
// with, so the card cannot show a different address than the config carries.
func TestServerEffectiveEndpoint(t *testing.T) {
	if got := (Server{Endpoint: "vpn.example.com", PublicIP: "203.0.113.5"}).EffectiveEndpoint(); got != "vpn.example.com" {
		t.Errorf("EffectiveEndpoint = %q, want the manual one", got)
	}
	if got := (Server{PublicIP: "203.0.113.5"}).EffectiveEndpoint(); got != "203.0.113.5" {
		t.Errorf("EffectiveEndpoint = %q, want the stamped one", got)
	}
	if got := (Server{}).EffectiveEndpoint(); got != "" {
		t.Errorf("EffectiveEndpoint = %q, want empty", got)
	}
}
