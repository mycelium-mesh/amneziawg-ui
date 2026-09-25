## Backend
- Go 1.26
- go-fiber 3

Backend packages live under `internal`, one responsibility each, with a
one-way dependency graph `httpapi → manager → {store, awg, wgconf,
amnezialink, publicip, config, sysinfo}`:

- `httpapi` — REST handlers; no `os`/`exec`, everything goes through the
  exported `manager` API and errors map to status codes in `fail`
- `manager` — the state and the operations on it; the only package that
  takes the config lock. Split by file (`servers.go`, `clients.go`,
  `suspend.go`, `status.go`, `traffic.go`, `system.go`)
- `awg` — the only package that shells out, behind the `Runner` interface;
  tests use `awg/awgtest` instead of the host
- `sysinfo` — CPU/RAM/disk readings of the host via `gopsutil`, carried in
  `/api/traffic` as instantaneous, cumulative figures; the dashboard keeps the
  history and computes rates, the backend stores nothing between polls
- `wgconf` — `.conf` text: rendering, peer markers, suspended blocks
- `amnezialink` — `vpn://` export; `store` — `web_config.json` and schema
  migrations; `config` — env → `Settings`; `frontend` — static assets

## Frontend
- Fyne v2.8 compiled to WebAssembly (GOOS=js GOARCH=wasm)
- Own Go module in `web-ui`, built with `go tool fyne package -os wasm`

Placement of frontend files in `web-ui` directory: `main.go` is the entry
point and the UI lives in the `web-ui/internal/ui`; the page top to bottom is
`dashboard` (charts), `newserver` (create form), `serverlist` (cards)

`web-ui/api` is what the two sides share, pulled into the root module via a
`replace` directive: the wire structs, and the AmneziaWG rules that go with
them - limits, `Validate`, `ValidateCPS`, `GenerateObfuscation`. Anything both
the create form and the backend need goes there, never twice; a rule stated in
two places is a rule that drifts, and the user sees it as "the form let me,
the server did not". The backend's `validateObfuscationParams` and
`validateISettings` are thin wrappers that turn the shared problem lists into
one error.

The one other frontend package is `web-ui/internal/fixes`: workarounds for
upstream bugs in fyne and its wasm driver, one per file, each documenting the
bug and when it can be dropped. `main.go` calls `fixes.Install()` before the
window starts; keep such patches out of the UI code.

Build the bundle with `make web-ui`; it lands in `web-ui/wasm`, which the
server serves straight off disk under the relative path `./web-ui/wasm`

## Frontend translations

The UI is in English and Russian, through Fyne's `lang` package
(https://docs.fyne.io/explore/translations/). `web-ui/translation/{en,ru}.json`
are embedded by `main.go` and loaded with `lang.AddTranslationsFS`; the
language comes from the browser's `navigator.languages`, `en` is the fallback.

# Running the project, and check docker build

In the project root directory, run command:
```sh
  make run
```

# Linting the project (after changes backend code)
In the project root directory, run command:
```sh
  make vet
```

# Building the project
In the project root directory, run command:
```sh
  make build
```

# Browser tests
The frontend is one WebAssembly canvas, so the browser tests in `e2e` click by
coordinate and assert against the REST API. They are Go tests on
[playwright-go](https://github.com/mxschmitt/playwright-go) in their own
module (`e2e/go.mod`). They expect an instance with no servers configured and build state across the
tests, so `make e2e` provisions one itself: it recreates the `awgui-test`
container from an empty volume on port 51836 first (see `e2e/README.md`).
That instance is deliberately separate from the stack `make run` leaves
behind - the reset must never discard the servers you are working on. `make e2e-down` removes it.
```sh
  make e2e
```

For manual testing possible to use `playwright-cli`

# Authentication

Everything except the `/status` health check is behind HTTP basic auth
(`WEB_UI_USER`, default `admin`;), including the frontend
assets. `WEB_UI_PASSWORD` holds the **base64 of the SHA-256 of the password**. default `changeme`

```sh
  make run
```

For the API, `curl -u admin:changeme` is enough. In the browser, credentials
in the URL are **not**: Chrome refuses `fetch()` on a URL that carries them,
so `http://admin:changeme@host/` loads the loader page and then fails on
`bundle.wasm`. Authenticate on another path first and let the browser reuse
the credentials for the realm:

```sh
  playwright-cli goto "http://admin:changeme@localhost:51836/api/system/status"
  playwright-cli goto "http://localhost:51836/"
```

The e2e suite does this properly through `HttpCredentials` on the browser
context in `e2e/main_test.go`.

# Changelog

`./CHANGELOG.md`
