# 🧩 Specialist Agents Reference

All agents are registered with GoLangGraph `SubAgentExecutor` and receive
**scoped TaskPacks** (never the whole repo). Per-agent providers let you mix
local SLMs and cloud LLMs in one run — see [PROVIDERS](PROVIDERS.md).

See also: [GUIDE](GUIDE.md) · [ARCHITECTURE](ARCHITECTURE.md) · [UnicoLab](https://unicolab.ai)

## Roster (14)

| ID | Tools | Output | When |
|----|-------|--------|------|
| `coordinator` | — | JSON actions | Pre-execute + after each wave |
| `orchestrator` | — | short decisions | Registered / reserved |
| `context` | — | CONTEXT.md body | Start of run |
| `explorer` | yes | JSON file map | When deep explore needed |
| `docs` | yes | JSON docs map | Docs/API/README queries |
| `architect` | — | JSON design | Design/refactor/large queries |
| `planner` | — | JSON plan | Always (multipass) |
| `splitter` | — | JSON tasks | Always (multipass) |
| `worker` | yes | JSON status | Kanban execute |
| `deep` | yes | JSON status | Multi-step tasks (`--role deep`) |
| `reviewer` | — | JSON approve | Self-critic per task |
| `corrector` | yes | JSON status | On review reject |
| `tester` | yes | JSON passed | End validation |
| `memory` | — | markdown bullets | End + wave learn |

List live: `curl -s localhost:7420/api/agents | jq`, Studio → Agents, or TUI `/agents`.

## Custom agents (Studio + TUI)

Persist under `.slmcode/agents/<id>.yaml` (or `~/.slmcode/agents/`).

```bash
# TUI (interactive wizard or key=value)
/agent new
/agent new id=night-auditor title=Night provider=openai endpoint=http://127.0.0.1:9000/v1
/agent edit worker model=qwen2.5-coder:14b   # builtin override
/agent show night-auditor
/agent delete night-auditor
```

Fields: `skills`, `model`, `provider`, `endpoint`, `tools`, `temperature`, `max_tokens`,
`max_iter`, `system_prompt` — same store as `POST/PUT/DELETE /api/agents`.

### Per-agent provider endpoints

YAML/UI keep friendly names (`provider: openai`). When `endpoint` (or API key) differs,
the runtime registers a unique backend key such as `openai@http://host:9000/v1` and
points the agent at that key so two agents with the same provider name never share the
wrong gateway.

## Coordinator actions

```json
{
  "summary": "…",
  "actions": [
    {"type":"promote","task_id":"T2","text":"deps met"},
    {"type":"reassign","task_id":"T3","role":"deep"},
    {"type":"add_task","text":"Add regression test","role":"tester"},
    {"type":"note","task_id":"T1","text":"watch edge case"}
  ],
  "focus_files": ["pkg/foo.go"]
}
```

## Delegating

```bash
slmcode task add "Deep refactor auth" --role deep --column ready_to_dev
slmcode task delegate T1 docs
```

Studio task inspector → role dropdown includes all specialists.

## Project instructions (auto-loaded)

Place any of these at the repo root (or under `.slmcode/`):

- `AGENTS.md` / `AGENT.md`
- `CLAUDE.md`
- `.cursorrules`
- `.slmcode/PROJECT.md`

They are injected into specialist packs at run start.
