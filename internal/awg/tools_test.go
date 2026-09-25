package awg_test

import (
	"encoding/base64"
	"errors"
	"testing"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/internal/awg/awgtest"
)

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
