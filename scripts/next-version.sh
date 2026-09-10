#!/usr/bin/env bash
# Choose the next SLMCode version from the conventional commits since the last
# release tag, so nobody has to decide it by hand or remember what "minor" meant
# for this batch.
#
# Prints the bare version ("0.25.0") on stdout and nothing else, so it can be
# used as `scripts/prepare-release.sh "$(scripts/next-version.sh)"`. Everything
# explanatory goes to stderr, so a caller capturing stdout still gets a clean
# version. Used by prepare-release.sh's `auto` argument.
#
# Usage:
#   scripts/next-version.sh [--bump auto|patch|minor|major] [--base <tag>] [--explain]
#
#   --bump    auto (default) reads the commits; patch/minor/major force a level.
#   --base    Compare against this tag instead of the latest vX.Y.Z.
#   --explain Write the reasoning to stderr (what drove the level, and why).
#
# The rules are the Conventional Commits ones:
#   "<type>!:" or a "BREAKING CHANGE:" body trailer  -> major
#   "feat:"                                          -> minor
#   anything else                                    -> patch
#
# Commits that are not conventional at all — a squashed PR title, say — are
# counted but drive nothing. They cannot: guessing a major bump out of an
# unparseable subject is how an accidental 1.0.0 happens.
set -euo pipefail

BUMP="auto"
BASE=""
EXPLAIN=0
# A 0.x line has no stable API to break, so semver keeps breaking changes inside
# the minor slot until someone deliberately cuts 1.0.0. Without this, the first
# "feat!:" after 0.24.0 would silently publish 1.0.0 — a number that promises
# stability nobody agreed to. Cutting 1.0.0 stays a `--bump major` decision made
# by a person, on a 1.x line where it is the ordinary meaning of major.
ALLOW_ZERO_MAJOR=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bump) BUMP="${2:-}"; shift 2 ;;
    --bump=*) BUMP="${1#*=}"; shift ;;
    --base) BASE="${2:-}"; shift 2 ;;
    --base=*) BASE="${1#*=}"; shift ;;
    --explain) EXPLAIN=1; shift ;;
    --allow-zero-major) ALLOW_ZERO_MAJOR=1; shift ;;
    -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

case "$BUMP" in
  auto|patch|minor|major) ;;
  *) echo "error: --bump must be auto, patch, minor or major (got '${BUMP}')" >&2; exit 2 ;;
esac

note() { [[ "$EXPLAIN" == 1 ]] && echo "$*" >&2 || true; }

if [[ -z "$BASE" ]]; then
  BASE="$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)"
fi
if [[ -z "$BASE" ]]; then
  # A repository with no release tag yet: everything so far is the first release.
  BASE_VERSION="0.0.0"
  RANGE="HEAD"
  note "no vX.Y.Z tag found — treating every commit as the first release"
else
  if ! git rev-parse -q --verify "refs/tags/${BASE}^{commit}" >/dev/null; then
    echo "error: base tag '${BASE}' does not exist" >&2
    exit 1
  fi
  BASE_VERSION="${BASE#v}"
  RANGE="${BASE}..HEAD"
  note "base: ${BASE}"
fi

if [[ ! "$BASE_VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "error: base version '${BASE_VERSION}' is not X.Y.Z" >&2
  exit 1
fi
MAJOR="${BASH_REMATCH[1]}"
MINOR="${BASH_REMATCH[2]}"
PATCH="${BASH_REMATCH[3]}"

LEVEL="patch"
REASON="no feat: or breaking commits — patch"
n_break=0
n_feat=0
n_fix=0
n_other=0
n_unconventional=0

# Held in variables, not written inline: bash cannot parse an unquoted `[^)]`
# inside a [[ =~ ]] regex, and quoting the pattern there would make it literal.
RE_BREAKING='^[a-zA-Z]+(\([^)]*\))?!:'
RE_FEAT='^feat(\([^)]*\))?:'
RE_FIXPERF='^(fix|perf)(\([^)]*\))?:'
RE_CONVENTIONAL='^[a-zA-Z]+(\([^)]*\))?:'

if [[ "$BUMP" == "auto" ]]; then
  # One commit at a time, by hash. Subjects and bodies are free text spanning
  # newlines, and a bash variable cannot hold the NUL byte that would otherwise
  # be the safe field separator — so asking git for each field separately is the
  # only version of this that cannot mis-split a commit message. Reading the log
  # as lines would let a body line reading "feat: ..." vote as if it were a
  # subject, which is a silent wrong bump.
  while IFS= read -r sha; do
    [[ -z "$sha" ]] && continue
    subject="$(git log -1 --format='%s' "$sha")"
    body="$(git log -1 --format='%b' "$sha")"

    # The body trailer is matched with grep so that "^" means "start of a line
    # in the body", which is where a BREAKING CHANGE trailer legitimately sits.
    if [[ "$subject" =~ $RE_BREAKING ]] \
       || printf '%s' "$body" | grep -qE '^BREAKING[ -]CHANGE:'; then
      n_break=$((n_break + 1))
    elif [[ "$subject" =~ $RE_FEAT ]]; then
      n_feat=$((n_feat + 1))
    elif [[ "$subject" =~ $RE_FIXPERF ]]; then
      n_fix=$((n_fix + 1))
    elif [[ "$subject" =~ $RE_CONVENTIONAL ]]; then
      n_other=$((n_other + 1))
    else
      n_unconventional=$((n_unconventional + 1))
    fi
  done < <(git log --no-merges --format='%H' "$RANGE")

  total=$((n_break + n_feat + n_fix + n_other + n_unconventional))
  if [[ "$total" -eq 0 ]]; then
    echo "error: no commits since ${BASE:-the beginning} — there is nothing to release" >&2
    exit 1
  fi

  note "commits since ${BASE:-start}: ${total} (breaking ${n_break}, feat ${n_feat}, fix/perf ${n_fix}, other ${n_other}, unconventional ${n_unconventional})"
  if [[ "$n_unconventional" -gt 0 ]]; then
    note "  ${n_unconventional} commit(s) are not conventional — counted, but they drive no bump"
  fi

  if [[ "$n_break" -gt 0 ]]; then
    LEVEL="major"
    REASON="${n_break} breaking change(s)"
  elif [[ "$n_feat" -gt 0 ]]; then
    LEVEL="minor"
    REASON="${n_feat} feat commit(s), no breaking changes"
  elif [[ "$n_fix" -gt 0 ]]; then
    LEVEL="patch"
    REASON="${n_fix} fix/perf commit(s)"
  else
    LEVEL="patch"
    REASON="only chore/docs-type commits"
  fi
else
  LEVEL="$BUMP"
  REASON="forced with --bump ${BUMP}"
fi

if [[ "$LEVEL" == "major" && "$MAJOR" -eq 0 && "$ALLOW_ZERO_MAJOR" -eq 0 ]]; then
  note "breaking change on a 0.x line — bumping the minor, not cutting 1.0.0"
  note "  (pass --bump major --allow-zero-major to cut 1.0.0 deliberately)"
  LEVEL="minor"
  REASON="${REASON}, held inside 0.x"
fi

case "$LEVEL" in
  major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
  minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
  patch) PATCH=$((PATCH + 1)) ;;
esac

note "level: ${LEVEL} (${REASON})"
note "next:  ${MAJOR}.${MINOR}.${PATCH}"
echo "${MAJOR}.${MINOR}.${PATCH}"
