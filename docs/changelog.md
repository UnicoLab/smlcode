# Changelog

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
