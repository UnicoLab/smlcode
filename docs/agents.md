# 🧩 Agents

Fourteen specialists. One kanban board. Zero “please hold the entire repo in your head” fantasies.

All agents register with GoLangGraph `SubAgentExecutor` and receive **scoped TaskPacks** — never the whole monorepo gift-wrapped as a prompt.

!!! tip "Mix brains"
    Per-agent providers let you run a cheap local explorer and a sharper cloud reviewer in the same pipeline. See [Providers](providers.md).

---

## 👥 Roster (14)

| ID | Tools | Output | When |
|----|-------|--------|------|
| `coordinator` | — | JSON actions | Pre-execute + after each wave |
| `orchestrator` | — | short decisions | Registered / reserved |
| `context` | — | CONTEXT.md body | Start of run |
| `explorer` | ✅ | JSON file map | When deep explore needed |
| `docs` | ✅ | JSON docs map | Docs/API/README queries |
| `architect` | — | JSON design | Design/refactor/large queries |
| `planner` | — | JSON plan | Always (multipass) |
| `splitter` | — | JSON tasks | Always (multipass) |
| `worker` | ✅ | JSON status | Kanban execute |
| `deep` | ✅ | JSON status | Multi-step tasks (`--role deep`) |
| `reviewer` | — | JSON approve | Self-critic per task |
| `corrector` | ✅ | JSON status | On review reject |
| `tester` | ✅ | JSON passed | End validation |
| `memory` | — | markdown bullets | End + wave learn |

List live:

```bash
curl -s localhost:7420/api/agents | jq '.[].id'
# Studio → Agents
# TUI → /agents
```

---

## 🛠️ Custom agents

Persist under `.slmcode/agents/<id>.yaml` (or `~/.slmcode/agents/`).

```bash
# TUI wizard or key=value
/agent new
/agent new id=night-auditor title=Night provider=openai endpoint=http://127.0.0.1:9000/v1
/agent edit worker model=qwen2.5-coder:14b   # builtin override
/agent show night-auditor
/agent delete night-auditor
```

Fields: `skills`, `model`, `provider`, `endpoint`, `tools`, `temperature`, `max_tokens`, `max_iter`, `system_prompt` — same store as `POST/PUT/DELETE /api/agents`.

### Per-agent endpoints

YAML/UI keep friendly names (`provider: openai`). When `endpoint` (or API key) differs, the runtime registers a unique backend key such as `openai@http://host:9000/v1` so two agents with the same provider name never share the wrong gateway.

---

## 🎯 Coordinator actions

The coordinator doesn't write code. It **steers the board**.

```json
{
  "summary": "Auth path is clear; tests still missing",
  "actions": [
    {"type": "promote", "task_id": "T2", "text": "deps met"},
    {"type": "reassign", "task_id": "T3", "role": "deep"},
    {"type": "add_task", "text": "Add regression test", "role": "tester"},
    {"type": "note", "task_id": "T1", "text": "watch edge case"}
  ],
  "focus_files": ["pkg/foo.go"]
}
```

---

## 📤 Delegating

```bash
slmcode task add "Deep refactor auth" --role deep --column ready_to_dev
slmcode task delegate T1 docs
```

Studio task inspector → role dropdown includes all specialists.

---

## 📜 Project instructions (auto-loaded)

Drop any of these at the repo root (or under `.slmcode/`):

- `AGENTS.md` / `AGENT.md`
- `CLAUDE.md`
- `.cursorrules`
- `.slmcode/PROJECT.md`

They're injected into specialist packs at run start. Teach the crew your house style once; stop repeating yourself forever.

!!! example "Tiny AGENTS.md"
    ```markdown
    # Agents

    - Prefer tiny, reviewable diffs
    - Table-driven tests in Go
    - Never rewrite unrelated files "while you're there"
    ```

Made with ♥ by [UnicoLab](https://unicolab.ai)
