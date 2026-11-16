package awg_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// upstreamAddIf is add_if as the pinned awg-quick ships it - the only part
// scripts/patch-awg-quick.sh rewrites - plus the minimum around it needed to
// run the function. When AWG_TOOLS_VERSION moves and upstream changes this
// function, the patch script fails the image build on its own line checks;
// this fixture then follows so the behavior stays covered.
const upstreamAddIf = `#!/bin/bash
set -e -o pipefail
INTERFACE=wg-test
cmd() {
	echo "[#] $*" >&2
	"$@"
}
add_if() {
	local ret
	if ! cmd ip link add "$INTERFACE" type amneziawg; then
		ret=$?
		[[ -e /sys/module/amneziawg ]] || ! command -v "${WG_QUICK_USERSPACE_IMPLEMENTATION:-amneziawg-go}" >/dev/null && exit $ret
		echo "[!] Missing WireGuard (Amnezia VPN) kernel module. Falling back to slow userspace implementation." >&2
		cmd "${WG_QUICK_USERSPACE_IMPLEMENTATION:-amneziawg-go}" "$INTERFACE"
	fi
}
add_if
echo add-if-returned
`

// The patched add_if and QuickUp's forced retry are one contract: with
// WG_QUICK_FORCE_USERSPACE=1 the interface must come from the userspace
// engine and never from the kernel, while without it upstream's behavior -
// kernel first, engine only when the interface cannot be created - must be
// untouched. The script is patched and then executed, because text matching
// alone is not evidence the fallback engages.
func TestPatchedAwgQuickFallsBackWhenForced(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is needed to run the patched awg-quick")
	}
	dir := t.TempDir()

	fixture := filepath.Join(dir, "awg-quick")
	if err := os.WriteFile(fixture, []byte(upstreamAddIf), 0o755); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join("..", "..", "scripts", "patch-awg-quick.sh")
	if out, err := exec.Command("sh", patch, fixture).CombinedOutput(); err != nil {
		t.Fatalf("patch-awg-quick.sh: %v\n%s", err, out)
	}

	// The one host fact a unit test cannot carry is /sys/module: point the
	// probe at a file this test creates, so each case controls "module
	// present" without needing the module. This runs after the patch, whose
	// checks hold the shipped path to be exactly the upstream one.
	patched, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(dir, "amneziawg-module")
	patched = []byte(strings.ReplaceAll(string(patched), "/sys/module/amneziawg", module))
	if err := os.WriteFile(fixture, patched, 0o755); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin := func(name, script string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeBin("ip", "#!/bin/sh\n[ -z \"$FAKE_IP_FAIL\" ] || exit 1\n")
	engineBin := filepath.Join(bin, "proxy")

	const (
		kernel   = "[#] ip link add wg-test type amneziawg"
		engine   = "proxy wg-test"
		returned = "add-if-returned"
	)
	cases := []struct {
		name          string
		force         bool
		ipFails       bool
		enginePresent bool
		modulePresent bool
		wantKernel    bool
		wantEngine    bool
		wantReturned  bool
		wantExit      string // "zero", "nonzero", "any"
	}{
		{
			// Issue #11: the old module is present, so its presence must not
			// drag the forced start back onto the kernel.
			name:  "forced start uses the engine even while the module is present",
			force: true, enginePresent: true, modulePresent: true,
			wantEngine: true, wantReturned: true, wantExit: "zero",
		},
		{
			name:  "forced start without the engine fails before touching the kernel",
			force: true, modulePresent: true,
			wantExit: "nonzero",
		},
		{
			name:          "kernel first, no engine, when not forced",
			enginePresent: true,
			wantKernel:    true, wantReturned: true, wantExit: "zero",
		},
		{
			name:    "engine only when the kernel refuses the interface, as upstream",
			ipFails: true, enginePresent: true,
			wantKernel: true, wantEngine: true, wantReturned: true, wantExit: "zero",
		},
		{
			name:    "no fallback while the module holds the interface, as upstream",
			ipFails: true, enginePresent: true, modulePresent: true,
			wantKernel: true,
			// Upstream exits through "exit $ret", whose $ret comes from an
			// "if !" condition and is always 0; which status it someday
			// settles on is upstream's to decide, not this patch's.
			wantExit: "any",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.modulePresent {
				if err := os.WriteFile(module, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				os.Remove(module)
			}
			if c.enginePresent {
				writeBin("proxy", "#!/bin/sh\necho \"proxy $*\" >&2\n")
			} else {
				os.Remove(engineBin)
			}
			env := []string{"PATH=" + bin, "WG_QUICK_USERSPACE_IMPLEMENTATION=proxy"}
			if c.force {
				env = append(env, "WG_QUICK_FORCE_USERSPACE=1")
			}
			if c.ipFails {
				env = append(env, "FAKE_IP_FAIL=1")
			}
			cmd := exec.Command("bash", fixture)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			got := string(out)

			for _, ch := range []struct {
				want bool
				text string
			}{
				{c.wantKernel, kernel},
				{c.wantEngine, engine},
				{c.wantReturned, returned},
			} {
				if ch.want && !strings.Contains(got, ch.text) {
					t.Errorf("missing %q in output:\n%s", ch.text, got)
				}
				if !ch.want && strings.Contains(got, ch.text) {
					t.Errorf("unexpected %q in output:\n%s", ch.text, got)
				}
			}
			// Reaching the engine means the branch's notice ran: it must name
			// the module by what is wrong with it, not as missing - in issue
			// #11's case the module is present.
			if c.wantEngine {
				if !strings.Contains(got, "Kernel module unavailable or incompatible") {
					t.Errorf("fallback notice missing from output:\n%s", got)
				}
				if strings.Contains(got, "Missing WireGuard") {
					t.Errorf("old fallback notice in output:\n%s", got)
				}
			}
			switch c.wantExit {
			case "zero":
				if err != nil {
					t.Errorf("exit = %v, want success:\n%s", err, got)
				}
			case "nonzero":
				if err == nil {
					t.Errorf("exit = success, want failure:\n%s", got)
				}
			}
		})
	}
}
