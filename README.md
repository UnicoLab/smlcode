# SLMCode

**Claude Code–style coding harness for local SLMs** — built on [GoLangGraph](https://github.com/piotrlaczkowski/GoLangGraph), defaulting to **oMLX** on Apple Silicon.

```
~/Desktop/PROJECT/slmcode/                 ← this project
~/Desktop/PROJECT/GoLangGraph-Project/GoLangGraph/   ← Go dependency
```

**Docs index:** [docs/README.md](docs/README.md) — start with **[TESTING.md](docs/TESTING.md)**.

Also: [GUIDE](docs/GUIDE.md) · [STUDIO](docs/STUDIO.md) · [AGENTS](docs/AGENTS.md) · [ARCHITECTURE](docs/ARCHITECTURE.md)

**Version 0.5.2** — offline Studio GUI (vendored React), system-wide `slmcode update`, hallucination guards, chat/sessions/permissions, 14 specialists, live streams.

## Why SLMs need this

| Large-model habit | SLMCode approach |
|-------------------|------------------|
| Stuff the repo into chat | Incremental `.slmcode/*.md` memory |
| One free-form agent | Plan → atomic tasks → specialists |
| Re-scan every turn | Reuse CONTEXT/MEMORY; skip deep explore |
| Hope the model self-corrects | Reviewer ↔ corrector + multipass |
| Opaque progress | Live CLI + Studio stream (agent/scope/output) |

## Install (system-wide, like Claude Code)

```bash
cd ~/Desktop/PROJECT/slmcode

# Recommended — puts slmcode next to `claude` on PATH (Homebrew /usr/local)
make install-system
# same as: ./scripts/install.sh --system

# Or user-local only
make install                 # → ~/.local/bin/slmcode

omlx start
slmcode doctor
slmcode version              # shows binary path
```

Then use it from **any** project:

```bash
cd ~/any-repo
slmcode init
slmcode run -v "fix the bug"
slmcode chat                 # interactive REPL
slmcode studio
```

### Update (after you improve the code)

```bash
# From anywhere — rebuilds the checkout recorded at install time
slmcode update
slmcode update --check          # dry compare installed vs source

# Or from the repo
cd ~/Desktop/PROJECT/slmcode && make update
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
slmcode studio          # http://127.0.0.1:7420
```

## Killer features

- **Planning + atomic split** sized for ~30B models  
- **Coordinator agent** supervising the kanban  
- **Parallel specialists**: explorer, docs, architect, worker/deep, reviewer, corrector, tester, memory  
- **Self-critic loop** with auto-correct retries  
- **Shared evolving context** — later agents inherit CONTEXT/MEMORY/skills  
- **Auto-updated `SKILLS.md` + `skills/learned/`** knowledge flywheel  
- **Explore reuse** — no deep dive every run when memory is rich  
- **Live streaming** in CLI and Studio (Antigravity/Zed style)  
- Optional **Claude Code CLI** backend  

## Pipeline

```
query → skills → context → explore|reuse → [docs] [architect]
      → plan → split → coordinator → parallel execute
      → review/correct → learn → test → memory → evolve skills
```

## CLI

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + oMLX health |
| `run -v` | Full pipeline with live agent stream |
| `board` / `watch` | Colored kanban |
| `task …` | add/show/edit/move/delegate/check/promote |
| `context` / `docs` / `plan` / `skills` | Markdown memory |
| `studio` | GUI + SSE API |

```bash
slmcode run --think-passes 2 --parallel 3 --retries 2 "…"
slmcode run --backend claude-code "…"
slmcode config set dry_run false
```

## Studio GUI

| Panel | Purpose |
|-------|---------|
| Query bar | Start / stop runs |
| Pipeline strip | Live phases including coord / learn |
| Live tab | Current **@agent**, **scope**, **output** |
| Kanban | Drag-and-drop; edit mid-run |
| Docs | CONTEXT / MEMORY / **SKILLS.md** / PLAN… |
| Settings | Model picker, think passes, parallel, dry-run |

## Develop & test

```bash
make tidy && make test
make install
RUN_E2E=1 go test ./test/e2e/ -run TestLiveOMLX -timeout 45m -v
```

## Embed

```go
import "github.com/piotrlaczkowski/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

## License

MIT
