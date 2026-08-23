#!/usr/bin/env bash
# Run the full test suite with coverage instrumentation and fail if TOTAL
# coverage (across all packages combined) drops below the floor.
#
# The floor is today's measured total, not a per-package target — several
# packages sit well below it (pkg/mcp's client.go, which spawns
# subprocesses, has zero tests; pkg/orchestrator is the 8.8k-LOC execution
# core at ~38%) and are tracked separately for follow-up, not gated here.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Measured 2026-08-23 on a clean build: total coverage was 51.6%. Floor set
# a hair below that measurement to absorb harmless rounding/ordering noise
# between runs, not to grant real headroom to regress.
FLOOR="${COVERAGE_FLOOR:-51.0}"

OUT="$(mktemp)"
trap 'rm -f "$OUT"' EXIT

echo "==> go test -coverprofile (this can take a while)"
go test ./... -covermode=atomic -coverprofile="$OUT" -count=1

TOTAL_LINE="$(go tool cover -func="$OUT" | tail -1)"
# Example: "total:                                          (statements)   51.6%"
PCT="$(echo "$TOTAL_LINE" | awk '{print $NF}' | tr -d '%')"

if [[ -z "$PCT" ]]; then
  echo "ERROR: could not parse total coverage from: $TOTAL_LINE" >&2
  exit 1
fi

echo "Total coverage: ${PCT}% (floor: ${FLOOR}%)"

below="$(awk -v p="$PCT" -v f="$FLOOR" 'BEGIN { print (p < f) ? "1" : "0" }')"
if [[ "$below" == "1" ]]; then
  echo "ERROR: total coverage ${PCT}% is below the floor of ${FLOOR}%." >&2
  echo "If this drop is intentional, update COVERAGE_FLOOR in scripts/coverage-check.sh." >&2
  exit 1
fi

echo "cover: OK"
