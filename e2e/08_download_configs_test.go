package e2e

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"

	"amneziawg-web-ui/web-ui/api"
)

// The archive of a server's client configs is what the "Download all
// configs" button hands out: one entry per client, holding the very same
// file the single-client download gives.
func TestDownloadAllClientConfigs(t *testing.T) {
	a := startApp(t)

	servers := getJSON[[]api.Server](t, "/api/servers")
	if len(servers) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	server := servers[0]
	clients := getJSON[[]api.Client](t, "/api/servers/"+server.ID+"/clients")
	if len(clients) == 0 {
		t.Fatal("TestAddClient must run first")
	}

	body, headers := apiGetWithHeaders(t, "/api/servers/"+server.ID+"/clients/configs")
	if got := headers.Get("Content-Type"); got != "application/zip" {
		t.Errorf("content type = %q, want application/zip", got)
	}
	disposition := headers.Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") || !strings.HasSuffix(disposition, `.zip"`) {
		t.Errorf("content disposition = %q, want an attachment named after the server", disposition)
	}

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the archive does not read: %v", err)
	}
	if len(archive.File) != len(clients) {
		t.Fatalf("the archive holds %d entries, want one per client (%d)", len(archive.File), len(clients))
	}

	entries := map[string]string{}
	for _, file := range archive.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = string(content)
	}

	for _, client := range clients {
		name := client.Name + ".conf"
		got, ok := entries[name]
		if !ok {
			t.Errorf("the archive has no entry %q", name)
			continue
		}
		want := string(apiGet(t, "/api/servers/"+server.ID+"/clients/"+client.ID+"/config"))
		if got != want {
			t.Errorf("entry %q differs from the single download:\n%s\n---\n%s", name, got, want)
		}
	}

	// The button on the card hands out that archive: the browser gets a
	// download named after the server.
	a.shot("14-download-all")
	download, err := a.page.ExpectDownload(func() error {
		a.click(downloadAllX, cardActionsY)
		return nil
	}, playwright.PageExpectDownloadOptions{Timeout: new(float64(30000))})
	if err != nil {
		t.Fatalf("the button did not start a download: %v", err)
	}
	if name := download.SuggestedFilename(); !strings.HasSuffix(name, ".zip") {
		t.Errorf("downloaded file = %q, want a zip", name)
	}

	a.noPageErrors()
}
