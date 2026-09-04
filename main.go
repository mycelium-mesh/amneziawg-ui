package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/basicauth"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/pprof"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"

	"amneziawg-web-ui/internal"
)

const (
	socketIOPath = "/socket.io"

	// staticDir is where "make web-ui" leaves the packaged frontend, relative
	// to the working directory the server is started from.
	staticDir = "./web-ui/wasm"
)

func main() {
	fmt.Println("AmneziaWG Web UI (Go/Fiber) starting...")

	// Initialise business logic
	mgr := internal.NewManager()

	// Initialise Socket.IO hub (registers connection handlers immediately)
	hub := internal.NewHub(mgr)
	mgr.SetHub(hub)

	// Start background goroutine for periodic traffic broadcasts
	go hub.StartTrafficUpdates()

	// Build Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if fe, ok := err.(*fiber.Error); ok {
				code = fe.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	app.Use(recover.New())
	app.Use(logger.New())

	// The frontend is served straight off disk: the loader page, the wasm
	// bundle and its assets, exactly as "fyne package -os wasm" wrote them -
	// except that the Docker build leaves the bundle gzipped. Nothing is baked
	// into the binary, so the bundle can be swapped without relinking the
	// server; it is looked up relative to the working directory, which is the
	// repository root in development and /app in the container (see the
	// WORKDIR in the Dockerfile).
	frontend := os.DirFS(staticDir)
	packed := packedAssets(frontend, ".")

	// The wasm bundle is tens of megabytes uncompressed, so compression is
	// not optional here. Socket.IO is excluded: its long-polling responses
	// are tiny, and compressing them only gets in the way of the framing. So
	// are the assets that already sit on disk gzipped - sendPacked hands those
	// bytes over verbatim, and re-encoding them would be wasted work.
	app.Use(compress.New(compress.Config{
		Next: func(c fiber.Ctx) bool {
			if strings.HasPrefix(c.Path(), socketIOPath) {
				return true
			}
			_, ok := packed[c.Path()]
			return ok
		},
	}))

	// Basic auth protects everything except static assets and the health-check
	app.Use(basicauth.New(basicauth.Config{
		Next: func(c fiber.Ctx) bool {
			// Only the container health check stays open; every asset of
			// the UI is behind the same credentials as the API.
			return c.Path() == "/status"
		},
		Users: map[string]string{
			webUIUser(): webUIPassword(),
		},
		Realm: "Restricted Content",
	}))

	// Profiling is opt-in: collecting a CPU profile costs the running server
	// real time, and the endpoints hand out stack traces and command line of
	// the process. Registered after basicauth on purpose, so /debug/pprof
	// requires the same credentials as everything else.
	if webUIPprof() {
		app.Use(pprof.New())
		fmt.Println("pprof enabled at /debug/pprof/")
	}

	// Socket.IO — must be registered before other routes.
	// adaptor.HTTPHandler wraps net/http.Handler; fasthttpadaptor under the hood
	// supports http.Hijacker so gorilla/websocket upgrades work correctly.
	app.Use(socketIOPath+"/", adaptor.HTTPHandler(hub.Server().ServeHandler(nil)))

	// REST routes first, so the catch-all below cannot shadow them.
	h := internal.NewHandlers(mgr, hub)
	h.RegisterRoutes(app)

	// Everything not claimed above is a frontend asset.
	app.Use(frontendCache(frontend, ".", packed))
	app.Use(sendPacked(frontend, packed))
	app.Get("/*", static.New("", static.Config{
		FS:         frontend,
		IndexNames: []string{"index.html"},
		Browse:     false,
	}))

	port := webUIPort()
	fmt.Printf("Serving the frontend from %s\n", staticDir)
	fmt.Printf("Listening on :%d\n", port)
	log.Fatal(app.Listen(":" + strconv.Itoa(port)))
}

// packedAssets maps a request path to the pre-compressed file that answers it.
// "make web-ui" leaves the bundle on disk uncompressed, but the Docker build
// gzips it and the image ships bundle.wasm.gz alone: that is 14 MB instead of
// 49 MB of layer, and it spares the server from gzipping the same 49 MB on
// every cold request. A development tree without any .gz simply yields an
// empty map, and every asset takes the ordinary static path.
func packedAssets(files fs.FS, root string) map[string]string {
	packed := map[string]string{}

	entries, err := fs.ReadDir(files, root)
	if err != nil {
		log.Printf("frontend assets: %v", err)
		return packed
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".gz") {
			continue
		}
		packed["/"+strings.TrimSuffix(name, ".gz")] = path.Join(root, name)
	}
	return packed
}

// sendPacked answers a request for an asset that only exists gzipped on disk.
// The static handler below cannot: it looks for a file named exactly like the
// path and would return a 404 for /bundle.wasm.
func sendPacked(files fs.FS, packed map[string]string) fiber.Handler {
	return func(c fiber.Ctx) error {
		name, ok := packed[c.Path()]
		if !ok || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		file, err := files.Open(name)
		if err != nil {
			return c.Next()
		}

		// The content type is the one of the asset, not of the envelope:
		// instantiateStreaming in the loader page refuses a bundle that does
		// not arrive as application/wasm.
		if ct := mime.TypeByExtension(path.Ext(strings.TrimSuffix(name, ".gz"))); ct != "" {
			c.Set(fiber.HeaderContentType, ct)
		}
		c.Vary(fiber.HeaderAcceptEncoding)

		// Browsers all advertise gzip, but curl sends no Accept-Encoding at
		// all unless asked to, and the bundle has to stay readable by a client
		// that will not decompress it itself.
		if !strings.Contains(c.Get(fiber.HeaderAcceptEncoding), "gzip") {
			unpacked, err := gzip.NewReader(file)
			if err != nil {
				file.Close() //nolint:errcheck
				return err
			}
			return c.SendStream(gzipFile{Reader: unpacked, file: file})
		}

		c.Set(fiber.HeaderContentEncoding, "gzip")

		info, err := fs.Stat(files, name)
		if err != nil {
			return c.SendStream(file)
		}
		return c.SendStream(file, int(info.Size()))
	}
}

// gzipFile ties the decompressor to the file underneath it. fasthttp closes
// the body stream it is handed if it is an io.Closer, and closing a gzip
// reader on its own leaks the descriptor it was reading from.
type gzipFile struct {
	*gzip.Reader
	file fs.File
}

func (g gzipFile) Close() error {
	err := g.Reader.Close()
	if cerr := g.file.Close(); err == nil {
		err = cerr
	}
	return err
}

// frontendCache makes the browser revalidate the frontend instead of caching
// it blindly. The file names are the same in every release, so
// without a content-derived validator a browser would happily keep running
// the previous bundle after an upgrade - and re-downloading ~50 MB of wasm on
// every page load is not an acceptable alternative.
func frontendCache(files fs.FS, root string, packed map[string]string) fiber.Handler {
	tags := map[string]string{}

	entries, err := fs.ReadDir(files, root)
	if err != nil {
		log.Printf("frontend cache: %v", err)
		return func(c fiber.Ctx) error { return c.Next() }
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		tag, err := fileETag(files, path.Join(root, entry.Name()))
		if err != nil {
			log.Printf("frontend cache: %v", err)
			continue
		}
		tags["/"+entry.Name()] = tag
	}

	// A packed asset is requested under the name it will have once unpacked,
	// so its tag has to answer to that path too. The tag is weak, which is
	// exactly what lets one identifier stand for both encodings of the bundle.
	for requested, name := range packed {
		tags[requested] = tags["/"+path.Base(name)]
	}
	tags["/"] = tags["/index.html"]

	return func(c fiber.Ctx) error {
		tag, ok := tags[c.Path()]
		if !ok || tag == "" || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Set(fiber.HeaderETag, tag)
		if etagMatches(c.Get(fiber.HeaderIfNoneMatch), tag) {
			return c.SendStatus(fiber.StatusNotModified)
		}

		// The content hash is the only validator worth trusting here: a
		// modification time survives a copy (docker COPY, rsync -a) that
		// replaces the file contents, and the static handler below would
		// answer such an If-Modified-Since with a 304 over a bundle the
		// browser has never seen. So the timestamp is dropped from both ends
		// of the exchange rather than left to contradict the hash.
		c.Request().Header.Del(fiber.HeaderIfModifiedSince)
		err := c.Next()
		c.Response().Header.Del(fiber.HeaderLastModified)
		return err
	}
}

// etagMatches reports whether an If-None-Match header covers tag. Browsers
// echo back what they were sent, but the header is a list and the weak marker
// is not part of the comparison, so both are handled.
func etagMatches(header, tag string) bool {
	tag = strings.TrimPrefix(tag, "W/")
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == tag {
			return true
		}
	}
	return false
}

func fileETag(files fs.FS, name string) (string, error) {
	file, err := files.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}

	// Weak on purpose. The tag hashes the file as it sits on disk, while what goes
	// on the wire is usually gzipped - a difference a strong tag is not
	// allowed to gloss over. It also keeps the compression middleware from
	// replacing the tag with a hash of the compressed body: that one changes
	// with the compression settings, and it is the wrong thing to compare a
	// cached copy against.
	return `W/"` + hex.EncodeToString(sum.Sum(nil)[:8]) + `"`, nil
}

func webUIPort() int {
	if s := os.Getenv("WEB_UI_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 54845
}

// webUIPprof reports whether the /debug/pprof handlers should be mounted.
// The value is parsed as a Go boolean ("1", "t", "true", "TRUE" and their
// negatives); anything else counts as "off" rather than aborting the start.
func webUIPprof() bool {
	enabled, err := strconv.ParseBool(os.Getenv("WEB_UI_PPROF"))
	return err == nil && enabled
}

func webUIUser() string {
	if s := os.Getenv("WEB_UI_USER"); s != "" {
		return s
	}
	return "admin"
}

func webUIPassword() string {
	if s := os.Getenv("WEB_UI_PASSWORD"); s != "" {
		return s
	}
	// SHA-256 hash of "changeme" in base64
	return "BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY="
}
