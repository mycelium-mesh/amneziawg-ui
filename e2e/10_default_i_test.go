package e2e

import (
	"bytes"
	"image"
	_ "image/png"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"amneziawg-web-ui/web-ui/api"
)

// The add-client dialog's "Apply I-settings" block and the standard's two
// buttons inside it, in the same coordinates as the rest of the suite: the
// dialog is the 760x620 one centred in the 1500x950 viewport, with the form
// above the checkbox, the five I-fields under it, the button row below them
// and the fixed footer holding Cancel at the bottom.
const (
	applyICheckboxX = 397
	applyICheckboxY = 367

	// The five I-fields: one column of stacked label+entry pairs, spanning
	// the dialog content from its left edge (dialog at 370 + 16 padding).
	iFieldLeft = 386
	iFieldX    = 700
	iFieldY    = 421 // I1, the topmost
	iFieldDY   = 58  // each field below the previous one

	saveStandardX    = 479
	saveStandardY    = 693
	restoreStandardX = 676
	restoreStandardY = 693

	// The footer's Cancel - the left cell of the dialog's centred button
	// pair, mirroring the ok button the other dialogs click at 807, 751.
	dialogCancelX = 693
	dialogCancelY = 751

	// The confirm dialog that guards the restore: "Yes" on the right of the
	// centred pair.
	confirmYesX = 782
	confirmYesY = 532
)

// The head of the shipped I1 - the start of the long captured handshake a
// standard with nothing saved under it still answers with.
const shippedI1Prefix = "<b 0xc70000000108ce1bf31"

// TestClientDialogStandardISettings drives the two buttons the add-client
// dialog gives the operator over their own standard I1-I5 set: save writes
// only what is typed, and restore is the full clear back to the shipped
// values. The canvas has no readable text, so the contract is asserted
// through the REST effect the buttons must produce, and the one thing the
// REST cannot show - the reopened dialog offering the saved value as the
// field's placeholder - through the pixels of that field.
//
// It runs last and leaves the standard as it ships, so a rerun finds the
// clean instance the suite expects.
func TestClientDialogStandardISettings(t *testing.T) {
	a := newApp(t)

	// Every request the buttons send to the standard, counted as it leaves
	// the browser: the Go-side polling below is not a browser request and
	// never appears here.
	var mu sync.Mutex
	var puts, deletes, refreshes int
	a.page.OnRequest(func(req playwright.Request) {
		u, err := url.Parse(req.URL())
		if err != nil || u.Path != "/api/default-i-settings" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch req.Method() {
		case http.MethodPut:
			puts++
		case http.MethodDelete:
			deletes++
		case http.MethodGet:
			refreshes++
		}
	})
	count := func(n *int) int {
		mu.Lock()
		defer mu.Unlock()
		return *n
	}
	a.start()

	// The suite builds on a clean instance: before anything is typed the
	// standard is the one that ships - the long I1 and no overrides.
	shipped := getJSON[api.ISettings](t, "/api/default-i-settings")
	if !strings.HasPrefix(shipped["i1"], shippedI1Prefix) {
		t.Fatalf("GET /api/default-i-settings i1 = %.40q..., want the shipped default before the test", shipped["i1"])
	}
	for _, key := range []string{"i2", "i3", "i4", "i5"} {
		if shipped[key] != "" {
			t.Fatalf("GET /api/default-i-settings %s = %q, want nothing saved before the test", key, shipped[key])
		}
	}

	// "Add client", then tick "Apply I-settings": the I1-I5 section and,
	// with it, the two buttons appear.
	a.click(addClientButtonX, cardActionsY)
	time.Sleep(1200 * time.Millisecond)
	a.click(applyICheckboxX, applyICheckboxY)
	time.Sleep(500 * time.Millisecond)
	a.shot("17-apply-i")

	// What the empty I1 field shows before anything is saved - the pixels
	// the reopened dialog must change below.
	before := clip(t, a, i1FieldRect())

	// 1. A valid custom I1, saved as the operator's standard.
	a.click(iFieldX, iFieldY)
	a.typeText("<b 0xaabb>")
	a.click(saveStandardX, saveStandardY)

	eventually(t, 30*time.Second, "the save never reached GET /api/default-i-settings", func() bool {
		return getJSON[api.ISettings](t, "/api/default-i-settings")["i1"] == "<b 0xaabb>"
	})
	wantStandard(t, "after the first save", api.ISettings{
		"i1": "<b 0xaabb>",
		"i2": shipped["i2"], "i3": shipped["i3"], "i4": shipped["i4"], "i5": shipped["i5"],
	})
	if n := count(&puts); n != 1 {
		t.Errorf("Save as my standard sent %d PUTs, want 1", n)
	}
	// The save re-reads the set from the backend - the cache the next
	// dialog opens with, and the source of its placeholders. One GET is the
	// page load; the save must add one of its own.
	if n := count(&refreshes); n < 2 {
		t.Errorf("after the save the page fetched the standard %d times, want at least 2 (page load + refresh)", n)
	}
	a.shot("18-i1-saved")

	// Close and reopen: the saved value is what the I1 field now offers as
	// its "server default: ..." placeholder. The text is canvas pixels, so
	// the two dialogs are compared where it is drawn.
	a.click(dialogCancelX, dialogCancelY)
	time.Sleep(500 * time.Millisecond)
	a.click(addClientButtonX, cardActionsY)
	time.Sleep(1200 * time.Millisecond)
	a.click(applyICheckboxX, applyICheckboxY)
	time.Sleep(500 * time.Millisecond)
	after := clip(t, a, i1FieldRect())
	if samePixels(before, after) {
		t.Error("the reopened dialog shows the I1 field as it was before the save, not the saved standard as its placeholder")
	}
	a.shot("19-placeholder")

	// 2. A partial save merges: only I2 is typed this time, and the standard
	// that comes back keeps the operator's I1 while the keys nobody typed
	// still answer with the shipped values.
	a.click(iFieldX, iFieldY+iFieldDY)
	a.typeText("<r 20>")
	a.click(saveStandardX, saveStandardY)

	eventually(t, 30*time.Second, "the second save never reached the backend", func() bool {
		got := getJSON[api.ISettings](t, "/api/default-i-settings")
		return got["i1"] == "<b 0xaabb>" && got["i2"] == "<r 20>"
	})
	wantStandard(t, "after the partial save", api.ISettings{
		"i1": "<b 0xaabb>", "i2": "<r 20>",
		"i3": shipped["i3"], "i4": shipped["i4"], "i5": shipped["i5"],
	})
	if n := count(&puts); n != 2 {
		t.Errorf("Save as my standard sent %d PUTs, want 2", n)
	}
	a.shot("20-i2-merged")

	// 3. With every field emptied, save is a warning rather than a write:
	// not one request leaves the browser and the set stands as it was.
	a.click(iFieldX, iFieldY)
	a.press("Control+a")
	a.press("Delete")
	a.click(iFieldX, iFieldY+iFieldDY)
	a.press("Control+a")
	a.press("Delete")
	saved := getJSON[api.ISettings](t, "/api/default-i-settings")
	putsBefore := count(&puts)

	a.click(saveStandardX, saveStandardY)
	time.Sleep(3 * time.Second)

	if n := count(&puts); n != putsBefore {
		t.Errorf("save with every field empty sent %d PUTs, want none", n-putsBefore)
	}
	wantStandard(t, "after the all-empty save", saved)
	a.shot("21-empty-guard")

	// 4. Restore is the full clear: confirm the dialog that appears, and
	// the saved standard is gone - the shipped values answer again.
	a.click(restoreStandardX, restoreStandardY)
	time.Sleep(1000 * time.Millisecond)
	a.shot("22-restore-confirm")
	a.click(confirmYesX, confirmYesY)

	eventually(t, 30*time.Second, "the restore never brought the shipped values back", func() bool {
		return getJSON[api.ISettings](t, "/api/default-i-settings")["i1"] == shipped["i1"]
	})
	wantShipped(t, "after the restore", getJSON[api.ISettings](t, "/api/default-i-settings"), shipped)
	if n := count(&deletes); n != 1 {
		t.Errorf("Restore shipped defaults sent %d DELETEs, want 1", n)
	}
	a.shot("23-restored")

	// Restoring again changes nothing: the shipped values are already what
	// stands, and the button keeps working rather than erroring.
	a.click(restoreStandardX, restoreStandardY)
	time.Sleep(1000 * time.Millisecond)
	a.click(confirmYesX, confirmYesY)

	eventually(t, 30*time.Second, "the second restore never completed", func() bool {
		return count(&deletes) == 2
	})
	wantShipped(t, "after the repeated restore", getJSON[api.ISettings](t, "/api/default-i-settings"), shipped)

	// Leave the dialog and the instance the way the next test expects them.
	a.click(dialogCancelX, dialogCancelY)
	time.Sleep(500 * time.Millisecond)
	a.shot("24-dialog-closed")
	wantShipped(t, "at the end of the test", getJSON[api.ISettings](t, "/api/default-i-settings"), shipped)

	a.noPageErrors()
}

// i1FieldRect is the patch of canvas where the I1 field draws its text: the
// caption above it and the buttons below it stay out, so the two clips of two
// dialog instances differ only when the placeholder does.
func i1FieldRect() playwright.Rect {
	return playwright.Rect{X: iFieldLeft, Y: iFieldY - 12, Width: 320, Height: 26}
}

// clip screenshots one rectangle of the page.
func clip(t *testing.T, a *app, r playwright.Rect) image.Image {
	t.Helper()
	raw, err := a.page.Screenshot(playwright.PageScreenshotOptions{Clip: &r})
	must(t, err)
	img, _, err := image.Decode(bytes.NewReader(raw))
	must(t, err)
	return img
}

// samePixels reports whether two clips of the same rectangle are identical.
func samePixels(a, b image.Image) bool {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return false
	}
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			if a.At(ab.Min.X+x, ab.Min.Y+y) != b.At(bb.Min.X+x, bb.Min.Y+y) {
				return false
			}
		}
	}
	return true
}

// wantStandard fails unless every key of the effective set matches exactly -
// the shape a partial save must leave behind.
func wantStandard(t *testing.T, what string, want api.ISettings) {
	t.Helper()
	got := getJSON[api.ISettings](t, "/api/default-i-settings")
	for _, key := range []string{"i1", "i2", "i3", "i4", "i5"} {
		if got[key] != want[key] {
			t.Errorf("%s: GET /api/default-i-settings %s = %q, want %q", what, key, got[key], want[key])
		}
	}
}

// wantShipped fails unless the effective set is exactly the one the test
// started from.
func wantShipped(t *testing.T, what string, got, shipped api.ISettings) {
	t.Helper()
	for _, key := range []string{"i1", "i2", "i3", "i4", "i5"} {
		if got[key] != shipped[key] {
			t.Errorf("%s: GET /api/default-i-settings %s = %q, want the shipped %q", what, key, got[key], shipped[key])
		}
	}
}
