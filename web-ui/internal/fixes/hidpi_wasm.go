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
// overridden property. Real HiDPI screens are left alone; every ratio below
// 2 is raised to 2 (a fractional zoom like 1.25 becomes 2.0: the canvas is
// supersampled harder while the layout stays exact), and a ratio already at
// or above 2 (a heavily zoomed page) is kept, rounded up on the 0.1 grid.
//
// Remove once fyne's wasm painter rasterises text convincingly at 1x (or
// rasterises text textures at a supersampled density by itself), or once the
// page no longer draws its UI through the GL canvas.
func installHiDPICanvas() {
	window := js.Global()

	// safe runs one call and reports js.Undefined() instead of panicking, so
	// a missing holder or property, a bad shape or a throwing accessor
	// anywhere in the chain falls through to the next holder and eventually
	// to the fallback instead of taking the app down at startup. It absorbs
	// the recoverable Go panic syscall/js turns a JS exception into on the
	// calls that catch one (valueCall, valueInvoke, valueNew); valueGet and
	// valueSet do not catch, so nothing here goes through one - see the
	// Reflect note.
	safe := func(f func() js.Value) (v js.Value) {
		defer func() {
			if recover() != nil {
				v = js.Undefined()
			}
		}()
		return f()
	}

	// get and set read and write one property through Reflect called as a JS
	// method. A throwing accessor, setter or proxy trap then surfaces as a Go
	// panic, which safe absorbs; a plain Value.Get or Value.Set would let the
	// JS exception escape the wasm frame and kill the process instead
	// (wasm_exec.js's syscall/js.valueGet and valueSet have no try/catch,
	// valueCall does).
	//
	// Reflect itself is the one value that cannot be obtained this way:
	// Value.Get, Value.Index and the method lookup inside Value.Call are all
	// the same uncatching lookup before anything callable is found, and
	// building one with the Function constructor would need an eval-capable
	// page. wasm_exec.js resolves this same global on every syscall, so a
	// page that replaces it with a throwing accessor takes the runtime down
	// with or without this read.
	jsReflect := window.Get("Reflect")
	get := func(obj js.Value, key string) js.Value {
		return safe(func() js.Value { return jsReflect.Call("get", obj, key) })
	}
	set := func(obj js.Value, key string, value any) js.Value {
		return safe(func() js.Value { return jsReflect.Call("set", obj, key, value) })
	}

	object := get(window, "Object")

	// The real ratio is an accessor: the common engines put it on the window
	// object itself, while a legacy engine may inherit it from
	// Window.prototype, so try both and keep whatever is found. A missing
	// descriptor or a descriptor without a callable get is "not found".
	original := js.Undefined()
	for _, holder := range []js.Value{window, get(get(window, "Window"), "prototype")} {
		desc := safe(func() js.Value {
			return object.Call("getOwnPropertyDescriptor", holder, "devicePixelRatio")
		})
		if desc.Type() != js.TypeObject {
			continue
		}
		if getter := get(desc, "get"); getter.Type() == js.TypeFunction {
			original = getter
			break
		}
	}

	// numeric reads one value, treating anything but a plain finite number -
	// including an accessor that throws - as unreadable.
	numeric := func(f func() js.Value) (r float64, ok bool) {
		v := safe(f)
		if v.Type() != js.TypeNumber {
			return 0, false
		}
		r = v.Float()
		return r, !math.IsNaN(r) && !math.IsInf(r, 0)
	}

	// The fallback is the ratio seen at startup, for when no usable accessor
	// exists at all; an unreadable ratio counts as 1 and is raised to 2 below
	// like every other low ratio.
	fallback := 1.0
	if r, ok := numeric(func() js.Value { return get(window, "devicePixelRatio") }); ok {
		fallback = r
	}
	// read calls the captured accessor with the window as its receiver and
	// reports the live ratio, so zooming the page or moving it to another
	// screen keeps being tracked. The accessor is a WebIDL getter: it only
	// answers for a Window and throws "Illegal invocation" for anything else,
	// while Value.Invoke maps to Reflect.apply(v, undefined, args) - the
	// window would arrive as a mere argument and `this` would be undefined,
	// so every call would throw and the ratio would freeze at the fallback.
	// Reflect.apply binds the receiver instead, and runs under valueCall's
	// try/catch like get and set. A captured accessor that throws or answers
	// something other than a finite number falls back to the startup ratio.
	read := func() float64 {
		if original.Type() != js.TypeFunction {
			return fallback
		}
		if r, ok := numeric(func() js.Value {
			return jsReflect.Call("apply", original, window, js.ValueOf([]any{}))
		}); ok {
			return r
		}
		return fallback
	}

	// The Func is deliberately never released: the property override calls
	// it for as long as the page runs, and a released js.Func panics when
	// called.
	scaled := js.FuncOf(func(js.Value, []js.Value) any {
		ratio := math.Ceil(read()*10) / 10
		if ratio < 2 {
			return 2.0
		}
		return ratio
	})

	// Failing to override - a non-configurable property - must not take the
	// app down either: the text then simply stays as the engine renders it.
	// The descriptor is built on a proto-less object, written through the
	// catching set: with no prototype, neither the writes nor the reads
	// Object.defineProperty does on the way in can reach a trap a page left
	// on Object.prototype under a descriptor field name ("get",
	// "configurable", ...) - js.ValueOf(map[string]any{...}) would do both
	// through the raw valueSet and valueGet, where such a trap kills the
	// process. A descriptor that cannot be built leaves the property alone
	// rather than half-overridden.
	descriptor := safe(func() js.Value { return object.Call("create", js.Null()) })
	set(descriptor, "get", scaled.Value)
	set(descriptor, "configurable", true)
	if get(descriptor, "get").Type() == js.TypeFunction {
		safe(func() js.Value {
			return object.Call("defineProperty", window, "devicePixelRatio", descriptor)
		})
	}
}
