#!/usr/bin/env bash
# Format check + vet + UI smoke for CI and local make lint.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "==> gofmt"
unformatted="$(gofmt -l . | grep -v '^vendor/' || true)"
if [[ -n "$unformatted" ]]; then
  echo "gofmt needed on:"
  echo "$unformatted"
  exit 1
fi

echo "==> go vet"
go vet ./...

echo "==> ui-check"
make ui-check

echo "lint: OK"
