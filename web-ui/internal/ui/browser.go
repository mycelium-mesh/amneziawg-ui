//go:build js && wasm

package ui

import (
	"encoding/base64"
	"syscall/js"
)

// The browser is the only supported target: the UI always runs inside the
// page served by the Go backend it talks to. See browser_unsupported.go for
// the stubs that keep a native toolchain able to type-check this package.

// baseURL is the origin the app was served from, so every API call stays
// same-origin and the browser replays the basic-auth credentials it already
// holds for this realm.
func baseURL() string {
	return js.Global().Get("location").Get("origin").String()
}

// openURL asks the browser to follow a link in a new tab. Used for the
// download endpoints, which reply with a Content-Disposition header.
func openURL(url string) {
	js.Global().Call("open", url, "_blank")
}

// copyText puts text on the system clipboard and reports through done whether
// that worked.
//
// Fyne's own Clipboard() is unusable here: on wasm it reaches straight into
// navigator.clipboard, which the browser only exposes in a secure context. A
// panel served over plain HTTP on a LAN address is not one, so the property is
// undefined there and the call panics the whole application instead of
// failing. Hence the direct implementation, with the legacy execCommand path
// as the fallback for exactly that case.
func copyText(text string, done func(ok bool)) {
	clipboard := js.Global().Get("navigator").Get("clipboard")
	if !clipboard.Truthy() || !clipboard.Get("writeText").Truthy() {
		done(copyViaTextArea(text))
		return
	}

	promise := clipboard.Call("writeText", text)
	if !promise.Truthy() || !promise.Get("then").Truthy() {
		done(true)
		return
	}

	// Writing can still be refused - the page may have lost the user
	// activation the browser wants to see - so the text area is the second
	// chance rather than an alternative.
	var onDone, onFail js.Func
	release := func() {
		onDone.Release()
		onFail.Release()
	}
	onDone = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		done(true)
		return nil
	})
	onFail = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		done(copyViaTextArea(text))
		return nil
	})

	// Two handlers on one "then" rather than a trailing "catch": exactly one
	// of them ever runs, so releasing both from either is safe.
	promise.Call("then", onDone, onFail)
}

// copyViaTextArea copies through a throwaway off-screen text area, the only
// route left when the asynchronous Clipboard API is missing or refuses.
func copyViaTextArea(text string) bool {
	doc := js.Global().Get("document")
	if !doc.Get("execCommand").Truthy() {
		return false
	}

	area := doc.Call("createElement", "textarea")
	area.Set("value", text)

	// Off-screen but not hidden: a display:none or visibility:hidden element
	// has no selection for the copy command to pick up.
	style := area.Get("style")
	style.Set("position", "fixed")
	style.Set("top", "0")
	style.Set("left", "0")
	style.Set("opacity", "0")

	body := doc.Get("body")
	body.Call("appendChild", area)
	area.Call("focus")
	area.Call("select")
	copied := doc.Call("execCommand", "copy").Truthy()
	body.Call("removeChild", area)

	return copied
}

// saveBytes hands the viewer a generated file (the QR code image) by clicking
// a synthetic anchor carrying a data: URL.
func saveBytes(name, mime string, data []byte) {
	doc := js.Global().Get("document")
	anchor := doc.Call("createElement", "a")
	anchor.Set("href", "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	anchor.Set("download", name)
	doc.Get("body").Call("appendChild", anchor)
	anchor.Call("click")
	doc.Get("body").Call("removeChild", anchor)
}
