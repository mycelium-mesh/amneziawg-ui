# Browser tests

End-to-end tests that drive the real WebAssembly UI in Chrome with Playwright
and assert against the backend's REST API.

The frontend renders into a single `<canvas>`, so there is nothing to select
by CSS: the specs click by coordinate (at a fixed 1500x950 viewport) and check
the result through `/api/...`. Screenshots of every step land in `shots/`.

## Running

Start a backend first - the container is the closest thing to production:

```sh
docker build -t awgui-test .
docker run -d --name awgui-test -p 51836:80 \
  -e WEB_UI_PORT=80 -e WEB_UI_USER=admin \
  -e 'WEB_UI_PASSWORD=BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY=' \
  --cap-add NET_ADMIN --cap-add SYS_MODULE --device /dev/net/tun \
  --sysctl net.ipv4.ip_forward=1 --sysctl net.ipv4.conf.all.src_valid_mark=1 \
  awgui-test
```

That password is the SHA-256 of `changeme`, which is what the specs log in
with. Then:

```sh
cd e2e
npm install
npx playwright test           # or: make e2e, from the repository root
```

`AWG_URL` points the suite somewhere else (default `http://localhost:51836`).

The specs share one backend and build on each other's state - `02` creates the
server that `03` adds a client to - so they run in file order with a single
worker, and expect to start against an instance with no servers configured.

The config reuses the Chrome installed on the machine (`channel: 'chrome'`)
rather than Playwright's own download; either way the browser needs WebGL,
which the app requires.
