package newserver

import (
	"strings"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

// pageState is a State with whatever the test needs the page to have heard
// from the backend.
type pageState struct {
	mtu     int
	subnets []string
}

func (s pageState) ServerMTU() int {
	if s.mtu < api.MinMTU || s.mtu > api.MaxMTU {
		return DefaultMTU
	}
	return s.mtu
}

func (s pageState) TakenPorts() map[int]string { return map[int]string{} }

func (s pageState) TakenSubnets() map[string]string {
	taken := map[string]string{}
	for _, subnet := range s.subnets {
		taken[subnet] = `server "srv"`
	}
	return taken
}

// The operator sets DEFAULT_MTU on the backend; the simple mode has no MTU
// field to show it in, so it has to be what a server created there actually
// gets. It used to send a number of its own and the setting was ignored.
func TestSimpleModeFollowsTheBackendMTU(t *testing.T) {
	f := &Form{state: pageState{mtu: 1280}}

	req := f.generated("srv", 54844)
	if req.MTU != 1280 {
		t.Fatalf("MTU = %d, want the backend's 1280", req.MTU)
	}
	if problems := req.ObfuscationParams.Validate(req.MTU); len(problems) > 0 {
		t.Fatalf("payload does not validate at that MTU:\n%s", strings.Join(problems, "\n"))
	}
	// The wider MTU headroom is the point: at 1280 a padding can use the whole
	// recommended window instead of collapsing onto its floor.
	if wire := req.MTU + 60 + req.ObfuscationParams.S4; wire > 1500 {
		t.Fatalf("a full transport packet would be %d bytes on the wire", wire)
	}
}

// A backend that reports nothing usable - the status call has not landed yet,
// or DEFAULT_MTU is outside the range a server may use - leaves the built-in
// default in place rather than sending something invalid.
func TestSimpleModeFallsBackToTheBuiltInMTU(t *testing.T) {
	for _, reported := range []int{0, 1279, 1441, -1} {
		f := &Form{state: pageState{mtu: reported}}

		if got := f.generated("srv", 54844).MTU; got != DefaultMTU {
			t.Errorf("backend reported %d: MTU = %d, want the built-in %d", reported, got, DefaultMTU)
		}
	}
}

// generated() is the whole simple-mode payload: a name, a port, and every
// other field derived. It is what "Create server" sends with "Advanced
// settings" unticked.
func TestSimpleModePayloadIsValid(t *testing.T) {
	f := &Form{state: pageState{}}

	req := f.generated("My VPN Server", 54844)

	if req.Name != "My VPN Server" || req.Port != 54844 {
		t.Fatalf("the two fields the user filled in should survive: %+v", req)
	}
	if req.Subnet != "10.0.0.0/24" {
		t.Errorf("Subnet = %q, want the first free /24", req.Subnet)
	}
	if req.MTU != DefaultMTU {
		t.Errorf("MTU = %d, want %d", req.MTU, DefaultMTU)
	}
	if req.DNS != defaultDNS {
		t.Errorf("DNS = %v, want %q", req.DNS, defaultDNS)
	}
	if req.Endpoint != "" {
		t.Errorf("Endpoint = %q, want it left to the backend's detected public IP", req.Endpoint)
	}
	if req.Obfuscation == nil || !*req.Obfuscation || req.AutoStart == nil || !*req.AutoStart {
		t.Fatalf("the simple mode should obfuscate and auto-start: %+v", req)
	}
	if req.ObfuscationParams == nil {
		t.Fatal("the simple mode should carry a generated parameter set")
	}
	if problems := req.ObfuscationParams.Validate(req.MTU); len(problems) > 0 {
		t.Fatalf("the generated payload does not pass validation:\n%s", strings.Join(problems, "\n"))
	}
}

// A second server created in the simple mode must not land on the subnet the
// first one took, since neither the form nor the backend would notice.
func TestSimpleModePicksAFreeSubnet(t *testing.T) {
	f := &Form{state: pageState{subnets: []string{"10.0.0.0/24", "10.1.0.0/24"}}}

	if got := f.generated("third", 54846).Subnet; got != "10.2.0.0/24" {
		t.Errorf("Subnet = %q, want 10.2.0.0/24", got)
	}
}

// An existing server on a wider block covers every /24 inside it, so the
// simple mode has to look past it rather than match subnets as strings.
func TestSimpleModeSkipsSubnetsInsideAWiderOne(t *testing.T) {
	f := &Form{state: pageState{subnets: []string{"10.0.0.0/15"}}}

	if got := f.generated("second", 54845).Subnet; got != "10.2.0.0/24" {
		t.Errorf("Subnet = %q, want 10.2.0.0/24", got)
	}
}
