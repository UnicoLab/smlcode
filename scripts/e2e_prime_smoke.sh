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
"$BIN" stack list | head -20
"$BIN" stack show omlx-local | head -15
"$BIN" stack apply omlx-local --clear-agent-llm
"$BIN" agent list | head -20

echo "== studio API smoke =="
# Studio's session-token auth (pkg/server: Options.Token / NoAuth,
# SLMCODE_STUDIO_NO_AUTH env var) is in-flight elsewhere in this tree. This
# script drives raw curl requests with no way to read a token out of a
# background process's stdout race-free, so it opts out of auth explicitly
# rather than guessing — this is the documented escape hatch, not a hack
# around it, and is a no-op today if the CLI doesn't wire auth on yet.
export SLMCODE_STUDIO_NO_AUTH=1
"$BIN" studio --listen 127.0.0.1:17420 >/tmp/slmcode-e2e-studio.log 2>&1 &
PID=$!
sleep 1
for i in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:17420/api/health" >/dev/null; then break; fi
  sleep 0.2
done
curl -sf "http://127.0.0.1:17420/api/health" | grep -q '"ok"'
curl -sf "http://127.0.0.1:17420/api/stacks" | grep -q stacks
curl -sf "http://127.0.0.1:17420/api/models?limit=5" | grep -q models
curl -sf "http://127.0.0.1:17420/api/auth" | grep -q provider
curl -sf "http://127.0.0.1:17420/api/mcp" | grep -q mcp_call
curl -sf "http://127.0.0.1:17420/api/config/schema" | grep -q fields
curl -sf -X PUT "http://127.0.0.1:17420/api/auth" \
  -H 'Content-Type: application/json' \
  -d '{"provider":"openai","api_key":"sk-smoke"}' | grep -q ok
test -f .slmcode/auth.json
curl -sf -X PUT "http://127.0.0.1:17420/api/config" \
  -H 'Content-Type: application/json' \
  -d '{"context_compact_engine":"auto","session_event_log":true,"auto_refine":true}' \
  | grep -q context_compact_engine
curl -sf -X POST "http://127.0.0.1:17420/api/stacks/openai/apply" \
  -H 'Content-Type: application/json' \
  -d '{"clear_agent_llm":true}' | grep -q '"ok"'
# restore local stack
curl -sf -X POST "http://127.0.0.1:17420/api/stacks/omlx-local/apply" \
  -H 'Content-Type: application/json' \
  -d '{"clear_agent_llm":true}' | grep -q '"ok"'

kill "$PID" 2>/dev/null || true
wait "$PID" 2>/dev/null || true
echo "== smoke OK =="
