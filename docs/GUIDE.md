# SLMCode User Guide

SLMCode is a **Claude Code–style harness for local SLMs** (default: oMLX + Qwen3-Coder-30B).
It turns a query into **plan → atomic tasks → parallel specialists → self-critic → learning**,
with live CLI + Studio streaming.

> New here? Follow **[TESTING.md](TESTING.md)** first.

## Install (system-wide)

```bash
cd ~/Desktop/PROJECT/slmcode
make install-system   # → /opt/homebrew/bin/slmcode (or /usr/local/bin)
# or:  ./scripts/install.sh --system
# user: make install  → ~/.local/bin/slmcode

omlx start            # OpenAI-compatible API on :8000
slmcode doctor
slmcode version       # shows path — use from any directory
```

API key: `~/.omlx/settings.json` → `auth.api_key`, or `OMLX_API_KEY` / `SLMCODE_API_KEY`.

Tab completion (installed automatically with `--system` on zsh/Homebrew):

```bash
slmcode completion zsh > "$(brew --prefix)/share/zsh/site-functions/_slmcode"
```

### Updating the installed binary

After you change SLMCode (or GoLangGraph), refresh the system install:

```bash
slmcode update                 # from any directory
slmcode update --check         # show installed vs source versions
make update                    # from the checkout (= make install-system)
```

`install.sh` writes `~/.config/slmcode/install.json` (source path + mode).  
Override with `SLMCODE_SRC=/path/to/slmcode` or `slmcode update --src …`.

## Day-to-day

```bash
cd your-project
slmcode init
# optional: AGENTS.md or CLAUDE.md at repo root (auto-loaded)
# edit .slmcode/PROJECT.md with stack + conventions

slmcode run -v "Add validation to the login handler"
slmcode run --agent explorer "Where is auth handled?"   # single specialist
slmcode run --skill atomic-coding "Refactor helpers"    # pin a skill
slmcode run "Fix login @skill:multipass-quality"        # Claude-style @skill ref
slmcode skills list | show | new | edit
slmcode board
slmcode watch                 # live terminal kanban
slmcode chat                  # interactive REPL
slmcode studio                # http://127.0.0.1:7420
```

### Skills & specialists

Every specialist has a bundled `specialist-<role>` skill (plus shared packs like
`atomic-coding`, `markdown-memory`, `multipass-quality`). Skills use Claude Code–style
`SKILL.md` frontmatter:

```yaml
---
name: my-skill
description: …
triggers: keyword1, keyword2
agents: worker, reviewer   # or * / omit for all
user-invocable: true
---
```

| Action | How |
|--------|-----|
| List | `slmcode skills` / Studio → Skills |
| Create | `slmcode skills new my-skill --agents worker` or Studio form |
| Edit | `slmcode skills edit my-skill` (writes project override under `.slmcode/skills/`) |
| Reference | `@skill:name` or `/skill name` in the query |
| Pin | `slmcode run --skill name` · Studio pin chips · `config.pinned_skills` |
| Full engine | `mode: full` (default) — context→plan→kanban→review… |
| One specialist | `mode: specialist` + `specialist: worker` · or `--agent worker` |

### Studio GUI

| Area | What you do |
|------|-------------|
| Query bar | Start / stop a pipeline run |
| Pipeline strip | Phases: context → explore → plan → split → coord → execute → learn… |
| Kanban | Drag cards, promote to `ready_to_dev`, edit mid-run |
| Live tab | Current `@agent`, scope (files), streamed outputs |
| Docs panel | Edit CONTEXT / MEMORY / SKILLS / PLAN live |
| Settings | Model, think passes, parallel, dry-run / permission |

### CLI (first-class)

```bash
slmcode run -v "…"                 # full pipeline + live agent stream
slmcode chat                       # REPL with slash commands
slmcode task add "…" --column ready_to_dev --role deep
slmcode task promote T2
slmcode task delegate T2 tester
slmcode context append "Prefer table-driven tests"
slmcode docs show MEMORY.md
slmcode session list
slmcode session resume run-…
slmcode diff
slmcode commit -m "slmcode: …"
slmcode config set permission review   # auto | dry-run | review
slmcode apply                          # apply staged review writes
```

### Interactive `chat` slash commands

| Command | Effect |
|---------|--------|
| `/help` | List commands |
| `/run <q>` | Full pipeline |
| `/board` `/status` `/diff` `/skills` `/doctor` | Inspect |
| `/permission auto\|dry-run\|review` | Write policy |
| `/model <id>` | Switch model (restart chat to rebuild) |
| `/quit` | Exit |

Plain lines (no `/`) also run the full pipeline.

## Pipeline

```
query
  → load AGENTS.md / CLAUDE.md / PROJECT
  → match skills
  → context agent (CONTEXT.md)
  → explore  OR  reuse CONTEXT/MEMORY (skip deep dive)
  → optional docs explorer / architect
  → planner (multi-pass) → splitter → sanitize
  → coordinator (board advice)
  → parallel workers + reviewer ↔ corrector
  → learn (CONTEXT/MEMORY per wave)
  → tester → memory distill
  → evolve SKILLS.md + skills/learned/
  → save session
```

### Shared project knowledge

| File | Role |
|------|------|
| `.slmcode/PROJECT.md` | Durable project facts |
| `.slmcode/CONTEXT.md` | Working focus + discovered files + wave deltas |
| `.slmcode/MEMORY.md` | Lessons / pitfalls |
| `.slmcode/SKILLS.md` | Auto index of skills + latest lessons |
| `.slmcode/skills/learned/SKILL.md` | Auto-grown conventions |
| `.slmcode/sessions/*.json` | Resumable runs |
| `.slmcode/pending/` | Staged writes when `permission=review` |
| `AGENTS.md` / `CLAUDE.md` (repo root) | Auto-injected instructions |

Force a fresh deep explore: `SLMCODE_FORCE_EXPLORE=1 slmcode run "…"`.

## Permissions (safety)

| Mode | Behavior |
|------|----------|
| `auto` | Write immediately |
| `dry-run` | Never write (simulate) |
| `review` | Stage under `.slmcode/pending/` → `slmcode apply` |

```bash
slmcode config set permission review
slmcode run "refactor foo"
slmcode apply
slmcode diff
slmcode commit -m "slmcode: refactor foo"
```

## Specialists

See **[AGENTS.md](AGENTS.md)** for the full roster (14 agents including `coordinator`, `docs`, `architect`, `deep`).

## Quality knobs for SLMs

```bash
slmcode config set think_passes 2   # draft → critique → refine
slmcode config set parallel 3
slmcode config set retries 2
slmcode config set max_context_kb 16
slmcode config set model Qwen3-Coder-30B-A3B-Instruct-MLX-4bit
```

## Hybrid Claude Code

```bash
slmcode run --backend claude-code "…"
```

Scoped packs + Claude Code CLI when available on `PATH`.

## Automated tests

```bash
cd ~/Desktop/PROJECT/slmcode
go test ./...
RUN_E2E=1 go test ./test/e2e/ -run 'TestLiveOMLX|TestStudioAPI' -timeout 45m -v
```
