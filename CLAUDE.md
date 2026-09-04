## Backend
- Go 1.26
- go-fiber 3

Placement of backend files in `internal` directory.

## Frontend
- Fyne v2.8 compiled to WebAssembly (GOOS=js GOARCH=wasm)
- Own Go module in `web-ui`, built with `go tool fyne package -os wasm`

Placement of frontend files in `web-ui` directory: `main.go` is the entry
point and everything else lives in the `web-ui/internal/ui` package, which
exports only `New`, `NewDarkTheme` and the `UI` type's `Build`/`Start`. Wire
types shared with the backend live in `web-ui/api` and are pulled into the
root module via a `replace` directive - add new request/response structs
there, never twice.

Build the bundle with `make web-ui`; it lands in `web-ui/wasm`, which the
server serves straight off disk under the relative path `./web-ui/wasm`

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
coordinate and assert against the REST API. They need a running instance -
see `e2e/README.md` - and are run with:
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

The Playwright suite does this properly through `httpCredentials` in
`e2e/playwright.config.js`.
