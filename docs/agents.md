# 🧩 Agents

Twenty built-in specialists. Scoped packs. No “hold the monorepo in your head” cosplay. 🎭

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🧬</span>
<p class="slm-banner__text" markdown>
<strong>Mix brains:</strong> cheap local explorer + sharper cloud reviewer in one run —
see <a href="providers.md">Providers</a>. Budget diplomacy is a feature.
</p>
</div>

---

## Roster 📋

| ID | Tools | Output | When |
|----|-------|--------|------|
| `coordinator` | — | JSON actions | Pre-exec + each wave 🧭 |
| `orchestrator` | — | decisions | Reserved |
| `context` | — | CONTEXT body | Run start 📝 |
| `explorer` | ✅ + `find_models` / `mcp_call` | file map | Deep explore 🔎 |
| `docs` | ✅ + `find_models` / `mcp_call` | docs map | Docs/API queries 📚 |
| `architect` | — | design JSON | Large/refactor 🏛️ |
| `planner` | — | plan JSON | Always 📋 |
| `splitter` | — | tasks JSON | Always ✂️ |
| `worker` | ✅ + `find_models` / `mcp_call` | status | Execute 🛠️ |
| `deep` | ✅ + `find_models` / `mcp_call` | status | Multi-step 🧪 |
| `reviewer` | — | approve JSON | Critic 🔍 |
| `corrector` | ✅ + `find_models` / `mcp_call` | status | On reject 🔧 |
| `tester` | ✅ + `find_models` / `mcp_call` | passed + commands | Real shell verify (pytest/go/smoke) ✅ |
| `placeholder` | ✅ + `find_models` / `mcp_call` | status + gaps | Fill stubs / flag precise gaps 🩹 |
| `escalate` | — | action JSON | HITL timeout arbitrator (retry/re-scope/…) ⚖️ |
| `memory` | — | bullets | Learn 💾 |
| `composer` | — | pipeline JSON | Assemble a task-specific pipeline (dynamic_pipeline) 🎯 |
| `reviewer-strict` | — | approve JSON | Second opinion in the speculative review race (`max_parallel >= 3`), temperature 0 🔍🔍 |
| `describer` | — | prose | Architect half of the describer→editor pair (`architect_editor`) 🗣️ |
| `editor` | ✅ + `find_models` / `mcp_call` | status | Editor half: applies a described change, minimal reasoning, strict format ✍️ |

!!! note "🧰 Coding tools"
    Coding agents share `ws_*` + `git_*` plus **`find_models`** (auth-gated catalog)
    and **`mcp_call`** (single MCP meta-tool — never one tool per MCP capability).

!!! note "⚖ @escalate"
    Fired only when a task hits **max review retries** and the human does not answer
    the escalate modal / `/escalate` within `escalate_ask_timeout` (default 5m) — and only when
    no human is attached; with a TTY or a Studio client the gate blocks instead of expiring.
    Override the specialist with `escalate_timeout_agent` (auto: escalate → reviewer → coordinator).

```bash
curl -s localhost:7420/api/agents | jq '.[].id'
# TUI: /agents
```

---

## Custom agents ✨

`.slmcode/agents/<id>.yaml` or `~/.slmcode/agents/`.

```bash
# TUI
/agent edit worker model=qwen2.5-coder:14b

# CLI
slmcode agent list
slmcode agent show worker
slmcode agent edit worker model=qwen2.5-coder:14b provider=ollama
slmcode agent clear-llm worker
```

Fields: `skills`, `model`, `provider`, `endpoint`, `tools`, `temperature`, `max_tokens`, `max_iter`, `system_prompt`.

**LLM inheritance:** empty `model` / `provider` / `endpoint` means inherit the active
[stack](providers.md#stacks-presets) (global `config.yaml`). Per-agent pins always win.

```text
agent.model    ?? stack/global.model
agent.provider ?? stack/global.provider
agent.endpoint ?? (agent.provider ? default : global.endpoint)
```

`model_profiles` resolve against each agent’s **effective** model (not only the global one).

Different endpoints → unique backend keys (no accidental shared gateway).

### Use anywhere in the pipeline

Custom agents are first-class:

- **Specialist mode** — select them in Studio / `mode: specialist`
- **Board tasks** — set `role: night-auditor` (dropdown lists all `/api/agents`)
- **Pipeline slots** — insert before/after/replace any phase in `.slmcode/pipeline.yaml`

```yaml
# .slmcode/pipeline.yaml
slots:
  - id: audit-explore
    agent: night-auditor
    after: explore
    input: |
      Review exploration for risks.
      {{exploration}}
```

→ [Pipeline](pipeline.md)

---

## Coordinator actions 🧭

```json
{
  "summary": "Auth clear; tests missing",
  "actions": [
    {"type": "promote", "task_id": "T2", "text": "deps met"},
    {"type": "reassign", "task_id": "T3", "role": "deep"},
    {"type": "add_task", "text": "Add regression test", "role": "tester"},
    {"type": "note", "task_id": "T1", "text": "edge case"}
  ],
  "focus_files": ["pkg/foo.go"]
}
```

Air-traffic control, not pilot. Let workers fly the plane.

---

## Delegating 📬

```bash
slmcode task add "Deep refactor auth" --role deep --column ready_to_dev
slmcode task delegate T1 docs
```

---

## Project instructions 📜

Auto-loaded from:

- `AGENTS.md` / `AGENT.md`
- `CLAUDE.md`
- `.cursorrules`
- `.slmcode/PROJECT.md`

!!! example "✨ Tiny AGENTS.md"
    ```markdown
    # Agents
    - Tiny, reviewable diffs
    - No drive-by refactors
    - Match existing style
    ```

→ [🦋 Skills](skills.md) · [🧠 Concepts](concepts.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
