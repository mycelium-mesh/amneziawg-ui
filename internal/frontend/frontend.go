// Package frontend serves the packaged web UI straight off disk: the loader
// page, the wasm bundle and its assets, exactly as "fyne package -os wasm"
// wrote them - except that the Docker build leaves the bundle gzipped.
// Nothing is baked into the binary, so the bundle can be swapped without
// relinking the server.
package frontend

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"mime"
	"path"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// Assets is one directory of frontend files and the index of those that sit
// on disk pre-compressed.
type Assets struct {
	files  fs.FS
	packed map[string]string // request path -> .gz file answering it
}

// Open indexes the directory. A tree without any .gz - a development build -
// simply has nothing packed, and every asset takes the ordinary static path.
func Open(files fs.FS) *Assets {
	return &Assets{files: files, packed: packedAssets(files, ".")}
}

// Compression is the compress middleware configured for these assets. The
// wasm bundle is tens of megabytes uncompressed, so compression is not
// optional; the assets that already sit on disk gzipped are excluded, since
// the packed handler hands those bytes over verbatim and re-encoding them
// would be wasted work.
func (a *Assets) Compression() fiber.Handler {
	return compress.New(compress.Config{
		Next: func(c fiber.Ctx) bool {
			_, ok := a.packed[c.Path()]
			return ok
		},
	})
}

// Mount registers the asset handlers. It goes last: everything the REST
// routes did not claim is a frontend asset.
func (a *Assets) Mount(app *fiber.App) {
	app.Use(a.cache())
	app.Use(a.sendPacked())
	app.Get("/*", static.New("", static.Config{
		FS:         a.files,
		IndexNames: []string{"index.html"},
		Browse:     false,
	}))
}

// packedAssets maps a request path to the pre-compressed file that answers it.
// "make web-ui" leaves the bundle on disk uncompressed, but the Docker build
// gzips it and the image ships bundle.wasm.gz alone: the layer holds the
// compressed bundle rather than the raw one, and it spares the server from
// gzipping it again on every cold request.
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
// The static handler cannot: it looks for a file named exactly like the path
// and would return a 404 for /bundle.wasm.
func (a *Assets) sendPacked() fiber.Handler {
	return func(c fiber.Ctx) error {
		name, ok := a.packed[c.Path()]
		if !ok || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		file, err := a.files.Open(name)
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
			// The length of the unpacked bundle is read before the first
			// byte is decompressed, so the identity response carries a
			// Content-Length instead of chunking a body whose length was
			// known all along.
			size, known := gzipISize(file)
			unpacked, err := gzip.NewReader(file)
			if err != nil {
				file.Close() //nolint:errcheck
				return err
			}
			if !known {
				return c.SendStream(gzipFile{Reader: unpacked, file: file})
			}
			return c.SendStream(gzipFile{Reader: unpacked, file: file}, size)
		}

		c.Set(fiber.HeaderContentEncoding, "gzip")

		info, err := fs.Stat(a.files, name)
		if err != nil {
			return c.SendStream(file)
		}
		return c.SendStream(file, int(info.Size()))
	}
}

// gzipISize is the length of the decompressed data, which the gzip format
// writes into the last four bytes of the file. The count is modulo 2^32,
// which is plenty for a bundle measured in tens of megabytes.
//
// A file that cannot seek has no footer to read at its end, and the caller
// falls back to a chunked body. The stream is left at its start: the
// decompressor that reads it next sees a fresh file.
func gzipISize(file fs.File) (int, bool) {
	seeker, ok := file.(io.Seeker)
	if !ok {
		return 0, false
	}
	end, err := seeker.Seek(0, io.SeekEnd)
	if err != nil || end < 4 {
		return 0, false
	}
	if _, err := seeker.Seek(end-4, io.SeekStart); err != nil {
		return 0, false
	}
	var footer [4]byte
	if _, err := io.ReadFull(file, footer[:]); err != nil {
		return 0, false
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return 0, false
	}
	return int(binary.LittleEndian.Uint32(footer[:])), true
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

// cache makes the browser revalidate the frontend instead of caching it
// blindly. The file names are the same in every release, so without a
// content-derived validator a browser would happily keep running the
// previous bundle after an upgrade - and re-downloading ~50 MB of wasm on
// every page load is not an acceptable alternative.
func (a *Assets) cache() fiber.Handler {
	tags := map[string]string{}

	entries, err := fs.ReadDir(a.files, ".")
	if err != nil {
		log.Printf("frontend cache: %v", err)
		return func(c fiber.Ctx) error { return c.Next() }
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		tag, err := fileETag(a.files, entry.Name())
		if err != nil {
			log.Printf("frontend cache: %v", err)
			continue
		}
		tags["/"+entry.Name()] = tag
	}

	// A packed asset is requested under the name it will have once unpacked,
	// so its tag has to answer to that path too. The tag is weak, which is
	// exactly what lets one identifier stand for both encodings of the bundle.
	for requested, name := range a.packed {
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

		// A packed asset is negotiated by Accept-Encoding, so its 304 has
		// to say the same: without the header a cache is free to hand the
		// gzipped bundle to a client that never asked for gzip (RFC 9110
		// §15.4.5).
		if _, packed := a.packed[c.Path()]; packed {
			c.Vary(fiber.HeaderAcceptEncoding)
		}

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
