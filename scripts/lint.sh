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
# Config: .golangci.yml (golangci-lint v2 schema). Non-blocking by default —
# we're mid-ratchet on a known baseline (see the count comment at the top of
# .golangci.yml). Set LINT_STRICT=1 (or run `make lint-strict`) to make this
# fail the script on any finding.
if command -v golangci-lint >/dev/null 2>&1; then
  if golangci-lint run --timeout=5m ./...; then
    echo "golangci-lint: no issues"
  else
    gl_status=$?
    echo "golangci-lint: issues found (see above)."
    if [[ "${LINT_STRICT:-0}" == "1" ]]; then
      exit "$gl_status"
    fi
    echo "(non-blocking for now — run 'make lint-strict' to enforce; see .golangci.yml)"
  fi
else
  echo "golangci-lint not found — skipping. Install: https://golangci-lint.run/welcome/install/"
fi

echo "==> ui-check"
# Validate embedded Vite/React UI files. cmd/slmcode/ui/index.html is always
# tracked (a placeholder ships in the repo so go:embed always finds
# something on a fresh clone); cmd/slmcode/ui/assets/ is the gitignored
# built-UI output and is optional here — its absence just means the binary
# will embed the placeholder page instead of the real Studio UI.
if [[ ! -f cmd/slmcode/ui/index.html ]]; then
  echo "ERROR: cmd/slmcode/ui/index.html missing — this should always be tracked in git"
  exit 1
fi

# Validate index.html references SLMCode Studio
if ! grep -q 'SLMCode Studio' cmd/slmcode/ui/index.html; then
  echo "ERROR: index.html missing SLMCode Studio reference"
  exit 1
fi

if [[ -d cmd/slmcode/ui/assets ]]; then
  echo "==> ui-check: OK (React Studio UI embedded)"
else
  echo "==> ui-check: OK (placeholder UI embedded — run 'make bootstrap' or 'make ui-react' for the real Studio UI)"
fi

echo "lint: OK"
