# 🎨 Studio

Offline cockpit: kanban, live feed, markdown memory, settings.
No CDN at runtime — React/Babel are vendored. Cafe Wi‑Fi can implode; Studio will not. ✈️

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🕹️</span>
<p class="slm-banner__text" markdown>
<strong>Mission control vibes:</strong> start a run, watch agents stream, drag cards mid-flight,
edit CONTEXT while the loop is still thinking. Feel powerful. Use responsibly.
</p>
</div>

```bash
slmcode studio
# → http://127.0.0.1:7420
slmcode studio --listen 127.0.0.1:7421
```

!!! success "✈️ Airplane mode"
    Cafe Wi‑Fi can implode. Studio will not. Bring snacks either way.

---

## Layout 🗺️

```text
┌──────────────────────────────────────────────────────────┐
│  Brand · Query · Run/Stop · Model                        │
├──────────────────────────────────────────────────────────┤
│  Pipeline: init → skills → … → execute → learn           │
├──────────┬────────────────────────────┬──────────────────┤
│  Nav     │  Kanban / Live / Pipeline  │  Docs / Settings │
│  Agents  │  Task inspector            │  CONTEXT/MEMORY  │
│  Skills  │  Phase · slots editor      │                  │
└──────────┴────────────────────────────┴──────────────────┘
```

| Zone | Job |
|------|-----|
| 🎯 Query bar | Start / stop |
| 🏭 Pipeline strip | Dynamic phases from `.slmcode/pipeline.yaml` |
| 🧩 Pipeline tab | Bind agents per phase, insert slots, loop roles, **switch presets** |
| 🧱 Blocks tab | Browse & apply language packs, pipelines, agents, quality packs |
| 📡 Live | `@agent`, scope, patches, slots, output |
| 📋 Kanban | Drag, promote, edit mid-run (any agent role) |
| 💾 Docs | Live markdown memory |
| ⚙️ Settings | Provider, knobs, safety, **stack + pack selector** |

---

## Mid-run editing ✏️

While agents run you can drag cards, promote columns, edit CONTEXT/MEMORY, and add notes.
The loop reloads `board.json` each wave. Chaos, but *structured* chaos.

---

## File inspector 🔍

The **File Inspector** page lets you browse and inspect any file in the workspace
without leaving Studio. Click any file in the tree to open it in a read-only viewer
with syntax highlighting and line numbers.

| Feature | Details |
|---------|---------|
| 📂 **File tree** | Full project tree — click any file to inspect |
| 🎨 **Syntax highlighting** | Language-aware highlighting for Go, Python, JS/TS, YAML, JSON, Markdown, and more |
| 🔢 **Line numbers** | Gutter line numbers for precise referencing |
| 🆚 **Diff view** | Toggle to compare current file content against the last checkpoint snapshot (`rewind` data) |
| 🔄 **Live refresh** | File content auto-refreshes when workers write changes during a run |
| 🧊 **Read-only** | Inspection only — no accidental edits. Use the TUI or your editor to make changes |

Use the File Inspector to:
- Verify worker edits at a glance during a run
- Spot-check generated code before the tester phase
- Compare diffs against the pre-run checkpoint to understand what changed
- Browse AGENTS.md, CONTEXT.md, and MEMORY.md in one place

Access it from the left navigation bar — the `📄 Files` tab sits alongside Agents,
Skills, and Blocks.

---

## Human-in-the-loop modals ✋

Studio blocks the relevant step with a modal (pipeline header shows **Awaiting you**):

| Modal | When | Options | Timeout default |
|-------|------|---------|-----------------|
| Clarify | vague PRD interview | pick options | 2m → recommended |
| Plan approve | before execute | approve / replan | 2m → approve |
| **Escalate** | task hits max review retries | **re-scope / retry / mark done / abort** | **30s → @escalate SLM decides** |
| Continue | QA/retries exhausted | continue / stop / flag | 2m → stop |
| Shell | `shell_permission=ask` | approve / deny | 2m |

Config (Settings → Planning / scope, or YAML):

```yaml
escalate_ask: ask              # ask | auto | off
escalate_ask_timeout: 30s      # then @escalate (or escalate_timeout_agent) decides
escalate_timeout_agent: ""     # empty = @escalate → @reviewer → @coordinator
continue_ask: ask
```

TUI: `/escalate re_scope|retry|mark_done|abort` while the banner shows escalate pending.
On timeout the dedicated **@escalate** arbitrator picks retry / re-scope / abort / mark_done.

API: `GET /api/escalate/pending` · `POST /api/escalate/answer`
`{"ask_id":"<pending ask.id>","action":"retry"}`.

Manual HITL calls should always read the matching `GET .../pending` response
first and post the returned `ask.id` as `ask_id`. Expired asks are cleared and
reported as `{"pending":false,"expired":true}`.

### HITL popup overlay

v0.10.1 introduced a redesigned **HITL popup** — a modal overlay that replaces the
old inline prompt pattern with a focused, non-dismissible dialog:

| Element | Behavior |
|---------|----------|
| ⏱️ **Countdown timer** | Visible countdown bar showing remaining decision time; pulses red when under 10 seconds |
| 🏷️ **Context header** | Shows affected task ID, agent name, and retry count (for escalate) |
| 🎯 **Action buttons** | Large, color-coded buttons for each available action (approve, deny, retry, re-scope, etc.) |
| 🚫 **Non-dismissible** | Cannot click away or close — a decision is required (or the timeout fires) |
| 📝 **Optional note** | Text field for adding a rationale that gets logged with the decision |
| 🔔 **Pipeline indicator** | The pipeline progress strip shows **Awaiting you** with a pulsing amber indicator |

The popup appears for all HITL triggers: clarify, plan approve, escalate, continue,
and shell permission requests. When the timeout fires, the configured fallback agent
(e.g. `@escalate` for escalate decisions) takes over automatically.

---

## Live events (SSE) 📡

Same stream as `slmcode run -v`:

| Field | Example |
|-------|---------|
| `agent` | `@worker` |
| `kind` | `agent_start` / `coord` / `learn` / `output` |
| `task_id` | `T1` |
| `scope` | focus files |
| `output` | truncated specialist text |

```bash
curl -N http://127.0.0.1:7420/api/events
```

---

## HTTP API 🔌

| Method | Path |
|--------|------|
| `GET` | `/api/health` |
| `GET`/`PUT` | `/api/config` |
| `GET`/`PUT` | `/api/pipeline` · `POST /api/pipeline/reset` |
| `GET`/`PUT` | `/api/docs`, `/api/docs/{name}` |
| `GET`/`POST`/`PATCH`/`DELETE` | board / tasks |
| `GET`/`POST`/`PUT`/`DELETE` | `/api/agents` (custom + overrides; includes `effective_model`) |
| `GET` | `/api/skills` |
| `GET` | `/api/models?q=&limit=` (search + auth + costs + enabled_models) |
| `GET` | `/api/auth` (provider credential status + auth.json keys) |
| `PUT` | `/api/auth` (`{"provider","api_key"}` → `.slmcode/auth.json`) |
| `GET` | `/api/mcp` (MCP servers + `mcp_call` meta-tool status) |
| `GET` | `/api/config/schema` (patchable field metadata + slash help) |
| `GET` | `/api/queries/{id}/events` (JSONL session event tree) |
| `GET` | `/api/stacks` · `/api/stacks/{id}` |
| `POST` | `/api/stacks/{id}/apply` (`clear_agent_llm`, `apply_agent_defaults`, `force_agents`) |
| `POST` | `/api/runs` `/api/runs/stop` |
| `GET` | `/api/runs/latest` `/api/events` |
| `GET`/`POST` | `/api/escalate/pending` · `/api/escalate/answer` |
| `GET`/`POST` | `/api/continue/pending` · `/api/continue/answer` |
| `GET` | `/` SPA |

→ Full pipeline schema: [Pipeline](pipeline.md)

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s -X POST http://127.0.0.1:7420/api/runs \
  -H 'Content-Type: application/json' \
  -d '{"query":"Add a doc comment to Hello()"}'
```

---

## Pair with the TUI 🥊

```bash
# A
slmcode studio
# B
slmcode watch
```

→ [🖥️ TUI](tui.md) · [🧪 Recipes](recipes.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
