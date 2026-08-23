#!/usr/bin/env bash
# Sync Formula/slmcode.rb with the actual release-binary checksums.
#
# Called by .github/workflows/release.yml after building the release binaries,
# so the Homebrew formula on main always matches the published assets.
#
# The formula's URLs use the "#{version}" Homebrew template (not a literal
# version), so the sha256 lines are matched via that template + asset suffix.
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

# 1. Bump the version line.
# 2. Replace each sha256 by matching the "#{version}" URL template that
#    precedes it (e.g. slmcode_#{version}_darwin_arm64"). The match consumes
#    the closing quote of the old value, so the replacement re-emits it.
perl -0pe '
  s/^  version "[^"]*"/  version "'"$VERSION"'"/m;
  s/(slmcode_#\{version\}_darwin_arm64"\n\s*sha256 ")[^"]*"/${1}'"$DARWIN_ARM64"'"/;
  s/(slmcode_#\{version\}_darwin_amd64"\n\s*sha256 ")[^"]*"/${1}'"$DARWIN_AMD64"'"/;
  s/(slmcode_#\{version\}_linux_arm64"\n\s*sha256 ")[^"]*"/${1}'"$LINUX_ARM64"'"/;
  s/(slmcode_#\{version\}_linux_amd64"\n\s*sha256 ")[^"]*"/${1}'"$LINUX_AMD64"'"/;
' "$FORMULA" > "$tmp"

# Sanity: every expected checksum must now appear in the output in its full
# quoted form (sha256 "<hex>") — catches both missing replacements and
# mangled quoting.
for want in "$DARWIN_ARM64" "$DARWIN_AMD64" "$LINUX_ARM64" "$LINUX_AMD64"; do
  grep -q "sha256 \"$want\"" "$tmp" || { echo "error: checksum $want not written to formula" >&2; exit 1; }
done

# No placeholder may survive. Between a version bump and this sync the formula
# carries all-zero sha256 values (see Formula/slmcode.rb for why zeros and not
# the previous release's real hashes); if any is still there, a substitution
# above silently did not match and Homebrew would be left unable to install.
if grep -n 'sha256 "0\{64\}"' "$tmp"; then
  echo "error: a placeholder sha256 survived the sync — the URL templates in $FORMULA no longer match the patterns in this script" >&2
  exit 1
fi

# And the version line must now be the one we were asked for, or the "#{version}"
# templates in every url resolve to the wrong release.
if ! grep -q "^  version \"$VERSION\"" "$tmp"; then
  echo "error: $FORMULA version line was not set to $VERSION" >&2
  exit 1
fi

mv "$tmp" "$FORMULA"

echo "✔ Formula/slmcode.rb synced for v${VERSION}"
echo "  darwin_arm64  $DARWIN_ARM64"
echo "  darwin_amd64  $DARWIN_AMD64"
echo "  linux_arm64   $LINUX_ARM64"
echo "  linux_amd64   $LINUX_AMD64"
