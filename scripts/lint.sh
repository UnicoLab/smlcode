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
# Validate embedded Vite/React UI files
if [[ ! -f cmd/slmcode/ui/index.html ]]; then
  echo "ERROR: cmd/slmcode/ui/index.html missing — run: make ui-react"
  exit 1
fi
if [[ ! -d cmd/slmcode/ui/assets ]]; then
  echo "ERROR: cmd/slmcode/ui/assets missing — run: make ui-react"
  exit 1
fi

# Validate index.html references SLMCode Studio
if ! grep -q 'SLMCode Studio' cmd/slmcode/ui/index.html; then
  echo "ERROR: index.html missing SLMCode Studio reference"
  exit 1
fi

echo "==> ui-check: OK"

echo "lint: OK"
