# Setting SHELL to bash allows bash commands to be executed in recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits with a non-zero status, or if a command in a pipeline fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

##@ General

# The help target prints all targets with their descriptions organized
# by category. Categories are represented by '##@' and target descriptions by '##'.
# The awk command is responsible for reading all makefiles included in this
# invocation, looking for lines of the form xyz: ## something, and then
# pretty-printing the target and help. If there is a line with ##@ something,
# it's pretty-printed as a category.
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php
# More info on ANSI escape codes for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters

.PHONY: help
help: ## List all available commands
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Frontend

# Name of the WebAssembly bundle; the generated loader page fetches exactly
# this file name.
WASM_NAME = bundle

# Reproducible, stripped builds. "fyne package --release" already applies the
# same two to the wasm bundle itself, so only the server needs them spelled
# out here.
GO_BUILD_FLAGS = -trimpath -ldflags="-s -w"

# Where "fyne package" writes its output: the loader page, its light and dark
# stylesheets, the spinners, wasm_exec.js and the bundle itself. Everything in there is
# generated, and gitignored.
WASM_DIR = web-ui/wasm

.PHONY: web-ui
web-ui: check-go web-ui-version web-ui-clean ## Build the Fyne WebAssembly frontend into web-ui/wasm/
	cd web-ui && go tool fyne package -os wasm --name $(WASM_NAME) --release

# The header shows the release the bundle was built from, and "fyne package"
# reads that release out of FyneApp.toml (bumping Build there itself on every
# run). This keeps the Version line in step with the newest git tag rather
# than leaving it to be remembered by hand. The image resolves the tag the
# same way, inline in its frontend stage, so the two builds agree without the
# Dockerfile having to call make.
.PHONY: web-ui-version
web-ui-version: ## Sync Version in web-ui/FyneApp.toml with the newest git tag
	@tag=$$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//'); \
	current=$$(sed -n 's/^Version = "\(.*\)"/\1/p' web-ui/FyneApp.toml); \
	if [ -z "$$tag" ]; then \
		echo "  no git tag found - web-ui/FyneApp.toml keeps Version $$current"; \
	elif [ "$$tag" != "$$current" ]; then \
		sed -i "s/^Version = .*/Version = \"$$tag\"/" web-ui/FyneApp.toml; \
		echo "  web-ui/FyneApp.toml: Version $$current -> $$tag"; \
	fi

.PHONY: web-ui-clean
web-ui-clean: ## Remove the built frontend bundle
	rm -rf $(WASM_DIR)
	mkdir -p $(WASM_DIR)

##@ Backend

.PHONY: run
run: ## Build image from sources and run via docker-compose
	docker compose -f docker-compose.yml -f docker-compose.build.yml up --build

.PHONY: build
build: web-ui ## Build the frontend bundle and the server binary that serves it
	go build $(GO_BUILD_FLAGS) -o server.bin .

.PHONY: build-server
build-server: check-go ## Build only the server; it serves the bundle already in web-ui/wasm
	go build $(GO_BUILD_FLAGS) -o server.bin .

.PHONY: exec
exec: ## Execute a command inside the container
	docker compose exec app sh

##@ Browser tests

# The playwright-go suite in e2e/ (its own Go module) drives the real
# WebAssembly UI and asserts against the REST API. It expects an instance with
# no servers configured and builds state across the tests (02 creates the
# server 03 adds a client to), so it needs a clean backend on every run.
#
# That backend is deliberately its own container, volume and port rather than
# the stack "make run" leaves behind: wiping the state before a test run must
# never throw away the servers you are working on.
E2E_IMAGE  ?= awgui-test
E2E_NAME   ?= awgui-test
E2E_VOLUME ?= awgui-test-data
E2E_PORT   ?= 51836
E2E_URL    ?= http://localhost:$(E2E_PORT)
# base64 of the SHA-256 of "changeme" - the password the tests log in with.
E2E_PASSWORD ?= BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY=
# How long to wait for the fresh container to answer, in seconds.
E2E_TIMEOUT ?= 120
# The ceiling for the whole suite, as go test -timeout takes it.
E2E_TEST_TIMEOUT ?= 20m

.PHONY: e2e
e2e: e2e-reset ## Rebuild the test instance from an empty volume and run the browser tests
	cd e2e && AWG_URL=$(E2E_URL) go test -count=1 -v -timeout $(E2E_TEST_TIMEOUT) .

.PHONY: e2e-reset
e2e-reset: ## Recreate the test instance from scratch, discarding every server it holds
	@$(MAKE) --no-print-directory e2e-down
	docker build -t $(E2E_IMAGE) .
	docker run -d --name $(E2E_NAME) -p $(E2E_PORT):$(E2E_PORT)/tcp \
		-e WEB_UI_PORT=$(E2E_PORT) -e WEB_UI_USER=admin -e 'WEB_UI_PASSWORD=$(E2E_PASSWORD)' \
		-v $(E2E_VOLUME):/etc/amnezia \
		--cap-add NET_ADMIN --cap-add SYS_MODULE --device /dev/net/tun \
		--sysctl net.ipv4.ip_forward=1 --sysctl net.ipv4.conf.all.src_valid_mark=1 \
		$(E2E_IMAGE)
	@printf 'waiting for %s ' "$(E2E_URL)"; \
	deadline=$$(( $$(date +%s) + $(E2E_TIMEOUT) )); \
	until curl -fsS -o /dev/null "$(E2E_URL)/status" 2>/dev/null; do \
		if [ "$$(date +%s)" -ge "$$deadline" ]; then \
			echo "timed out after $(E2E_TIMEOUT)s"; \
			docker logs --tail 50 $(E2E_NAME); \
			exit 1; \
		fi; \
		printf '.'; sleep 2; \
	done; \
	echo ' ready'

# Separate from e2e-reset so a finished run can be cleaned up without starting
# another instance, and so the volume is gone even if the tests failed.
.PHONY: e2e-down
e2e-down: ## Remove the test instance and its volume
	@docker rm -f $(E2E_NAME) >/dev/null 2>&1 || true
	@docker volume rm -f $(E2E_VOLUME) >/dev/null 2>&1 || true

##@ Profiling

# The pprof handlers are only mounted when the server is started with
# WEB_UI_PPROF=1, and they sit behind the same basic auth as the rest of the
# API. Override any of these on the command line, e.g.
#   make profile-cpu PPROF_PORT=51836 PPROF_SECONDS=60
PPROF_HOST ?= localhost
PPROF_PORT ?= 54845
PPROF_USER ?= admin
# The plaintext password, not the hash that goes into WEB_UI_PASSWORD.
PPROF_PASSWORD ?= changeme
# How long the CPU profile is collected for, in seconds.
PPROF_SECONDS ?= 30
# Where the downloaded profiles are kept; gitignored.
PPROF_DIR ?= profiles
# Extra flags for "go tool pprof". Empty means its interactive prompt;
# "-http=:8081" opens the web UI instead (needs graphviz), "-top -nodecount=20"
# just prints the hot spots and exits.
PPROF_FLAGS ?=-http=:8081

PPROF_URL = http://$(PPROF_HOST):$(PPROF_PORT)/debug/pprof

# Which kind of profile "profile-diff" compares: heap, cpu or alloc.
PPROF_KIND ?= heap

.PHONY: profile-mem
profile-mem: check-go ## Fetch the heap profile and open it in "go tool pprof"
	@mkdir -p $(PPROF_DIR)
	@out="$(PPROF_DIR)/heap-$$(date +%Y%m%d-%H%M%S).pprof"; \
	curl -fsS -u "$(PPROF_USER):$(PPROF_PASSWORD)" -o "$$out" "$(PPROF_URL)/heap?gc=1"; \
	echo "saved $$out"; \
	go tool pprof $(PPROF_FLAGS) "$$out"

.PHONY: profile-cpu
profile-cpu: check-go ## Collect a CPU profile (PPROF_SECONDS, default 30s) and open it in "go tool pprof"
	@mkdir -p $(PPROF_DIR)
	@echo "Collecting $(PPROF_SECONDS)s of CPU profile from $(PPROF_URL) - put the server under load now"
	@out="$(PPROF_DIR)/cpu-$$(date +%Y%m%d-%H%M%S).pprof"; \
	curl -fsS --max-time $$(($(PPROF_SECONDS) + 30)) -u "$(PPROF_USER):$(PPROF_PASSWORD)" \
		-o "$$out" "$(PPROF_URL)/profile?seconds=$(PPROF_SECONDS)"; \
	echo "saved $$out"; \
	go tool pprof $(PPROF_FLAGS) "$$out"

.PHONY: profile-alloc
profile-alloc: check-go ## Fetch the allocation profile (everything allocated since start, freed included)
	@mkdir -p $(PPROF_DIR)
	@out="$(PPROF_DIR)/alloc-$$(date +%Y%m%d-%H%M%S).pprof"; \
	curl -fsS -u "$(PPROF_USER):$(PPROF_PASSWORD)" -o "$$out" "$(PPROF_URL)/allocs"; \
	echo "saved $$out"; \
	go tool pprof $(PPROF_FLAGS) -sample_index=alloc_space "$$out"

# Every snapshot is kept under its own timestamp, so the two most recent ones
# of a kind are a before/after pair: pprof subtracts the older from the newer
# and shows only what the change moved. Works across binaries - the samples
# are matched by function name, not by address.
.PHONY: profile-diff
profile-diff: check-go ## Compare the two most recent profiles (PPROF_KIND=heap|cpu|alloc)
	@files=$$(ls -1 "$(PPROF_DIR)"/$(PPROF_KIND)-*.pprof 2>/dev/null | sort || true); \
	count=$$(printf '%s' "$$files" | grep -c . || true); \
	if [ "$$count" -lt 2 ]; then \
		echo "need two $(PPROF_KIND) profiles in $(PPROF_DIR)/, have $$count"; \
		echo "take another snapshot with: make profile-$(if $(filter cpu,$(PPROF_KIND)),cpu,$(if $(filter alloc,$(PPROF_KIND)),alloc,mem))"; \
		exit 1; \
	fi; \
	base=$$(printf '%s\n' "$$files" | tail -2 | head -1); \
	new=$$(printf '%s\n' "$$files" | tail -1); \
	echo "base: $$base"; \
	echo "new:  $$new"; \
	go tool pprof $(PPROF_FLAGS) -base "$$base" "$$new"

.PHONY: profile-clean
profile-clean: ## Remove the downloaded profiles
	rm -rf $(PPROF_DIR)

##@ Backend utilities

.PHONY: check-go
check-go: ## Check Go version
	@go version | grep -q 'go1\.26' || (echo "Please use Go 1.26.X"; exit 1)

.PHONY: vet
vet: check-go ## Run go vet over every module
	go vet ./...
	cd web-ui && GOOS=js GOARCH=wasm go vet ./...
	cd e2e && go vet ./...

.PHONY: fix
fix: check-go ## Run go fix
	go fix ./...

.PHONY: deploy
deploy: ## Deploy via docker (host name is requested at runtime)
	@read -p "Enter host for deployment: " HOST; \
	echo "build"; \
	docker buildx build --load --file Dockerfile --progress=plain --tag "proxy:latest" .; \
	echo "exporting image"; \
	docker save "proxy:latest" > "proxy.tar"; \
	echo "remove local image"; \
	docker image rm -f "proxy:latest"; \
	echo "copying to $$HOST"; \
	rsync -avzP --mkpath deploy/.env \
		Makefile \
		docker-compose.yml \
		proxy.tar \
		"$$HOST:/usr/local/include/proxy/"; \
	echo "deploying on $$HOST"; \
	ssh "$$HOST" "cd /usr/local/include/proxy && (docker compose down || true) && (docker image rm -f 'proxy:latest' || true) && docker load < proxy.tar && docker compose up -d && rm -rf /usr/local/include/proxy"

##@ Updates

# Upstream sources of the two components the image pins by exact tag. The
# Dockerfile stays the single source of truth: this target only reads the
# pinned values out of it and reports, it never edits anything.
AWG_GO_REPO    ?= https://github.com/amnezia-vpn/amneziawg-go.git
AWG_TOOLS_REPO ?= https://github.com/amnezia-vpn/amneziawg-tools.git

.PHONY: check-updates
check-updates: ## Compare AWG_GO_VERSION/AWG_TOOLS_VERSION from the Dockerfile with the latest upstream tags
	@pinned() { sed -n "s/^ARG $$1=//p" Dockerfile | head -1; }; \
	latest() { \
		git ls-remote --tags --refs "$$1" 2>/dev/null \
			| sed 's|.*refs/tags/||' \
			| grep -E '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9]+)?$$' \
			| sort -V | tail -1; \
	}; \
	outdated=0; \
	report() { \
		if [ -z "$$3" ]; then \
			status='lookup failed'; \
		elif [ "$$2" = "$$3" ]; then \
			status='up to date'; \
		else \
			status='UPDATE AVAILABLE'; outdated=$$((outdated + 1)); \
		fi; \
		printf '  %-16s %-18s %-18s %s\n' "$$1" "$$2" "$${3:-?}" "$$status"; \
	}; \
	printf '\n  %-16s %-18s %-18s %s\n' 'COMPONENT' 'PINNED' 'LATEST' 'STATUS'; \
	report AWG_GO    "$$(pinned AWG_GO_VERSION)"    "$$(latest $(AWG_GO_REPO))"; \
	report AWG_TOOLS "$$(pinned AWG_TOOLS_VERSION)" "$$(latest $(AWG_TOOLS_REPO))"; \
	echo; \
	if [ "$$outdated" -gt 0 ]; then \
		echo "  $$outdated component(s) behind upstream - bump the ARG lines at the top of Dockerfile"; \
	else \
		echo '  Both versions pinned in Dockerfile are current'; \
	fi; \
	echo
