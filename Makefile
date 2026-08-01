MODULE := github.com/UnicoLab/slmcode
BIN    := slmcode
VERSION ?= 0.6.0
PREFIX ?= $(HOME)/.local
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.SourceRoot=$(CURDIR) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)

# Optional local GoLangGraph checkout for hacking (use: go mod edit -replace ...).
# Production builds use the tagged module github.com/piotrlaczkowski/GoLangGraph@v0.2.1+.
GOLANGGRAPH ?= $(CURDIR)/../GoLangGraph-Project/GoLangGraph

# System prefix: Homebrew on Apple Silicon, else /usr/local
SYSTEM_PREFIX := $(shell \
	if command -v brew >/dev/null 2>&1; then brew --prefix; \
	elif [ -d /opt/homebrew/bin ]; then echo /opt/homebrew; \
	else echo /usr/local; fi)

.PHONY: tidy lint build ui-check install install-user install-system update uninstall uninstall-system test e2e studio doctor clean docs docs-serve docs-build docs-venv

tidy:
	go mod tidy

# Studio UI is source under cmd/slmcode/ui/ and embedded via go:embed (no npm bundle step).
# Always rebuild the Go binary after UI edits so the served assets match.
ui-check:
	@test -f cmd/slmcode/ui/styles.css && test -f cmd/slmcode/ui/app.jsx && test -f cmd/slmcode/ui/index.html
	@grep -q 'data-theme' cmd/slmcode/ui/styles.css
	@grep -q 'slmcode-theme' cmd/slmcode/ui/app.jsx
	@echo "ui-check: OK (embedded by go:embed all:ui)"

lint:
	@./scripts/lint.sh

build: tidy ui-check
	go build -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/slmcode

# Default: user install (~/.local/bin)
install: install-user

install-user:
	./scripts/install.sh --user --prefix "$(PREFIX)"

# System-wide install: /opt/homebrew/bin or /usr/local/bin
install-system:
	./scripts/install.sh --system

# Rebuild from this checkout and reinstall system-wide (alias for day-to-day)
update: install-system

uninstall:
	./scripts/install.sh --uninstall --user --prefix "$(PREFIX)"

uninstall-system:
	./scripts/install.sh --uninstall --system

test:
	go test ./...
	@if command -v node >/dev/null 2>&1; then node cmd/slmcode/ui/markdown_node_test.js; fi

# Fast board/docs tests + optional live oMLX e2e (set RUN_E2E=1)
e2e:
	go test ./test/e2e/ -count=1 -timeout 30m
	@if command -v node >/dev/null 2>&1; then node cmd/slmcode/ui/markdown_node_test.js; fi
	@if [ "$$RUN_E2E" = "1" ]; then \
		go test ./test/e2e/ -count=1 -timeout 45m -run 'TestLiveOMLX|TestIsolatedMultiAgent'; \
	fi

studio: build
	./bin/$(BIN) studio

doctor: build
	./bin/$(BIN) doctor

docs-venv:
	@test -d .venv-docs || python3 -m venv .venv-docs
	@.venv-docs/bin/pip install -q -r requirements-docs.txt

docs-build: docs-venv
	@mkdir -p docs/assets
	@cp -f assets/slmcode-logo.png docs/assets/slmcode-logo.png
	.venv-docs/bin/mkdocs build --strict

docs-serve: docs-venv
	@mkdir -p docs/assets
	@cp -f assets/slmcode-logo.png docs/assets/slmcode-logo.png
	.venv-docs/bin/mkdocs serve

docs: docs-build

clean:
	rm -rf bin site
