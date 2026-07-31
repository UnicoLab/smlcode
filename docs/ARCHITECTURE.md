# SLMCode Architecture

## Design thesis

30B-class SLMs fail when asked to “be Claude” in one giant free-form loop.
SLMCode keeps **routing in Go** and gives each specialist a **tiny scoped pack**:

- selected `.slmcode/*.md` slices
- few focus files
- matched + learned skills
- one atomic task

## Package map

```
cmd/slmcode          CLI + embedded Studio UI
pkg/orchestrator     Code-driven pipeline + coordinator + sessions
pkg/loop             Parallel execute → review → correct (+ live events)
pkg/agents           Specialist prompts + factory (14 roles)
pkg/plan             Kanban, sanitize, filesystem discover
pkg/context          Markdown store + TaskPack budgeter
pkg/knowledge        Auto SKILLS.md + learned skill evolution
pkg/learning         Wave lessons / context deltas
pkg/instructions     AGENTS.md / CLAUDE.md / PROJECT loader
pkg/session          Resumable run snapshots
pkg/permissions      auto | dry-run | review write policy
pkg/multipass        Think → critique → refine
pkg/stream           Live event schema (CLI + SSE)
pkg/server           Studio HTTP + SSE
pkg/skills           SKILL.md loader (Claude Code compatible)
pkg/workspace        Real FS/git tools (ws_*, git_*)
pkg/backends         oMLX / Ollama / Claude Code
pkg/harness          Public New / OpenWorkspace API
pkg/cli              Colored terminal + live event formatter
```

## Live streaming

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

## Explore reuse

If CONTEXT is rich, MEMORY/PROJECT exist, and filesystem discovery finds
relevant files, the explorer deep-dive is **skipped**. Later runs stay fast
and consistent with accumulated knowledge.

Override: `SLMCODE_FORCE_EXPLORE=1`.

## Self-critic loop

```
worker/deep → reviewer (chat JSON) → corrector (tools) → reviewer …
```

Heuristics trust clear `status:done` + `files_changed` when SLM reviewers are flaky.
GoLangGraph `finalizeNode` also recovers from tool-call XML junk finals.

## Knowledge flywheel

After each run:

1. MEMORY.md append (lessons)
2. CONTEXT.md append (run complete)
3. `knowledge.Evolve` → `SKILLS.md` + `skills/learned/SKILL.md`
4. PROJECT.md auto-notes for touched files
5. Session JSON under `.slmcode/sessions/`

## Parallelism & deps

`SubAgentExecutor` runs ready kanban tasks up to `max_parallel`.
Blocked upstream deps are soft-skipped so one failed locate task cannot freeze the board.

## Permissions

Workspace tools honor `config.permission`:

- `auto` — write
- `dry-run` — simulate
- `review` — stage JSON patches under `.slmcode/pending/` for `slmcode apply`

## Dependency

```
slmcode ──go.mod──► GoLangGraph
              └── replace → ../GoLangGraph-Project/GoLangGraph (local)
```
