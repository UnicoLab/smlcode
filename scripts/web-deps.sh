#!/usr/bin/env bash
# Get web/node_modules into a usable state — the one place that knows how.
#
# Single source of truth for `make web-deps` (which every target that runs a
# web/package.json script depends on) and for `make web-check`, which calls it
# directly rather than as a prerequisite so that "npm cannot reach the registry"
# can be a SKIP instead of aborting the whole gate.
#
# Why this exists at all: `make bootstrap` used to short-circuit on
# cmd/slmcode/ui/assets/ already existing and so never installed or refreshed
# anything. A months-old node_modules stayed months old, and the first sign of
# it was `tsc` failing with 21 x TS2307 "Cannot find module 'vitest'" — a
# message about the wrong thing entirely.
#
# Exit codes: 0 deps are ready · 1 they are not (message on stderr).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

STAMP="web/node_modules/.slmcode-deps.stamp"

if ! command -v npm >/dev/null 2>&1; then
  echo "ERROR: npm is not on PATH." >&2
  echo "  The Studio UI in web/ is a React 18 + Vite + TypeScript app and needs Node.js 18+." >&2
  echo "  Install Node (https://nodejs.org, 'brew install node', or nvm), then: make bootstrap" >&2
  echo "  Everything else — the CLI, the TUI, the Studio API — builds without Node." >&2
  exit 1
fi

# Fresh enough? node_modules exists, we installed it ourselves, and
# package.json has not changed since. Cheap, so every target can depend on it.
if [[ -d web/node_modules && -f "$STAMP" && ! web/package.json -nt "$STAMP" ]]; then
  echo "web-deps: up to date (web/node_modules is newer than web/package.json)"
  exit 0
fi

echo "==> installing web/ dependencies"

installed=0
if [[ -f web/package-lock.json ]]; then
  echo "    npm ci"
  if ( cd web && npm ci ); then
    installed=1
  else
    cat <<'EOF'

    'npm ci' failed. It installs strictly from package-lock.json and refuses to
    run at all when the lock does not match package.json — which is the case in
    this tree: package.json gained vitest, @testing-library/*, eslint and the
    rest of the test toolchain that package-lock.json predates.

    Falling back to 'npm install', which resolves from package.json and
    REWRITES web/package-lock.json.
    >> Commit the regenerated web/package-lock.json. That is the actual fix, and
    >> it is what puts CI and every other clone back on the faster 'npm ci'.

EOF
  fi
else
  echo "    no web/package-lock.json — using 'npm install' (it writes one; commit it)"
fi

if [[ "$installed" -eq 0 ]]; then
  if ! ( cd web && npm install ); then
    echo "ERROR: installing web/ dependencies failed." >&2
    echo "  Both 'npm ci' and 'npm install' failed. Usual causes: the npm registry is" >&2
    echo "  unreachable (offline, proxy, or an egress allowlist), or a dependency in" >&2
    echo "  web/package.json cannot be resolved." >&2
    echo "  The Go build does not need any of this: 'make build' still works and the" >&2
    echo "  binary serves the built-in placeholder page instead of the Studio SPA." >&2
    exit 1
  fi
fi

mkdir -p web/node_modules
touch "$STAMP"
echo "web-deps: OK"
