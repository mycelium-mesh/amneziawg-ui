package manager

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"amneziawg-web-ui/web-ui/api"
)

func TestAddClientDetachesObfuscationParamsFromTheServer(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if client.ObfuscationParams == nil {
		t.Fatal("client got no obfuscation params")
	}

	// Retuning the server must not rewrite what an already-issued client was
	// handed: both ends have to keep agreeing on the values in its config.
	m.cfg.Servers[0].ObfuscationParams.S1 = 999

	stored, ok := m.Client("s1", client.ID)
	if !ok {
		t.Fatal("client vanished from its server")
	}
	if stored.ObfuscationParams.S1 != 50 {
		t.Errorf("server edit leaked into the stored client: S1 = %d, want 50", stored.ObfuscationParams.S1)
	}
	if client.ObfuscationParams.S1 != 50 {
		t.Errorf("server edit leaked into the returned client: S1 = %d, want 50", client.ObfuscationParams.S1)
	}
}

func TestClientLivesOnlyInItsServer(t *testing.T) {
	m, _ := newTestManager(t)

	const signature = "<b 0xcafe><r 20>"

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice", ApplyISettings: true, ISettings: map[string]string{"i2": signature}})
	if err != nil {
		t.Fatal(err)
	}

	if got := m.ClientCount(); got != 1 {
		t.Errorf("clientCount = %d, want 1", got)
	}
	all := m.Clients("")
	perServer := m.Clients("s1")
	if len(all) != 1 || len(perServer) != 1 {
		t.Fatalf("client listed %d time(s) globally and %d time(s) per server, want 1 and 1", len(all), len(perServer))
	}
	if all[0].ID != client.ID || perServer[0].ID != client.ID {
		t.Errorf("listings disagree on the client: %q / %q, want %q", all[0].ID, perServer[0].ID, client.ID)
	}

	// A returned client is a copy: writing to it must not reach the config.
	all[0].Name = "mutated"
	all[0].ISettings["i2"] = "mutated"
	stored, _ := m.Client("s1", client.ID)
	if stored.Name != "alice" || stored.ISettings["i2"] != signature {
		t.Errorf("caller's edits leaked into the config: %+v", stored)
	}

	if err := m.DeleteClient("s1", client.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if got := m.ClientCount(); got != 0 {
		t.Errorf("clientCount after delete = %d, want 0", got)
	}
	if got := len(m.Clients("")); got != 0 {
		t.Errorf("deleted client still listed %d time(s)", got)
	}
}

func TestSuspendAndActivateKeepASingleClientRecord(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.SuspendClient("s1", client.ID); err != nil {
		t.Fatalf("SuspendClient: %v", err)
	}
	listed := m.Clients("")
	if len(listed) != 1 || listed[0].Status != "suspended" {
		t.Fatalf("after suspend: %+v", listed)
	}

	conf, _ := os.ReadFile(m.cfg.Servers[0].ConfigPath)
	if strings.Contains(string(conf), client.ClientPublicKey) {
		t.Errorf("suspended peer still in the server conf:\n%s", conf)
	}

	if _, err := m.ActivateClient("s1", client.ID); err != nil {
		t.Fatalf("ActivateClient: %v", err)
	}
	listed = m.Clients("")
	if len(listed) != 1 || listed[0].Status != "active" {
		t.Fatalf("after activate: %+v", listed)
	}
}

// Before peer blocks carried the client ID, deleting one of two clients that
// share a name took both peers out of the server .conf.
func TestDeletingOneOfTwoNamesakesKeepsTheOther(t *testing.T) {
	m, _ := newTestManager(t)

	first, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteClient("s1", first.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}

	conf, err := os.ReadFile(m.cfg.Servers[0].ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(conf), first.ClientPublicKey) {
		t.Errorf("deleted peer still in the conf:\n%s", conf)
	}
	if !strings.Contains(string(conf), second.ClientPublicKey) {
		t.Errorf("the namesake's peer was removed too:\n%s", conf)
	}
	if !strings.Contains(string(conf), "S1 = 50") {
		t.Errorf("[Interface] section damaged:\n%s", conf)
	}
	if got := m.ClientCount(); got != 1 {
		t.Errorf("clientCount = %d, want 1", got)
	}
}

// A name is free text; it must not be able to inject config directives into
// the peer comment it lands in.
func TestClientNameCannotInjectConfigLines(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "evil\n[Peer]\nPublicKey = INJECTED"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(client.Name, "\n\r") {
		t.Errorf("stored name still has line breaks: %q", client.Name)
	}

	conf, _ := os.ReadFile(m.cfg.Servers[0].ConfigPath)
	if strings.Contains(string(conf), "INJECTED\n") && !strings.Contains(string(conf), "# Client:") {
		t.Errorf("name broke out of its comment:\n%s", conf)
	}
	if got := strings.Count(string(conf), "[Peer]"); got != 1 {
		t.Errorf("[Peer] blocks = %d, want 1:\n%s", got, conf)
	}
}

// The archive of a server's client configs must hold exactly what the
// single-client downloads hand out - the same rendering, one entry per
// client - or a user who unpacks it gets something else than the app does.
func TestClientConfigsRenderEveryClientOfTheServer(t *testing.T) {
	m, _ := newTestManager(t)

	first, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := m.AddClient("s1", api.AddClientRequest{Name: "bob"})
	if err != nil {
		t.Fatal(err)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("the archive holds %d entries, want one per client (2)", len(files))
	}

	want := map[string]string{}
	for _, client := range []api.Client{*first, *second} {
		single, err := m.ClientConfig("s1", client.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		want[client.Name+".conf"] = single
	}

	for _, file := range files {
		expected, ok := want[file.Name]
		if !ok {
			t.Errorf("unexpected entry %q in the archive", file.Name)
			continue
		}
		if file.Content != expected {
			t.Errorf("entry %q differs from the single download:\n%s\n---\n%s", file.Name, file.Content, expected)
		}
	}
}

// A suspended client's config is still a valid one: the peer is only not
// served until it is activated again, so the archive has to carry it.
func TestClientConfigsCarrySuspendedClients(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SuspendClient("s1", client.ID); err != nil {
		t.Fatal(err)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "alice.conf" {
		t.Fatalf("the archive of a suspended client = %+v", files)
	}
}

// The endpoint a client config carries is the server's own: an archive must
// not hand out a different host than the single download does.
func TestClientConfigsCarryTheServerEndpoint(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	m.cfg.Servers[0].Endpoint = "vpn.example.com"
	m.mu.Unlock()

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("the archive holds %d entries, want 1", len(files))
	}
	if !strings.Contains(files[0].Content, "Endpoint = vpn.example.com:54844") {
		t.Errorf("the entry does not carry the endpoint:\n%s", files[0].Content)
	}
	single, err := m.ClientConfig("s1", client.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Content != single {
		t.Errorf("entry differs from the single download:\n%s\n---\n%s", files[0].Content, single)
	}
}

// Two clients may carry the same name; the archive has to keep them apart,
// or the second one overwrites the first on unpacking.
func TestClientConfigsDisambiguateNamesakes(t *testing.T) {
	m, _ := newTestManager(t)

	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	second, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("the archive holds %d entries, want 2", len(files))
	}
	if files[0].Name == files[1].Name {
		t.Fatalf("both entries are named %q", files[0].Name)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name, "alice") || !strings.HasSuffix(file.Name, ".conf") {
			t.Errorf("entry %q is not a client config", file.Name)
		}
	}
	// The namesake that lost the plain name carries the ID that tells the
	// two apart.
	if !strings.Contains(files[1].Name, second.ID) {
		t.Errorf("entry %q does not carry the client ID", files[1].Name)
	}
	if files[0].Name != "alice.conf" {
		t.Errorf("the first entry = %q, want the plain name", files[0].Name)
	}
}

// A client named after the entry a namesake was given must not land on it:
// every entry of the archive has a name of its own.
func TestClientConfigsKeepEveryEntryNameUnique(t *testing.T) {
	m, _ := newTestManager(t)

	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	second, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	// The name of the entry the second namesake was given.
	third, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice_" + second.ID})
	if err != nil {
		t.Fatal(err)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("the archive holds %d entries, want 3", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		if seen[file.Name] {
			t.Errorf("two entries are named %q", file.Name)
		}
		seen[file.Name] = true
	}
	if !seen["alice.conf"] || !seen["alice_"+second.ID+".conf"] {
		t.Errorf("the archive does not hold the namesake entries: %v", seen)
	}
	// The client named after the namesake's entry carries its own ID.
	if !seen["alice_"+second.ID+"_"+third.ID+".conf"] {
		t.Errorf("the client named after a namesake entry has no name of its own: %v", seen)
	}
}

// A name that cannot be a file name on the desktops people unpack an
// archive on is refused at the input: the file would not carry the name the
// user gave it, and a name with a path separator in it would write outside
// the directory it is unpacked into.
func TestAddClientRefusesANameThatCannotBeAFileName(t *testing.T) {
	m, _ := newTestManager(t)

	for _, bad := range []string{
		"a<b", "a>b", "a:b", `a"b`, "a|b", "a?b", "a*b", "a/b", `a\b`,
		"name.", "name ",
	} {
		if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddClient(%q): error = %v, want ErrInvalid", bad, err)
		}
	}
	if got := m.ClientCount(); got != 0 {
		t.Errorf("clientCount = %d, want 0: a refused name adds no client", got)
	}

	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"}); err != nil {
		t.Fatalf("a usable name was refused: %v", err)
	}
}

// The name is checked as it was typed, before the cleaning: a name the
// rule refuses stays refused, and the characters a config must not carry
// are still cleaned away rather than turned down.
func TestAddClientStillCleansTheNameItAccepts(t *testing.T) {
	m, _ := newTestManager(t)

	client, _, err := m.AddClient("s1", api.AddClientRequest{Name: "ali\tce [x]"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name != "ali ce x" {
		t.Errorf("stored name = %q, want %q", client.Name, "ali ce x")
	}
}

// An archive is unpacked by tools that take its paths literally, so a name
// with a path separator in it must not write outside the directory it is
// unpacked into. Such a name is refused at the input now, but a config
// written before that rule still holds one.
func TestClientConfigsKeepEntriesInsideTheArchive(t *testing.T) {
	m, _ := newTestManager(t)

	seedClient(t, m, "c1", "../../etc/passwd")

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("the archive holds %d entries, want 1", len(files))
	}
	if strings.ContainsAny(files[0].Name, `/\`) {
		t.Errorf("entry %q carries a path separator", files[0].Name)
	}
	if files[0].Name != ".._.._etc_passwd.conf" {
		t.Errorf("entry = %q, want the separators flattened", files[0].Name)
	}
}

func TestClientConfigsOfAnUnknownServerFail(t *testing.T) {
	m, _ := newTestManager(t)

	files, err := m.ClientConfigs("nope")
	if err == nil {
		t.Fatalf("the archive of an unknown server = %+v, want an error", files)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want a not-found", err)
	}
}

// Concurrent reads and writes must not race, and every one of them must be
// accounted for. Meaningful under -race.
func TestConcurrentClientChurnIsRaceFree(t *testing.T) {
	m, _ := newTestManager(t)

	const n = 12
	added := make(chan string, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client, _, err := m.AddClient("s1", api.AddClientRequest{Name: fmt.Sprintf("client-%d", i)})
			if err != nil {
				t.Errorf("AddClient: %v", err)
				return
			}
			added <- client.ID
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Clients("")
			m.Clients("s1")
			m.Servers()
			m.ClientCount()
		}()
	}
	wg.Wait()
	close(added)

	if got := m.ClientCount(); got != n {
		t.Fatalf("clientCount = %d, want %d", got, n)
	}

	// Every client must have its own address and its own peer block.
	conf, _ := os.ReadFile(m.cfg.Servers[0].ConfigPath)
	if got := strings.Count(string(conf), "[Peer]"); got != n {
		t.Errorf("[Peer] blocks = %d, want %d", got, n)
	}
	ips := map[string]bool{}
	for _, c := range m.Clients("s1") {
		if ips[c.ClientIP] {
			t.Errorf("duplicate client IP %s", c.ClientIP)
		}
		ips[c.ClientIP] = true
	}

	for id := range added {
		if _, ok := m.Client("s1", id); !ok {
			t.Errorf("client %s was lost", id)
		}
	}
}

// Two clients whose names differ only in case are two files on Linux and one
// file on macOS and Windows: whoever unpacks the archive would lose one of
// the two configs, so the names are told apart here as well.
func TestClientConfigsTellCaseInsensitiveNamesakesApart(t *testing.T) {
	m, _ := newTestManager(t)

	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.AddClient("s1", api.AddClientRequest{Name: "alice"}); err != nil {
		t.Fatal(err)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("the archive holds %d entries, want 2", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		key := strings.ToLower(file.Name)
		if seen[key] {
			t.Errorf("two entries are named %q, which is one file on Windows and macOS", file.Name)
		}
		seen[key] = true
	}
}

// The archive is handed to people who unpack it on Windows, where a file
// name may not hold a set of characters, may not end in a dot or a space,
// and may not be one of the device names Windows keeps for itself. The API
// refuses such names now, but a config written before that rule holds them.
func TestClientConfigsKeepEntryNamesUsableOnWindows(t *testing.T) {
	m, _ := newTestManager(t)

	for i, name := range []string{`a:b*c?`, "trailing.", "CON", "with space "} {
		seedClient(t, m, fmt.Sprintf("c%d", i), name)
	}

	files, err := m.ClientConfigs("s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.ContainsAny(file.Name, `<>:"|?*/\`) {
			t.Errorf("entry %q carries a character Windows refuses", file.Name)
		}
		if strings.HasSuffix(file.Name, ".") || strings.HasSuffix(file.Name, " ") {
			t.Errorf("entry %q ends in a dot or a space, which Windows drops", file.Name)
		}
		base := strings.TrimSuffix(file.Name, ".conf")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
			t.Errorf("entry %q is a name Windows keeps for itself", file.Name)
		}
	}
}
