# Studio UI

```bash
slmcode studio
# → http://127.0.0.1:7420
# override: slmcode studio --listen 127.0.0.1:7421
```

Works **offline** — React / ReactDOM / Babel are vendored under `cmd/slmcode/ui/vendor/` (no CDN at runtime).

## Layout

1. **Top** — brand, query bar, Run/Stop, model chip  
2. **Pipeline strip** — init → skills → context → explore → … → coord → execute → learn → done  
3. **Left** — nav (`board` / `run` / `agents` / `skills`), stats, specialist chips  
4. **Center** — kanban + task inspector, or live agent feed  
5. **Right** — markdown docs (CONTEXT, MEMORY, **SKILLS.md**, …) + settings  

## Live feed (Run tab)

SSE events may include:

| Field | Example |
|-------|---------|
| `agent` | `@worker`, `@coordinator` |
| `kind` | `agent_start` / `agent_end` / `coord` / `learn` / `output` |
| `task_id` | `T1` |
| `scope` | `hello.go, pkg/foo.go` |
| `output` | Truncated specialist JSON / text |

The same stream powers `slmcode run -v` and `slmcode chat`.

## Mid-run editing

While agents run you can:

- drag cards between columns  
- promote `to_scope → ready_to_dev`  
- edit CONTEXT/MEMORY (next packs pick it up after the wave)  
- add checklist items / notes  

The loop reloads `board.json` every wave.

## Settings panel

| Control | Effect |
|---------|--------|
| Provider / model / endpoint | LLM backend |
| Backend | `slmcode` specialists or `claude-code` CLI |
| Think passes | Multipass quality |
| Max parallel / review retries | Throughput & critic loop |
| Context budget KB | Pack size for SLMs |
| Dry-run | No code writes |

## HTTP API

| Method | Path |
|--------|------|
| GET | `/api/health` |
| GET, PUT | `/api/config` |
| GET | `/api/docs` |
| GET, PUT | `/api/docs/{name}` |
| GET | `/api/board` `/api/columns` `/api/tasks` |
| POST | `/api/tasks` |
| PATCH, DELETE | `/api/tasks/{id}` |
| GET | `/api/skills` `/api/agents` `/api/models` |
| POST | `/api/runs` `/api/runs/stop` |
| GET | `/api/runs/latest` |
| GET | `/api/events` (SSE) |
| GET | `/` (embedded SPA: `index.html`, `app.jsx`, `styles.css`) |

### Quick curl checks

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s http://127.0.0.1:7420/api/agents | jq '.[].id'
curl -s -X POST http://127.0.0.1:7420/api/runs \
  -H 'Content-Type: application/json' \
  -d '{"query":"Add a doc comment to Hello()"}'
# stream: curl -N http://127.0.0.1:7420/api/events
```
