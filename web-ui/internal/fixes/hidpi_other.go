//go:build !js || !wasm

package fixes

// installHiDPICanvas is a no-op off the browser: only the wasm driver sizes
// its canvas and text textures from window.devicePixelRatio, see
// hidpi_wasm.go. The stub only exists so "go vet" and editors can still
// type-check this package natively.
func installHiDPICanvas() {}
