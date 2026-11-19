package e2e

import (
	"bytes"
	"encoding/json"
	"image"
	_ "image/png"
	"io"
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

	// The first client row's Edit button, and the edit dialog's own
	// coordinates - measured off screenshots: its form carries two more
	// rows than the add dialog's, so the I-section sits behind the
	// dialog's scrollbar instead of under the fold.
	editClientX = 1147
	editClientY = 341

	// Dragging that scrollbar's thumb (four pixels wide at the content's
	// right edge) from well below its top to well past its travel lands
	// the content at the bottom on every run - the wheel cannot: fyne
	// routes it to the page scroll behind the overlay first.
	editScrollX     = 1112
	editScrollFromY = 300
	editScrollToY   = 580

	// I1 and "Save as my standard" where they stand once scrolled there.
	editIFieldX       = 750
	editIFieldY       = 305
	editSaveStandardX = 477
	editSaveStandardY = 576

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

// TestClientDialogStandardISettings drives the two buttons the client
// dialog gives the operator over their own standard I1-I5 set: save writes
// only what the dialog's fields gained since it opened - everything typed
// in the add dialog, only what the operator touched in the edit dialog -
// and restore is the full clear back to the shipped values. The canvas has
// no readable text, so the contract is asserted through the REST effect the
// buttons must produce, and the one thing the REST cannot show - the
// reopened dialog offering the saved value as the field's placeholder -
// through the pixels of that field.
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

	// The edit-dialog case below needs a client whose own I-settings
	// prefill the dialog with values the standard does not hold. It is
	// written through the REST API before the page loads, so the row
	// renders with the stored values already - the case under test is the
	// dialog's button, not this setup.
	servers := getJSON[[]api.Server](t, "/api/servers")
	if len(servers) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	clientsPath := "/api/servers/" + servers[0].ID + "/clients"
	clients := getJSON[[]api.Client](t, clientsPath)
	if len(clients) == 0 {
		t.Fatal("TestAddClient must run first")
	}
	clientISettings := api.ISettings{"i1": "<b 0xfeed>", "i2": "<r 77>", "i5": "<r 90>"}
	apply := true
	setup, err := json.Marshal(api.UpdateISettingsRequest{ApplyISettings: &apply, ISettings: clientISettings})
	if err != nil {
		t.Fatal(err)
	}
	apiPut(t, clientsPath+"/"+clients[0].ID+"/i-settings", string(setup))
	stored := getJSON[[]api.Client](t, clientsPath)
	if stored[0].ISettings["i5"] != clientISettings["i5"] {
		t.Fatalf("the client never stored its own I-settings: i5 = %q, want %q",
			stored[0].ISettings["i5"], clientISettings["i5"])
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
	// The request event reaches this counter asynchronously, so wait for it
	// to arrive and then check for exactly one: no further save is clicked
	// before the next count, so anything above one is a duplicate.
	eventually(t, 30*time.Second, "the save never left the browser as a PUT", func() bool {
		return count(&puts) >= 1
	})
	if n := count(&puts); n != 1 {
		t.Errorf("Save as my standard sent %d PUTs, want 1", n)
	}
	// The save re-reads the set from the backend - the cache the next
	// dialog opens with, and the source of its placeholders. One GET is the
	// page load; the save must add one of its own. That refresh only starts
	// once the PUT comes back, so its request is waited for the same way: a
	// single read here races the browser under load, while a refresh that
	// never happens still fails the wait below.
	eventually(t, 30*time.Second, "after the save the page never re-read the standard", func() bool {
		return count(&refreshes) >= 2
	})
	a.shot("18-i1-saved")

	// Close and reopen: the saved value is what the I1 field now offers as
	// its "standard: ..." placeholder. The text is canvas pixels, so
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
	// Wait for the second request event as well, then insist on exactly two
	// - and settle the counter before the all-empty guard snapshots it.
	eventually(t, 30*time.Second, "the second save never left the browser as a PUT", func() bool {
		return count(&puts) >= 2
	})
	if n := count(&puts); n != 2 {
		t.Errorf("Save as my standard sent %d PUTs, want 2", n)
	}
	a.shot("20-i2-merged")

	// 3. The edit dialog saves only what changed in it: its I-fields open
	// prefilled from the client's own record, so "Save as my standard"
	// after touching a single field must write that one key - every value
	// the operator never touched keeps its state in the standard, and the
	// client itself is not touched at all. The client got its stored
	// values from the setup at the top of the test.
	//
	// The dialog carries a scrollbar of its own: the wheel cannot reach
	// it (fyne routes the event to the page behind the overlay), so the
	// thumb is dragged past its travel to land at the bottom every run.
	a.click(dialogCancelX, dialogCancelY)
	time.Sleep(500 * time.Millisecond)
	a.click(editClientX, editClientY)
	time.Sleep(1500 * time.Millisecond)
	a.shot("25-edit-dialog")

	mouse := a.page.Mouse()
	must(t, mouse.Move(editScrollX, editScrollFromY))
	must(t, mouse.Down())
	for y := float64(editScrollFromY) + 20; y <= float64(editScrollToY); y += 10 {
		must(t, mouse.Move(editScrollX, y))
		time.Sleep(30 * time.Millisecond)
	}
	must(t, mouse.Up())
	time.Sleep(800 * time.Millisecond)
	a.shot("26-edit-scrolled")

	// Change exactly one field: I1, which opened as the client's own
	// value.
	a.click(editIFieldX, editIFieldY)
	a.press("Control+a")
	a.typeText("<b 0xdead>")

	a.click(editSaveStandardX, editSaveStandardY)

	// Only i1 moves: i2 keeps "<r 20>" from the partial save above (the
	// client's "<r 77>" must not leak in) and i5 stays empty (the
	// client's "<r 90>" must not appear).
	eventually(t, 30*time.Second, "the edit-dialog save never reached the backend", func() bool {
		return getJSON[api.ISettings](t, "/api/default-i-settings")["i1"] == "<b 0xdead>"
	})
	wantStandard(t, "after the edit-dialog save", api.ISettings{
		"i1": "<b 0xdead>", "i2": "<r 20>",
		"i3": shipped["i3"], "i4": shipped["i4"], "i5": shipped["i5"],
	})
	// The same wait-then-exact counter: this save is the test's third
	// PUT, and no further save is clicked before the next count.
	eventually(t, 30*time.Second, "the edit-dialog save never left the browser as a PUT", func() bool {
		return count(&puts) >= 3
	})
	if n := count(&puts); n != 3 {
		t.Errorf("Save as my standard from the edit dialog sent %d PUTs, want 3 in total", n)
	}

	// The standard's save never writes back into the client it was
	// opened from.
	editedClient := getJSON[[]api.Client](t, clientsPath)
	for _, key := range []string{"i1", "i2", "i3", "i4", "i5"} {
		if editedClient[0].ISettings[key] != clientISettings[key] {
			t.Errorf("the client's i_settings %s = %q, want the stored %q",
				key, editedClient[0].ISettings[key], clientISettings[key])
		}
	}
	if !editedClient[0].ApplyISettings {
		t.Error("the client's apply_i_settings flipped; Save as my standard must not modify the client")
	}
	a.shot("27-edit-saved")

	// Back to the add dialog for the guard below - the edit case closed
	// it - with the I-section ticked open as before.
	a.click(dialogCancelX, dialogCancelY)
	time.Sleep(500 * time.Millisecond)
	a.click(addClientButtonX, cardActionsY)
	time.Sleep(1200 * time.Millisecond)
	a.click(applyICheckboxX, applyICheckboxY)
	time.Sleep(500 * time.Millisecond)

	// 4. With every field emptied, save is a warning rather than a write:
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

	// 5. Restore is the full clear: confirm the dialog that appears, and
	// the saved standard is gone - the shipped values answer again.
	a.click(restoreStandardX, restoreStandardY)
	time.Sleep(1000 * time.Millisecond)
	a.shot("22-restore-confirm")
	a.click(confirmYesX, confirmYesY)

	eventually(t, 30*time.Second, "the restore never brought the shipped values back", func() bool {
		return getJSON[api.ISettings](t, "/api/default-i-settings")["i1"] == shipped["i1"]
	})
	wantShipped(t, "after the restore", getJSON[api.ISettings](t, "/api/default-i-settings"), shipped)
	// Same asynchronous counter: wait for the DELETE to be observed, then
	// insist there was exactly one.
	eventually(t, 30*time.Second, "the restore never left the browser as a DELETE", func() bool {
		return count(&deletes) >= 1
	})
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

// apiPut sends a JSON body to the backend with the suite's credentials,
// failing the test on anything but 200 - the setup writes that go around
// the browser, since the cases under test are the dialogs' own buttons.
func apiPut(t *testing.T, path, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+path, strings.NewReader(body))
	must(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s -> %d: %s", path, resp.StatusCode, b)
	}
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
