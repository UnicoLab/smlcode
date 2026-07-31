# 🎨 Studio

The offline cockpit. Kanban, live agent feed, markdown memory, settings — no CDN required at runtime.

```bash
slmcode studio
# → http://127.0.0.1:7420
# busy port?  slmcode studio --listen 127.0.0.1:7421
```

!!! success "Airplane mode friendly"
    React / ReactDOM / Babel are vendored under `cmd/slmcode/ui/vendor/`. Cafe Wi‑Fi can implode; Studio will not.

---

## 🗺️ Layout

```text
┌──────────────────────────────────────────────────────────┐
│  Brand · Query bar · Run/Stop · Model chip               │
├──────────────────────────────────────────────────────────┤
│  Pipeline strip: init → skills → … → execute → learn     │
├──────────┬────────────────────────────┬──────────────────┤
│  Nav     │  Kanban / Live feed        │  Docs + Settings │
│  Agents  │  Task inspector            │  CONTEXT/MEMORY  │
│  Skills  │                            │                  │
└──────────┴────────────────────────────┴──────────────────┘
```

| Zone | What you do |
|------|-------------|
| Query bar | Start / stop runs |
| Pipeline strip | Watch phases light up (including coord / learn) |
| Live tab | Current `@agent`, **scope**, file patches, output |
| Kanban | Drag cards, promote, edit mid-run |
| Docs | Edit CONTEXT / MEMORY / SKILLS / PLAN live |
| Settings | Provider, model, endpoint, think passes, dry-run |

---

## 📡 Live feed

SSE events (same stream as `slmcode run -v`):

| Field | Example |
|-------|---------|
| `agent` | `@worker`, `@coordinator` |
| `kind` | `agent_start` / `agent_end` / `coord` / `learn` / `output` |
| `task_id` | `T1` |
| `scope` | `hello.go, pkg/foo.go` |
| `output` | Truncated specialist JSON / text |

---

## ✏️ Mid-run editing

While agents run you can:

- drag cards between columns
- promote `to_scope → ready_to_dev`
- edit CONTEXT / MEMORY (next packs pick it up after the wave)
- add checklist items / notes

The loop reloads `board.json` every wave. Chaos, but *coordinated* chaos.

---

## ⚙️ Settings that matter

| Control | Effect |
|---------|--------|
| Provider / model / endpoint | Which brain answers the phone |
| Think passes | Multipass quality |
| Max parallel / review retries | Throughput & critic stubbornness |
| Context budget KB | Pack size (SLMs like diets) |
| Dry-run / permission | Safety rails |

---

## 🌐 HTTP API

| Method | Path |
|--------|------|
| `GET` | `/api/health` |
| `GET`, `PUT` | `/api/config` |
| `GET` | `/api/docs` |
| `GET`, `PUT` | `/api/docs/{name}` |
| `GET` | `/api/board` `/api/columns` `/api/tasks` |
| `POST` | `/api/tasks` |
| `PATCH`, `DELETE` | `/api/tasks/{id}` |
| `GET` | `/api/skills` `/api/agents` `/api/models` |
| `POST` | `/api/runs` `/api/runs/stop` |
| `GET` | `/api/runs/latest` |
| `GET` | `/api/events` (SSE) |
| `GET` | `/` embedded SPA |

### Quick curl checks

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s http://127.0.0.1:7420/api/agents | jq '.[].id'
curl -s -X POST http://127.0.0.1:7420/api/runs \
  -H 'Content-Type: application/json' \
  -d '{"query":"Add a doc comment to Hello()"}'
# stream:
curl -N http://127.0.0.1:7420/api/events
```

Made with ♥ by [UnicoLab](https://unicolab.ai)
