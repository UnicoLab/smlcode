#!/usr/bin/env bash
# Run the full test suite with coverage instrumentation and fail if TOTAL
# coverage (across all packages combined) drops below the floor.
#
# The floor is today's measured total, not a per-package target — the lowest
# package is pkg/orchestrator, the 8.8k-LOC execution core, at ~42%, and it is
# tracked for follow-up rather than gated here. (pkg/mcp used to be the other
# offender at zero; it is at ~73% now.)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Measured 2026-08-23 on a clean build: total coverage is 64.5%, up from the
# 51.6% this floor was first set against. The floor sits a hair below the
# measurement to absorb rounding and package-ordering noise between runs — not
# to grant headroom to regress. Raise it with the number, never leave it
# trailing by ten points: a floor that far under reality stops being a ratchet
# and starts being decoration.
FLOOR="${COVERAGE_FLOOR:-63.0}"

OUT="$(mktemp)"
trap 'rm -f "$OUT"' EXIT

# -timeout is explicit because Go's default is 10 MINUTES PER PACKAGE and
# test/e2e is already close to it: 520s uninstrumented on this machine, and
# coverage instrumentation plus a loaded box pushes it over. The failure mode is
# the worst kind — `panic: test timed out`, indistinguishable at a glance from a
# hang in the code under test, on the gate CI runs. 30m matches the `make e2e`
# target and is a backstop against a wedged test, not a budget anyone should be
# spending; if a package ever approaches it, that package is the bug.
COVER_TIMEOUT="${COVER_TIMEOUT:-30m}"

echo "==> go test -coverprofile (this can take a while; timeout ${COVER_TIMEOUT})"
go test ./... -covermode=atomic -coverprofile="$OUT" -count=1 -timeout "$COVER_TIMEOUT"

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
