//go:build js && wasm

package fixes

import (
	"math"
	"syscall/js"
)

// installHiDPICanvas works around the wasm driver rendering text with visibly
// jagged, stair-stepped edges on displays whose devicePixelRatio is 1 (or any
// value below 2): labels come out looking like antialiasing had been switched
// off, while the same text on a HiDPI screen looks clean.
//
// The whole toolkit takes its pixel density from window.devicePixelRatio and
// then keeps it 1:1 all the way to the screen: glfw-js sizes the canvas
// backing store from it (browser_wasm.go, CreateWindow), fyne turns it into the
// canvas scale (internal/driver/glfw/device_wasm.go, SystemScaleForWindow), and
// the painter rasterises every text texture at exactly that density and maps
// it onto a quad of the same size (internal/painter/gl/texture.go). Nothing is
// ever scaled after rasterisation, so at 1x each 11-22pt label is rasterised
// one screen pixel per font pixel and the coverage the glyph rasteriser
// produced lands on the screen as rugged steps. Supersampling proves the
// point: the same strings rendered at 2x and shown at 1x are smooth and clean,
// so the raster at 1x is where the quality is lost.
//
// The workaround is to hand the toolkit a devicePixelRatio of at least 2,
// rounded up to one decimal - exactly the grid fyne's calculateScale rounds
// its scale to, so canvas scale, backing store and texture density stay in
// agreement. The browser then downsamples a 2x canvas to the screen on
// compositing, which is smooth. Nothing else changes: the layout, the mouse
// mapping (glfw-js multiplies client coordinates by the same number) and so
// every click coordinate keep working, because all of them read the same
// overridden property. Real HiDPI screens are left alone; only ratios below 2
// are raised, and a ratio already above 2 (a heavily zoomed page) is kept.
//
// Remove once fyne's wasm painter rasterises text convincingly at 1x (or
// rasterises text textures at a supersampled density by itself), or once the
// page no longer draws its UI through the GL canvas.
func installHiDPICanvas() {
	window := js.Global()
	object := window.Get("Object")

	// The real ratio is an accessor inherited from Window.prototype; keep it
	// so zooming the page or moving it to another screen keeps being tracked.
	// Some engines put the accessor on the window object itself, so try both.
	original := js.Undefined()
	for _, holder := range []js.Value{window, window.Get("Window").Get("prototype")} {
		if holder.IsUndefined() || holder.IsNull() {
			continue
		}
		original = object.Call("getOwnPropertyDescriptor", holder, "devicePixelRatio").Get("get")
		if !original.IsUndefined() && !original.IsNull() {
			break
		}
	}

	fallback := window.Get("devicePixelRatio").Float()
	read := func() float64 {
		if original.IsUndefined() || original.IsNull() {
			return fallback
		}
		return original.Invoke(window).Float()
	}

	scaled := js.FuncOf(func(js.Value, []js.Value) any {
		ratio := math.Ceil(read()*10) / 10
		if ratio < 2 {
			return 2.0
		}
		return ratio
	})

	object.Call("defineProperty", window, "devicePixelRatio", js.ValueOf(map[string]any{
		"get":          scaled.Value,
		"configurable": true,
	}))
}
