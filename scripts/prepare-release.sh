#!/usr/bin/env bash
# Prepare + tag a new SLMCode release: bumps the version everywhere, resets the
# Homebrew checksums to their placeholder, appends a changelog entry from the
# commits since the last tag, runs the full gate, then commits and tags. Push is
# left to the caller so you can review first.
#
# The GitHub release workflow (.github/workflows/release.yml) then builds the
# binaries, auto-syncs the Homebrew formula checksums and publishes the release.
# See RELEASE.md for the whole sequence.
#
# Usage:
#   scripts/prepare-release.sh <x.y.z> [--dry-run] [--no-changelog]
#   scripts/prepare-release.sh 0.14.0 --dry-run   # show changes, commit nothing
#
#   --dry-run        Print what would run; restore the working tree at the end.
#   --no-changelog   Do not generate a changelog entry. Implied when
#                    docs/changelog.md already has a "## v<version>" heading —
#                    a hand-written entry is better than a commit-subject dump
#                    and must not be shadowed by one.
set -euo pipefail

VERSION=""
DRY=0
NO_CHANGELOG=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY=1 ;;
    --no-changelog) NO_CHANGELOG=1 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) VERSION="$arg" ;;
  esac
done

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: prepare-release.sh <x.y.z> [--dry-run] [--no-changelog]" >&2
  exit 1
fi
if git tag -l "v${VERSION}" | grep -q .; then
  echo "error: tag v${VERSION} already exists" >&2
  exit 1
fi
# README.md is NOT in this list any more: its release badge is
# shields.io/github/v/release, which resolves at render time, so there is
# nothing in it to bump. The step that used to rewrite `label=vX.Y.Z` had been
# a silent no-op ever since the badge changed.
TOUCHED=(cmd/slmcode/version.go Makefile Formula/slmcode.rb docs/changelog.md)
for f in "${TOUCHED[@]}"; do
  if ! git diff --quiet -- "$f"; then
    echo "error: $f has uncommitted changes — commit or stash first" >&2
    exit 1
  fi
done

# In dry-run the edits below are made against the real tree and reverted at the
# end. A failure in `make check` used to exit before that revert and leave the
# tree half-bumped; a trap makes the restore unconditional.
if [[ "$DRY" == 1 ]]; then
  trap 'git checkout -- "${TOUCHED[@]}" 2>/dev/null || true; echo "  [dry-run] working tree restored — nothing committed"' EXIT
fi

PREV="$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)"
PREV="${PREV:-v0.0.0}"
echo "Releasing v${VERSION} (previous: ${PREV})"

run() {
  if [[ "$DRY" == 1 ]]; then
    echo "  [dry-run] $*"
  else
    "$@"
  fi
}

# 1. cmd/slmcode/version.go
perl -0pi -e 's/Version    = "[^"]*"/Version    = "'"$VERSION"'"/' cmd/slmcode/version.go

# 2. Makefile VERSION
perl -0pi -e 's/^VERSION \?= .*/VERSION ?= '"$VERSION"'/m' Makefile

# 3. Homebrew formula version, AND the checksums back to their placeholder.
#    The sha256 values are synced by scripts/update-formula.sh from the release
#    workflow once the binaries exist. Until then they cannot be right, and
#    leaving the previous release's real-looking hashes behind produces a `brew
#    install` failure indistinguishable from a tampered download. Sixty-four
#    zeros say "not synced yet" and nothing else. See Formula/slmcode.rb.
perl -0pi -e 's/^  version "[^"]*"/  version "'"$VERSION"'"/m' Formula/slmcode.rb
perl -0pi -e 's/^(\s*sha256 ")[0-9a-f]{64}"/${1}'"$(printf '0%.0s' {1..64})"'"/mg' Formula/slmcode.rb

# 4. Changelog entry from conventional commits since the previous tag — unless
#    one was written by hand, which is the norm for anything but a patch.
if grep -q "^## v${VERSION}\b" docs/changelog.md; then
  echo "changelog: docs/changelog.md already has a '## v${VERSION}' entry — keeping it"
elif [[ "$NO_CHANGELOG" == 1 ]]; then
  echo "changelog: skipped (--no-changelog)"
else
  entry="$(mktemp)"
  {
    echo "## v${VERSION} — $(date -u +%Y-%m-%d)"
    echo ""
    git --no-pager log --oneline --no-merges "${PREV}..HEAD" | awk '{
      $1 = ""
      sub(/^ /, "")
      sub(/^(feat|fix|chore|docs|refactor|test|ci|build|perf)(\([^)]*\))?: /, "")
      print "- " toupper(substr($0, 1, 1)) substr($0, 2)
    }'
    echo ""
  } > "$entry"
  ENTRY_FILE="$entry" perl -0pi -e 'my $entry = do { local $/; open my $fh, "<", $ENV{ENTRY_FILE} or die $!; <$fh> };
    s/(# Changelog\n\n)/$1$entry/' docs/changelog.md
  rm -f "$entry"
  echo "changelog: generated an entry from ${PREV}..HEAD — EDIT IT before pushing"
fi

# "Nothing changed" is a legitimate state, not an error: the version bump and a
# hand-written changelog entry are often committed as part of the release-prep
# work itself, and by the time this runs the tree is already AT the version
# being cut. That is a tag-only release, and the gate below still has to pass
# before the tag exists. The old hard failure here forced the operator to bypass
# this script entirely — and therefore to skip the gate — in exactly the case
# where a large release most needed it.
changed="$(git diff --name-only -- "${TOUCHED[@]}")"
if [[ -z "$changed" ]]; then
  echo "No file changes needed — the tree is already at v${VERSION}."
  echo "Proceeding as a tag-only release (the gate still runs)."
else
  echo "Changed files:"
  git --no-pager diff --stat -- "${TOUCHED[@]}"
fi

# The version must now agree everywhere, including with the tag about to be
# created. Cheap, and it is the exact check the release workflow re-runs before
# it spends 40 minutes building artifacts named after that tag.
./scripts/check-version.sh --tag "v${VERSION}"
./scripts/check-repo-refs.sh

# The one gate, not two thirds of it: `make check` is what CONTRIBUTING tells
# contributors to run and what CI runs, and it degrades with a named skip where
# a step genuinely cannot run here.
run make check

if [[ -n "$changed" ]]; then
  run git add "${TOUCHED[@]}"
  run git commit -m "chore: release v${VERSION}"
else
  echo "  (no release commit — nothing to commit)"
fi
run git tag "v${VERSION}"

if [[ "$DRY" == 1 ]]; then
  exit 0   # the EXIT trap restores the tree
fi

echo ""
echo "Next steps:"
echo "  git push origin main && git push origin v${VERSION}"
echo "The release workflow builds binaries, syncs the Homebrew formula checksums"
echo "and publishes the GitHub release automatically. See RELEASE.md."
