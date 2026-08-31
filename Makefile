MODULE := github.com/UnicoLab/slmcode
BIN    := slmcode
VERSION ?= 0.23.0
PREFIX ?= $(HOME)/.local
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.SourceRoot=$(CURDIR) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)

# Cross-compile targets for `make release-binaries`. PREBUILT_PLATFORMS is the
# subset that gets committed to prebuilt/ — macOS only by default, because each
# platform costs ~10 MiB of permanent git history per release.
PLATFORMS ?= darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
PREBUILT_PLATFORMS ?= darwin/arm64 darwin/amd64

# Optional local GoLangGraph checkout for hacking (use: go mod edit -replace ...).
GOLANGGRAPH ?= $(CURDIR)/../GoLangGraph-Project/GoLangGraph

# System prefix: Homebrew on Apple Silicon, else /usr/local
SYSTEM_PREFIX := $(shell \
	if command -v brew >/dev/null 2>&1; then brew --prefix; \
	elif [ -d /opt/homebrew/bin ]; then echo /opt/homebrew; \
	else echo /usr/local; fi)

# Default stack file
STACKS_DIR := $(CURDIR)/stacks
stack ?= omlx-local

.PHONY: help tidy tidy-check web-deps web-check ui-react lint lint-strict build bootstrap ui-check install install-user install-system update uninstall uninstall-system test race race-e2e cover e2e e2e-slm e2e-release check studio doctor clean docs docs-serve docs-build docs-venv govulncheck release-binaries prebuilt install-offline

# ── Stack management ──
.PHONY: stack-list stack-show stack-apply stack-edit stack-new

# ── Block management ──
.PHONY: blocks-list blocks-validate blocks-show blocks-apply-go blocks-apply-python blocks-apply-react

help: ## Show this help
	@echo "SLMCode Makefile — v$(VERSION)"
	@echo ""
	@echo "  Core commands:"
	@echo "    make build           Build the binary"
	@echo "    make bootstrap       Build the Studio UI into the binary (npm deps + vite build)"
	@echo "    make ui-react        Rebuild the Studio UI after editing web/"
	@echo "    make install         Install user-wide (~/.local/bin)"
	@echo "    make install-system  Install system-wide"
	@echo "    make test            Run unit tests"
	@echo "    make race            Run unit tests with the race detector (pkg/...)"
	@echo "    make race-e2e        Run the integration suite with the race detector (slow)"
	@echo "    make cover           Run tests with coverage, enforce the floor"
	@echo "    make e2e             Run e2e tests"
	@echo "    make e2e-slm         Live-model e2e vs a REAL SLM — needs a running oMLX,"
	@echo "                         costs real time (~15-30 min on the 9B). Not in 'make check'."
	@echo "                         ARGS=\"--model … --scenario … --timeout … --json … --keep\""
	@echo "    make studio          Build & launch Studio UI"
	@echo "    make lint            Format-check + vet + golangci-lint (blocking) + UI smoke"
	@echo "    make lint-strict     Alias for lint (both blocking — the lint baseline is zero)"
	@echo "    make check           Full local gate — same as CI (fmt, vet, lint, tests+coverage, race, web)"
	@echo "    make web-check       Lint/typecheck/test/build web/ (skips cleanly without npm)"
	@echo "    make govulncheck     Scan dependencies for known vulnerabilities"
	@echo "    make doctor          Run system health check"
	@echo ""
	@echo "  Stack commands (model/provider presets):"
	@echo "    make stack-list                          List available stacks"
	@echo "    make stack-show         stack=deepseek   Show a stack config"
	@echo "    make stack-apply        stack=deepseek   Apply a stack to .slmcode/config.yaml"
	@echo "    make stack-apply-force  stack=deepseek   Apply stack (overwrite existing)"
	@echo "    make stack-edit         stack=deepseek   Open stack in $$EDITOR"
	@echo "    make stack-new          name=my-stack    Create new stack from current config"
	@echo ""
	@echo "  Available stacks: $(shell ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//' | tr '\n' ' ')"
	@echo ""
	@echo "  Block commands:"
	@echo "    make blocks-list                        List all building blocks"
	@echo "    make blocks-validate                    Validate all block YAML configs"
	@echo "    make blocks-show      kind=pipeline id=go   Show a block"
	@echo "    make blocks-apply-go                   Apply Go language pack"
	@echo "    make blocks-apply-python               Apply Python language pack"
	@echo "    make blocks-apply-react                Apply React/TS language pack"
	@echo ""
	@echo "  Docs:"
	@echo "    make docs-build      Build MkDocs site"
	@echo "    make docs-serve      Serve docs locally"
	@echo "    make clean           Remove build artifacts"

# ── Stack: list available stacks ──
stack-list:
	@if command -v slmcode >/dev/null 2>&1 || [ -x ./bin/slmcode ]; then \
		(./bin/slmcode stack list 2>/dev/null || slmcode stack list); \
	else \
		echo "Available SLMCode stacks:"; \
		echo ""; \
		for f in $(STACKS_DIR)/*.yaml; do \
			name=$$(basename "$$f" .yaml); \
			prov=$$(grep -E '^provider:' "$$f" | head -1 | sed 's/provider: *//'); \
			model=$$(grep -E '^model:' "$$f" | head -1 | sed 's/model: *//'); \
			printf "  %-20s → %-12s %s\n" "$$name" "$$prov" "$$model"; \
		done; \
		echo ""; \
		echo "Usage: slmcode stack apply <name>   (or: make stack-apply stack=<name>)"; \
	fi

# ── Stack: show a stack config ──
stack-show:
	@if [ ! -f "$(STACKS_DIR)/$(stack).yaml" ]; then \
		echo "Stack '$(stack)' not found in $(STACKS_DIR)"; \
		echo "Available: $(shell ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//' | tr '\n' ' ' | sed 's/  / /g')"; \
		exit 1; \
	fi
	@echo "═══ Stack: $(stack) ═══"
	@cat "$(STACKS_DIR)/$(stack).yaml"

# ── Stack: apply via slmcode (merge — keeps listen/skills/mcp/api_key) ──
# Optional: agents=1 clear=1 force=1
stack-apply:
	@if [ -z "$(stack)" ]; then echo "Usage: make stack-apply stack=<name> [agents=1] [clear=1]"; exit 1; fi
	@FLAGS=""; \
	if [ "$(agents)" = "1" ]; then FLAGS="$$FLAGS --agents"; fi; \
	if [ "$(clear)" = "1" ]; then FLAGS="$$FLAGS --clear-agent-llm"; fi; \
	if [ "$(force)" = "1" ]; then FLAGS="$$FLAGS --force-agents"; fi; \
	if [ -x ./bin/slmcode ]; then \
		./bin/slmcode stack apply $(stack) $$FLAGS; \
	elif command -v slmcode >/dev/null 2>&1; then \
		slmcode stack apply $(stack) $$FLAGS; \
	else \
		echo "slmcode binary not found — run: make build && ./bin/slmcode stack apply $(stack)"; \
		exit 1; \
	fi

# ── Stack: apply (same as stack-apply; kept for backwards compat) ──
stack-apply-force: stack-apply

# ── Stack: edit a stack ──
stack-edit:
	@if [ ! -f "$(STACKS_DIR)/$(stack).yaml" ]; then \
		echo "Stack '$(stack)' not found. Available:"; \
		ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//'; \
		exit 1; \
	fi
	$${EDITOR:-vim} "$(STACKS_DIR)/$(stack).yaml"

# ── Stack: create new stack from current config ──
name ?= custom
stack-new:
	@CONFIG_PATH="$${SLMCODE_CONFIG:-$$(pwd)/.slmcode/config.yaml}"; \
	if [ ! -f "$$CONFIG_PATH" ]; then \
		echo "No existing config found. Apply a stack first: make stack-apply stack=<name>"; \
		exit 1; \
	fi
	@mkdir -p $(STACKS_DIR)
	@cp "$${SLMCODE_CONFIG:-$(pwd)/.slmcode/config.yaml}" "$(STACKS_DIR)/$(name).yaml"
	@echo "✔ Created stack '$(name)' at stacks/$(name).yaml"
	@echo "  Edit: make stack-edit stack=$(name)"

# ── Blocks: list all building blocks ──
blocks-list:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks list; \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks list; \
	else echo "Run: make build && ./bin/slmcode blocks list"; exit 1; fi

# ── Blocks: validate all YAML configs ──
blocks-validate:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks validate; \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks validate; \
	else echo "Run: make build && ./bin/slmcode blocks validate"; exit 1; fi

# ── Blocks: show a specific block ──
# Usage: make blocks-show kind=pipeline id=go
kind ?= pipeline
id ?= go
blocks-show:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks show $(kind) $(id); \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks show $(kind) $(id); \
	else echo "Run: make build"; exit 1; fi

# ── Blocks: apply language packs ──
blocks-apply-go:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks apply go; \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks apply go; \
	else echo "Run: make build"; exit 1; fi

blocks-apply-python:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks apply python; \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks apply python; \
	else echo "Run: make build"; exit 1; fi

blocks-apply-react:
	@if [ -x ./bin/slmcode ]; then ./bin/slmcode blocks apply react; \
	elif command -v slmcode >/dev/null 2>&1; then slmcode blocks apply react; \
	else echo "Run: make build"; exit 1; fi

# ── UI: build React Studio and update embedded UI ──
#
# Target graph:
#   web-deps ──┬── ui-react ── bootstrap
#              └── web-check   (calls web-deps tolerantly: a failure is a SKIP)
#   ui-check (no npm needed) ── build ── studio / doctor
#
# web-deps is the single place that knows how to get web/node_modules into a
# usable state. Every target that runs a script from web/package.json goes
# through it, so a missing or stale install can never surface as a wall of
# TS2307 "Cannot find module" errors again.
web-deps: ## Install web/ dependencies if missing or stale (npm ci, falling back to npm install)
	@./scripts/web-deps.sh

ui-react: web-deps ## Build React/Vite Studio UI and sync it into the go:embed directory
	@if [ ! -d web/node_modules ]; then \
		echo "ERROR: web/node_modules is missing — the Studio UI dependencies are not installed." >&2; \
		echo "       Run 'make bootstrap' (installs dependencies, then builds)." >&2; \
		echo "       Building without them fails with dozens of TS2307 'Cannot find module'" >&2; \
		echo "       errors, which say nothing about the real problem." >&2; \
		exit 1; \
	fi
	@echo "Building React Studio UI..."
	cd web && npm run build
	@echo "Syncing to embed directory..."
	@rm -rf cmd/slmcode/ui/assets cmd/slmcode/ui/vendor cmd/slmcode/ui/index.html
	@mkdir -p cmd/slmcode/ui
	cp -r web/dist/* cmd/slmcode/ui/
	@$(MAKE) --no-print-directory ui-check

bootstrap: web-deps ## Install web deps and build the Studio UI into cmd/slmcode/ui (run once per clone)
	@$(MAKE) --no-print-directory ui-react
	@echo "✔ Studio UI built and embedded — 'make build' now ships the real SPA."

tidy: ## Tidy Go modules (rewrites go.mod/go.sum — needs the module proxy)
	go mod tidy

# The non-mutating form, for `make check`.
#
# `check` used to depend on `tidy`, which meant two things it should not: the
# one command CONTRIBUTING tells people to run REWROTE go.mod as a side effect,
# and it hard-failed anywhere the module proxy is unreachable — a plane, an
# air-gapped runner, a sandboxed agent. A tree is still worth verifying when
# the proxy is not. Unreachable proxy is a SKIP with a named reason; a genuine
# go.mod/imports mismatch is still a failure.
tidy-check:
	@echo "==> go mod tidy -diff"
	@out="$$(go mod tidy -diff 2>&1)"; status=$$?; \
	if [ $$status -eq 0 ]; then \
		echo "tidy: OK (go.mod and go.sum match the imports)"; \
	elif echo "$$out" | grep -qiE 'dial tcp|no such host|forbidden|i/o timeout|connection refused|unrecognized import path|proxy|TLS|certificate|network is unreachable'; then \
		echo "tidy: SKIP — the Go module proxy is not reachable from here, so go.mod cannot be verified."; \
		echo "      CI verifies it; run 'make tidy' once you are online."; \
	else \
		echo "$$out"; \
		echo "ERROR: go.mod/go.sum do not match the imports — run 'make tidy'." >&2; \
		exit 1; \
	fi

# cmd/slmcode/ui/ is a go:embed directory with exactly ONE tracked file:
# .gitkeep. index.html / assets/ / vendor/ in there are gitignored BUILD OUTPUT
# written by `make ui-react`. When they are absent the binary serves the
# placeholder page compiled into pkg/server, so `go build` works on a fresh
# clone with no Node at all. scripts/ui-check.sh is shared with scripts/lint.sh.
ui-check: ## Smoke-test the embedded UI directory (built UI or placeholder — both valid)
	@./scripts/ui-check.sh

lint: ## Go format + vet + golangci-lint (blocking) + UI smoke check
	@./scripts/lint.sh

lint-strict: lint ## Alias for lint — kept for muscle memory and CI history; lint is blocking now that the baseline is zero

# NOT `tidy`: a build target must not rewrite go.mod, and it must work offline.
# `make check` verifies go.mod separately (see tidy-check).
build: ui-check ## Build the slmcode binary
	go build -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/slmcode

# Default: user install (~/.local/bin)
install: install-user ## Install user-wide

install-user: ## Install to ~/.local/bin
	./scripts/install.sh --user --prefix "$(PREFIX)"

install-system: ## System-wide install (Homebrew /usr/local)
	./scripts/install.sh --system

update: install-system ## Rebuild & reinstall system-wide

uninstall: ## Uninstall user-wide
	./scripts/install.sh --uninstall --user --prefix "$(PREFIX)"

uninstall-system: ## Uninstall system-wide
	./scripts/install.sh --uninstall --system

# ── Offline / locked-down distribution ──
#
# For machines where Homebrew, the Go toolchain and GitHub release downloads all
# come back 403 but `git clone` works. `prebuilt` puts macOS binaries INSIDE the
# repository; `install-offline` installs one without touching the network.
# See prebuilt/README.md for the size trade-off this makes.

# Cross-compiled the way the release workflow does it: SourceRoot stamped EMPTY,
# so the binary never points `slmcode update` at a checkout that does not exist
# on the machine it lands on.
release-binaries: ui-check ## Cross-compile release binaries into dist/ (PLATFORMS=...)
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		goos=$${platform%%/*}; goarch=$${platform##*/}; \
		ext=""; if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		out="dist/$(BIN)_$(VERSION)_$${goos}_$${goarch}$${ext}"; \
		echo "Building $$out"; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 go build -trimpath \
			-ldflags "-s -w -X main.Version=$(VERSION) -X main.SourceRoot= -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)" \
			-o "$$out" ./cmd/slmcode || exit 1; \
		chmod +x "$$out"; \
	done
	@ls -lh dist

# Rebuilds the binaries first: committing a stale prebuilt/ is the one failure
# mode of this channel that nobody notices until a user installs last month's
# build and reports a bug that was fixed weeks ago.
#
# The Studio assertion is the release workflow's, repeated here because this
# target COMMITS what it builds. `ui-check` deliberately accepts "no build
# output at all" so contributors without Node can still `make build` — the
# binary then serves the pkg/server placeholder. Publishing that to every
# offline user is not a downgrade anyone would notice until they ran
# `slmcode studio`.
prebuilt: release-binaries ## Refresh prebuilt/ (the binaries committed to the repo)
	@if [ -z "$(PREBUILT_ALLOW_PLACEHOLDER)" ] && \
	   { [ ! -f cmd/slmcode/ui/index.html ] || [ ! -d cmd/slmcode/ui/assets ]; }; then \
		echo "ERROR: the Studio SPA is not embedded — these binaries would ship the placeholder page." >&2; \
		echo "       Run 'make bootstrap' (needs Node 18+), then 'make prebuilt' again." >&2; \
		echo "       Override only if you know why: PREBUILT_ALLOW_PLACEHOLDER=1 make prebuilt" >&2; \
		exit 1; \
	fi
	@PREBUILT_PLATFORMS="$(PREBUILT_PLATFORMS)" ./scripts/update-prebuilt.sh "$(VERSION)" dist

install-offline: ## Install from prebuilt/ without network, Go or Homebrew
	./scripts/install-offline.sh --prefix "$(PREFIX)"

test: ## Run unit tests
	go test ./...

race: ## Run unit tests under the Go race detector (pkg/... — the engine core)
	go test -race -count=1 ./pkg/...

# The integration suite races the parts unit tests cannot: a full run has
# parallel workers and background probes emitting while the run goroutine
# rewrites session state, and that is where the currentTurn race lived — invisible
# to `make race` because no pkg/... test starts a whole run.
race-e2e: ## Run the integration suite under the race detector (slow — ~5 min)
	go test -race -count=1 -timeout 30m ./test/e2e/

# Coverage floor: today's measured total (see scripts/coverage-check.sh for
# how the number is derived, and the floor value itself).
cover: ## Run tests with coverage and fail if total coverage drops below the floor
	@./scripts/coverage-check.sh

govulncheck: ## Scan all packages for known vulnerabilities
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck not found — installing (go install golang.org/x/vuln/cmd/govulncheck@latest)…"; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	govulncheck ./...

e2e: ## Run e2e tests (set RUN_E2E=1 for live oMLX tests)
	go test ./test/e2e/ -count=1 -timeout 30m
	@./scripts/e2e_prime_smoke.sh
	@if [ "$$RUN_E2E" = "1" ]; then \
		go test ./test/e2e/ -count=1 -timeout 45m -run 'TestLiveOMLX|TestIsolatedMultiAgent'; \
	fi

# Deliberately NOT a dependency of `check` or `e2e`: this one drives a REAL
# model, so it needs a running oMLX (or any OpenAI-compatible endpoint serving
# the model) and it costs real wall-clock time — roughly 15-30 minutes for all
# five scenarios on the fast 9B, and an hour or more on a 27B. `make check` must
# stay runnable on a laptop with no model, which is why this lives alone.
#
#   make e2e-slm                                  # all scenarios, fast 9B
#   make e2e-slm ARGS="--model Qwen3.8-27B-4bit"  # slower, stronger
#   make e2e-slm ARGS="--scenario fix-a-bug --keep"
e2e-slm: ## Live-model e2e against a real SLM — NEEDS a running oMLX, costs real time (not in `make check`)
	@./scripts/e2e-slm.sh $(ARGS)

# The pre-release check, against the model server you actually use.
#
# `make check` runs against fakes and answers "is the tree correct". It cannot
# answer "does this release work on my machine", because the things that break
# between a fake and a real server are exactly the things a fake cannot show: a
# model list whose real names rank differently, an endpoint that serves
# /v1/models but cannot complete a chat, a manager asked for an org chart at
# temperature. So this drives the real binary and a real model.
#
# Needs a running model server (oMLX, Ollama, LM Studio, vLLM) or a hosted
# provider's API key in the environment. Costs real wall-clock time — the
# squads subtest builds a two-language app and can run an hour on a local SLM.
#
#   make e2e-release                      # everything
#   make e2e-release ARGS="-run TestLiveReleaseSurface/configure"
#   RUN_E2E_SQUADS=0 make e2e-release     # skip the slow two-language run
e2e-release: ## Pre-release check against YOUR model server — needs a live endpoint, costs real time
	RUN_E2E=1 go test ./test/e2e/ -count=1 -timeout 90m -v \
		-run 'TestLiveReleaseSurface' $(ARGS)

# docs/slm-learnings.md is evidence, so its tables are REGENERATED from the e2e
# reports rather than hand-maintained. DIR defaults to the current directory;
# point it at wherever SLMCODE_E2E_REPORT wrote them.
.PHONY: slm-learnings
slm-learnings: ## Recompute the docs/slm-learnings.md tables from e2e reports (DIR=path)
	@python3 scripts/slm-learnings-stats.py $(or $(DIR),.)

# The one gate: gofmt check + vet + golangci-lint (blocking) + unit tests
# + race tests + web lint/build. This is exactly what CI's lint-test job and
# .pre-commit-config.yaml both run, so local and CI cannot diverge — if you
# want to know whether a PR will pass CI, run `make check`.
check: tidy-check lint cover race web-check ## Run the full local gate (fmt, vet, lint, ui-check, tests+coverage floor, race, web) — same as CI
	@echo "check: OK"

# The web half of `check`, as its own target so it can be run and debugged
# alone. Every reason it cannot run is a NAMED skip, not a failure: the Go tree
# is not broken because npm is missing or the registry is unreachable, and a
# gate that fails for a reason the developer cannot fix is a gate people learn
# to bypass. A lint or build error with node_modules already present IS a
# failure — that is the tree's fault, and CI's web-check job runs it for real.
#
# This is the ONE web target that does not take web-deps as a prerequisite: a
# prerequisite failure aborts make, and "npm cannot reach the registry" must be
# a skip here, not an abort. It calls web-deps as a sub-make instead and treats
# a non-zero exit as the skip.
web-check: ## Lint, typecheck, test + build the Studio UI (skips with a reason when npm/registry are unavailable)
	@echo "==> web lint + typecheck + test + build"
	@if [ ! -d web ]; then \
		echo "web: SKIP — no web/ directory in this tree."; \
	elif ! command -v npm >/dev/null 2>&1; then \
		echo "web: SKIP — npm is not on PATH. Install Node.js to lint and build the Studio UI."; \
	elif ! ./scripts/web-deps.sh; then \
		echo "web: SKIP — web/ dependencies could not be installed (npm registry unreachable?)."; \
		echo "     The Go gate above still ran. See the npm output above for the reason."; \
	else \
		( cd web && npm run lint && npm run typecheck:test && npm run test && npm run build ) || exit 1; \
		echo "web: OK"; \
	fi

studio: build ## Build & launch Studio UI
	./bin/$(BIN) studio

doctor: build ## Run system health check
	./bin/$(BIN) doctor

docs-venv: ## Set up Python venv for docs
	@test -d .venv-docs || python3 -m venv .venv-docs
	@.venv-docs/bin/pip install -q -r requirements-docs.txt

docs-build: docs-venv ## Build MkDocs site
	@mkdir -p docs/assets
	@cp -f assets/slmcode-logo.png docs/assets/slmcode-logo.png
	.venv-docs/bin/mkdocs build --strict

docs-serve: docs-venv ## Serve docs locally (live-reload)
	@mkdir -p docs/assets
	@cp -f assets/slmcode-logo.png docs/assets/slmcode-logo.png
	.venv-docs/bin/mkdocs serve

docs: docs-build ## Build docs (alias)

clean: ## Remove build artifacts (never prebuilt/ — that is tracked, not built here)
	rm -rf bin dist site
