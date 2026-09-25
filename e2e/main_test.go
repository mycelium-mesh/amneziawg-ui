// Package e2e drives the real WebAssembly UI in Chrome through playwright-go
// and asserts against the backend's REST API.
//
// The UI is a single Fyne canvas: nothing in it can be selected by CSS, so
// the tests click by coordinate at a fixed viewport and check the result
// through /api/.... The tests share one backend and build on each other's
// state, so they run in file order, one at a time - never add t.Parallel.
package e2e

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const (
	user     = "admin"
	password = "changeme"

	// Every coordinate in the tests assumes this viewport.
	viewportWidth  = 1500
	viewportHeight = 950

	// How long the loader page may take to fetch and start bundle.wasm.
	bootTimeout = 120 * time.Second
)

var (
	baseURL string
	browser playwright.Browser
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	baseURL = strings.TrimRight(cmp.Or(os.Getenv("AWG_URL"), "http://localhost:51836"), "/")

	// Only the driver: the tests reuse the Chrome installed on the machine
	// instead of Playwright's own download; the app needs WebGL either way.
	if err := playwright.Install(&playwright.RunOptions{SkipInstallBrowsers: true}); err != nil {
		fmt.Fprintln(os.Stderr, "install the playwright driver:", err)
		return 1
	}
	pw, err := playwright.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start playwright:", err)
		return 1
	}
	defer pw.Stop()

	browser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Channel: new("chrome"),
		Args:    []string{"--enable-unsafe-swiftshader"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "launch chrome:", err)
		return 1
	}
	defer browser.Close()

	if err := os.MkdirAll("shots", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// app is one test's browser page with the UI loaded in it.
type app struct {
	t    *testing.T
	page playwright.Page

	mu     sync.Mutex
	errors []string
}

// newApp opens a fresh browser context - its own cookies, credentials and
// viewport, as Playwright Test gives every test - without loading the UI yet,
// so a test can hook page events before the first request goes out.
func newApp(t *testing.T) *app {
	t.Helper()
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		BaseURL:         new(baseURL),
		HttpCredentials: &playwright.HttpCredentials{Username: user, Password: password},
		Viewport:        &playwright.Size{Width: viewportWidth, Height: viewportHeight},
	})
	must(t, err)
	t.Cleanup(func() { _ = ctx.Close() })

	page, err := ctx.NewPage()
	must(t, err)

	a := &app{t: t, page: page}
	page.OnPageError(func(err error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.errors = append(a.errors, err.Error())
	})
	return a
}

// startApp opens a page and loads the UI in it.
func startApp(t *testing.T) *app {
	t.Helper()
	a := newApp(t)
	a.start()
	return a
}

// start loads the page and waits until the Fyne canvas is up and the app has
// fetched its first data from the backend.
func (a *app) start() {
	a.t.Helper()
	_, err := a.page.Goto("/", playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded})
	must(a.t, err)
	must(a.t, a.page.Locator("canvas").WaitFor(playwright.LocatorWaitForOptions{
		Timeout: new(float64(bootTimeout.Milliseconds())),
	}))
	// "fyne package" generates the loader page: it hides #main as soon as
	// Fyne puts its canvas up.
	_, err = a.page.WaitForFunction(`() => document.getElementById('main')?.style.display === 'none'`, nil,
		playwright.PageWaitForFunctionOptions{Timeout: new(float64(bootTimeout.Milliseconds()))})
	must(a.t, err)
	time.Sleep(3 * time.Second)

	// Fold the monitoring panel away. It opens by default and is a quarter of
	// the viewport tall, which would push the form and the cards the tests
	// click on below the fold; collapsed, it is one 56px row above them.
	a.click(87, 87)
}

// click moves the pointer first: the Fyne web driver taps wherever its last
// known cursor position is, so a bare click would land in the wrong place.
func (a *app) click(x, y float64) {
	a.t.Helper()
	mouse := a.page.Mouse()
	must(a.t, mouse.Move(x, y))
	time.Sleep(150 * time.Millisecond)
	must(a.t, mouse.Click(x, y))
	time.Sleep(600 * time.Millisecond)
}

func (a *app) typeText(text string) {
	a.t.Helper()
	must(a.t, a.page.Keyboard().Type(text, playwright.KeyboardTypeOptions{Delay: new(25.0)}))
	time.Sleep(300 * time.Millisecond)
}

func (a *app) press(key string) {
	a.t.Helper()
	must(a.t, a.page.Keyboard().Press(key))
}

func (a *app) scroll(x, y, delta float64) {
	a.t.Helper()
	mouse := a.page.Mouse()
	must(a.t, mouse.Move(x, y))
	must(a.t, mouse.Wheel(0, delta))
	time.Sleep(800 * time.Millisecond)
}

// shot saves a screenshot of the page to shots/<name>.png.
func (a *app) shot(name string) {
	a.t.Helper()
	_, err := a.page.Screenshot(playwright.PageScreenshotOptions{Path: new("shots/" + name + ".png")})
	must(a.t, err)
}

// noPageErrors fails the test if the page threw anything uncaught - a Go
// panic in the wasm bundle surfaces this way.
func (a *app) noPageErrors() {
	a.t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.errors) > 0 {
		a.t.Errorf("uncaught page errors:\n%s", strings.Join(a.errors, "\n"))
	}
}

// apiGet returns the body of a GET to the backend, with the same credentials
// the browser uses, failing the test on anything but 200.
func apiGet(t *testing.T, path string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	must(t, err)
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	must(t, err)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s -> %d: %s", path, resp.StatusCode, body)
	}
	return body
}

// getJSON decodes the JSON the backend returns for path into a T.
func getJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(apiGet(t, path), &v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return v
}

// eventually retries cond once a second until it holds, and fails the test
// with what if it still does not after timeout.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %s", timeout, what)
		}
		time.Sleep(time.Second)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
