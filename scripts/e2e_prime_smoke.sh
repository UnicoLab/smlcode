#!/usr/bin/env bash
# Offline CLI + HTTP smoke for stacks/agents/models/auth/mcp/schema/events.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${ROOT}/bin/slmcode"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/slmcode-e2e-XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

cd "$ROOT"
echo "== build =="
make build >/dev/null

echo "== workspace init in $TMP =="
cd "$TMP"
"$BIN" init >/dev/null 2>&1 || true
# init may need flags — ensure .slmcode exists
mkdir -p .slmcode/agents
if [[ ! -f .slmcode/config.yaml ]]; then
  cat > .slmcode/config.yaml <<'YAML'
provider: ollama
endpoint: http://127.0.0.1:11434
model: qwen2.5-coder:7b
backend: slmcode
listen: 127.0.0.1:17420
YAML
fi

export SLMCODE_STACKS="$ROOT/stacks"

echo "== CLI stack list/show/apply =="
# `sed -n '1,Np'`, never `head -N`: head exits after N lines, the producer gets
# EPIPE, and under `set -o pipefail` the whole script dies with 141. That is
# exactly what happened once `stack show` grew past 15 lines — this script
# (CI's "Studio API offline smoke test" step) was aborting before it reached a
# single Studio assertion, and the failure looked like a server problem.
"$BIN" stack list | sed -n '1,20p'
"$BIN" stack show omlx-local | sed -n '1,15p'
"$BIN" stack apply omlx-local --clear-agent-llm
"$BIN" agent list | sed -n '1,20p'

echo "== studio API smoke =="
# Studio's session token is REAL now, so this script drives the authenticated
# path instead of opting out of it.
#
# It used to export SLMCODE_STUDIO_NO_AUTH=1 with a comment saying auth was
# "in-flight elsewhere in this tree" and that the opt-out was "a no-op today if
# the CLI doesn't wire auth on yet". Auth is wired. Keeping the opt-out meant
# the one script CI runs against a live Studio exercised the ONE configuration
# no user runs — and would have passed just as happily if the token check had
# been deleted.
#
# The token is not a race to read: the CLI prints the tokenised URL before it
# serves anything, so poll the log until the line appears.
STUDIO_LOG=/tmp/slmcode-e2e-studio.log
: > "$STUDIO_LOG"
"$BIN" studio --listen 127.0.0.1:17420 >"$STUDIO_LOG" 2>&1 &
PID=$!
kill_studio() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap 'kill_studio; cleanup' EXIT

TOKEN=""
for _ in $(seq 1 100); do
  TOKEN="$(grep -oE '\?t=[0-9a-f]+' "$STUDIO_LOG" 2>/dev/null | head -1 | cut -d= -f2 || true)"
  [[ -n "$TOKEN" ]] && break
  sleep 0.1
done
if [[ -z "$TOKEN" ]]; then
  echo "ERROR: studio never printed a session token — the URL a user must open is missing." >&2
  cat "$STUDIO_LOG" >&2
  exit 1
fi

for _ in $(seq 1 30); do
  if curl -sf -H "X-SLMCode-Token: ${TOKEN}" "http://127.0.0.1:17420/api/health" >/dev/null; then break; fi
  sleep 0.2
done

# Auth must actually refuse. A smoke test that only ever sends a valid token
# cannot tell a working check from a deleted one.
echo "-- unauthenticated requests are refused --"
code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:17420/api/health")"
[[ "$code" == "401" ]] || { echo "ERROR: /api/health without a token returned $code, want 401" >&2; exit 1; }
code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:17420/api/health?t=not-the-token")"
[[ "$code" == "401" ]] || { echo "ERROR: /api/health with a wrong token returned $code, want 401" >&2; exit 1; }
# The HTML shell is authenticated too, and the 401 page must not leak the token.
gate="$(curl -s -o "$TMP/gate.html" -w '%{http_code}' "http://127.0.0.1:17420/")"
[[ "$gate" == "401" ]] || { echo "ERROR: GET / without a token returned $gate, want 401" >&2; exit 1; }
if grep -q "$TOKEN" "$TMP/gate.html"; then
  echo "ERROR: the unauthenticated page leaks the session token — that is the bug the meta tag had." >&2
  exit 1
fi
# A cross-origin request is refused even WITH a valid token.
code="$(curl -s -o /dev/null -w '%{http_code}' -H "X-SLMCode-Token: ${TOKEN}" \
  -H 'Origin: http://evil.example' "http://127.0.0.1:17420/api/health")"
[[ "$code" == "403" ]] || { echo "ERROR: cross-origin request returned $code, want 403" >&2; exit 1; }
# Presenting ?t= once must mint the session cookie.
curl -s -D "$TMP/head.txt" -o /dev/null "http://127.0.0.1:17420/?t=${TOKEN}"
grep -qi 'set-cookie: *slmcode_studio=' "$TMP/head.txt" || {
  echo "ERROR: the ?t= bootstrap did not mint a session cookie" >&2; cat "$TMP/head.txt" >&2; exit 1; }
grep -qi 'httponly' "$TMP/head.txt" || { echo "ERROR: the session cookie is not HttpOnly" >&2; exit 1; }
grep -qi 'samesite=strict' "$TMP/head.txt" || { echo "ERROR: the session cookie is not SameSite=Strict" >&2; exit 1; }

echo "-- authenticated API --"
# api <curl-args...> — adds the session token and returns the body on stdout.
#
# The body is captured whole and grepped from a variable rather than piped
# straight into `grep -q`: grep exits on its first match, curl then gets EPIPE
# and exits 23, and under `set -o pipefail` that is a failure of the smoke test
# rather than of the server.
api() { command curl -sf -H "X-SLMCode-Token: ${TOKEN}" "$@"; }
expect() { # expect <needle> <curl-args...>
  local needle="$1"; shift
  local body
  body="$(api "$@")" || { echo "ERROR: request failed: $*" >&2; exit 1; }
  case "$body" in
    *"$needle"*) : ;;
    *) echo "ERROR: response to '$*' does not contain '$needle':" >&2; echo "$body" >&2; exit 1 ;;
  esac
}

expect '"ok"'  "http://127.0.0.1:17420/api/health"
expect stacks  "http://127.0.0.1:17420/api/stacks"
expect models  "http://127.0.0.1:17420/api/models?limit=5"
expect provider "http://127.0.0.1:17420/api/auth"
expect mcp_call "http://127.0.0.1:17420/api/mcp"
expect fields  "http://127.0.0.1:17420/api/config/schema"

expect ok -X PUT "http://127.0.0.1:17420/api/auth" \
  -H 'Content-Type: application/json' \
  -d '{"provider":"openai","api_key":"sk-smoke"}'
test -f .slmcode/auth.json

expect context_compact_engine -X PUT "http://127.0.0.1:17420/api/config" \
  -H 'Content-Type: application/json' \
  -d '{"context_compact_engine":"auto","session_event_log":true,"auto_refine":true}'

expect '"ok"' -X POST "http://127.0.0.1:17420/api/stacks/openai/apply" \
  -H 'Content-Type: application/json' -d '{"clear_agent_llm":true}'
# restore local stack
expect '"ok"' -X POST "http://127.0.0.1:17420/api/stacks/omlx-local/apply" \
  -H 'Content-Type: application/json' -d '{"clear_agent_llm":true}'

kill_studio
echo "== smoke OK =="
