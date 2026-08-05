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
│   │   ├── TopBar.tsx         # Query input, run/stop, model selector
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

The StackSelector provides one-click switching between 8 pre-configured model stacks:

| Stack | Provider | Model |
|-------|----------|-------|
| oMLX Local | omlx | Qwen3-Coder-30B-A3B-Instruct-MLX-4bit |
| DeepSeek | deepseek | deepseek-chat |
| Qwen Coder | openrouter | qwen/qwen-2.5-coder-32b-instruct |
| Google Gemini | gemini | gemini-2.0-flash |
| OpenAI GPT-4o | openai | gpt-4o |
| Ollama Local | ollama | qwen2.5-coder:7b |
| OpenRouter | openrouter | anthropic/claude-sonnet-4 |
| Groq | groq | llama-3.3-70b-versatile |

Stack YAML configs live in `../stacks/` — use `make stack-apply stack=<name>` to apply them from the CLI.

## Build

```bash
npm run build        # Production build → dist/
npm run preview      # Preview production build
npx tsc --noEmit     # Type check only
```
