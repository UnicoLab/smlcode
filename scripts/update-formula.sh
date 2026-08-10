#!/usr/bin/env bash
# Sync Formula/slmcode.rb with the actual release-binary checksums.
#
# Called by .github/workflows/release.yml after building the release binaries,
# so the Homebrew formula on main always matches the published assets.
#
# Usage:
#   scripts/update-formula.sh <version> <dist-dir>
#   scripts/update-formula.sh 0.13.1 dist
set -euo pipefail

VERSION="${1:-}"
DIST="${2:-dist}"
FORMULA="Formula/slmcode.rb"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: update-formula.sh <x.y.z> <dist-dir>" >&2
  exit 1
fi
if [[ ! -f "$FORMULA" ]]; then
  echo "error: $FORMULA not found (run from the repo root)" >&2
  exit 1
fi

sha() {
  local asset="slmcode_${VERSION}_$1"
  if [[ ! -f "$DIST/$asset" ]]; then
    echo "error: $DIST/$asset not found" >&2
    exit 1
  fi
  shasum -a 256 "$DIST/$asset" | awk '{print $1}'
}

# Compute all checksums up front so a missing asset fails before we touch the formula.
DARWIN_ARM64="$(sha darwin_arm64)"
DARWIN_AMD64="$(sha darwin_amd64)"
LINUX_ARM64="$(sha linux_arm64)"
LINUX_AMD64="$(sha linux_amd64)"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

# Patch the version line + the four sha256 lines. Each sha256 line is matched
# via the URL that precedes it, so the right asset always gets its own checksum.
perl -0pe '
  s/^  version "[^"]*"/  version "'"$VERSION"'"/m;
  s/(slmcode_'"$VERSION"'_darwin_arm64"\n\s*sha256 ")[^"]*"/${1}'"$DARWIN_ARM64"'/;
  s/(slmcode_'"$VERSION"'_darwin_amd64"\n\s*sha256 ")[^"]*"/${1}'"$DARWIN_AMD64"'/;
  s/(slmcode_'"$VERSION"'_linux_arm64"\n\s*sha256 ")[^"]*"/${1}'"$LINUX_ARM64"'/;
  s/(slmcode_'"$VERSION"'_linux_amd64"\n\s*sha256 ")[^"]*"/${1}'"$LINUX_AMD64"'/;
' "$FORMULA" > "$tmp"

mv "$tmp" "$FORMULA"

echo "✔ Formula/slmcode.rb synced for v${VERSION}"
echo "  darwin_arm64  $DARWIN_ARM64"
echo "  darwin_amd64  $DARWIN_AMD64"
echo "  linux_arm64   $LINUX_ARM64"
echo "  linux_amd64   $LINUX_AMD64"
