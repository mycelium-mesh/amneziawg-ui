# Browser tests

End-to-end tests that drive the real WebAssembly UI in Chrome through
[playwright-go](https://github.com/mxschmitt/playwright-go) and assert against
the backend's REST API.

The frontend renders into a single `<canvas>`, so there is nothing to select
by CSS: the tests click by coordinate (at a fixed 1500x950 viewport) and check
the result through `/api/...`, decoding the responses into the shared wire
structs of `web-ui/api`. Screenshots of every step land in `shots/`.

`startApp` collapses the monitoring panel at the top of the page before a
test gets to click: open, it would push the form and the cards below the
fold. Every coordinate in the tests assumes that collapsed 56px row above the
"Create New VPN Server" button; dialogs are centred in the window and do not
move either way.

## Layout

This directory is its own Go module, so playwright-go stays out of the root
`go.mod`; `web-ui` comes in through a `replace` directive, as it does for the
backend.

- `main_test.go` - `TestMain` installs the Playwright driver and launches
  Chrome once; the helpers give every test a fresh browser context with the
  credentials and viewport, and wrap clicks, typing, screenshots and the REST
  calls
- `01_smoke_test.go` ... `06_port_test.go` - one test each

## Running

From the repository root:

```sh
make e2e
```

That is the whole thing. `make e2e` depends on `make e2e-reset`, which
recreates the backend from scratch before every run: it removes the
`awgui-test` container and its `awgui-test-data` volume, rebuilds the image
from the working tree, starts it on port 51836 and waits for `/status` to
answer.

The reset is why the suite gets its own container, volume and port rather than
the stack `make run` leaves behind: the tests share one backend and build on
each other's state - `02` creates the server that `03` adds a client to - so
they run in file order, one at a time (never `t.Parallel`), and expect to
start against an instance with no servers configured. Wiping state before a
test run must never discard the servers you are working on.

The instance stays up after the run, so a failed test can be inspected in the
browser at <http://localhost:51836> (admin / changeme). `make e2e-down`
removes the container and the volume.

Override any of it with the Makefile variables `E2E_PORT`, `E2E_NAME`,
`E2E_VOLUME`, `E2E_IMAGE`, `E2E_URL`, `E2E_TIMEOUT`, `E2E_TEST_TIMEOUT`.

To run the tests against a backend you started yourself, skip the Makefile:

```sh
cd e2e
AWG_URL=http://localhost:54845 go test -count=1 -v .
```

`AWG_URL` defaults to `http://localhost:51836`. Whatever it points at needs
`WEB_UI_USER=admin` and `WEB_UI_PASSWORD=BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY=`
- the base64 SHA-256 of `changeme`, which is what the tests log in with.

No Node.js is needed: on the first run `TestMain` downloads the Playwright
driver (with its own Node) into `~/.cache/ms-playwright-go`. Browsers are not
downloaded - the tests reuse the Chrome installed on the machine
(`Channel: "chrome"`); it needs WebGL, which the app requires.
