package awg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// QuickUp brings an interface up from its .conf.
func (t *Tools) QuickUp(iface string) error {
	if _, err := t.run.Run(fmt.Sprintf("%s up %s", filepath.Join(t.BinDir, "awg-quick"), iface)); err != nil {
		return fmt.Errorf("awg-quick up %s: %w", iface, quickFailure(err))
	}
	return nil
}

// QuickDown tears an interface down.
func (t *Tools) QuickDown(iface string) error {
	if _, err := t.run.Run(fmt.Sprintf("%s down %s", filepath.Join(t.BinDir, "awg-quick"), iface)); err != nil {
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

// SyncConf pushes the interface's .conf onto the running interface without
// taking it down, so existing peers keep their sessions.
func (t *Tools) SyncConf(iface string) error {
	cmd := fmt.Sprintf("bash -c 'awg syncconf %s <(awg-quick strip %s)'", iface, iface)
	_, err := t.run.Run(cmd)
	return err
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
// script for it is deployed.
func (t *Tools) SetupIPTables(iface, subnet string) {
	t.runScript("setup_iptables.sh", iface, subnet)
}

// CleanupIPTables removes what SetupIPTables added.
func (t *Tools) CleanupIPTables(iface, subnet string) {
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
// of the `transfer` listing, and the endpoint and handshake age as the plain
// listing words them.
type PeerStats struct {
	RXBytes, TXBytes        uint64
	LastHandshake, Endpoint string
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
