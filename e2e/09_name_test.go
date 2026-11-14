package e2e

import (
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"amneziawg-web-ui/web-ui/api"
)

// A name that cannot be a file name is refused before the request leaves the
// browser: the characters a desktop refuses are named under the field, and a
// name carrying one must not produce a server at all. The check is the same
// one the form's port test makes - no POST, no server.
func TestCreateServerRefusesANameThatCannotBeAFileName(t *testing.T) {
	a := startApp(t)

	before := getJSON[[]api.Server](t, "/api/servers")

	var posts atomic.Int32
	a.page.OnRequest(func(req playwright.Request) {
		if req.Method() != "POST" {
			return
		}
		if u, err := url.Parse(req.URL()); err == nil && u.Path == "/api/servers" {
			posts.Add(1)
		}
	})

	a.click(130, 143)

	a.click(380, 205)
	a.typeText("Bad:Name")

	a.click(87, 364)
	time.Sleep(3 * time.Second)
	a.shot("15-name-refused")

	if n := posts.Load(); n != 0 {
		t.Errorf("the request must not leave the browser at all, got %d POSTs", n)
	}
	if after := getJSON[[]api.Server](t, "/api/servers"); len(after) != len(before) {
		t.Errorf("no server should have been created: %d servers, was %d", len(after), len(before))
	}

	// A name every desktop accepts goes through on the same form. The
	// error line above the button is still there, so the button sits
	// lower than it does before any refusal.
	a.click(380, 205)
	a.press("Control+a")
	a.press("Backspace")
	a.typeText("Good Name")
	a.click(87, 404)
	a.shot("16-name-accepted")

	if n := posts.Load(); n != 1 {
		t.Errorf("a usable name should have created the server: %d POSTs", n)
	}
	if after := getJSON[[]api.Server](t, "/api/servers"); len(after) != len(before)+1 {
		t.Errorf("the usable name created no server: %d servers, was %d", len(after), len(before))
	}

	a.noPageErrors()
}
