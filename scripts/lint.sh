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

echo "==> ui-check"
# Validate embedded UI files
if [[ ! -f cmd/slmcode/ui/styles.css ]]; then
  echo "ERROR: cmd/slmcode/ui/styles.css missing"
  exit 1
fi
if [[ ! -f cmd/slmcode/ui/app.jsx ]]; then
  echo "ERROR: cmd/slmcode/ui/app.jsx missing"
  exit 1
fi
if [[ ! -f cmd/slmcode/ui/index.html ]]; then
  echo "ERROR: cmd/slmcode/ui/index.html missing"
  exit 1
fi

# CSS validation
if ! grep -q 'data-theme' cmd/slmcode/ui/styles.css; then
  echo "ERROR: styles.css missing data-theme selector"
  exit 1
fi
if ! grep -q 'slmcode-theme' cmd/slmcode/ui/app.jsx; then
  echo "ERROR: app.jsx missing slmcode-theme reference"
  exit 1
fi

echo "==> ui-check: OK"

echo "lint: OK"
