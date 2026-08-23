#!/usr/bin/env bash
# Fail when the release version has drifted between the places that declare it.
#
# The version is written down in four places that must agree, and each one is
# load-bearing in a different way:
#
#   cmd/slmcode/version.go   the fallback compiled into the binary when a build
#                            carries no -ldflags at all (`go install`, `go run`)
#   Makefile   VERSION ?=    what -ldflags stamps for every local build
#   Formula/slmcode.rb       what Homebrew resolves #{version} to inside every
#                            release-asset URL — a wrong value here 404s
#   the git tag              what the release workflow puts in every artifact
#                            FILENAME and stamps into every binary
#
# Drift between the first three is caught by TestVersionMetadata too, but only
# when the Go test suite runs from cmd/slmcode; this is the cheap standalone
# form CI's guard job and the release workflow both call, and it is the only
# one that can compare against the TAG — the one input that does not exist
# until the release is being cut.
#
# Usage:
#   scripts/check-version.sh                 # the three files must agree
#   scripts/check-version.sh --tag v0.17.0   # …and match the tag being released
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TAG=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag) TAG="${2:-}"; shift 2 ;;
    -h|--help)
      sed -n '2,25p' "$0"
      exit 0
      ;;
    *)
      echo "usage: check-version.sh [--tag vX.Y.Z]" >&2
      exit 2
      ;;
  esac
done

fail=0
err() {
  echo "ERROR: $*" >&2
  fail=1
}

# --- the source of truth --------------------------------------------------
VERSION_GO="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' cmd/slmcode/version.go | head -1)"
if [[ -z "$VERSION_GO" ]]; then
  echo "ERROR: could not read Version from cmd/slmcode/version.go" >&2
  exit 1
fi
if [[ ! "$VERSION_GO" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  err "cmd/slmcode/version.go declares a non-semver Version: '$VERSION_GO'"
fi

# --- the other declarations ------------------------------------------------
MAKE_VERSION="$(sed -n 's/^VERSION ?= *\(.*\)$/\1/p' Makefile | head -1)"
[[ -n "$MAKE_VERSION" ]] || err "Makefile has no 'VERSION ?= …' line"
[[ "$MAKE_VERSION" == "$VERSION_GO" ]] || \
  err "Makefile declares VERSION '$MAKE_VERSION' but cmd/slmcode/version.go says '$VERSION_GO'"

FORMULA_VERSION="$(sed -n 's/^  version "\([^"]*\)".*/\1/p' Formula/slmcode.rb | head -1)"
[[ -n "$FORMULA_VERSION" ]] || err "Formula/slmcode.rb has no 'version \"…\"' line"
[[ "$FORMULA_VERSION" == "$VERSION_GO" ]] || \
  err "Formula/slmcode.rb declares version '$FORMULA_VERSION' but cmd/slmcode/version.go says '$VERSION_GO'"

# --- the tag, when one is being cut ---------------------------------------
if [[ -n "$TAG" ]]; then
  TAG_VERSION="${TAG#v}"
  [[ "$TAG_VERSION" == "$VERSION_GO" ]] || \
    err "tag '$TAG' does not match cmd/slmcode/version.go ('$VERSION_GO'). Every published artifact would be named for '$TAG_VERSION' and report '$VERSION_GO'. Run: scripts/prepare-release.sh $TAG_VERSION"
fi

# --- hardcoded release-asset names in docs and scripts --------------------
# A literal, fully-numbered asset name in a doc (the pattern below) is a
# download instruction that 404s the moment the version moves. Templated
# forms (#{version}, ${VERSION}, <version>) are the correct way to write
# these and are ignored.
stale_assets="$(grep -rnE 'slmcode_[0-9]+\.[0-9]+\.[0-9]+_' \
  --exclude=check-version.sh \
  --include='*.md' --include='*.sh' --include='*.rb' --include='*.ps1' --include='*.yml' \
  docs/ scripts/ Formula/ .github/ README.md RELEASE.md 2>/dev/null \
  | grep -v "slmcode_${VERSION_GO}_" || true)"
if [[ -n "$stale_assets" ]]; then
  err "hardcoded release-asset names for a version other than ${VERSION_GO}:"
  echo "$stale_assets" >&2
fi

# Same idea for a pinned download URL: .../releases/download/vX.Y.Z/… stops
# resolving the moment X.Y.Z is not the current release. Formula/slmcode.rb is
# exempt — its URLs use the "v#{version}" Homebrew template, which is the
# correct form and never matches this pattern anyway.
stale_urls="$(grep -rnE 'releases/download/v[0-9]+\.[0-9]+\.[0-9]+/' \
  --exclude=check-version.sh \
  --include='*.md' --include='*.sh' --include='*.rb' --include='*.ps1' --include='*.yml' \
  docs/ scripts/ Formula/ .github/ README.md RELEASE.md 2>/dev/null \
  | grep -v "releases/download/v${VERSION_GO}/" || true)"
if [[ -n "$stale_urls" ]]; then
  err "hardcoded release-download URLs for a version other than ${VERSION_GO}:"
  echo "$stale_urls" >&2
fi

# --- the Homebrew formula's checksum state --------------------------------
# Between a version bump and the post-release sync, the sha256 values in the
# committed formula MUST be the all-zero placeholder: an old-but-real-looking
# hash makes `brew install` fail with a mismatch that is indistinguishable
# from a tampered download. See Formula/slmcode.rb and scripts/update-formula.sh.
sha_count="$(grep -c '^ *sha256 "' Formula/slmcode.rb || true)"
if [[ "$sha_count" -ne 4 ]]; then
  err "Formula/slmcode.rb has $sha_count sha256 lines, expected 4 (darwin arm64/amd64, linux arm64/amd64)"
fi
bad_sha="$(grep -nE '^ *sha256 "' Formula/slmcode.rb | grep -vE 'sha256 "([0-9a-f]{64})"' || true)"
if [[ -n "$bad_sha" ]]; then
  err "Formula/slmcode.rb has a sha256 that is not 64 lowercase hex characters:"
  echo "$bad_sha" >&2
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

zeros="$(grep -c 'sha256 "0\{64\}"' Formula/slmcode.rb || true)"
echo "check-version: OK — ${VERSION_GO} in cmd/slmcode/version.go, Makefile and Formula/slmcode.rb${TAG:+, matching tag ${TAG}}"
if [[ "$zeros" -gt 0 ]]; then
  echo "check-version: Formula/slmcode.rb carries ${zeros}/4 placeholder checksums —"
  echo "               the release workflow's update-formula.sh step fills them in."
fi
