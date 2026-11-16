package awg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tools wraps the host commands behind names that say what they are for.
// Nothing here knows about servers or clients; it takes interface names and
// subnets and reports what the host said.
type Tools struct {
	run Runner

	// ScriptsDir holds setup_iptables.sh and cleanup_iptables.sh. A
	// deployment without them - a development tree - simply skips the
	// firewall step.
	ScriptsDir string

	// BinDir is where awg and awg-quick are installed.
	BinDir string

	// ifaceLocks serializes the lifecycle operations - up, down, and
	// syncconf's apply-and-restart - per interface name, so two operations
	// on one interface never interleave while different interfaces stay
	// independent. The registry mutex guards only the map and is released
	// before any interface lock is awaited, so nothing nests.
	ifaceLocksMu sync.Mutex
	ifaceLocks   map[string]*sync.Mutex
}

// New returns Tools issuing commands through run, with the container's
// default locations.
func New(run Runner) *Tools {
	return &Tools{run: run, ScriptsDir: "/app/scripts", BinDir: "/usr/bin"}
}

// Available reports whether both amneziawg-tools binaries are installed.
func (t *Tools) Available() bool {
	_, awgErr := os.Stat(filepath.Join(t.BinDir, "awg"))
	_, quickErr := os.Stat(filepath.Join(t.BinDir, "awg-quick"))
	return awgErr == nil && quickErr == nil
}

// lockInterface takes the interface's operation lock and returns its
// unlock, so a whole up/down/sync sequence runs without another operation
// on the same interface between its commands.
func (t *Tools) lockInterface(iface string) func() {
	t.ifaceLocksMu.Lock()
	if t.ifaceLocks == nil {
		t.ifaceLocks = map[string]*sync.Mutex{}
	}
	mu := t.ifaceLocks[iface]
	if mu == nil {
		mu = &sync.Mutex{}
		t.ifaceLocks[iface] = mu
	}
	t.ifaceLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// QuickUp brings an interface up from its .conf. A failed start is retried
// with WG_QUICK_FORCE_USERSPACE=1, which the image's awg-quick - patched by
// scripts/patch-awg-quick.sh, which holds the why - answers by starting the
// interface on the userspace engine. An unpatched script ignores the
// variable and runs the attempt twice. If both attempts fail, the first
// failure is reported: the reason the deployment would have seen without
// the retry, and the retry cannot add a better one.
func (t *Tools) QuickUp(iface string) error {
	unlock := t.lockInterface(iface)
	defer unlock()
	return t.quickUp(iface)
}

func (t *Tools) quickUp(iface string) error {
	quick := t.quickCmd("up", iface)
	_, err := t.run.Run(quick)
	if err == nil {
		return nil
	}
	first := err
	if _, retryErr := t.run.Run("WG_QUICK_FORCE_USERSPACE=1 " + quick); retryErr == nil {
		return nil
	}
	return fmt.Errorf("awg-quick up %s: %w", iface, quickFailure(first))
}

// quickCmd is one awg-quick verb invocation on iface.
func (t *Tools) quickCmd(verb, iface string) string {
	return fmt.Sprintf("%s %s %s", filepath.Join(t.BinDir, "awg-quick"), verb, iface)
}

// QuickDown tears an interface down.
func (t *Tools) QuickDown(iface string) error {
	unlock := t.lockInterface(iface)
	defer unlock()
	return t.quickDown(iface)
}

func (t *Tools) quickDown(iface string) error {
	if _, err := t.run.Run(t.quickCmd("down", iface)); err != nil {
		return fmt.Errorf("awg-quick down %s: %w", iface, quickFailure(err))
	}
	return nil
}

// quickFailure cuts awg-quick's stderr down to the output of the command that
// failed. awg-quick echoes each step as "[#] command", and a start that falls
// back to userspace also carries the kernel attempt's "Unknown device type",
// a "[!]" notice and a box-drawn banner - none of it the reason, all of it
// ahead of the one line that is. The last step with output is that line: the
// steps after a failure are awg-quick's own cleanup, and they say nothing.
func quickFailure(err error) error {
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	var last, current []string
	inBanner := false
	for _, line := range strings.Split(exitErr.Stderr, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "┌"):
			inBanner = true
		case strings.HasPrefix(line, "└"):
			inBanner = false
		case inBanner, line == "", strings.HasPrefix(line, "[!]"):
		case strings.HasPrefix(line, "[#]"):
			current = nil
		default:
			current = append(current, line)
			last = current
		}
	}
	if len(last) == 0 {
		return err
	}
	return &ExitError{Err: exitErr.Err, Stderr: strings.Join(last, "\n")}
}

// SyncConf pushes the interface's .conf onto the running interface, so
// existing peers keep their sessions. Only a failure configRefusal
// positively identifies as a configuration being refused restarts the
// interface: down and back up with the current .conf, on the kernel where
// it takes the config, on the userspace engine where it refuses - a
// genuinely broken config leaves the server down rather than on the stale
// settings. Any other failure returns as the sync error with the
// interface left running.
func (t *Tools) SyncConf(iface string) error {
	unlock := t.lockInterface(iface)
	defer unlock()
	cmd := fmt.Sprintf("bash -c 'awg syncconf %s <(awg-quick strip %s)'", iface, iface)
	_, syncErr := t.run.Run(cmd)
	if syncErr == nil {
		return nil
	}
	if !configRefusal(syncErr) {
		return fmt.Errorf("awg syncconf %s: %w", iface, quickFailure(syncErr))
	}
	if err := t.quickDown(iface); err != nil {
		return fmt.Errorf("awg syncconf %s: %w; restart failed bringing the interface down: %w", iface, syncErr, err)
	}
	if err := t.quickUp(iface); err != nil {
		return fmt.Errorf("awg syncconf %s: %w; restart failed: %w", iface, syncErr, err)
	}
	return nil
}

// configRefusal positively identifies an awg syncconf failure as the
// configuration being refused - the only class that earns the disruptive
// restart. Parse-level signatures are verbatim from the pinned
// amneziawg-tools (AWG_TOOLS_VERSION): "Line unrecognized" from
// config.c:617, "Configuration parsing error" from setconf.c:126 and
// "Invalid configuration" from setconf.c:132. The apply refusal is
// setconf.c:144's perror "Unable to modify interface: <strerror>" with
// ipc.c:70 turning the device's errno into that strerror; its tails are
// musl's strerror texts as the pinned image's libc prints them (English,
// __strerror.h lines 49/85/83), case-folded, plus glibc's "Operation not
// supported" for EOPNOTSUPP. A localized or unrecognized strerror, the get
// failure at setconf.c:44, a non-ExitError, or any other stderr shape is
// not identified: unknown means no restart.
func configRefusal(err error) bool {
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	stderr := exitErr.Stderr
	if strings.Contains(stderr, "Line unrecognized: `") ||
		strings.Contains(stderr, "Configuration parsing error") ||
		strings.Contains(stderr, "Invalid configuration") {
		return true
	}
	low := strings.ToLower(stderr)
	return strings.Contains(low, "unable to modify interface: invalid argument") ||
		strings.Contains(low, "unable to modify interface: operation not supported") ||
		strings.Contains(low, "unable to modify interface: not supported") ||
		strings.Contains(low, "unable to modify interface: protocol not supported")
}

// InterfaceUp reports whether the kernel has the interface. It shells out,
// so callers must not hold a lock other requests wait on while asking.
func (t *Tools) InterfaceUp(iface string) bool {
	if iface == "" {
		return false
	}
	result, err := t.run.Run(fmt.Sprintf("ip link show %s", iface))
	if err != nil {
		return false
	}
	return strings.Contains(result, "state UNKNOWN") || strings.Contains(result, iface)
}

// SetupIPTables adds the forwarding and NAT rules for an interface, if the
// script for it is deployed. It takes the interface lock, so the rules can
// never be added inside - or stripped from under - a concurrent
// up/down/syncconf sequence on the same interface.
func (t *Tools) SetupIPTables(iface, subnet string) {
	unlock := t.lockInterface(iface)
	defer unlock()
	t.runScript("setup_iptables.sh", iface, subnet)
}

// CleanupIPTables removes what SetupIPTables added, under the same
// interface lock as every other operation.
func (t *Tools) CleanupIPTables(iface, subnet string) {
	unlock := t.lockInterface(iface)
	defer unlock()
	t.runScript("cleanup_iptables.sh", iface, subnet)
}

func (t *Tools) runScript(name, iface, subnet string) {
	script := filepath.Join(t.ScriptsDir, name)
	if _, err := os.Stat(script); err != nil {
		return
	}
	t.run.Run(fmt.Sprintf("%s %s %s", script, iface, subnet)) //nolint:errcheck
}

// CheckIPTables looks for the rules SetupIPTables should have left, keyed by
// the command that looked. The values are "Found" or "Not found".
func (t *Tools) CheckIPTables(iface, subnet string) map[string]string {
	checks := []string{
		fmt.Sprintf("iptables -L INPUT -n | grep %s", iface),
		fmt.Sprintf("iptables -L FORWARD -n | grep %s", iface),
		fmt.Sprintf("iptables -t nat -L POSTROUTING -n | grep %s", subnet),
	}
	results := map[string]string{}
	for _, cmd := range checks {
		out, err := t.run.Run(cmd)
		if err == nil && out != "" {
			results[cmd] = "Found"
		} else {
			results[cmd] = "Not found"
		}
	}
	return results
}

// RouteSourceIP is the address the host would use to reach the internet,
// from its routing table. It is the fallback when no external service could
// say what the public address is.
func (t *Tools) RouteSourceIP() (string, error) {
	return t.run.Run("ip route get 1 | awk '{print $7}' | head -1")
}

// PeerStats is what `awg show` reports about one peer: the exact byte counts
// of the `transfer` listing, the endpoint and handshake age as the plain
// listing words them, and the moment of that handshake from the
// `latest-handshakes` listing (zero when the peer never shook hands).
type PeerStats struct {
	RXBytes, TXBytes        uint64
	LastHandshake, Endpoint string
	HandshakeAt             time.Time
}

// ShowPeers reads the peers of an interface, keyed by public key. The bytes
// come from `awg show <iface> transfer` rather than the rounded figures of
// the plain listing (or the interface counters of ifconfig, which count the
// decrypted payload and so fall short of what the peers moved on the wire)
// - the server card sums these same numbers, so it always agrees with its
// client rows. A down interface yields nil.
func (t *Tools) ShowPeers(iface string) map[string]PeerStats {
	awg := filepath.Join(t.BinDir, "awg")
	output, err := t.run.Run(fmt.Sprintf("%s show %s", awg, iface))
	if err != nil || output == "" {
		return nil
	}
	peers := ParseShow(output)
	transfer, _ := t.run.Run(fmt.Sprintf("%s show %s transfer", awg, iface))
	for key, tr := range ParseTransfer(transfer) {
		p := peers[key]
		p.RXBytes, p.TXBytes = tr.RXBytes, tr.TXBytes
		peers[key] = p
	}
	handshakes, _ := t.run.Run(fmt.Sprintf("%s show %s latest-handshakes", awg, iface))
	for key, at := range ParseLatestHandshakes(handshakes) {
		if p, ok := peers[key]; ok {
			p.HandshakeAt = at
			peers[key] = p
		}
	}
	return peers
}

// ParseShow reads the output of `awg show`: who the peers are, where they
// are, and when they last shook hands. It is separate from ShowPeers so the
// parser can be tested on captured output.
func ParseShow(output string) map[string]PeerStats {
	peers := map[string]PeerStats{}
	current := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "peer:"):
			current = strings.TrimSpace(strings.TrimPrefix(line, "peer:"))
			peers[current] = PeerStats{LastHandshake: "Never"}
		case current == "":
			continue
		case strings.HasPrefix(line, "endpoint:"):
			p := peers[current]
			p.Endpoint = strings.TrimSpace(strings.TrimPrefix(line, "endpoint:"))
			peers[current] = p
		case strings.HasPrefix(line, "latest handshake:"):
			p := peers[current]
			p.LastHandshake = strings.TrimSpace(strings.TrimPrefix(line, "latest handshake:"))
			peers[current] = p
		}
	}
	return peers
}

// Transfer is one line of `awg show <iface> transfer`: what the interface
// received from a peer and sent to it, in bytes.
type Transfer struct {
	RXBytes, TXBytes uint64
}

// ParseTransfer reads `awg show <iface> transfer`, one "<key>\t<rx>\t<tx>"
// line per peer. Lines that do not fit are skipped.
func ParseTransfer(output string) map[string]Transfer {
	peers := map[string]Transfer{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		rx, errRX := strconv.ParseUint(fields[1], 10, 64)
		tx, errTX := strconv.ParseUint(fields[2], 10, 64)
		if errRX != nil || errTX != nil {
			continue
		}
		peers[fields[0]] = Transfer{RXBytes: rx, TXBytes: tx}
	}
	return peers
}

// ParseLatestHandshakes reads `awg show <iface> latest-handshakes`, one
// "<key>\t<unix seconds>" line per peer. A zero timestamp is a peer that
// never shook hands and is left out, as are lines that do not fit.
func ParseLatestHandshakes(output string) map[string]time.Time {
	peers := map[string]time.Time{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || sec <= 0 {
			continue
		}
		peers[fields[0]] = time.Unix(sec, 0)
	}
	return peers
}
