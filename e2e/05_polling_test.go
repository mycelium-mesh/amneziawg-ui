package e2e

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// The counters on the cards come from a polling loop, not from an event feed:
// if that loop ever dies, the page keeps showing stale numbers with nothing
// to tell it apart from a quiet server. Make sure it keeps ticking.
func TestTrafficPolling(t *testing.T) {
	a := newApp(t)
	var polls atomic.Int32
	a.page.OnRequest(func(req playwright.Request) {
		if strings.HasSuffix(req.URL(), "/api/traffic") {
			polls.Add(1)
		}
	})

	a.start()
	time.Sleep(12 * time.Second)

	if n := polls.Load(); n < 2 {
		t.Errorf("traffic polling stopped: %d requests to /api/traffic", n)
	}
}
