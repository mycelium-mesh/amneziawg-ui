// Package fixes holds the workarounds for upstream bugs (fyne and its wasm
// driver), kept out of the UI code: each one lives in its own file, together
// with a description of the bug and the condition under which it can be
// dropped again.
package fixes

import "fyne.io/fyne/v2"

// Install puts every workaround in place; call it on the application before
// the window starts, since one of them replaces the clipboard it hands out.
func Install(a fyne.App) {
	installHiDPICanvas()
	installNonASCIIKeyboard()
	installClipboard(a)
	installVisibilityRepaint(a)
}
