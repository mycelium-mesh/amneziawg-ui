# ── Application ───────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git make gcc musl-dev linux-headers

WORKDIR /build
COPY go.mod go.sum ./
COPY web-ui/go.mod web-ui/go.sum ./web-ui/
RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    go mod download && cd web-ui && go mod download

COPY . .

# The frontend is a Fyne application compiled to WebAssembly. "fyne package"
# emits the loader page, its stylesheets and the bundle straight into
# web-ui/wasm.
#
# The bundle then ships gzipped and nothing else: 14 MB of layer instead of 49,
# and the server hands the file over as it is rather than compressing 49 MB
# again on every cold request (see packedAssets in main.go).
RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    cd web-ui && go tool fyne package -os wasm --name bundle --release \
    && gzip -9 wasm/bundle.wasm

RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/api .

# ── AmneziaWG upstream ───────────────────────────────────────────
FROM golang:1.26-alpine AS awg

RUN apk add --no-cache git make gcc musl-dev linux-headers

WORKDIR /build

# Pinned to explicit release tags so a rebuild can never silently pull a newer
# (or broken) master: bump these deliberately and rebuild. Both must be from
# the same AmneziaWG generation - the tools only know how to serialize the UAPI
# keys the matching engine understands.
#   amneziawg-go    https://github.com/amnezia-vpn/amneziawg-go/tags
#   amneziawg-tools https://github.com/amnezia-vpn/amneziawg-tools/tags
ARG AWG_GO_VERSION=v3.1.20260828
ARG AWG_TOOLS_VERSION=v3.1.20260812

# Upstream builds with a plain "go build": patch it so the binary is stripped
# of its symbol table and of the absolute build paths, like /app/api above.
RUN --mount=type=cache,id=awg_mod,target=/go/pkg/mod \
    --mount=type=cache,id=awg_build,target=/root/.cache/go-build \
    git clone --depth 1 --branch "$AWG_GO_VERSION" https://github.com/amnezia-vpn/amneziawg-go.git \
    && cd amneziawg-go \
    && sed -i 's|go build |go build -trimpath -ldflags="-s -w" |' Makefile \
    && grep -q -- '-trimpath' Makefile \
    && make && make install

RUN git clone --depth 1 --branch "$AWG_TOOLS_VERSION" https://github.com/amnezia-vpn/amneziawg-tools.git \
    && cd amneziawg-tools/src && make && make WITH_WGQUICK=yes install

# ── Final image ───────────────────────────────────────────────────────────────
FROM alpine:3.24.1

# awg-quick is a bash script and shells out to ip, iptables and resolvconf;
# "iproute2-minimal" is the ip binary alone - tc, ss and friends from the full
# package are never called. The health check runs on busybox wget, so curl and
# the ~4.7 MB of libcurl/brotli/libunistring behind it stay out of the image.
RUN apk add --no-cache \
    iptables \
    iptables-legacy \
    bash \
    iproute2-minimal \
    openresolv \
    tini

RUN mkdir -p /var/log/amnezia /etc/amnezia/amneziawg

COPY --from=awg /usr/bin/amneziawg-go /usr/bin/proxy
COPY --from=awg /usr/bin/awg /usr/bin/awg
COPY --from=awg /usr/bin/awg-quick /usr/bin/awg-quick
COPY --from=builder /app/api /usr/bin/api
COPY --from=builder /build/web-ui/wasm/ /app/web-ui/wasm/

COPY scripts/ /app/scripts/
RUN chmod +x /app/scripts/*.sh

# The api binary resolves ./web-ui/wasm from here.
WORKDIR /app

ENV WEB_UI_PORT=54845
# awg-quick launches the userspace WireGuard implementation via this env var
# (defaults to "amneziawg-go"); renaming the binary to "proxy" and pointing
# awg-quick at it hides "amneziawg-go" from `ps aux` output.
ENV WG_QUICK_USERSPACE_IMPLEMENTATION=proxy

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:$WEB_UI_PORT/status || exit 1

# tini becomes the real PID 1 in the container: it reaps any orphaned
# grandchild processes (e.g. leftover from awg-quick internals) that would
# otherwise pile up as zombies, since "api" itself only reaps its own
# direct exec.Command children, not reparented orphans.
ENTRYPOINT ["/sbin/tini", "--", "/app/scripts/start.sh"]
