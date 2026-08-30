#!/usr/bin/env bash
#
# Live end-to-end sweep against a real local model.
#
# Unit tests and the fakemodel e2e suite prove the harness's logic. They cannot
# prove the thing that actually breaks: a run that is not doing the work still
# looking like a run that is. Every defect this sweep was written to catch was
# invisible to `go test` and obvious the moment a real model drove the harness
# against a real project.
#
# THE RULE: every assertion is an OBJECTIVE outcome — a file on disk, a
# compiler's exit code, git state. The harness's own success line is never the
# assertion. That is not stylistic. The bugs found here include a run reporting
# ✔ with half the request unbuilt, a green gate over an absent deliverable, and
# acceptance criteria that were planned and never verified. In each case the
# harness's self-report was the thing that was wrong.
#
# Usage:
#   test/live/sweep.sh                 # all scenarios
#   test/live/sweep.sh 1 4             # only scenarios 1 and 4
#   SWEEP_WORK=/tmp/sweep test/live/sweep.sh
#
# Requires: a configured provider that `slmcode doctor` can reach, plus `go`.
# Scenarios 5 and 6 additionally need node/npx and network access for the
# component CLIs; they are skipped when npx is absent.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${SWEEP_WORK:-${TMPDIR:-/tmp}/slmcode-live-sweep}"
BIN="${SLMCODE_BIN:-$REPO/bin/slmcode}"
GIT="git -c user.email=sweep@localhost -c user.name=sweep"

if [ ! -x "$BIN" ]; then
  echo "no slmcode binary at $BIN — run: go build -o bin/slmcode ./cmd/slmcode" >&2
  exit 2
fi

WANT=("$@")
want() { # scenario number
  [ ${#WANT[@]} -eq 0 ] && return 0
  for w in "${WANT[@]}"; do [ "$w" = "$1" ] && return 0; done
  return 1
}

PASS=0; FAIL=0; SKIP=0
declare -a RESULTS
check() { # name, exit-status, detail
  if [ "$2" -eq 0 ]; then RESULTS+=("PASS  $1"); PASS=$((PASS+1))
  else RESULTS+=("FAIL  $1  — $3"); FAIL=$((FAIL+1)); fi
}
skip() { RESULTS+=("SKIP  $1  — $2"); SKIP=$((SKIP+1)); }

run() { # dir, query, extra-args, logfile
  ( cd "$1" && $BIN run "$2" --root "$1" --on-gate-timeout approve --color never $3 > "$4" 2>&1 )
  return 0
}

mkdir -p "$WORK"

# ── 1. Bug fix: criteria verified, tests green, scope respected ────────────
if want 1; then
  echo "[1] go-bugfix"
  d="$WORK/bugfix"; rm -rf "$d"; mkdir -p "$d"; cd "$d"
  printf 'module bugfix\n\ngo 1.24\n' > go.mod
  cat > stats.go <<'GO'
package bugfix

import "sort"

// Median returns the middle value of xs.
func Median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[len(s)/2]
}
GO
  cat > stats_test.go <<'GO'
package bugfix

import "testing"

func TestMedianOdd(t *testing.T) {
	if got := Median([]int{3, 1, 2}); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}

func TestMedianEven(t *testing.T) {
	if got := Median([]int{1, 2, 3, 4}); got != 2 {
		t.Fatalf("got %d, want 2 (mean of 2 and 3, truncated)", got)
	}
}
GO
  $GIT init -q && $GIT add -A && $GIT commit -q -m "fixture: median is wrong for even input"

  run "$d" "Fix the failing test TestMedianEven: with an even number of elements Median must return the mean of the two middle elements, truncated. Change only stats.go — do not edit stats_test.go." "" "$WORK/1.log"

  ( cd "$d" && go test ./... >/dev/null 2>&1 )
  check "go-bugfix: project tests pass" $? "go test failed"
  ( cd "$d" && $GIT diff --quiet -- stats_test.go )
  check "go-bugfix: the test file was not edited" $? "stats_test.go was modified to make the test pass"
  grep -q "CRITERIA-OK\|Acceptance criteria" "$d/.slmcode/board.json" 2>/dev/null
  check "go-bugfix: acceptance criteria were verified" $? "no criteria evidence on the board"
fi

# ── 2. Worktree isolation: harness state must not reach the commit ─────────
if want 2; then
  echo "[2] worktree-isolation"
  d="$WORK/isolate"; rm -rf "$d"; mkdir -p "$d"; cd "$d"
  rm -rf "$WORK"/.slmcode-worktree-* 2>/dev/null
  printf 'module isolate\n\ngo 1.24\n' > go.mod
  printf 'package isolate\n' > strutil.go
  cat > strutil_test.go <<'GO'
package isolate

import "testing"

func TestReverse(t *testing.T) {
	if got := Reverse("añb"); got != "bña" {
		t.Fatalf("got %q, want %q", got, "bña")
	}
}
GO
  $GIT init -q && $GIT add -A && $GIT commit -q -m "fixture: strutil missing Reverse"

  run "$d" "Add a Reverse function to strutil.go that reverses a string by runes, so TestReverse passes. Change only strutil.go." "--isolate worktree" "$WORK/2.log"

  ( cd "$d" && go test ./... >/dev/null 2>&1 )
  check "isolation: project tests pass" $? "go test failed"
  ls -d "$WORK"/.slmcode-worktree-* >/dev/null 2>&1; [ $? -ne 0 ]
  check "isolation: no orphan worktree left behind" $? "an orphan worktree directory survived"
  ( cd "$d" && $GIT show --stat HEAD 2>/dev/null | grep -q "\.slmcode/" ); [ $? -ne 0 ]
  check "isolation: the commit carries no harness state" $? ".slmcode files were committed"
fi

# ── 3. Greenfield: the whole request gets built, or the run says so ────────
# The failure this exists for: `go test ./...` went green over the one package
# that existed, the wave that would have written the rest never ran, and the run
# reported ✔ with the deliverable absent.
if want 3; then
  echo "[3] greenfield-agency"
  d="$WORK/agency"; rm -rf "$d"; mkdir -p "$d"; cd "$d"
  printf 'module agency\n\ngo 1.24\n' > go.mod
  printf '.slmcode/\n' > .gitignore
  $GIT init -q && $GIT add -A && $GIT commit -q -m "fixture: empty go module"

  run "$d" "Build a Go REST backend in cmd/server/main.go with an in-memory task store in pkg/tasks/store.go and Go unit tests in pkg/tasks/store_test.go." "" "$WORK/3.log"

  [ -f "$d/pkg/tasks/store.go" ]
  check "agency: the store was built" $? "pkg/tasks/store.go missing"
  [ -f "$d/pkg/tasks/store_test.go" ]
  check "agency: tests were written" $? "pkg/tasks/store_test.go missing"
  ( cd "$d" && go build ./... >/dev/null 2>&1 )
  check "agency: the project compiles" $? "go build failed"

  # Honesty: whatever the model managed, ✔ must never appear over a file the
  # request named and nothing built.
  if [ -f "$d/cmd/server/main.go" ]; then
    RESULTS+=("PASS  agency: no vacuous green (everything named was built)"); PASS=$((PASS+1))
  elif grep -q "^✔" "$WORK/3.log"; then
    RESULTS+=("FAIL  agency: no vacuous green  — reported ✔ with cmd/server/main.go absent"); FAIL=$((FAIL+1))
  else
    RESULTS+=("PASS  agency: no vacuous green (reported honestly)"); PASS=$((PASS+1))
  fi
fi

# ── 4. Multi-language: both halves staffed by their own specialists ────────
if want 4; then
  echo "[4] fullstack-squads"
  d="$WORK/fullstack"; rm -rf "$d"; mkdir -p "$d/web/src"; cd "$d"
  printf 'module fullstack\n\ngo 1.24\n' > go.mod
  printf '{\n  "name": "web",\n  "private": true\n}\n' > web/package.json
  printf '.slmcode/\nnode_modules/\n' > .gitignore
  $GIT init -q && $GIT add -A && $GIT commit -q -m "fixture: empty go + react workspace"

  run "$d" "Build a task API: a Go HTTP server in cmd/server/main.go with an in-memory store in pkg/tasks/store.go and Go unit tests in pkg/tasks/store_test.go, plus a React task list component in web/src/TaskList.tsx that fetches from it." "" "$WORK/4.log"

  [ -f "$d/pkg/tasks/store.go" ]
  check "fullstack: the Go half was built" $? "pkg/tasks/store.go missing"
  ( cd "$d" && go build ./... >/dev/null 2>&1 )
  check "fullstack: the Go half compiles" $? "go build failed"
  ( cd "$d" && go test ./... >/dev/null 2>&1 )
  check "fullstack: the Go tests pass" $? "go test failed"
  grep -q "go-worker" "$WORK/4.log"
  check "fullstack: a Go specialist was staffed" $? "no go-worker in the run"
  grep -qE "react-worker|ts-worker|shadcn-worker|untitledui-worker" "$WORK/4.log"
  check "fullstack: a frontend specialist was staffed" $? "no frontend specialist in the run"

  # The measured regression: ✔ 2/2 tasks done, 0 failed — with the React half
  # absent and its task no longer on the board.
  if [ -f "$d/web/src/TaskList.tsx" ]; then
    RESULTS+=("PASS  fullstack: no vacuous green (both halves built)"); PASS=$((PASS+1))
  elif grep -q "^✔" "$WORK/4.log"; then
    RESULTS+=("FAIL  fullstack: no vacuous green  — reported ✔ with web/src/TaskList.tsx absent"); FAIL=$((FAIL+1))
  else
    RESULTS+=("PASS  fullstack: no vacuous green (reported honestly)"); PASS=$((PASS+1))
  fi
fi

# ── 5/6. Frontend assemblers on a real scaffold ───────────────────────────
# These need npx and network access. The assertion that matters is that the
# assembler is CHOSEN and DISPATCHED TO — a run that silently falls back to the
# generic worker still installs components when told to, so "it worked" is not
# evidence the feature did.
assembler_scenario() { # n, label, marker-setup, query, worker-id
  local n="$1" label="$2" setup="$3" query="$4" worker="$5"
  echo "[$n] $label"
  if ! command -v npx >/dev/null 2>&1; then
    skip "$label: assembler dispatched" "npx not installed"
    return
  fi
  local d="$WORK/$label"; rm -rf "$d"; mkdir -p "$d/src/components/ui"; cd "$d"
  eval "$setup"
  printf '.slmcode/\nnode_modules/\n' > .gitignore
  $GIT init -q && $GIT add -A && $GIT commit -q -m "fixture: $label scaffold"

  run "$d" "$query" "" "$WORK/$n.log"

  grep -q "$worker" "$WORK/$n.log"
  check "$label: the assembler was chosen and dispatched" $? "$worker never appears in the run"
  local refusals
  refusals=$(grep -c "shell refused.*npx" "$WORK/$n.log" || true)
  [ "${refusals:-0}" -eq 0 ]
  check "$label: the component CLI was not refused" $? "the harness refused ${refusals:-0} npx command(s)"
}

if want 5; then
  assembler_scenario 5 shadcn \
    'printf "{\n  \"style\": \"new-york\",\n  \"tailwind\": {},\n  \"aliases\": {\"components\": \"@/components\"}\n}\n" > components.json; printf "export const Button = () => null;\n" > src/components/ui/button.tsx' \
    "In src/App.tsx, render a list of tasks: each task in a Card with its title and a Badge showing its status. Install the shadcn badge component if it is missing." \
    "shadcn-worker"
fi

if want 6; then
  assembler_scenario 6 untitledui \
    'printf "{\n  \"name\": \"uui\",\n  \"private\": true,\n  \"dependencies\": {\"@untitledui/icons\": \"^0.1.0\"}\n}\n" > package.json; printf "export const Button = () => null;\n" > src/components/base/button.tsx 2>/dev/null || (mkdir -p src/components/base && printf "export const Button = () => null;\n" > src/components/base/button.tsx)' \
    "In src/App.tsx, render a table of tasks with a title column and a status column. Install the Untitled UI table component if it is missing." \
    "untitledui-worker"
fi

echo
echo "════════ LIVE SWEEP ════════"
for r in "${RESULTS[@]}"; do echo "  $r"; done
echo "────────────────────────────"
echo "  PASS=$PASS  FAIL=$FAIL  SKIP=$SKIP"
echo "  logs: $WORK"
[ "$FAIL" -eq 0 ]
