# 🏗️ Architecture

How SLMCode stays sharp when the model is small, tired, or both. 😴➡️😎

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🦃</span>
<p class="slm-banner__text" markdown>
<strong>Design thesis:</strong> routing in Go. Tiny packs per specialist. Disk evidence over vibes.
If you stuff the whole repo into context, don’t be surprised when the model naps.
</p>
</div>

---

## Design thesis 🧠

30B-class SLMs fail when asked to “be a frontier agent” in one free-form loop.

**Routing in Go. Tiny packs per specialist. Disk evidence over vibes.**

!!! quote "🦃 Turkey rule"
    If you stuff the whole repo into context, don't be surprised when the model naps.

→ Narrative version: [🧠 Concepts](concepts.md)

---

## Package map 📦

```text
cmd/slmcode          CLI + embedded Studio (go:embed ui/)
pkg/orchestrator     Pipeline runner + coordinator + sessions
pkg/pipeline         Config-driven phases / slots / loop agents
pkg/composer         Dynamic pipeline composition (Composition/Parse/Apply)
pkg/blocks           Building block registry + bundled YAML presets
pkg/loop             Parallel execute → review → correct
pkg/agents           Specialist prompts + custom YAML factory
pkg/stacks           Provider/model stack presets
pkg/plan             Kanban, sanitize, discover
pkg/context          Markdown store + TaskPack budgeter
pkg/quality          QA gate runner + smoke checks
pkg/skills           SKILL.md loader
pkg/knowledge        SKILLS.md + learned evolution
pkg/learning         Wave lessons / deltas
pkg/instructions     AGENTS.md / PROJECT loader
pkg/session          Resumable snapshots + ReAct resume
pkg/permissions      auto | dry-run | review
pkg/multipass        Think → critique → refine
pkg/stream           Live events (CLI + SSE)
pkg/server           Studio HTTP + SSE
pkg/workspace        Real FS/git tools
pkg/backends         OpenAI-compat / Ollama / optional CLIs
pkg/harness          Public embed API
pkg/cli              Terminal UX
pkg/config           Presets + project config
pkg/repair           SLM JSON repair
pkg/retrieval        Embeddings / lexical ranking
```

Pipeline graph is loaded from `.slmcode/pipeline.yaml` — see [Pipeline](pipeline.md).

---

## Live streaming 📡

`stream.Event` fields: `phase`, `kind`, `agent`, `task_id`, `scope`, `output`.
Consumers: CLI, Studio SSE (`/api/events`), `/api/runs/latest`.

---

## Explore reuse · critic · flywheel 🔁

- **Reuse** when CONTEXT/MEMORY are rich (override with `SLMCODE_FORCE_EXPLORE=1`) ♻️
- **Critic**: worker → reviewer → corrector loop; disk evidence preferred 🔍
- **Flywheel**: MEMORY / CONTEXT / SKILLS / sessions after each run 🦋

---

## Quality + HITL gates 🛡️

```text
worker → smoke + acceptance smoke + static/claims
      → reviewer / corrector (≤ max_retries)
      → escalate? → Studio/TUI pause (timeout → @escalate decides)
finalize → placeholder polish → completeness bar → QA gate (pytest preferred)
      → continue-ask if work remains
```

- Greenfield Python QA prefers **pytest**, not `compileall`
- Whitelisted acceptance commands (`pytest`, `go test`, `python main.py`, …) run after workers
- Syntax-only QA cannot alone mark the run successful
- Escalate timeout → dedicated **@escalate** arbitrator (or `escalate_timeout_agent`)

→ [Config](config.md) · [Studio HITL](studio.md) · [Agents](agents.md)

---

## Parallelism ⚡

`SubAgentExecutor` runs ready tasks up to `max_parallel`. Soft-skip blocked deps so one stuck locate can't freeze the board.
One blocked task should not become a board-wide existential crisis.

---

## Embed 🧩

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

Dependency: `github.com/piotrlaczkowski/GoLangGraph` (optional local replace for hacking).

→ [🤝 Contributing](contributing.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
