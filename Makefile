MODULE := github.com/UnicoLab/slmcode
BIN    := slmcode
VERSION ?= 0.5.7
PREFIX ?= $(HOME)/.local
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.SourceRoot=$(CURDIR) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)

# Local GoLangGraph checkout used via go.mod replace
GOLANGGRAPH ?= $(CURDIR)/../GoLangGraph-Project/GoLangGraph

# System prefix: Homebrew on Apple Silicon, else /usr/local
SYSTEM_PREFIX := $(shell \
	if command -v brew >/dev/null 2>&1; then brew --prefix; \
	elif [ -d /opt/homebrew/bin ]; then echo /opt/homebrew; \
	else echo /usr/local; fi)

.PHONY: tidy build install install-user install-system update uninstall uninstall-system test e2e studio doctor clean

tidy:
	@test -d "$(GOLANGGRAPH)" || (echo "GoLangGraph not found at $(GOLANGGRAPH)"; exit 1)
	go mod tidy

build: tidy
	go build -ldflags "$(LDFLAGS)" -o bin/$(BIN) ./cmd/slmcode

# Default: user install (~/.local/bin)
install: install-user

install-user:
	./scripts/install.sh --user --prefix "$(PREFIX)"

# System-wide (Claude Code–style): /opt/homebrew/bin or /usr/local/bin
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

clean:
	rm -rf bin
