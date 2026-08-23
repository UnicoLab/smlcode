#!/usr/bin/env bash
# Guard against reintroducing the broken "UnicoLab/slmcode" GitHub repo slug
# in install/download URLs. The real GitHub repo is UnicoLab/smlcode; a
# handful of raw.githubusercontent.com / github.com links historically used
# the wrong (swapped-letters) "slmcode" slug and 404'd.
#
# One legitimate exception: the Go module itself really is named
# "github.com/UnicoLab/slmcode" (see go.mod, which this repo's ownership
# rules keep off-limits for changes here), so `import "github.com/UnicoLab/
# slmcode/..."` lines in docs are correct code, not a broken URL, and are
# excluded below.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

matches="$(grep -rn --exclude=check-repo-refs.sh 'UnicoLab/slmcode' .github/ scripts/ README.md docs/ 2>/dev/null \
  | grep -v 'import "github.com/UnicoLab/slmcode' \
  || true)"

if [[ -n "$matches" ]]; then
  echo "ERROR: found stale/broken 'UnicoLab/slmcode' GitHub references (the real repo is UnicoLab/smlcode):" >&2
  echo "$matches" >&2
  exit 1
fi

echo "check-repo-refs: OK"
