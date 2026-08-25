#!/usr/bin/env bash
# Live-model end-to-end suite: runs the harness against a REAL local SLM and
# checks objective outcomes (fixture `go test` passes, off-limits files are
# byte-identical, an impossible task is not reported as a success).
#
# The scenarios themselves live in test/e2e/slm_live_test.go — this script is
# the preflight, the budget policy, and the PASS/FAIL table around them.
#
# It costs real time and needs a running oMLX (or any OpenAI-compatible
# endpoint serving the model). It is deliberately NOT part of `make check`.
#
#   ./scripts/e2e-slm.sh                                    # all scenarios, fast 9B
#   ./scripts/e2e-slm.sh --model Qwen3.8-27B-4bit           # slower, stronger
#   ./scripts/e2e-slm.sh --scenario fix-a-bug --keep        # one case, keep the workspace
#   ./scripts/e2e-slm.sh --json report.json                 # machine-readable report
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BIN="${ROOT}/bin/slmcode"

# Keep in sync with slmLiveScenarios() in test/e2e/slm_live_test.go. The script
# validates --scenario against this list so a typo fails in a second instead of
# after `go test` quietly matches nothing and exits 0.
ALL_SCENARIOS=(
  implement-from-tests
  fix-a-bug
  existing-codebase-feature
  respects-scope
  honest-failure
)

# Must match slmLiveDefaultModel in test/e2e/slm_live_test.go.
MODEL="Qwen3.5-9B-MLX-4bit"
SCENARIOS=()
BUDGET_MIN=""
REPORT=""
JSON_STDOUT=0
KEEP=0

usage() {
  cat <<EOF
Usage: scripts/e2e-slm.sh [options]

  --model NAME        model id to drive (default: ${MODEL})
  --scenario NAME     run only this scenario; repeatable (default: all)
                      one of: ${ALL_SCENARIOS[*]}
  --timeout MINUTES   per-scenario wall budget, e.g. "25" or "25m"
                      (default: derived from the model — see below)
  --json [PATH]       write the machine-readable report to PATH and echo it
                      (default path: .slmcode/e2e-slm/report.json)
  --keep              retain each scenario's temp workspace for debugging
  -h, --help          this text

Requires a reachable endpoint serving --model. Check with: slmcode doctor
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --model)      [[ $# -ge 2 ]] || { echo "--model needs a value" >&2; exit 2; }; MODEL="$2"; shift 2 ;;
    --model=*)    MODEL="${1#*=}"; shift ;;
    --scenario)   [[ $# -ge 2 ]] || { echo "--scenario needs a value" >&2; exit 2; }; SCENARIOS+=("$2"); shift 2 ;;
    --scenario=*) SCENARIOS+=("${1#*=}"); shift ;;
    --timeout)    [[ $# -ge 2 ]] || { echo "--timeout needs a value" >&2; exit 2; }; BUDGET_MIN="$2"; shift 2 ;;
    --timeout=*)  BUDGET_MIN="${1#*=}"; shift ;;
    --json)       JSON_STDOUT=1
                  if [[ $# -ge 2 && "$2" != -* ]]; then REPORT="$2"; shift 2; else shift; fi ;;
    --json=*)     JSON_STDOUT=1; REPORT="${1#*=}"; shift ;;
    --keep)       KEEP=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *)            echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# ── scenario selection ──────────────────────────────────────────────────────
if [[ ${#SCENARIOS[@]} -eq 0 ]]; then
  SCENARIOS=("${ALL_SCENARIOS[@]}")
else
  for want in "${SCENARIOS[@]}"; do
    ok=0
    for known in "${ALL_SCENARIOS[@]}"; do
      [[ "$want" == "$known" ]] && ok=1 && break
    done
    if [[ $ok -eq 0 ]]; then
      echo "ERROR: unknown scenario '${want}'." >&2
      echo "Valid scenarios: ${ALL_SCENARIOS[*]}" >&2
      exit 2
    fi
  done
fi
RUN_FILTER="TestSLMLiveScenarios/($(IFS='|'; echo "${SCENARIOS[*]}"))"

# ── budget policy ───────────────────────────────────────────────────────────
# A 27B/30B-class model needs a task_timeout of at least 15 minutes — that is
# not a guess, it is what the harness's own timeout remedy tells the user to
# set. Picking it here is the difference between "the model is slow" and a run
# that fails on a budget this script chose badly.
#
# The scenario budget is a different number and a looser one: a scenario is a
# dozen-plus sequential role calls, so it is normal for one to take half an
# hour on a 9B. The budget exists to catch a run that never stops, and a
# ceiling tight enough to trip on an honest slow run proves nothing.
case "$MODEL" in
  *27B*|*30B*|*32B*|*33B*|*34B*|*70B*|*72B*) TASK_MIN=15; DEFAULT_BUDGET_MIN=75; SIZE_NOTE="large model (≥27B class)" ;;
  *)                                          TASK_MIN=8;  DEFAULT_BUDGET_MIN=45; SIZE_NOTE="small/fast model" ;;
esac

if [[ -n "$BUDGET_MIN" ]]; then
  BUDGET_MIN="${BUDGET_MIN%m}"
  if ! [[ "$BUDGET_MIN" =~ ^[0-9]+$ ]] || [[ "$BUDGET_MIN" -lt 1 ]]; then
    echo "ERROR: --timeout takes whole minutes, e.g. '25' or '25m'." >&2
    exit 2
  fi
else
  BUDGET_MIN="$DEFAULT_BUDGET_MIN"
fi
# task_timeout can never exceed the wall budget it lives inside.
if [[ "$TASK_MIN" -gt "$BUDGET_MIN" ]]; then TASK_MIN="$BUDGET_MIN"; fi
# Whole-suite ceiling for `go test`. The slack is PER SCENARIO, not a flat five
# minutes: when every scenario runs to its ceiling — which is exactly what
# happens while a harness bug keeps a run from terminating — a flat margin puts
# the go-test deadline a rounding error above the sum of the budgets, and
# `go test -timeout` expiring kills the process without running deferred
# functions. Two minutes each covers fixture `go test` runs and engine setup.
GOTEST_MIN=$(( (BUDGET_MIN + 2) * ${#SCENARIOS[@]} + 10 ))

# ── preflight: refuse clearly, never hang ───────────────────────────────────
if [[ ! -x "$BIN" ]]; then
  echo "==> building ${BIN} (needed for the preflight check)"
  make build >/dev/null
fi

# The endpoint the TEST will use: SLMCODE_ENDPOINT if set, else whatever this
# workspace's config resolves to — which is what `slmcode doctor` reports. The
# preflight has to probe the same URL the run will, or it proves nothing.
#
# awk here has NO `exit`, and doctor's output is captured to a variable first:
# an `awk '…{print; exit}'` in a pipeline closes the pipe, doctor takes EPIPE,
# and under `set -o pipefail` the script dies with 141 before printing a word.
# That is the same trap scripts/e2e_prime_smoke.sh documents for `head`.
ENDPOINT="${SLMCODE_ENDPOINT:-}"
if [[ -z "$ENDPOINT" ]]; then
  DOCTOR_BOOT="$("$BIN" doctor 2>/dev/null || true)"
  ENDPOINT="$(awk '$1=="endpoint" && !seen {print $2; seen=1}' <<<"$DOCTOR_BOOT")"
fi
if [[ -z "$ENDPOINT" ]]; then
  echo "ERROR: could not determine the LLM endpoint." >&2
  echo "Remedy: run 'slmcode doctor' in this repo, or set SLMCODE_ENDPOINT=http://127.0.0.1:8000/v1" >&2
  exit 1
fi
export SLMCODE_ENDPOINT="$ENDPOINT"

# Reachability first: a refused connection must not look like an auth problem,
# and --max-time is what keeps "endpoint is down" from becoming "script hangs".
if ! curl -s -o /dev/null --max-time 8 "${ENDPOINT%/}/models"; then
  echo "ERROR: no LLM endpoint answering at ${ENDPOINT}." >&2
  echo "Remedy: start the server (oMLX: 'omlx serve'), or point elsewhere with" >&2
  echo "        SLMCODE_ENDPOINT=http://host:port/v1 ./scripts/e2e-slm.sh" >&2
  exit 1
fi

# Model availability + auth, via the binary so key resolution is the real one
# (env, .slmcode/auth.json, ~/.omlx/settings.json — not a copy of it here).
# Watchdogged: doctor has its own HTTP timeouts, but this script promises never
# to hang and that promise should not depend on another program keeping it.
DOCTOR_OUT="$(mktemp "${TMPDIR:-/tmp}/slmcode-e2e-doctor-XXXXXX")"
cleanup() { rm -f "$DOCTOR_OUT"; }
trap cleanup EXIT

"$BIN" doctor --model "$MODEL" --endpoint "$ENDPOINT" >"$DOCTOR_OUT" 2>&1 &
DOCTOR_PID=$!
( sleep 30; kill -TERM "$DOCTOR_PID" 2>/dev/null || true ) &
WATCHDOG_PID=$!
wait "$DOCTOR_PID" 2>/dev/null || true
kill -TERM "$WATCHDOG_PID" 2>/dev/null || true
wait "$WATCHDOG_PID" 2>/dev/null || true

if grep -q 'LLM check failed' "$DOCTOR_OUT"; then
  echo "ERROR: model '${MODEL}' is not served at ${ENDPOINT}." >&2
  grep 'LLM check failed' "$DOCTOR_OUT" >&2
  echo "Remedy: load the model on the server, or pass one it already serves:" >&2
  echo "        ./scripts/e2e-slm.sh --model <id from the list above>" >&2
  exit 1
fi
if ! grep -q 'LLM ok' "$DOCTOR_OUT"; then
  echo "ERROR: preflight could not confirm model '${MODEL}' at ${ENDPOINT} within 30s." >&2
  sed -n '1,40p' "$DOCTOR_OUT" >&2
  echo "Remedy: run 'slmcode doctor --model ${MODEL}' by hand and fix what it reports." >&2
  exit 1
fi

# ── report destination ──────────────────────────────────────────────────────
if [[ -z "$REPORT" ]]; then
  REPORT="${ROOT}/.slmcode/e2e-slm/report.json"
fi
mkdir -p "$(dirname "$REPORT")"
LOG="$(mktemp "${TMPDIR:-/tmp}/slmcode-e2e-slm-XXXXXX")"

# ── go ──────────────────────────────────────────────────────────────────────
echo "══ live SLM e2e ══"
printf '  %-16s %s\n' "model"           "$MODEL  (${SIZE_NOTE})"
printf '  %-16s %s\n' "endpoint"        "$ENDPOINT"
printf '  %-16s %s\n' "scenarios"       "${SCENARIOS[*]}"
printf '  %-16s %s\n' "task_timeout"    "${TASK_MIN}m  — one task's model budget; the harness asks for ≥15m on a 27B"
printf '  %-16s %s\n' "scenario_budget" "${BUDGET_MIN}m  — wall ceiling per scenario (honest-failure is asserted against it)"
printf '  %-16s %s\n' "go test timeout" "${GOTEST_MIN}m"
printf '  %-16s %s\n' "report"          "$REPORT"
printf '  %-16s %s\n' "full log"        "$LOG"
if [[ $KEEP -eq 1 ]]; then
  printf '  %-16s %s\n' "keep" "workspaces retained (paths in the log)"
fi
echo

export RUN_E2E=1
export SLMCODE_MODEL="$MODEL"
export SLMCODE_E2E_TASK_TIMEOUT="${TASK_MIN}m"
export SLMCODE_E2E_SCENARIO_BUDGET="${BUDGET_MIN}m"
export SLMCODE_E2E_REPORT="$REPORT"
if [[ $KEEP -eq 1 ]]; then
  export SLMCODE_E2E_KEEP=1
fi

# -v is required, not cosmetic: t.Log output (the report, the marker rows) is
# swallowed on a PASSING run without it.
#
# The filter keeps a 30-minute run legible; $LOG always has everything. No
# `head` anywhere in this pipeline — it exits early, the producer takes EPIPE,
# and under `set -o pipefail` that reads as a suite failure (the mistake
# scripts/e2e_prime_smoke.sh documents at length).
FILTER='^(=== RUN|--- (PASS|FAIL|SKIP)|ok |FAIL|E2E-SLM-)|slm_live_test\.go:|^ *(✖|[a-z-]+: wall=)'
set +e
go test ./test/e2e/ -count=1 -v -timeout "${GOTEST_MIN}m" -run "$RUN_FILTER" 2>&1 \
  | tee "$LOG" \
  | { grep --line-buffered -E "$FILTER" || true; }
GO_STATUS="${PIPESTATUS[0]}"
set -e

# ── table ───────────────────────────────────────────────────────────────────
echo
echo "══ results ══"
printf '%-28s %-6s %9s %7s %7s %9s %9s\n' SCENARIO RESULT WALL TASKS LLM_CALLS TOK_IN TOK_OUT
ROWS=0
while read -r _marker name result wall tasks calls tin tout; do
  ROWS=$((ROWS + 1))
  printf '%-28s %-6s %8ss %7s %7s %9s %9s\n' "$name" "$result" "$wall" "$tasks" "$calls" "$tin" "$tout"
done < <(grep -E '^[[:space:]]*E2E-SLM-ROW ' "$LOG" | sed 's/^[[:space:]]*//')

if [[ $ROWS -eq 0 ]]; then
  echo "(no scenario rows — the suite did not get far enough to report; see $LOG)"
fi
grep -E '^[[:space:]]*E2E-SLM-VERDICT ' "$LOG" | sed 's/^[[:space:]]*E2E-SLM-VERDICT /verdict: /' || true

if [[ $JSON_STDOUT -eq 1 && -f "$REPORT" ]]; then
  echo
  echo "══ report ($REPORT) ══"
  cat "$REPORT"
fi

echo
if [[ "$GO_STATUS" -ne 0 ]]; then
  echo "e2e-slm: FAIL — full output: $LOG"
  echo "Read a failure by its check name: 'unchanged:<file>' means the run edited"
  echo "something it was told not to; 'go-test-passes' means the code does not work;"
  echo "'engine-success-is' means the harness's own verdict was wrong."
  exit "$GO_STATUS"
fi
echo "e2e-slm: OK — full output: $LOG"
