# Changelog

## v0.9.0 — Stacks, auth store & strict-provider ReAct

### Highlights
- **Stacks CLI + Studio** — `slmcode stack list|show|apply`, `GET/POST /api/stacks…`,
  Settings → Model Stack; hierarchy `stack → agent pin → runtime`
- **Per-agent LLM pins** — `slmcode agent …`, `effective_model` / `effective_provider` on APIs
- **Models catalog** — `GET /api/models`, `find_models` tool, costs + enabled_models allowlist
- **Auth store** — `.slmcode/auth.json` via `/auth`, `PUT /api/auth` (keys out of config.yaml)
- **Prime-agent ports** — LLM compaction engines, session `events.jsonl`, MCP status,
  auto-refine after waves, config schema (`GET /api/config/schema`)
- **DeepSeek** — default endpoint `https://api.deepseek.com` (client appends `/v1`)
- **GoLangGraph v0.2.2** — ReAct appends `role=tool` messages; no finalize race that
  skipped `act` (fixes DeepSeek 400 “insufficient tool messages”)
- **Success semantics** — historical `ESCALATED…` notes on **done** tasks no longer fail a green run

### Live smoke
- `RUN_E2E=1 go test ./test/e2e/ -run TestLiveStacksOMLXAndDeepSeek` (omlx-local + deepseek)

---

## v0.8.3 — Studio React style fix

### Fixes
- Studio Quick-start footer used HTML string `style` attrs → React error #62 crash
- `make lint` gofmt ignores `.slmcode/` workspace artifacts

---

## v0.7.3 — Incomplete finalize recovery

### Highlights
- Detect empty finalize and synthetic `model ended on a tool call` blocked JSON
- Up to two finish-steer corrector passes (demand status JSON, stop tool chains)
- Provisional done from disk/tool evidence when finalize still fails
- Knowledge cards: Python / Go project bars for language expectations

---

## v0.7.2 — Escalate timeout → SLM arbitrator

### Highlights
- On escalate HITL timeout, dedicated **@escalate** agent decides
  `retry` / `re_scope` / `abort` / `mark_done` (override via `escalate_timeout_agent`)
- Heuristic fallback when the LLM is unavailable (stubs → retry, vague → re_scope)
- Docs: Studio / TUI / Agents / Architecture / Config cover escalate + runnable QA bar
- CI: trailing-whitespace fix in `docs/pipeline.md`

---

## v0.7.1 — Runnable quality bar + escalate HITL

### Highlights
- Greenfield Python QA defaults to **pytest** (fail closed), not `compileall`
- **Acceptance smoke** — whitelisted commands from task acceptance run after each worker
- Worker critique loops until smoke/static/acceptance green (bounded by `max_retries`)
- Run success requires a **strong** QA gate — syntax-only cannot rubber-stamp incomplete boards
- **Escalate HITL** — Studio modal + TUI `/escalate`; options: re-scope / retry / mark done / abort

### API
- `GET` / `POST` `/api/escalate/pending|answer`

---

## v0.7.0 — Pipeline control + reference quality

### Highlights
- **Config-driven pipeline** (`.slmcode/pipeline.yaml`) — bind any agent to any phase,
  configure execute-loop reviewer/corrector, insert slots before/after/replace phases
- **Studio Pipeline tab** — edit phases/slots live; progress header follows config dynamically
- **Reference-quality bar** — project completeness gate blocks TestSLMs-style false success
- **Real-query eval suite** — offline harness + optional live LangGraph / FastAPI / CLI cases
- **Loop feedback** — SSE `kind=loop` with wave reasons + continue/abort/flag HITL
- **Placeholder polish** — `@placeholder` pass + precise gap flagging
- Custom agents accepted in specialist mode and board role dropdowns

### API
- `GET` / `PUT` `/api/pipeline`
- `POST` `/api/pipeline/reset`
- `GET` / `POST` `/api/continue/pending|answer`

### Docs
- New [Pipeline](pipeline.md) guide; updates to Studio, Agents, Config, Architecture

---

## v0.6.0 — SLM quality ports + eval

Harness gates, interventions, turn meter, `slmcode eval`, and Studio/TUI polish.
