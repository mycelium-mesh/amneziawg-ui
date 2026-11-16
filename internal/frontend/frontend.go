// Package frontend serves the packaged web UI straight off disk: the loader
// page, the wasm bundle and its assets, exactly as "fyne package -os wasm"
// wrote them - except that the Docker build leaves the bundle pre-compressed.
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
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// Assets is one directory of frontend files and the index of those that sit
// on disk pre-compressed.
type Assets struct {
	files  fs.FS
	packed map[string]packedAsset // request path -> pre-compressed copies
}

// packedAsset is where one request path's pre-compressed copies live inside
// the FS. The .gz is required: it answers every client that refuses brotli
// and it is what the shared weak ETag is taken from. The .br is optional.
type packedAsset struct {
	gz, br string
}

// Open indexes the directory. A tree without any .gz - a development build -
// simply has nothing packed, and every asset takes the ordinary static path.
func Open(files fs.FS) *Assets {
	return &Assets{files: files, packed: packedAssets(files, ".")}
}

// Compression is the compress middleware configured for these assets. The
// wasm bundle is tens of megabytes uncompressed, so compression is not
// optional; the assets that already sit on disk pre-compressed are excluded,
// since the packed handler hands those bytes over verbatim and re-encoding
// them would be wasted work.
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

// packedAssets maps a request path to the pre-compressed files that answer
// it. "make web-ui" leaves the bundle uncompressed; the Docker build replaces
// the source with its gzip and brotli copies - two pre-compressed files and no
// raw bundle in the layer - and an identity answer is inflated from the .gz,
// its length read off the ISIZE footer, so no cold request pays for
// compressing the same bundle again. A .br without its .gz is not indexed:
// the gz copy answers the clients refusing brotli and the ETag comes from it,
// so a half-packed tree behaves as an unpacked one.
func packedAssets(files fs.FS, root string) map[string]packedAsset {
	packed := map[string]packedAsset{}

	entries, err := fs.ReadDir(files, root)
	if err != nil {
		log.Printf("frontend assets: %v", err)
		return packed
	}

	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names[entry.Name()] = true
		}
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".gz") {
			continue
		}
		base := strings.TrimSuffix(name, ".gz")
		asset := packedAsset{gz: path.Join(root, name)}
		if names[base+".br"] {
			asset.br = path.Join(root, base+".br")
		}
		packed["/"+base] = asset
	}
	return packed
}

// sendPacked answers a request for an asset that only exists pre-compressed
// on disk. The static handler cannot: it looks for a file named exactly like
// the path and would return a 404 for /bundle.wasm.
func (a *Assets) sendPacked() fiber.Handler {
	return func(c fiber.Ctx) error {
		asset, ok := a.packed[c.Path()]
		if !ok || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		encoding := pickEncoding(c.Get(fiber.HeaderAcceptEncoding), asset.br != "")
		if encoding == "none" {
			// Identity itself was refused and none of the codings this client
			// takes exist here: RFC 9110 wants a 406, not a body it rejected.
			c.Vary(fiber.HeaderAcceptEncoding)
			return c.SendStatus(fiber.StatusNotAcceptable)
		}

		name := asset.gz
		if encoding == "br" {
			name = asset.br
		}
		file, err := a.files.Open(name)
		if err != nil {
			return c.Next()
		}

		// The content type is the one of the asset, not of the envelope:
		// instantiateStreaming in the loader page refuses a bundle that does
		// not arrive as application/wasm.
		if ct := mime.TypeByExtension(path.Ext(strings.TrimSuffix(asset.gz, ".gz"))); ct != "" {
			c.Set(fiber.HeaderContentType, ct)
		}
		c.Vary(fiber.HeaderAcceptEncoding)

		if encoding == "" {
			// A client with no Accept-Encoding at all - curl without
			// --compressed - still has to read the bundle: inflate it here.
			unpacked, err := gzip.NewReader(file)
			if err != nil {
				file.Close() //nolint:errcheck
				return err
			}
			body := gzipFile{Reader: unpacked, file: file}
			if size, ok := inflatedSize(a.files, asset.gz); ok {
				return c.SendStream(body, size)
			}
			return c.SendStream(body)
		}

		c.Set(fiber.HeaderContentEncoding, encoding)

		info, err := fs.Stat(a.files, name)
		if err != nil {
			return c.SendStream(file)
		}
		return c.SendStream(file, int(info.Size()))
	}
}

// pickEncoding settles one request's Accept-Encoding against what is on
// disk: "br" or "gzip" for a copy to send verbatim, "" for the inflated
// bundle, "none" when the client refuses everything on offer including
// identity. A coding the header does not list is not acceptable, q=0 is a
// refusal, and among the codings taken at an equal q brotli wins, then gzip;
// an explicit client preference (a higher q) is honoured over that order.
func pickEncoding(header string, hasBrotli bool) string {
	quality := acceptedQuality(header)

	qualityOf := func(coding string) float64 {
		if q, ok := quality[coding]; ok {
			return q
		}
		if q, ok := quality["*"]; ok {
			return q
		}
		return -1 // not listed and no wildcard: not acceptable
	}

	br, gzipQ := qualityOf("br"), qualityOf("gzip")
	if !hasBrotli {
		br = -1
	}
	switch {
	case br > 0 && br >= gzipQ:
		return "br"
	case gzipQ > 0:
		return "gzip"
	}

	// Identity is what is left: acceptable by default, taken away only by an
	// explicit identity;q=0 - the asterisk answers for codings, not for it.
	identity := 1.0
	if q, ok := quality["identity"]; ok {
		identity = q
	}
	if identity <= 0 {
		return "none"
	}
	return ""
}

// acceptedQuality reads an Accept-Encoding header into coding -> q, keeping
// only the codings the client listed - the x-gzip alias among them, counted
// as gzip. A q that cannot be read counts as 0.
func acceptedQuality(header string) map[string]float64 {
	quality := map[string]float64{}

	for entry := range strings.SplitSeq(header, ",") {
		coding, params, _ := strings.Cut(entry, ";")
		if coding = strings.ToLower(strings.TrimSpace(coding)); coding == "" {
			continue
		}
		q := 1.0
		for param := range strings.SplitSeq(params, ";") {
			name, value, ok := strings.Cut(param, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(value), `"`), 64)
			if err != nil {
				q = 0
				continue
			}
			q = min(max(parsed, 0), 1)
		}
		quality[coding] = q
	}

	// x-gzip is the deprecated alias of gzip (RFC 9110): a client spelling
	// the coding that way is asking for gzip and must not be handed the whole
	// 32 MB bundle as identity. An explicit gzip keeps its own q - only a
	// header offering the alias alone is answered through it.
	if _, listed := quality["gzip"]; !listed {
		if q, aliased := quality["x-gzip"]; aliased {
			quality["gzip"] = q
		}
	}
	return quality
}

// inflatedSize reads a gz file's uncompressed size from its 4-byte ISIZE
// footer, so the inflated answer can carry an exact Content-Length instead
// of being chunked. The footer counts one gzip member modulo 4 GiB - true of
// the single member "gzip -9" writes, for any bundle - and an FS that cannot
// hand back a byte at an offset simply gets no length, never a wrong one.
func inflatedSize(files fs.FS, name string) (int, bool) {
	info, err := fs.Stat(files, name)
	if err != nil || info.Size() < 4 {
		return 0, false
	}
	file, err := files.Open(name)
	if err != nil {
		return 0, false
	}
	defer file.Close()

	readerAt, ok := file.(io.ReaderAt)
	if !ok {
		return 0, false
	}
	var footer [4]byte
	if _, err := readerAt.ReadAt(footer[:], info.Size()-4); err != nil {
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
// previous bundle after an upgrade - and re-downloading the whole
// multi-megabyte wasm bundle on every page load is not an acceptable
// alternative.
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
	for requested, asset := range a.packed {
		tags[requested] = tags["/"+path.Base(asset.gz)]
	}
	tags["/"] = tags["/index.html"]

	return func(c fiber.Ctx) error {
		tag, ok := tags[c.Path()]
		if !ok || tag == "" || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		// The 304 below never reaches sendPacked: RFC 9110 requires Vary on
		// it exactly as on the 200 it answers, whose encoding was negotiated
		// from this same header.
		if _, packed := a.packed[c.Path()]; packed {
			c.Vary(fiber.HeaderAcceptEncoding)
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
