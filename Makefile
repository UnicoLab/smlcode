MODULE := github.com/UnicoLab/slmcode
BIN    := slmcode
VERSION ?= 0.16.0
PREFIX ?= $(HOME)/.local
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.SourceRoot=$(CURDIR) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)

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

.PHONY: help tidy lint lint-strict build bootstrap ui-check install install-user install-system update uninstall uninstall-system test race cover e2e check studio doctor clean docs docs-serve docs-build docs-venv govulncheck

# ── Stack management ──
.PHONY: stack-list stack-show stack-apply stack-edit stack-new

# ── Block management ──
.PHONY: blocks-list blocks-validate blocks-show blocks-apply-go blocks-apply-python blocks-apply-react

help: ## Show this help
	@echo "SLMCode Makefile — v$(VERSION)"
	@echo ""
	@echo "  Core commands:"
	@echo "    make build           Build the binary"
	@echo "    make bootstrap       Build the Studio UI from source if missing (npm ci && vite build)"
	@echo "    make install         Install user-wide (~/.local/bin)"
	@echo "    make install-system  Install system-wide"
	@echo "    make test            Run unit tests"
	@echo "    make race            Run unit tests with the race detector (pkg/...)"
	@echo "    make cover           Run tests with coverage, enforce the floor"
	@echo "    make e2e             Run e2e tests"
	@echo "    make studio          Build & launch Studio UI"
	@echo "    make lint            Format-check + vet + golangci-lint (non-blocking) + UI smoke"
	@echo "    make lint-strict     Same as lint, but golangci-lint failures are blocking"
	@echo "    make check           Full local gate — same as CI (fmt, vet, lint, test, race, web)"
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
ui-react: ## Build React/Vite Studio UI and sync to embed directory
	@echo "Building React Studio UI..."
	cd web && npm run build
	@echo "Syncing to embed directory..."
	rm -rf cmd/slmcode/ui/assets cmd/slmcode/ui/vendor
	cp -r web/dist/* cmd/slmcode/ui/
	@echo "✔ React Studio UI synced to cmd/slmcode/ui/"

tidy: ## Tidy Go modules
	go mod tidy

# Studio UI is source under cmd/slmcode/ui/ and embedded via go:embed.
# index.html is always tracked (a placeholder ships so go:embed always finds
# something on a fresh clone); assets/ is the gitignored built-UI output and
# is optional — see `make bootstrap`.
ui-check: ## Smoke-test the embedded UI files
	@test -f cmd/slmcode/ui/index.html
	@grep -q 'SLMCode Studio' cmd/slmcode/ui/index.html
	@if [ -d cmd/slmcode/ui/assets ]; then \
		echo "ui-check: OK (React Studio embedded by go:embed all:ui)"; \
	else \
		echo "ui-check: OK (placeholder UI embedded — run 'make bootstrap' for the real Studio UI)"; \
	fi

bootstrap: ## Build the Studio UI from source if it hasn't been built yet (npm ci && vite build)
	@if [ -d cmd/slmcode/ui/assets ]; then \
		echo "Studio UI assets already present at cmd/slmcode/ui/assets — nothing to do (run 'make ui-react' to rebuild)."; \
	else \
		echo "Studio UI assets missing — bootstrapping (cd web && npm ci && npm run build)…"; \
		if ! command -v npm >/dev/null 2>&1; then \
			echo "ERROR: npm not found on PATH. Install Node.js (see web/package.json for the expected version), then re-run: make bootstrap" >&2; \
			exit 1; \
		fi; \
		(cd web && npm ci && npm run build) || { echo "ERROR: web UI build failed — see output above." >&2; exit 1; }; \
		$(MAKE) ui-react; \
	fi

lint: ## Go format + vet + golangci-lint (non-blocking) + UI smoke check
	@./scripts/lint.sh

lint-strict: ## Same as lint, but golangci-lint issues fail the build (used to ratchet .golangci.yml's baseline down)
	@LINT_STRICT=1 ./scripts/lint.sh

build: tidy ui-check ## Build the slmcode binary
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

test: ## Run unit tests
	go test ./...

race: ## Run unit tests under the Go race detector (pkg/... — the engine core)
	go test -race -count=1 ./pkg/...

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

# The one gate: gofmt check + vet + golangci-lint (non-blocking) + unit tests
# + race tests + web lint/build. This is exactly what CI's lint-test job and
# .pre-commit-config.yaml both run, so local and CI cannot diverge — if you
# want to know whether a PR will pass CI, run `make check`.
check: tidy lint test race ## Run the full local gate (fmt, vet, lint, unit+race tests, web lint+build) — same as CI
	@echo "==> web lint + build"
	@if [ -d web ]; then \
		( cd web && \
		  if [ ! -d node_modules ]; then npm ci; fi && \
		  npm run lint && \
		  npm run build ); \
	else \
		echo "(no web/ directory — skipping)"; \
	fi
	@echo "check: OK"

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

clean: ## Remove build artifacts
	rm -rf bin site
