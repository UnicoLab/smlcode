# 🏗️ Architecture

How SLMCode stays sharp when the model is small, tired, or both.

---

## 🎯 Design thesis

30B-class SLMs fail when asked to “be a frontier agent” in one giant free-form loop.

SLMCode keeps **routing in Go** and gives each specialist a **tiny scoped pack**:

- selected `.slmcode/*.md` slices
- a few focus files
- matched + learned skills
- **one** atomic task

!!! quote "The turkey rule"
    If you stuff the whole repo into context, don't be surprised when the model falls asleep at the table.

---

## 📦 Package map

```text
cmd/slmcode          CLI + embedded Studio UI
pkg/orchestrator     Code-driven pipeline + coordinator + sessions
pkg/loop             Parallel execute → review → correct (+ live events)
pkg/agents           Specialist prompts + factory (14 roles)
pkg/plan             Kanban, sanitize, filesystem discover
pkg/context          Markdown store + TaskPack budgeter
pkg/knowledge        Auto SKILLS.md + learned skill evolution
pkg/learning         Wave lessons / context deltas
pkg/instructions     AGENTS.md / PROJECT loader
pkg/session          Resumable run snapshots
pkg/permissions      auto | dry-run | review write policy
pkg/multipass        Think → critique → refine
pkg/stream           Live event schema (CLI + SSE)
pkg/server           Studio HTTP + SSE
pkg/skills           SKILL.md loader
pkg/workspace        Real FS/git tools (ws_*, git_*)
pkg/backends         OpenAI-compat / Ollama / optional CLI backends
pkg/harness          Public New / OpenWorkspace API
pkg/cli              Colored terminal + live event formatter
pkg/config           Provider presets + project config
pkg/repair           SLM JSON repair helpers
```

---

## 📡 Live streaming

`stream.Event` (alias `orchestrator.Event`):

| Field | Meaning |
|-------|---------|
| `phase` | Pipeline stage |
| `kind` | `phase` / `agent_start` / `agent_end` / `coord` / `learn` / `output` |
| `agent` | Specialist id |
| `task_id` | Kanban task |
| `scope` | Focus files |
| `output` | Truncated agent output |

Consumed by CLI (`cli.PrintEvent`), Studio SSE (`GET /api/events`), and `GET /api/runs/latest`.

---

## ♻️ Explore reuse

If CONTEXT is rich, MEMORY/PROJECT exist, and filesystem discovery finds relevant files, the explorer deep-dive is **skipped**. Later runs stay fast and consistent with accumulated knowledge.

```bash
SLMCODE_FORCE_EXPLORE=1 slmcode run "…"   # force a fresh dig
```

---

## 🔁 Self-critic loop

```text
worker/deep → reviewer (JSON) → corrector (tools) → reviewer …
```

Heuristics trust clear `status:done` + `files_changed` when reviewers get flaky. Disk evidence beats vibes.

---

## 🦋 Knowledge flywheel

After each run:

1. MEMORY.md append (lessons)
2. CONTEXT.md append (run complete)
3. `knowledge.Evolve` → `SKILLS.md` + `skills/learned/SKILL.md`
4. PROJECT.md auto-notes for touched files
5. Session JSON under `.slmcode/sessions/`

The project gets smarter. You get less typing. Everybody wins (except bugs).

---

## 🧵 Parallelism & deps

`SubAgentExecutor` runs ready kanban tasks up to `max_parallel`.
Blocked upstream deps are soft-skipped so one failed locate task cannot freeze the whole board.

---

## 🔐 Permissions

Workspace tools honor `config.permission`:

| Mode | Behavior |
|------|----------|
| `auto` | Write |
| `dry-run` | Simulate |
| `review` | Stage JSON patches under `.slmcode/pending/` for `slmcode apply` |

---

## 📎 Dependency

```text
slmcode ──go.mod──► github.com/piotrlaczkowski/GoLangGraph
                       └── optional local replace for hacking
```

Embed the harness:

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

Made with ♥ by [UnicoLab](https://unicolab.ai)
