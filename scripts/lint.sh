#!/usr/bin/env bash
# Format check + vet + UI smoke for CI and local make lint.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "==> gofmt"
# Skip gitignored workspace state (.slmcode/) and vendored trees.
unformatted="$(gofmt -l . | grep -vE '^(vendor/|\.slmcode/)' || true)"
if [[ -n "$unformatted" ]]; then
  echo "gofmt needed on:"
  echo "$unformatted"
  exit 1
fi

echo "==> go vet"
go vet ./...

echo "==> golangci-lint"
# Config: .golangci.yml (golangci-lint v2 schema). BLOCKING: the ratchet reached
# zero, so any new finding fails the build. LINT_STRICT is still honored for
# `make lint-strict`, but it no longer changes anything — both are blocking.
if command -v golangci-lint >/dev/null 2>&1; then
  if golangci-lint run --timeout=5m ./...; then
    echo "golangci-lint: no issues"
  else
    gl_status=$?
    echo "golangci-lint: issues found (see above)."
    echo "The baseline is ZERO — fix the finding, or add a //nolint:<linter> with a"
    echo "specific reason if it is genuinely a false positive. See .golangci.yml."
    exit "$gl_status"
  fi
else
  echo "golangci-lint not found — skipping. Install: https://golangci-lint.run/welcome/install/"
fi

echo "==> ui-check"
# Validate the go:embed'ed Studio UI directory. Shared with `make ui-check` —
# scripts/ui-check.sh is the single source of truth for what a valid
# cmd/slmcode/ui/ looks like in both states (built UI, or placeholder-only).
./scripts/ui-check.sh

echo "lint: OK"
