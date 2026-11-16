package frontend

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func gzipped(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return buf.Bytes()
}

// brotliBundle is "\x00asm" as "brotli -q 11 -w 24" packs it. The standard
// library has no brotli decoder, so these tests pin the wire contract
// instead: the file has to go out byte for byte, verbatim.
var brotliBundle = []byte{0x8f, 0x01, 0x80, 0x00, 0x61, 0x73, 0x6d, 0x03}

func newApp(t *testing.T) *fiber.App {
	t.Helper()
	return newAppWith(t, fstest.MapFS{
		"index.html":     {Data: []byte("<html>loader</html>")},
		"bundle.wasm.gz": {Data: gzipped(t, "\x00asm")},
	})
}

func newAppWith(t *testing.T, files fstest.MapFS) *fiber.App {
	t.Helper()
	app := fiber.New()
	assets := Open(files)
	app.Use(assets.Compression())
	assets.Mount(app)
	return app
}

// packedFiles is the Docker build's tree: the bundle present in both
// encodings beside the loader page.
func packedFiles(t *testing.T) (fstest.MapFS, []byte) {
	t.Helper()
	gz := gzipped(t, "\x00asm")
	return fstest.MapFS{
		"index.html":     {Data: []byte("<html>loader</html>")},
		"bundle.wasm.gz": {Data: gz},
		"bundle.wasm.br": {Data: brotliBundle},
	}, gz
}

func get(t *testing.T, app *fiber.App, path string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The bundle only exists gzipped on disk, and is served under its unpacked
// name: verbatim to a browser, inflated to a client that did not ask for
// gzip, and as application/wasm either way.
func TestPackedBundleIsServedBothWays(t *testing.T) {
	app := newApp(t)

	resp := get(t, app, "/bundle.wasm", map[string]string{"Accept-Encoding": "gzip"})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Content-Type") != "application/wasm" {
		t.Errorf("gzip client: %d %v", resp.StatusCode, resp.Header)
	}
	if r, err := gzip.NewReader(bytes.NewReader(body)); err != nil {
		t.Errorf("body is not gzip: %v", err)
	} else if raw, _ := io.ReadAll(r); string(raw) != "\x00asm" {
		t.Errorf("inflated body = %q", raw)
	}

	resp = get(t, app, "/bundle.wasm", nil)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || string(body) != "\x00asm" {
		t.Errorf("plain client: %d %v %q", resp.StatusCode, resp.Header, body)
	}
}

// Every asset carries a content-derived weak ETag, and a matching
// If-None-Match is answered 304 - under the unpacked name too.
func TestAssetsRevalidateByContentHash(t *testing.T) {
	app := newApp(t)

	for _, path := range []string{"/", "/index.html", "/bundle.wasm"} {
		resp := get(t, app, path, nil)
		tag := resp.Header.Get("ETag")
		if resp.StatusCode != 200 || tag == "" || resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %v", path, resp.StatusCode, resp.Header)
			continue
		}
		if resp.Header.Get("Last-Modified") != "" {
			t.Errorf("%s: Last-Modified sent alongside the hash", path)
		}

		resp = get(t, app, path, map[string]string{"If-None-Match": tag})
		if resp.StatusCode != http.StatusNotModified {
			t.Errorf("%s with matching tag: %d, want 304", path, resp.StatusCode)
		}
	}
}

func TestEtagMatches(t *testing.T) {
	tag := `W/"abc"`
	for header, want := range map[string]bool{
		`W/"abc"`: true, `"abc"`: true, `"x", W/"abc"`: true, "*": true, `"other"`: false, "": false,
	} {
		if got := etagMatches(header, tag); got != want {
			t.Errorf("etagMatches(%q) = %v, want %v", header, got, want)
		}
	}
}

// The packed bundle goes out in an encoding the client actually accepts:
// brotli and gzip when both are on offer and taken, the inflated bundle when
// neither is, 406 when identity was refused as well. Every 200 names the
// asset's own content type, its exact Content-Length and the Vary the choice
// of encoding depends on.
func TestEncodingNegotiation(t *testing.T) {
	files, gz := packedFiles(t)
	app := newAppWith(t, files)

	for _, tc := range []struct {
		name   string
		accept string // "" means no Accept-Encoding at all
		status int
		ce     string // Content-Encoding; "" is identity
		length int
	}{
		{name: "no header", accept: "", status: 200, ce: "", length: 4},
		{name: "only br", accept: "br", status: 200, ce: "br", length: len(brotliBundle)},
		{name: "only gzip", accept: "gzip", status: 200, ce: "gzip", length: len(gz)},
		{name: "browser header", accept: "gzip, deflate, br, zstd", status: 200, ce: "br", length: len(brotliBundle)},
		{name: "equal quality", accept: "br, gzip", status: 200, ce: "br", length: len(brotliBundle)},
		{name: "gzip refused br kept", accept: "br, gzip;q=0", status: 200, ce: "br", length: len(brotliBundle)},
		{name: "gzip;q=0", accept: "gzip;q=0", status: 200, ce: "", length: 4},
		{name: "client prefers gzip", accept: "gzip;q=1, br;q=0.5", status: 200, ce: "gzip", length: len(gz)},
		{name: "unknown token", accept: "unicorns", status: 200, ce: "", length: 4},
		{name: "unknown beside gzip", accept: "unicorns, gzip", status: 200, ce: "gzip", length: len(gz)},
		{name: "uppercase token", accept: "GZip", status: 200, ce: "gzip", length: len(gz)},
		{name: "br;q=0 beside gzip", accept: "br;q=0, gzip", status: 200, ce: "gzip", length: len(gz)},
		{name: "wildcard", accept: "*", status: 200, ce: "br", length: len(brotliBundle)},
		{name: "identity refused alone", accept: "identity;q=0", status: 406},
		{name: "identity refused too", accept: "identity;q=0, gzip;q=0, br;q=0", status: 406},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.accept != "" {
				headers["Accept-Encoding"] = tc.accept
			}
			resp := get(t, app, "/bundle.wasm", headers)
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tc.status || resp.Header.Get("Content-Encoding") != tc.ce {
				t.Fatalf("got %d CE=%q, want %d CE=%q (%v)",
					resp.StatusCode, resp.Header.Get("Content-Encoding"), tc.status, tc.ce, resp.Header)
			}
			if resp.Header.Get("Vary") != "Accept-Encoding" {
				t.Errorf("Vary = %q, want Accept-Encoding", resp.Header.Get("Vary"))
			}
			if tc.status != 200 {
				return
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/wasm" {
				t.Errorf("Content-Type = %q, want application/wasm", ct)
			}
			if resp.Header.Get("ETag") == "" {
				t.Errorf("no ETag on a packed response: %v", resp.Header)
			}
			if resp.ContentLength != int64(tc.length) {
				t.Errorf("Content-Length = %d, want %d", resp.ContentLength, tc.length)
			}
			if len(resp.TransferEncoding) != 0 {
				t.Errorf("transfer encoding %v alongside Content-Length", resp.TransferEncoding)
			}

			switch tc.ce {
			case "":
				if string(body) != "\x00asm" {
					t.Errorf("identity body = %q", body)
				}
			case "br":
				if !bytes.Equal(body, brotliBundle) {
					t.Errorf("br body = %x, want the sidecar verbatim", body)
				}
			case "gzip":
				r, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatalf("body is not gzip: %v", err)
				}
				raw, _ := io.ReadAll(r)
				if string(raw) != "\x00asm" {
					t.Errorf("inflated body = %q", raw)
				}
			}
		})
	}
}

// With only the gzip copy on disk, brotli is never invented: a client
// offering both gets gzip, and one offering only brotli - none of whose
// codings exist here - gets the inflated bundle, which it must take as
// identity.
func TestNegotiationWithoutBrotliSidecar(t *testing.T) {
	app := newApp(t) // bundle.wasm.gz alone, like a build that skipped brotli

	for _, tc := range []struct {
		accept string
		ce     string
	}{
		{accept: "br, gzip", ce: "gzip"},
		{accept: "br", ce: ""},
		{accept: "", ce: ""},
	} {
		headers := map[string]string{}
		if tc.accept != "" {
			headers["Accept-Encoding"] = tc.accept
		}
		resp := get(t, app, "/bundle.wasm", headers)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != tc.ce {
			t.Errorf("Accept-Encoding %q: %d CE=%q, want 200 CE=%q",
				tc.accept, resp.StatusCode, resp.Header.Get("Content-Encoding"), tc.ce)
		}
		if tc.ce == "gzip" {
			r, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Errorf("Accept-Encoding %q: body is not gzip: %v", tc.accept, err)
				continue
			}
			raw, _ := io.ReadAll(r)
			body = raw
		}
		if string(body) != "\x00asm" {
			t.Errorf("Accept-Encoding %q: body = %q", tc.accept, body)
		}
	}
}

// A packed path only exists where its .gz does - that copy answers the
// clients refusing brotli and sources the shared ETag. A stray .br without
// it, or no sidecars at all, leaves the path to the static handler, which
// has no bundle.wasm to serve.
func TestPackedPathNeedsItsGzipCopy(t *testing.T) {
	for name, files := range map[string]fstest.MapFS{
		"only brotli": {
			"index.html":     {Data: []byte("<html>loader</html>")},
			"bundle.wasm.br": {Data: brotliBundle},
		},
		"no sidecars": {
			"index.html": {Data: []byte("<html>loader</html>")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := newAppWith(t, files)
			for _, accept := range []string{"br", "gzip", ""} {
				headers := map[string]string{}
				if accept != "" {
					headers["Accept-Encoding"] = accept
				}
				resp := get(t, app, "/bundle.wasm", headers)
				if resp.StatusCode != 404 {
					t.Errorf("Accept-Encoding %q: %d %v, want 404", accept, resp.StatusCode, resp.Header)
				}
			}
		})
	}
}

// RFC 9110 §15.4.5: a 304 carries the validator and the Vary of the 200 it
// replaces, even though sendPacked - the handler that negotiates the
// encoding - never runs on the way to it. One weak tag answers for both
// encodings of the bundle.
func TestPackedNotModifiedKeepsVary(t *testing.T) {
	files, _ := packedFiles(t)
	app := newAppWith(t, files)

	first := get(t, app, "/bundle.wasm", map[string]string{"Accept-Encoding": "br"})
	tag := first.Header.Get("ETag")
	if first.StatusCode != 200 || tag == "" {
		t.Fatalf("first request: %d %v", first.StatusCode, first.Header)
	}
	viaGzip := get(t, app, "/bundle.wasm", map[string]string{"Accept-Encoding": "gzip"})
	if viaGzip.Header.Get("ETag") != tag {
		t.Errorf("the gzip copy carries ETag %q, the br copy %q: one weak tag must cover both",
			viaGzip.Header.Get("ETag"), tag)
	}

	resp := get(t, app, "/bundle.wasm", map[string]string{
		"Accept-Encoding": "gzip, br",
		"If-None-Match":   tag,
	})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("matching tag: %d, want 304", resp.StatusCode)
	}
	if resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q on the 304, want Accept-Encoding", resp.Header.Get("Vary"))
	}
	if resp.Header.Get("ETag") != tag || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("validator headers: %v", resp.Header)
	}
	if len(body) != 0 {
		t.Errorf("304 carried a body: %q", body)
	}
}
