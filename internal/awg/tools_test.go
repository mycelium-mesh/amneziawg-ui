package awg_test

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/awg/awgtest"
)

// applyPrefix is where every apply command starts, and applyCmd is the whole
// one-command apply SyncConf runs for iface: strip's output goes to a temp
// file whose status is checked before syncconf ever reads it. Both mirror
// the production composition exactly - these tests pin it.
const applyPrefix = "bash -c 'strip=$(mktemp)"

func applyCmd(iface string) string {
	return applyPrefix + " || exit 1\n" +
		"awg-quick strip " + iface + " > \"$strip\" || { s=$?; rm -f \"$strip\"; exit $s; }\n" +
		"awg syncconf " + iface + " \"$strip\"\n" +
		"s=$?\n" +
		"rm -f \"$strip\"\n" +
		"exit $s'"
}

const showOutput = `interface: wg-abc123
  public key: SRVPUB
  listening port: 54844

peer: PEER1
  endpoint: 203.0.113.5:51820
  allowed ips: 10.0.1.2/32
  latest handshake: 1 minute, 3 seconds ago
  transfer: 1.20 MiB received, 3.40 MiB sent

peer: PEER2
  allowed ips: 10.0.1.3/32
`

func TestParseShow(t *testing.T) {
	peers := awg.ParseShow(showOutput)
	if len(peers) != 2 {
		t.Fatalf("peers = %v", peers)
	}
	p1 := peers["PEER1"]
	if p1.Endpoint != "203.0.113.5:51820" || p1.LastHandshake != "1 minute, 3 seconds ago" {
		t.Errorf("PEER1 = %+v", p1)
	}
	p2 := peers["PEER2"]
	if p2.LastHandshake != "Never" || p2.Endpoint != "" {
		t.Errorf("PEER2 = %+v", p2)
	}
}

// The bytes come from the transfer listing, exact and for every peer, not
// from the rounded "1.20 MiB" of the plain one.
func TestShowPeersTakesBytesFromTheTransferListing(t *testing.T) {
	run := awgtest.New().
		Stub("/usr/bin/awg show wg-up", showOutput).
		Stub("/usr/bin/awg show wg-up transfer", "PEER1\t1258291\t3565158\nPEER2\t0\t0\nbroken line\n")
	tools := awg.New(run)

	peers := tools.ShowPeers("wg-up")
	if p := peers["PEER1"]; p.RXBytes != 1258291 || p.TXBytes != 3565158 || p.Endpoint != "203.0.113.5:51820" {
		t.Errorf("PEER1 = %+v", p)
	}
	if p := peers["PEER2"]; p.RXBytes != 0 || p.TXBytes != 0 || p.LastHandshake != "Never" {
		t.Errorf("PEER2 = %+v", p)
	}
	if len(peers) != 2 {
		t.Errorf("peers = %v, want the two of the listing", peers)
	}
	if !peers["PEER1"].HandshakeAt.IsZero() {
		t.Errorf("PEER1 handshake = %v, want zero without the listing", peers["PEER1"].HandshakeAt)
	}
	if tools.ShowPeers("wg-down") != nil {
		t.Error("a down interface reported peers")
	}
}

// A zero timestamp is a peer that never shook hands, not the Unix epoch.
func TestParseLatestHandshakes(t *testing.T) {
	got := awg.ParseLatestHandshakes("PEER1\t1700000000\nPEER2\t0\nbroken\nPEER3\tsoon\n")
	if len(got) != 1 || got["PEER1"].Unix() != 1700000000 {
		t.Errorf("handshakes = %v, want only PEER1", got)
	}
}

func TestInterfaceUpAsksTheKernel(t *testing.T) {
	run := awgtest.New().Stub("ip link show wg-up", "5: wg-up: <POINTOPOINT,NOARP,UP,LOWER_UP> state UNKNOWN")
	tools := awg.New(run)

	if !tools.InterfaceUp("wg-up") {
		t.Error("an interface the kernel lists was reported down")
	}
	if tools.InterfaceUp("wg-down") || tools.InterfaceUp("") {
		t.Error("a missing interface was reported up")
	}
}

// Without the binaries the fallback keys must still be well-formed, so the
// config can be written and read back on a development machine.
func TestKeyGenerationFallsBackToRandomKeys(t *testing.T) {
	tools := awg.New(awgtest.New())

	pair := tools.GenerateKeyPair()
	for _, k := range []string{pair.Private, pair.Public, tools.GeneratePresharedKey()} {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			t.Errorf("fallback key %q: %d bytes, %v", k, len(raw), err)
		}
	}
	if pair.Private == pair.Public {
		t.Error("the fallback handed out one key twice")
	}
}

func TestKeyGenerationUsesAwgWhenPresent(t *testing.T) {
	run := awgtest.New().Stub("awg genkey", "PRIV").Stub("echo 'PRIV' | awg pubkey", "PUB").Stub("awg genpsk", "PSK")
	tools := awg.New(run)

	if pair := tools.GenerateKeyPair(); pair.Private != "PRIV" || pair.Public != "PUB" {
		t.Errorf("GenerateKeyPair = %+v", pair)
	}
	if psk := tools.GeneratePresharedKey(); psk != "PSK" {
		t.Errorf("GeneratePresharedKey = %q", psk)
	}
}

func TestIPTablesScriptsAreSkippedWhenNotDeployed(t *testing.T) {
	run := awgtest.New()
	tools := awg.New(run)
	tools.ScriptsDir = t.TempDir()

	tools.SetupIPTables("wg-x", "10.0.1.0/24")
	tools.CleanupIPTables("wg-x", "10.0.1.0/24")
	if len(run.Commands) != 0 {
		t.Errorf("scripts that do not exist were run: %v", run.Commands)
	}
}

// The host's amneziawg kernel module is present but older than the config:
// awg-quick creates the kernel interface in add_if, set_config rejects the
// 3.x fields, and upstream has no fallback for that case. QuickUp must retry
// the failed start with WG_QUICK_FORCE_USERSPACE=1 - the image's patched
// awg-quick answers it by starting straight on the userspace engine - and
// that retry's success is the server's start.
func TestQuickUpRetriesOnTheUserspaceEngineWhenTheKernelRejectsTheConfig(t *testing.T) {
	kernelRejected := "[#] ip link add wg-1 type amneziawg\n" +
		"[#] awg setconf wg-1 /dev/fd/63\n" +
		"Unable to modify interface: invalid argument\n" +
		"[#] ip link delete dev wg-1"
	forced := "WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1"
	run := awgtest.New().
		Fail("/usr/bin/awg-quick up", &awg.ExitError{Err: errors.New("exit status 1"), Stderr: kernelRejected}).
		Stub(forced, "").
		Stub("ip link show wg-1", "wg-1: state UNKNOWN")

	err := awg.New(run).QuickUp("wg-1")
	if err != nil {
		t.Fatalf("QuickUp = %q, want nil from the userspace retry", err)
	}
	want := []string{"/usr/bin/awg-quick up wg-1", forced, "ip link show wg-1"}
	if len(run.Commands) != len(want) {
		t.Fatalf("commands = %v, want the kernel attempt, the retry and the interface check %v", run.Commands, want)
	}
	for i := range want {
		if run.Commands[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, run.Commands[i], want[i])
		}
	}
}

// A first attempt the kernel accepts is the whole start: no retry, no second
// awg-quick on the userspace engine - only the check that the success left
// an interface behind.
func TestQuickUpStopsAtTheFirstSuccessfulStart(t *testing.T) {
	run := awgtest.New().
		Stub("/usr/bin/awg-quick up wg-1", "").
		Stub("ip link show wg-1", "wg-1: state UNKNOWN")

	if err := awg.New(run).QuickUp("wg-1"); err != nil {
		t.Fatalf("QuickUp = %v, want nil", err)
	}
	want := []string{"/usr/bin/awg-quick up wg-1", "ip link show wg-1"}
	if len(run.Commands) != len(want) {
		t.Fatalf("commands = %v, want %v", run.Commands, want)
	}
	for i := range want {
		if run.Commands[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, run.Commands[i], want[i])
		}
	}
}

// When both attempts fail, the first failure is what reaches the user: it is
// the reason the deployment would have seen before the retry existed, and the
// retry cannot add a better one.
func TestQuickUpReportsTheFirstFailureWhenTheUserspaceRetryFails(t *testing.T) {
	kernelRejected := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "[#] awg setconf wg-1 /dev/fd/63\nUnable to modify interface: invalid argument"}
	run := awgtest.New().
		Fail("/usr/bin/awg-quick up", kernelRejected).
		Fail("WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up", errors.New("no such container-side engine"))

	err := awg.New(run).QuickUp("wg-1")
	want := "awg-quick up wg-1: exit status 1: Unable to modify interface: invalid argument"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

// The running interface rejects the new settings - the old-module class
// from the follow-up report: a kernel-mode server whose .conf gains 3.x
// fields. The restart applies the current .conf, which carries them: down,
// up (the kernel refuses), then QuickUp's forced userspace retry - and that
// retry's success is the result.
func TestSyncConfRejectedRestartsOnTheUserspaceEngine(t *testing.T) {
	rejected := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: invalid argument"}
	kernelRefused := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: invalid argument"}
	forced := "WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1"
	run := awgtest.New().
		Fail(applyPrefix, rejected).
		Stub("/usr/bin/awg-quick down wg-1", "").
		Fail("/usr/bin/awg-quick up wg-1", kernelRefused).
		Stub(forced, "").
		Stub("ip link show wg-1", "wg-1: state UNKNOWN")

	err := awg.New(run).SyncConf("wg-1")
	if err != nil {
		t.Fatalf("SyncConf = %q, want nil after the restart", err)
	}
	want := []string{
		applyCmd("wg-1"),
		"/usr/bin/awg-quick down wg-1",
		"/usr/bin/awg-quick up wg-1",
		forced,
		"ip link show wg-1",
	}
	if len(run.Commands) != len(want) {
		t.Fatalf("commands = %v, want %v", run.Commands, want)
	}
	for i := range want {
		if run.Commands[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, run.Commands[i], want[i])
		}
	}
}

// The canonical old-module semantic - the running interface reports the
// 3.x settings or features unsupported - restarts like any other refusal;
// each apply tail the classifier counts gets its own run, mirroring the
// rejection test's chain.
func TestSyncConfRestartsWhenTheInterfaceReportsUnsupportedFeatures(t *testing.T) {
	for _, tail := range []string{"operation not supported", "not supported", "protocol not supported"} {
		t.Run(tail, func(t *testing.T) {
			refused := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: " + tail}
			forced := "WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1"
			run := awgtest.New().
				Fail(applyPrefix, refused).
				Stub("/usr/bin/awg-quick down wg-1", "").
				Fail("/usr/bin/awg-quick up wg-1", refused).
				Stub(forced, "").
				Stub("ip link show wg-1", "wg-1: state UNKNOWN")

			err := awg.New(run).SyncConf("wg-1")
			if err != nil {
				t.Fatalf("SyncConf = %q, want nil after the restart", err)
			}
			want := []string{
				applyCmd("wg-1"),
				"/usr/bin/awg-quick down wg-1",
				"/usr/bin/awg-quick up wg-1",
				forced,
				"ip link show wg-1",
			}
			if len(run.Commands) != len(want) {
				t.Fatalf("commands = %v, want %v", run.Commands, want)
			}
			for i := range want {
				if run.Commands[i] != want[i] {
					t.Errorf("command %d = %q, want %q", i, run.Commands[i], want[i])
				}
			}
		})
	}
}

// Each parse-level signature the classifier counts earns the same restart:
// this is the branch the four "Unable to modify interface" tails do not
// cover. A config the parser itself rejects is exactly a configuration being
// refused, so the chain mirrors the rejection test - apply fails, down, up,
// the forced attempt carrying the restart to success - and each signature
// gets its own run.
func TestSyncConfRestartsWhenTheConfigFailsToParse(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{"line unrecognized", "Line unrecognized: `Jc = 5'"},
		{"configuration parsing error", "Configuration parsing error"},
		{"invalid configuration", "Invalid configuration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refused := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: c.stderr}
			forced := "WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1"
			run := awgtest.New().
				Fail(applyPrefix, refused).
				Stub("/usr/bin/awg-quick down wg-1", "").
				Fail("/usr/bin/awg-quick up wg-1", refused).
				Stub(forced, "").
				Stub("ip link show wg-1", "wg-1: state UNKNOWN")

			err := awg.New(run).SyncConf("wg-1")
			if err != nil {
				t.Fatalf("SyncConf = %q, want nil after the restart", err)
			}
			want := []string{
				applyCmd("wg-1"),
				"/usr/bin/awg-quick down wg-1",
				"/usr/bin/awg-quick up wg-1",
				forced,
				"ip link show wg-1",
			}
			if len(run.Commands) != len(want) {
				t.Fatalf("commands = %v, want %v", run.Commands, want)
			}
			for i := range want {
				if run.Commands[i] != want[i] {
					t.Errorf("command %d = %q, want %q", i, run.Commands[i], want[i])
				}
			}
		})
	}
}

// The value parsers' exit(1) shapes are config refusals too: pinned
// config.c:408 (parse_uint16: Jc/Jmin/Jmax/S1-S4) and config.c:440
// (parse_bool: RandomTrailers/DisableCookies) die mid-read on a malformed
// value - `Jc = 5.5`, `RandomTrailers = 2x` - and setconf's
// "Configuration parsing error" (setconf.c:126) never follows, because that
// prints only when config_read_line returns false. The restart chain must
// still engage; with the value still broken it fails and reports both, and
// the server stops rather than running on stale settings.
func TestSyncConfRestartsWhenAValueParserExitsMidRead(t *testing.T) {
	malformed := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to parse RandomTrailers: `2x'"}
	run := awgtest.New().
		Fail(applyPrefix, malformed).
		Stub("/usr/bin/awg-quick down wg-1", "").
		Fail("/usr/bin/awg-quick up wg-1", malformed)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exit status 1: Unable to parse RandomTrailers: `2x'" +
		"; restart failed: awg-quick up wg-1: exit status 1:" +
		" Unable to parse RandomTrailers: `2x'"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 4 {
		t.Errorf("commands = %v, want the apply, the down and both up attempts of the restart", run.Commands)
	}
}

// A signature echoed back inside config text is not a signature. Strip's own
// read_bool (pinned linux.bash:80, die() at :35-38) echoes the raw value:
// `SaveConfig = Invalid configuration` dies with
// "awg-quick: `Invalid configuration' is neither true nor false" - whose
// echo contains a substring the old whole-stderr match classified as a
// refusal, restarting a live server on an unclassifiable shape. Anchored to
// the tool messages' line forms, the echo matches nothing: the error comes
// back and not one command beyond the apply runs.
func TestSyncConfDoesNotRestartOnAnEchoedSignature(t *testing.T) {
	stripDied := &awg.ExitError{
		Err:    errors.New("exit status 1"),
		Stderr: "awg-quick: `Invalid configuration' is neither true nor false",
	}
	run := awgtest.New().Fail(applyPrefix, stripDied)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exit status 1:" +
		" awg-quick: `Invalid configuration' is neither true nor false"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 1 {
		t.Errorf("commands = %v, want the apply alone, interface untouched", run.Commands)
	}
}

// An infrastructural failure - a busy device, not a refusal of the
// settings - leaves the interface exactly as it was: the sync error comes
// back naming the failure, and not one restart command runs.
func TestSyncConfInfrastructuralFailureLeavesTheInterfaceRunning(t *testing.T) {
	busy := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Device or resource busy"}
	run := awgtest.New().Fail(applyPrefix, busy)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exit status 1: Device or resource busy"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 1 {
		t.Errorf("commands = %v, want the syncconf alone, interface untouched", run.Commands)
	}
}

// An error shape no signature positively matches cannot be called a
// refusal: the conservative default is no restart - pre-change behavior,
// interface still running, sessions kept.
func TestSyncConfUnknownFailureDoesNotRestart(t *testing.T) {
	spawn := errors.New("exec: bash: executable file not found in $PATH")
	run := awgtest.New().Fail(applyPrefix, spawn)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exec: bash: executable file not found in $PATH"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 1 {
		t.Errorf("commands = %v, want the syncconf alone, interface untouched", run.Commands)
	}
}

// Issue #32: a zero exit must not be taken as proof of an interface. The
// genuinely reachable phantom is the userspace engine: it daemonizes, so
// the parent can report success while the daemon dies again and leaves
// nothing behind (the daemon-death race) - and the check is also
// defense-in-depth against any other exit-0-with-nothing. QuickUp verifies
// both attempts this way, so a phantom success is a failure.
func TestQuickUpDoesNotTrustASuccessThatCreatedNoInterface(t *testing.T) {
	run := awgtest.New().
		Stub("/usr/bin/awg-quick up wg-1", "").
		Stub("WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1", "")

	err := awg.New(run).QuickUp("wg-1")
	want := "awg-quick up wg-1: reported success but the interface does not exist"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 4 {
		t.Errorf("commands = %v, want both attempts each followed by the interface check", run.Commands)
	}
}

// The check applies to the forced retry as well: when that attempt exits 0
// with no interface behind it, the first failure - the reason the deployment
// would have seen without the retry - is what reaches the user, as on any
// double failure.
func TestQuickUpReportsTheFirstFailureWhenTheRetryCreatesNothing(t *testing.T) {
	kernelFailed := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "[#] ip link add wg-1 type amneziawg\nDevice or resource busy"}
	run := awgtest.New().
		Fail("/usr/bin/awg-quick up", kernelFailed).
		Stub("WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up wg-1", "")

	err := awg.New(run).QuickUp("wg-1")
	want := "awg-quick up wg-1: exit status 1: Device or resource busy"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 3 {
		t.Errorf("commands = %v, want the failed attempt, the retry and its interface check", run.Commands)
	}
}

// The interface takes the settings: exactly the syncconf, no restart - the
// sessions it exists to preserve stay up.
func TestSyncConfAcceptedIsExactlyOneCommand(t *testing.T) {
	run := awgtest.New().Stub(applyPrefix, "")

	if err := awg.New(run).SyncConf("wg-1"); err != nil {
		t.Fatalf("SyncConf = %v, want nil", err)
	}
	if len(run.Commands) != 1 {
		t.Errorf("commands = %v, want the syncconf alone", run.Commands)
	}
}

// A .conf so broken that awg-quick strip dies on it (SaveConfig = maybe)
// must fail the apply before awg syncconf ever sees input. The process
// substitution of the old single command hid strip's exit status, so syncconf
// applied an empty config - and with WGDEVICE_REPLACE_PEERS it silently
// removed every peer while reporting success. The real shell runs the real
// composition against fake binaries, because text matching alone cannot show
// which side of the pipeline's exit status wins.
func TestSyncConfStripFailureFailsTheApplyBeforeSyncConf(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is needed to run the apply composition")
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("APPLY_MARKER", marker)

	// awg-quick strip dies on the broken .conf the way the pinned script
	// does: message on stderr, no output, nonzero exit.
	writeBin(t, filepath.Join(bin, "awg-quick"),
		"#!/bin/sh\n"+
			"if [ \"$1\" = \"strip\" ]; then\n"+
			"    echo \"awg-quick: line 3: SaveConfig = maybe\" >&2\n"+
			"    exit 1\n"+
			"fi\n"+
			"echo \"awg-quick $*\" >> \"$APPLY_MARKER\"\n")
	// awg records every invocation, so syncconf running - or any restart -
	// is visible.
	writeBin(t, filepath.Join(bin, "awg"),
		"#!/bin/sh\necho \"awg $*\" >> \"$APPLY_MARKER\"\n")

	err := awg.New(awg.Shell{}).SyncConf("wg-1")
	if err == nil {
		t.Fatal("a strip that died was reported as a successful apply")
	}
	if !strings.Contains(err.Error(), "SaveConfig = maybe") {
		t.Errorf("error does not carry strip's reason: %v", err)
	}
	if strings.Contains(err.Error(), "restart") {
		t.Errorf("an unidentifiable strip failure must not restart the interface: %v", err)
	}
	if ran, readErr := os.ReadFile(marker); readErr == nil {
		t.Fatalf("awg syncconf ran on strip's failure: %s", ran)
	}
}

// A healthy .conf flows through the real composition to awg syncconf: strip's
// output lands in the temp file, syncconf reads that file, and the file is
// gone afterwards. The stubbed tests cannot show this - a quoting mistake in
// the shell composition would break the success path silently.
func TestSyncConfAppliedThroughTheRealShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is needed to run the apply composition")
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("APPLY_MARKER", marker)

	writeBin(t, filepath.Join(bin, "awg-quick"),
		"#!/bin/sh\n[ \"$1\" = \"strip\" ] || exit 1\necho \"PrivateKey = OKKEY\"\n")
	writeBin(t, filepath.Join(bin, "awg"),
		"#!/bin/sh\nprintf 'awg %s %s <%s>\\n' \"$1\" \"$2\" \"$(cat \"$3\")\" >> \"$APPLY_MARKER\"\necho \"$3\" >> \"$APPLY_MARKER.path\"\n")

	if err := awg.New(awg.Shell{}).SyncConf("wg-1"); err != nil {
		t.Fatalf("SyncConf = %v, want nil", err)
	}
	ran, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("awg syncconf never ran: %v", err)
	}
	if want := "awg syncconf wg-1 <PrivateKey = OKKEY>"; !strings.Contains(string(ran), want) {
		t.Errorf("syncconf saw %q, want it to read strip's output via %q", ran, want)
	}
	path, err := os.ReadFile(marker + ".path")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Errorf("the temp file was left behind (err %v)", err)
	}
}

func writeBin(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The apply failed and the restart failed as it did: the error carries both
// - the settings did not land, and the server is down. The restart runs
// QuickUp, so its own error - kernel attempt first - is what is named.
func TestSyncConfReportsTheApplyAndTheFailedRestart(t *testing.T) {
	rejected := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: invalid argument"}
	kernelRefused := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "[#] awg setconf wg-1 /dev/fd/63\nUnable to modify interface: invalid argument\n[#] ip link delete dev wg-1"}
	userspaceAlsoRefused := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "[#] awg setconf wg-1 /dev/fd/63\nLine unrecognized: `BogusKey=42'\nConfiguration parsing error\n[#] ip link delete dev wg-1"}
	run := awgtest.New().
		Fail(applyPrefix, rejected).
		Stub("/usr/bin/awg-quick down wg-1", "").
		Fail("/usr/bin/awg-quick up wg-1", kernelRefused).
		Fail("WG_QUICK_FORCE_USERSPACE=1 /usr/bin/awg-quick up", userspaceAlsoRefused)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exit status 1: Unable to modify interface: invalid argument" +
		"; restart failed: awg-quick up wg-1: exit status 1:" +
		" Unable to modify interface: invalid argument"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

// The interface cannot even be brought down: no up follows, and the error
// says both what failed to apply and where the restart stopped.
func TestSyncConfReportsWhenTheRestartCannotBringTheInterfaceDown(t *testing.T) {
	rejected := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Unable to modify interface: invalid argument"}
	downFailed := &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "Device or resource busy"}
	run := awgtest.New().
		Fail(applyPrefix, rejected).
		Fail("/usr/bin/awg-quick down wg-1", downFailed)

	err := awg.New(run).SyncConf("wg-1")
	want := "awg syncconf wg-1: exit status 1: Unable to modify interface: invalid argument" +
		"; restart failed bringing the interface down: awg-quick down wg-1: exit status 1: Device or resource busy"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
	if len(run.Commands) != 2 {
		t.Errorf("commands = %v, want the syncconf and the down, no up", run.Commands)
	}
}

// A start that fell back to userspace and then had its .conf rejected: what
// reaches the user is the rejection, not awg-quick's trace and banner.
func TestQuickUpReportsTheFailingStep(t *testing.T) {
	stderr := "[#] ip link add wg-1 type amneziawg\n" +
		"Error: Unknown device type.\n" +
		"[!] Missing WireGuard (Amnezia VPN) kernel module. Falling back to slow userspace implementation.\n" +
		"[#] proxy wg-1\n" +
		"┌────┐\n│  Running amneziawg-go is not required  │\n| https://github.com/amnezia-vpn/amneziawg-linux-kernel-module │\n└────┘\n" +
		"[#] awg setconf wg-1 /dev/fd/63\n" +
		"Line unrecognized: `BogusKey=42'\n" +
		"Configuration parsing error\n" +
		"[#] ip link delete dev wg-1"
	run := awgtest.New().Fail("/usr/bin/awg-quick up", &awg.ExitError{Err: errors.New("exit status 1"), Stderr: stderr})

	err := awg.New(run).QuickUp("wg-1")
	want := "awg-quick up wg-1: exit status 1: Line unrecognized: `BogusKey=42'\nConfiguration parsing error"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

// Without the "[#]" trace there is nothing to cut, and stderr goes out whole.
func TestQuickUpKeepsUntracedStderr(t *testing.T) {
	run := awgtest.New().Fail("/usr/bin/awg-quick up", &awg.ExitError{Err: errors.New("exit status 1"), Stderr: "awg-quick: `wg-1' already exists"})

	err := awg.New(run).QuickUp("wg-1")
	if want := "awg-quick up wg-1: exit status 1: awg-quick: `wg-1' already exists"; err == nil || err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}
