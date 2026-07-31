# 🎨 Studio

Offline cockpit: kanban, live feed, markdown memory, settings.
No CDN at runtime — React/Babel are vendored.

```bash
slmcode studio
# → http://127.0.0.1:7420
slmcode studio --listen 127.0.0.1:7421
```

!!! success "Airplane mode"
    Cafe Wi‑Fi can implode. Studio will not.

---

## Layout

```text
┌──────────────────────────────────────────────────────────┐
│  Brand · Query · Run/Stop · Model                        │
├──────────────────────────────────────────────────────────┤
│  Pipeline: init → skills → … → execute → learn           │
├──────────┬────────────────────────────┬──────────────────┤
│  Nav     │  Kanban / Live             │  Docs / Settings │
│  Agents  │  Task inspector            │  CONTEXT/MEMORY  │
│  Skills  │                            │                  │
└──────────┴────────────────────────────┴──────────────────┘
```

| Zone | Job |
|------|-----|
| Query bar | Start / stop |
| Pipeline strip | Phase visibility |
| Live | `@agent`, scope, patches, output |
| Kanban | Drag, promote, edit mid-run |
| Docs | Live markdown memory |
| Settings | Provider, knobs, safety |

---

## Mid-run editing

While agents run you can drag cards, promote columns, edit CONTEXT/MEMORY, and add notes.
The loop reloads `board.json` each wave.

---

## Live events (SSE)

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

## HTTP API

| Method | Path |
|--------|------|
| `GET` | `/api/health` |
| `GET`/`PUT` | `/api/config` |
| `GET`/`PUT` | `/api/docs`, `/api/docs/{name}` |
| `GET`/`POST`/`PATCH`/`DELETE` | board / tasks |
| `GET` | `/api/skills` `/api/agents` `/api/models` |
| `POST` | `/api/runs` `/api/runs/stop` |
| `GET` | `/api/runs/latest` `/api/events` |
| `GET` | `/` SPA |

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s -X POST http://127.0.0.1:7420/api/runs \
  -H 'Content-Type: application/json' \
  -d '{"query":"Add a doc comment to Hello()"}'
```

---

## Pair with the TUI

```bash
# A
slmcode studio
# B
slmcode watch
```

→ [TUI](tui.md) · [Recipes](recipes.md)

Made with ♥ by [UnicoLab](https://unicolab.ai)
