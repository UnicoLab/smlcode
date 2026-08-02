# Changelog

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
