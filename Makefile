MODULE := github.com/UnicoLab/slmcode
BIN    := slmcode
VERSION ?= 0.8.3
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

.PHONY: help tidy lint build ui-check install install-user install-system update uninstall uninstall-system test e2e studio doctor clean docs docs-serve docs-build docs-venv

# ── Stack management ──
.PHONY: stack-list stack-show stack-apply stack-edit stack-new

help: ## Show this help
	@echo "SLMCode Makefile — v$(VERSION)"
	@echo ""
	@echo "  Core commands:"
	@echo "    make build           Build the binary"
	@echo "    make install         Install user-wide (~/.local/bin)"
	@echo "    make install-system  Install system-wide"
	@echo "    make test            Run unit tests"
	@echo "    make e2e             Run e2e tests"
	@echo "    make studio          Build & launch Studio UI"
	@echo "    make lint            Format-check + vet + UI smoke"
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
	@echo "  Docs:"
	@echo "    make docs-build      Build MkDocs site"
	@echo "    make docs-serve      Serve docs locally"
	@echo "    make clean           Remove build artifacts"

# ── Stack: list available stacks ──
stack-list:
	@echo "Available SLMCode stacks:"
	@echo ""
	@for f in $(STACKS_DIR)/*.yaml; do \
		name=$$(basename "$$f" .yaml); \
		prov=$$(grep -E '^provider:' "$$f" | head -1 | sed 's/provider: *//'); \
		model=$$(grep -E '^model:' "$$f" | head -1 | sed 's/model: *//'); \
		printf "  %-20s → %-12s %s\n" "$$name" "$$prov" "$$model"; \
	done
	@echo ""
	@echo "Usage: make stack-apply stack=<name>"

# ── Stack: show a stack config ──
stack-show:
	@if [ ! -f "$(STACKS_DIR)/$(stack).yaml" ]; then \
		echo "Stack '$(stack)' not found in $(STACKS_DIR)"; \
		echo "Available: $(shell ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//' | tr '\n' ' ' | sed 's/  / /g')"; \
		exit 1; \
	fi
	@echo "═══ Stack: $(stack) ═══"
	@cat "$(STACKS_DIR)/$(stack).yaml"

# ── Stack: apply a stack config ──
stack-apply:
	@if [ ! -f "$(STACKS_DIR)/$(stack).yaml" ]; then \
		echo "Stack '$(stack)' not found. Available:"; \
		ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//'; \
		exit 1; \
	fi
	@CONFIG_PATH="$${SLMCODE_CONFIG:-$$(pwd)/.slmcode/config.yaml}"; \
	if [ -f "$$CONFIG_PATH" ]; then \
		echo "⚠  Config already exists at $$CONFIG_PATH"; \
		echo "   Run 'make stack-apply-force stack=$(stack)' to overwrite"; \
		echo "   Or 'make stack-new name=my-backup' to save current config first"; \
		exit 1; \
	fi
	@mkdir -p .slmcode
	@cp "$(STACKS_DIR)/$(stack).yaml" .slmcode/config.yaml
	@echo "✔ Stack '$(stack)' applied → .slmcode/config.yaml"
	@echo "  Provider: $$(grep '^provider:' .slmcode/config.yaml | sed 's/provider: *//')"
	@echo "  Model:    $$(grep '^model:' .slmcode/config.yaml | sed 's/model: *//')"
	@echo ""
	@echo "  Run your task: slmcode run -v \"your task\""

# ── Stack: force-apply a stack config ──
stack-apply-force:
	@if [ ! -f "$(STACKS_DIR)/$(stack).yaml" ]; then \
		echo "Stack '$(stack)' not found. Available:"; \
		ls $(STACKS_DIR)/*.yaml 2>/dev/null | xargs -n1 basename | sed 's/.yaml//'; \
		exit 1; \
	fi
	@mkdir -p .slmcode
	@cp "$(STACKS_DIR)/$(stack).yaml" .slmcode/config.yaml
	@echo "✔ Stack '$(stack)' force-applied → .slmcode/config.yaml"
	@grep -E '^(provider|model|backend):' .slmcode/config.yaml | sed 's/^/  /'

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

tidy: ## Tidy Go modules
	go mod tidy

# Studio UI is source under cmd/slmcode/ui/ and embedded via go:embed.
ui-check: ## Smoke-test the embedded UI files
	@test -f cmd/slmcode/ui/styles.css && test -f cmd/slmcode/ui/app.jsx && test -f cmd/slmcode/ui/index.html
	@grep -q 'data-theme' cmd/slmcode/ui/styles.css
	@grep -q 'slmcode-theme' cmd/slmcode/ui/app.jsx
	@echo "ui-check: OK (embedded by go:embed all:ui)"

lint: ## Go format + vet + UI smoke check
	@./scripts/lint.sh

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
	@if command -v node >/dev/null 2>&1; then node cmd/slmcode/ui/markdown_node_test.js; fi

e2e: ## Run e2e tests (set RUN_E2E=1 for live oMLX tests)
	go test ./test/e2e/ -count=1 -timeout 30m
	@if command -v node >/dev/null 2>&1; then node cmd/slmcode/ui/markdown_node_test.js; fi
	@if [ "$$RUN_E2E" = "1" ]; then \
		go test ./test/e2e/ -count=1 -timeout 45m -run 'TestLiveOMLX|TestIsolatedMultiAgent'; \
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

clean: ## Remove build artifacts
	rm -rf bin site
