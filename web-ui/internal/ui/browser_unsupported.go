//go:build !js || !wasm

package ui

// Linux (and every other native target) is not supported: this frontend is a
// WebAssembly application that only works inside a browser, where it can read
// the page origin, open download links and hand files to the viewer. There is
// no meaningful native equivalent, so the stubs below exist purely so editors
// and "go vet" can still type-check the package - running the result is not a
// supported way to use it.
//
// Build the real thing with:
//
//	make web-ui        # go tool fyne package -os wasm
const unsupportedPlatform = "the AmneziaWG Web UI frontend runs in the browser only: " +
	"build it for WebAssembly with \"make web-ui\" (GOOS=js GOARCH=wasm)"

func baseURL() string {
	panic(unsupportedPlatform)
}

func openURL(string) {
	panic(unsupportedPlatform)
}

func copyText(string, func(bool)) {
	panic(unsupportedPlatform)
}

func saveBytes(string, string, []byte) {
	panic(unsupportedPlatform)
}
