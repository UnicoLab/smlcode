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
# It also gates the Homebrew formula's four sha256 values against the version
# line above them — the one pair here that a *clean* rebase can silently pull
# apart. See the long note at that check.
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
      sed -n '2,28p' "$0"
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

# --- the Homebrew formula's checksum provenance ---------------------------
# A GATE, not a report. Exactly two sha256 states are legitimate; every other
# one leaves `brew install` broken:
#
#   sha256 "0000…0"           unsynced. The binaries for this version do not
#                             exist yet, so no real checksum can. Valid between
#                             the version bump and the release workflow's
#                             update-formula.sh step, and only then.
#   sha256 "<hex>" # vX.Y.Z   synced — and X.Y.Z MUST be the version this
#                             formula declares.
#
# That trailing "# vX.Y.Z" is written by scripts/update-formula.sh, and it
# exists for exactly one reason: to make a checksum carry, ON ITS OWN LINE, the
# version it was computed for.
#
# On 2026-08-25 the v0.18.4 release commit was rebased onto the release bot's
# "sync Homebrew formula checksums for v0.18.3" commit. It did not conflict:
# the release commit only moved the version line, because the placeholders it
# writes were ALREADY placeholders in its parent, so the sha256 lines were not
# in its diff at all. Git replayed the version bump straight onto v0.18.3's
# real digests and produced `version "0.18.4"` carrying v0.18.3's hashes. The
# shape checks below all passed. It was caught by counting placeholders by hand.
#
# The label defeats that because git resolves a merge or rebase line by line,
# and a label on the same line as the digest cannot be separated from it: any
# resolution that takes v0.18.3's checksum takes "# v0.18.3" with it, and this
# check then has both halves in front of it. The two alternatives considered
# both miss this case — a marker on its own line is just another hunk for the
# merge to resolve independently, and inferring "a release is in flight" from
# `git tag` is unavailable in CI, whose repo-refs job checks out at depth 1
# with no tags, i.e. precisely where this must run on every push.
sha_lines="$(grep -n '^[[:space:]]*sha256 ' Formula/slmcode.rb || true)"
sha_count="$(grep -c '^[[:space:]]*sha256 ' Formula/slmcode.rb || true)"
if [[ "$sha_count" -ne 4 ]]; then
  err "Formula/slmcode.rb has $sha_count sha256 lines, expected 4 (darwin arm64/amd64, linux arm64/amd64)"
fi

sha_re='^[[:space:]]*sha256 "([0-9a-f]{64})"(.*)$'
label_re='^[[:space:]]+#[[:space:]]+v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)[[:space:]]*$'
PLACEHOLDER="$(printf '0%.0s' {1..64})"
placeholders=0
synced=0
mismatched=""   # a real digest labelled for some OTHER release
malformed=""    # neither shape: no label, a stray label, bad hex, a labelled placeholder
while IFS=: read -r lineno text; do
  [[ -n "$lineno" ]] || continue
  if [[ ! "$text" =~ $sha_re ]]; then
    malformed+="Formula/slmcode.rb:${lineno}:${text}"$'\n'
    continue
  fi
  digest="${BASH_REMATCH[1]}"
  rest="${BASH_REMATCH[2]}"
  if [[ "$digest" == "$PLACEHOLDER" ]]; then
    # A placeholder must be bare. Labelled zeros would otherwise read as
    # "synced" below and this script would report a formula that installs
    # nothing as fully in sync.
    if [[ -n "${rest//[[:space:]]/}" ]]; then
      malformed+="Formula/slmcode.rb:${lineno}: unsynced placeholder carrying a label —${rest}"$'\n'
    else
      placeholders=$((placeholders + 1))
    fi
  elif [[ "$rest" =~ $label_re ]]; then
    if [[ "${BASH_REMATCH[1]}" == "$VERSION_GO" ]]; then
      synced=$((synced + 1))
    else
      mismatched+="Formula/slmcode.rb:${lineno}: synced for v${BASH_REMATCH[1]}"$'\n'
    fi
  else
    malformed+="Formula/slmcode.rb:${lineno}:${text}"$'\n'
  fi
done <<< "$sha_lines"

if [[ -n "$mismatched" ]]; then
  err "Formula/slmcode.rb declares version ${VERSION_GO}, but these sha256 values were computed for a different release:"
  printf '%s' "$mismatched" | sed 's/^/  /' >&2
  echo "       \`brew install\` would fetch the ${VERSION_GO} assets and check them against another release's" >&2
  echo "       digests: a mismatch indistinguishable from a tampered download. A rebase or merge has mixed" >&2
  echo "       two releases. Reset all four sha256 values to 64 zeros — scripts/prepare-release.sh does this —" >&2
  echo "       and let the release workflow's update-formula.sh step re-sync them." >&2
fi
if [[ -n "$malformed" ]]; then
  err "Formula/slmcode.rb has sha256 lines that are neither a bare unsynced placeholder (64 zeros)"
  echo "       nor a synced checksum (64 lowercase hex followed by '# v${VERSION_GO}'):" >&2
  printf '%s' "$malformed" | sed 's/^/  /' >&2
fi

# All four are written by one update-formula.sh run and zeroed by one
# prepare-release.sh run, so a split is not a state either script can produce —
# it means someone hand-edited a subset, and the platforms still on a
# placeholder cannot install.
if [[ "$placeholders" -gt 0 && "$synced" -gt 0 ]]; then
  err "Formula/slmcode.rb mixes ${placeholders} unsynced placeholder(s) with ${synced} synced checksum(s) — all four move together or none do"
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "check-version: OK — ${VERSION_GO} in cmd/slmcode/version.go, Makefile and Formula/slmcode.rb${TAG:+, matching tag ${TAG}}"
if [[ "$placeholders" -gt 0 ]]; then
  echo "check-version: Formula/slmcode.rb carries ${placeholders}/4 placeholder checksums —"
  echo "               the release workflow's update-formula.sh step fills them in."
fi
