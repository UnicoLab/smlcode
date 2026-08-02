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
| 🧩 Pipeline tab | Bind agents per phase, insert slots, loop roles |
| 📡 Live | `@agent`, scope, patches, slots, output |
| 📋 Kanban | Drag, promote, edit mid-run (any agent role) |
| 💾 Docs | Live markdown memory |
| ⚙️ Settings | Provider, knobs, safety |

---

## Mid-run editing ✏️

While agents run you can drag cards, promote columns, edit CONTEXT/MEMORY, and add notes.
The loop reloads `board.json` each wave. Chaos, but *structured* chaos.

---

## Human-in-the-loop modals ✋

Studio blocks the relevant step with a modal (pipeline header shows **Awaiting you**):

| Modal | When | Options | Timeout default |
|-------|------|---------|-----------------|
| Clarify | vague PRD interview | pick options | 2m → recommended |
| Plan approve | before execute | approve / replan | 2m → approve |
| **Escalate** | task hits max review retries | **re-scope / retry / mark done / abort** | **30s → re-scope** |
| Continue | QA/retries exhausted | continue / stop / flag | 2m → stop |
| Shell | `shell_permission=ask` | approve / deny | 2m |

Config (Settings → Planning / scope, or YAML):

```yaml
escalate_ask: ask              # ask | auto | off
escalate_ask_timeout: 30s
continue_ask: ask
```

TUI: `/escalate re_scope|retry|mark_done|abort` while the banner shows escalate pending.

API: `GET /api/escalate/pending` · `POST /api/escalate/answer` `{"action":"retry"}`.

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
| `GET`/`POST`/`PUT`/`DELETE` | `/api/agents` (custom + overrides) |
| `GET` | `/api/skills` `/api/models` |
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
