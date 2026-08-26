#!/usr/bin/env bash
# Refresh prebuilt/ — the macOS binaries carried inside the repository itself.
#
# WHY THIS EXISTS. Every other install path needs something the network can
# refuse: Homebrew needs to update itself, `install.sh` needs a Go toolchain,
# `install-remote.sh` needs api.github.com plus a release-asset download from
# objects.githubusercontent.com. On a locked-down corporate machine all three
# come back 403 while `git clone` keeps working, because the proxy allowlists
# the git endpoint and nothing else. prebuilt/ makes the clone itself the
# delivery mechanism: the binary arrives with the source, and
# scripts/install-offline.sh puts it on PATH without touching the network.
#
# The checksums written here are of the UNCOMPRESSED binaries, under their
# release asset names, so a line in prebuilt/SHA256SUMS is byte-identical to
# the matching line in the published release SHA256SUMS. Anyone who can reach
# the release page can compare the two without decompressing anything.
#
# Called by:
#   make prebuilt                      (from a local checkout, after a build)
#   .github/workflows/release.yml      (on main, right after a release is cut)
#
# Usage:
#   scripts/update-prebuilt.sh <version> [dist-dir]
#   scripts/update-prebuilt.sh 0.19.1 dist
#
# Env:
#   PREBUILT_PLATFORMS   space-separated os/arch list (default: the two macOS
#                        targets). Adding linux/* here costs ~10 MiB of git
#                        history per platform per release — see prebuilt/README.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-}"
DIST="${2:-dist}"
OUT="prebuilt"
PLATFORMS="${PREBUILT_PLATFORMS:-darwin/arm64 darwin/amd64}"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: update-prebuilt.sh <x.y.z> [dist-dir]" >&2
  exit 2
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "error: neither sha256sum nor shasum is available" >&2
    exit 1
  fi
}

mkdir -p "$OUT"

# One version at a time. Without this the directory accumulates every release
# ever cut and a fresh clone downloads all of them; the installer also has to
# guess which one you meant. Old blobs stay in git history either way — that is
# the cost of this distribution channel and it is documented in the README.
find "$OUT" -maxdepth 1 -name 'slmcode_*' -type f -delete

tmp_sums="$(mktemp)"
trap 'rm -f "$tmp_sums"' EXIT

count=0
for platform in $PLATFORMS; do
  goos="${platform%%/*}"
  goarch="${platform##*/}"
  asset="slmcode_${VERSION}_${goos}_${goarch}"
  src="${DIST}/${asset}"
  if [[ ! -f "$src" ]]; then
    echo "error: ${src} not found — build it first (make release-binaries)" >&2
    exit 1
  fi

  # Checksum the binary, not the archive: this is the artifact that ends up on
  # PATH, and hashing it here means the line below can be diffed straight
  # against the published release SHA256SUMS.
  printf '%s  %s\n' "$(sha256 "$src")" "$asset" >> "$tmp_sums"

  # -n omits the source filename and mtime from the gzip header, so rebuilding
  # an unchanged binary produces an identical archive and git records no diff.
  gzip -9 -n -c "$src" > "${OUT}/${asset}.gz"
  echo "→ ${OUT}/${asset}.gz ($(du -h "${OUT}/${asset}.gz" | cut -f1))"
  count=$((count + 1))
done

if [[ "$count" -eq 0 ]]; then
  echo "error: PREBUILT_PLATFORMS is empty — nothing to publish" >&2
  exit 1
fi

sort -k2 "$tmp_sums" > "${OUT}/SHA256SUMS"
printf '%s\n' "$VERSION" > "${OUT}/VERSION"

echo
echo "prebuilt/ now carries SLMCode ${VERSION} for: ${PLATFORMS}"
cat "${OUT}/SHA256SUMS"
