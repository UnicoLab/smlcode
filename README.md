<p align="center">
  <img src="assets/slmcode-logo.png" alt="SLMCode" width="160" />
</p>

<h1 align="center">SLMCode</h1>

<p align="center">
  <strong>Claude Code–style coding harness for local SLMs</strong><br/>
  Plan → atomic tasks → parallel specialists → self-critic → learning<br/>
  Built on <a href="https://github.com/piotrlaczkowski/GoLangGraph">GoLangGraph</a> · defaults to <strong>oMLX</strong> on Apple Silicon
</p>

<p align="center">
  <img alt="version" src="https://img.shields.io/badge/version-0.5.14-0f6e8c?style=flat-square" />
  <img alt="go" src="https://img.shields.io/badge/go-1.23+-00ADD8?style=flat-square&logo=go&logoColor=white" />
  <img alt="license" src="https://img.shields.io/badge/license-MIT-0ea5e9?style=flat-square" />
  <img alt="platform" src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-2dd4bf?style=flat-square" />
</p>

---

## Why SLMs need this

Cloud coding agents assume a frontier model that can swallow a whole repo.
Small local models need a different loop.

| Large-model habit | SLMCode approach |
|-------------------|------------------|
| Stuff the repo into chat | Incremental `.slmcode/*.md` memory |
| One free-form agent | Plan → atomic tasks → specialists |
| Re-scan every turn | Reuse CONTEXT/MEMORY; skip deep explore |
| Hope the model self-corrects | Reviewer ↔ corrector + multipass |
| Opaque progress | Live CLI + Studio stream (agent / scope / output) |

## Features

- **Planning + atomic split** sized for ~30B models
- **Coordinator** supervising a live kanban board
- **14 specialists** — explorer, docs, architect, worker/deep, reviewer, corrector, tester, memory, and more
- **Self-critic loop** with auto-correct retries
- **Shared evolving context** — later agents inherit CONTEXT / MEMORY / skills
- **Skills flywheel** — auto-updated `SKILLS.md` + `skills/learned/`
- **Explore reuse** — no deep dive every run when memory is rich
- **Token-stream early-exit** — cancel wasted decode once JSON / tool-call args are complete (GoLangGraph `CompleteStream`)
- **SLM JSON repair** — trailing commas, single quotes, truncated braces, KV fallbacks for weak tool-calling
- **Phase latency telemetry** — plan/split/execute/worker timings in logs + TUI `/stats`
- **Shell permission modes** — `allow` | `ask` | `deny` for `ws_shell` (independent of file writes)
- **Premium TUI** — `/compact`, `/sessions`, `/permission`, `/stats`, agent CRUD, stop/cancel
- **Live streaming** in CLI and Studio
- **Offline Studio GUI** (vendored React) at `http://127.0.0.1:7420`
- Optional **Claude Code CLI** backend

## Pipeline

```
query → skills → context → explore|reuse → [docs] [architect]
      → plan → split → coordinator → parallel execute
      → review/correct → learn → test → memory → evolve skills
```

## Install

```bash
# From this repo
make install-system          # → Homebrew /usr/local (or /opt/homebrew)
# or user-local:
make install                 # → ~/.local/bin/slmcode

omlx start
slmcode doctor
slmcode version
```

Then from **any** project:

```bash
cd ~/any-repo
slmcode                      # premium TUI (default; also: slmcode tui)
slmcode init
slmcode run -v "fix the bug"
slmcode chat                 # classic REPL
slmcode studio               # http://127.0.0.1:7420
```

> Docs sometimes spell the project **smlcode** — the installed binary is **`slmcode`**.

### Update

```bash
slmcode update               # rebuild from the checkout recorded at install
slmcode update --check       # dry compare installed vs source
make update                  # from the repo (= install-system)
```

Uninstall: `make uninstall-system` or `./scripts/install.sh --uninstall --system`

## Quick start

```bash
cd your-project
slmcode init
# edit .slmcode/PROJECT.md

slmcode run -v "add validation to the login handler"
slmcode board
slmcode watch
slmcode studio
```

Useful flags:

```bash
slmcode run --think-passes 2 --parallel 3 --retries 2 "…"
slmcode run --backend claude-code "…"
slmcode run --agent explorer "Where is auth handled?"
slmcode run --skill atomic-coding "Refactor helpers"
slmcode config set dry_run false
```

### Provider & model (any OpenAI-compatible endpoint)

Defaults target local oMLX, but nothing is hard-wired. Switch freely via flags, env, config, or Studio Settings:

```bash
# OpenAI cloud
slmcode run --provider openai --model gpt-4o-mini \
  --endpoint https://api.openai.com/v1 --api-key "$OPENAI_API_KEY" "…"

# Ollama
slmcode run --provider ollama --model qwen2.5-coder:14b \
  --endpoint http://127.0.0.1:11434 "…"

# LM Studio / vLLM / any OpenAI-compat gateway
slmcode run --provider lmstudio --model local-coder \
  --endpoint http://127.0.0.1:1234/v1 "…"

# Env overrides (applied on every command)
export SLMCODE_PROVIDER=openrouter
export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
export SLMCODE_ENDPOINT=https://openrouter.ai/api/v1
export SLMCODE_API_KEY=…   # or OPENAI_API_KEY / provider-specific *_API_KEY

# Persist in the project
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set endpoint http://127.0.0.1:11434
slmcode doctor               # shows active provider + model + reachability
```

`provider` may be any name: known presets (`omlx`, `ollama`, `openai`, `lmstudio`, `openrouter`, `vllm`, `litellm`, `together`, `groq`, `deepseek`, …) or a custom id. Non-Ollama providers use the OpenAI Chat Completions API at `endpoint` (auto-appends `/v1` when needed).

## CLI

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + active provider/model health |
| `run -v` | Full pipeline with live agent stream + latency |
| `tui` / bare `slmcode` | Premium interactive TUI (default) |
| `chat` | Classic REPL |
| `board` / `watch` | Colored kanban |
| `task …` | add / show / edit / move / delegate / check / promote |
| `context` / `docs` / `plan` / `skills` | Markdown memory |
| `studio` | GUI + SSE API |
| `update` | Rebuild & reinstall from source |

TUI slash commands worth knowing: `/compact`, `/sessions`, `/stats`, `/permission`, `/agents`, `/stop`, `/resume`.

Mid-run **Ctrl+C** or `/stop` checkpoints the board plus ReAct message history under `.slmcode/queries/<id>/react/`; `/resume` or `slmcode session resume` continues from that history when present (not a cold replan). Memory injection ranks prior summaries via `/v1/embeddings` when available, else a pure-Go local hashing embedder, else lexical TF-IDF (`doctor` reports `openai` / `local` / `lexical`).

## Studio

| Panel | Purpose |
|-------|---------|
| Query bar | Start / stop runs |
| Pipeline strip | Live phases including coord / learn |
| Live tab | Current **@agent**, **scope**, **file patches**, **output** |
| Kanban | Drag-and-drop; edit mid-run |
| Docs | Markdown edit / preview / split |
| Settings | Provider + model + endpoint, QA gate, think passes |

## Develop & test

```bash
make tidy && make test          # unit + race-friendly pkgs + Studio JS smoke
make e2e                        # API/UI interaction + isolated board sandbox
make install / make install-system
RUN_E2E=1 make e2e              # also live multi-agent + oMLX pipeline
```

Engine notes (0.5.14+): mid-ReAct HITL resume with message history, local hashing embeddings offline, rename disk fast-path before reviewer. Prior (0.5.12+): token-stream early-exit, tool-arg JSON repair, phase latency, shell permission modes, TUI compact/sessions/stats.

Local layout during development:

```
~/Desktop/PROJECT/slmcode/                           ← this project
~/Desktop/PROJECT/GoLangGraph-Project/GoLangGraph/   ← Go dependency (go.mod replace)
```

## Embed

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

## Docs

| Doc | When to read |
|-----|----------------|
| **[TESTING](docs/TESTING.md)** | Start here — smoke test, Studio, chat, e2e |
| **[GUIDE](docs/GUIDE.md)** | Daily CLI / Studio workflow |
| **[STUDIO](docs/STUDIO.md)** | GUI panels + HTTP/SSE API |
| **[AGENTS](docs/AGENTS.md)** | Specialist roster & coordinator actions |
| **[ARCHITECTURE](docs/ARCHITECTURE.md)** | Internals, streaming, knowledge flywheel |

Full index: [docs/README.md](docs/README.md)

## License

[MIT](LICENSE)
