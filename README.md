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
  <img alt="version" src="https://img.shields.io/badge/version-0.5.6-14b8a6?style=flat-square" />
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
slmcode init
slmcode run -v "fix the bug"
slmcode chat                 # interactive REPL
slmcode studio               # http://127.0.0.1:7420
```

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

## CLI

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + oMLX health |
| `run -v` | Full pipeline with live agent stream |
| `chat` | Interactive REPL |
| `board` / `watch` | Colored kanban |
| `task …` | add / show / edit / move / delegate / check / promote |
| `context` / `docs` / `plan` / `skills` | Markdown memory |
| `studio` | GUI + SSE API |
| `update` | Rebuild & reinstall from source |

## Studio

| Panel | Purpose |
|-------|---------|
| Query bar | Start / stop runs |
| Pipeline strip | Live phases including coord / learn |
| Live tab | Current **@agent**, **scope**, **output** |
| Kanban | Drag-and-drop; edit mid-run |
| Docs | CONTEXT / MEMORY / SKILLS.md / PLAN… |
| Settings | Model picker, think passes, parallel, dry-run |

## Develop & test

```bash
make tidy && make test
make install
RUN_E2E=1 go test ./test/e2e/ -run TestLiveOMLX -timeout 45m -v
```

Local layout during development:

```
~/Desktop/PROJECT/slmcode/                           ← this project
~/Desktop/PROJECT/GoLangGraph-Project/GoLangGraph/   ← Go dependency (go.mod replace)
```

## Embed

```go
import "github.com/piotrlaczkowski/slmcode/pkg/harness"

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
