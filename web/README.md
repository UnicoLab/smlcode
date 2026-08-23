# SLMCode Studio — Web Frontend

Modern React + TypeScript frontend for the SLMCode coding agent harness.

## Quick Start

```bash
# From the slmcode root:
cd web
npm install
npm run dev
```

The dev server runs on `http://localhost:5173` and proxies API calls to `http://127.0.0.1:7420` (the SLMCode Studio backend).

Start the backend:
```bash
# From slmcode root:
make studio
# or:
slmcode studio --listen 127.0.0.1:7420 --dev-cors
```

`--dev-cors` is required for `npm run dev`: the Vite server is a different
origin (`:5173`) from the API (`:7420`), and Studio ships **no** CORS headers by
default. See "Security model" below.

## Scripts

| Command | What it does |
|---------|--------------|
| `npm run dev` | Vite dev server on :5173, proxying `/api` to :7420 |
| `npm run build` | `tsc -b` + production build into `dist/` |
| `npm run typecheck` | `tsc --noEmit` |
| `npm run lint` | typecheck **and** ESLint (react-hooks + jsx-a11y) |
| `npm test` | Vitest + Testing Library |
| `npm run test:coverage` | Vitest with v8 coverage |

`react-hooks/exhaustive-deps` is an **error**, not a warning: a stale-closure
bug in the SSE handler once reduced the live event log to a single row, and that
rule is what catches it.

## Security model

Studio is a local agent with file-read, config-write, API-key-write and
run-start capability, so the API is locked down by default:

* **Loopback only** — a request whose `Host` is not `127.0.0.1` / `::1` /
  `localhost` is rejected with 403. This blocks DNS rebinding.
* **Same-origin only** — no `Access-Control-Allow-Origin` is emitted at all
  unless the server was started with `--dev-cors`, which allows exactly the Vite
  dev origins. A cross-origin `Origin` or a `Sec-Fetch-Site: cross-site` request
  is refused, so no page the user happens to visit can start a run or read the
  repo.
* **Session token** — when the CLI starts Studio with a token, every `/api/*`
  request must carry it as `X-SLMCode-Token`, as `Authorization: Bearer …`, or
  as `?t=…` (EventSource cannot set headers). The SPA picks it up from the `?t=`
  parameter of the URL the CLI prints, or from the
  `<meta name="slmcode-token">` tag the server injects into `index.html`, and
  stores it in `sessionStorage` — see `src/api/session.ts`. The parameter is
  stripped from the address bar on first read.
* `--no-auth` disables the token for embedded use; loopback and origin
  enforcement stay on.

## Fonts

Studio downloads **no** webfonts. Typography uses the platform UI stack; drop
`inter-variable.woff2` / `jetbrains-mono-variable.woff2` into `public/fonts/` to
opt into Inter and JetBrains Mono locally. See `public/fonts/README.md`.

## Architecture

```
web/
├── src/
│   ├── api/client.ts          # Typed API client for all 30+ endpoints
│   ├── types/index.ts         # TypeScript interfaces
│   ├── components/
│   │   ├── Layout.tsx         # App shell (sidebar + topbar + content)
│   │   ├── TopBar.tsx         # Query, run/stop, model search + costs + cycle
│   │   ├── Sidebar.tsx        # Navigation + stats + connection status
│   │   ├── Board/
│   │   │   ├── KanbanBoard.tsx # Drag-and-drop task board
│   │   │   └── TaskCard.tsx   # Individual task card
│   │   ├── Live/
│   │   │   ├── LiveView.tsx   # SSE agent stream viewer
│   │   │   └── EventLog.tsx   # Phase-colored event log
│   │   ├── Settings/
│   │   │   ├── SettingsPanel.tsx # Full config editor
│   │   │   └── StackSelector.tsx # One-click model stack switching
│   │   ├── Pipeline/
│   │   │   └── PipelineEditor.tsx # Phase/agent pipeline editor
│   │   ├── Agents/
│   │   │   └── AgentManager.tsx # CRUD for custom specialist agents
│   │   ├── Skills/
│   │   │   └── SkillManager.tsx # Skill packs manager
│   │   └── Docs/
│   │       └── MarkdownEditor.tsx # Split-pane markdown editor
│   ├── styles/globals.css     # Tailwind + custom components
│   ├── App.tsx                # Routes + app context
│   └── main.tsx               # Entry point
```

## Model Stacks

Studio **Settings → Model Stack** loads presets from `GET /api/stacks` (YAML in
`../stacks/`). Apply uses `POST /api/stacks/{id}/apply` (merge — keeps listen,
skills, MCP, API keys). Optional checkboxes: clear agent LLM pins, apply stack
`agents:` defaults, force overwrite.

```bash
slmcode stack list
slmcode stack apply deepseek
slmcode stack apply openai --agents
# or: make stack-apply stack=deepseek agents=1
```

Hierarchy: stack sets global provider/model → each agent may override → empty
agent fields inherit the stack.

TopBar model menu: `GET /api/models` (auth-aware, `enabled_models` filter, `$/MTok`
costs, Cycle through allow-list). Settings: compact engine, retries, auto-refine,
MCP status (`GET /api/mcp`), Save key to `auth.json` (`PUT /api/auth`).

## Build

```bash
npm run build        # Production build → dist/
npm run preview      # Preview production build
npx tsc --noEmit     # Type check only
```
