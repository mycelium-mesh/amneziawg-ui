package e2e

import (
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"amneziawg-web-ui/web-ui/api"
	"github.com/mxschmitt/playwright-go"
)

// The form knows which ports are already spoken for - every existing server's,
// plus the panel's own - and refuses one before it reaches the backend.
func TestTakenPortRejected(t *testing.T) {
	a := startApp(t)

	before := getJSON[[]api.Server](t, "/api/servers")
	if len(before) == 0 {
		t.Fatal("TestCreateServer must run first")
	}
	taken := before[0].Port

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
	a.typeText("Duplicate Port")

	// Replace whatever free port the form offered with one that is not.
	a.click(1100, 205)
	a.press("Control+a")
	a.press("Backspace")
	a.typeText(strconv.Itoa(taken))
	a.shot("08-port-taken")

	a.click(87, 323)
	time.Sleep(3 * time.Second)
	a.shot("09-port-rejected")

	if n := posts.Load(); n != 0 {
		t.Errorf("the request must not leave the browser at all, got %d POSTs", n)
	}
	if after := getJSON[[]api.Server](t, "/api/servers"); len(after) != len(before) {
		t.Errorf("no server should have been created: %d servers, was %d", len(after), len(before))
	}
	a.noPageErrors()
}
