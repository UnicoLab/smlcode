#!/usr/bin/env bash
# Smoke-test the go:embed'ed Studio UI directory (cmd/slmcode/ui/).
#
# Single source of truth for `make ui-check` and the ui-check step of
# scripts/lint.sh — they used to carry two copies of this logic and drifted.
#
# cmd/slmcode/ui/ has exactly one tracked file: .gitkeep. It exists so the
# directory is present on a fresh clone and `//go:embed all:ui` has something
# to embed (the `all:` prefix is what makes a dotfile count). Everything the
# Vite build writes there — index.html, assets/, vendor/ — is gitignored
# BUILD OUTPUT, never tracked: `make ui-react` used to overwrite a tracked
# cmd/slmcode/ui/index.html, so every developer who built Studio had a
# permanently dirty tree and could commit a machine-specific bundle reference.
#
# Two states are valid:
#   built       — index.html + assets/ present (the real React SPA is embedded)
#   placeholder — neither present (the binary serves the built-in placeholder
#                 page from pkg/server; run `make bootstrap` for the real UI)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

UI_DIR="cmd/slmcode/ui"

if [[ ! -f "$UI_DIR/.gitkeep" ]]; then
  echo "ERROR: $UI_DIR/.gitkeep is missing." >&2
  echo "       It is the one tracked file in $UI_DIR — without it the directory can" >&2
  echo "       disappear on a fresh clone and '//go:embed all:ui' fails to compile." >&2
  echo "       Fix: mkdir -p $UI_DIR && touch $UI_DIR/.gitkeep" >&2
  exit 1
fi

if [[ -f "$UI_DIR/index.html" && -d "$UI_DIR/assets" ]]; then
  if ! grep -q 'id="root"' "$UI_DIR/index.html"; then
    echo "ERROR: $UI_DIR/index.html is not a Vite build of web/ (no <div id=\"root\">)." >&2
    echo "       Fix: rm -rf $UI_DIR/index.html $UI_DIR/assets $UI_DIR/vendor && make bootstrap" >&2
    exit 1
  fi
  echo "ui-check: OK (built React Studio UI present — embedded by go:embed all:ui)"
  exit 0
fi

if [[ -f "$UI_DIR/index.html" ]]; then
  echo "ERROR: $UI_DIR/index.html exists but $UI_DIR/assets/ does not." >&2
  echo "       That is a half-built UI: the HTML shell would load and then ask the" >&2
  echo "       browser for /assets/*.js that are not there — a blank screen." >&2
  echo "       Most likely it is the old checked-in placeholder, which is no longer" >&2
  echo "       tracked (the placeholder now lives in Go source, pkg/server)." >&2
  echo "       Fix: rm -f $UI_DIR/index.html   # then, for the real UI: make bootstrap" >&2
  exit 1
fi

if [[ -d "$UI_DIR/assets" ]]; then
  echo "ERROR: $UI_DIR/assets/ exists but $UI_DIR/index.html does not." >&2
  echo "       Fix: rm -rf $UI_DIR/assets $UI_DIR/vendor && make bootstrap" >&2
  exit 1
fi

echo "ui-check: OK (no built UI — the binary serves the built-in placeholder page;"
echo "          run 'make bootstrap' to build the real Studio UI into it)"
