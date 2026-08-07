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
slmcode studio --listen 127.0.0.1:7420
```

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
