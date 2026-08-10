#!/usr/bin/env bash
# Prepare + tag a new SLMCode release: bumps the version everywhere, appends a
# changelog entry from the commits since the last tag, runs lint + tests, then
# commits and tags. Push is left to the caller so you can review first.
#
# The GitHub release workflow (.github/workflows/release.yml) then builds the
# binaries, auto-syncs the Homebrew formula checksums and publishes the release.
#
# Usage:
#   scripts/prepare-release.sh <x.y.z> [--dry-run]
#   scripts/prepare-release.sh 0.14.0 --dry-run   # show changes, commit nothing
set -euo pipefail

VERSION="${1:-}"
DRY=0
[[ "${2:-}" == "--dry-run" ]] && DRY=1

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: prepare-release.sh <x.y.z> [--dry-run]" >&2
  exit 1
fi
if git tag -l "v${VERSION}" | grep -q .; then
  echo "error: tag v${VERSION} already exists" >&2
  exit 1
fi
for f in cmd/slmcode/version.go Makefile README.md docs/changelog.md; do
  if ! git diff --quiet -- "$f"; then
    echo "error: $f has uncommitted changes — commit or stash first" >&2
    exit 1
  fi
done

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

# 3. README release badge
perl -0pi -e 's/label=v[0-9]+\.[0-9]+\.[0-9]+/label=v'"$VERSION"'/' README.md

# 4. Changelog entry from conventional commits since the previous tag.
{
  echo "## v${VERSION} — $(date -u +%Y-%m-%d)"
  echo ""
  git --no-pager log --oneline --no-merges "${PREV}..HEAD" | awk '{
    $1 = ""
    sub(/^ /, "")
    sub(/^(feat|fix|chore|docs|refactor|test|ci|build|perf)(\([^)]*\))?: /, "")
    print "- " toupper(substr($0, 1, 1)) substr($0, 2)
  }'
} > /tmp/slmcode-changelog-entry.txt

perl -0pi -e 'my $entry = do { local $/; open my $fh, "<", "/tmp/slmcode-changelog-entry.txt" or die $!; <$fh> };
  s/(# Changelog\n\n)/$1$entry/' docs/changelog.md

changed="$(git diff --name-only -- cmd/slmcode/version.go Makefile README.md docs/changelog.md)"
if [[ -z "$changed" ]]; then
  echo "error: no changes produced — nothing to release" >&2
  exit 1
fi

echo "Changed files:"
git --no-pager diff --stat -- cmd/slmcode/version.go Makefile README.md docs/changelog.md

run make lint
run make test

run git add cmd/slmcode/version.go Makefile README.md docs/changelog.md
run git commit -m "chore: release v${VERSION}"
run git tag "v${VERSION}"

if [[ "$DRY" == 1 ]]; then
  git checkout -- cmd/slmcode/version.go Makefile README.md docs/changelog.md
  echo "  [dry-run] working tree restored — nothing committed"
fi

echo ""
echo "Next steps:"
echo "  git push origin main && git push origin v${VERSION}"
echo "The release workflow builds binaries, syncs the Homebrew formula checksums"
echo "and publishes the GitHub release automatically."
