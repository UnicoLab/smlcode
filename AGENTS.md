# 🤖 AGENTS.md — slmcode Contributor Guide for AI Agents

> Practical reference for AI coding agents working on the slmcode project.
> Read once; contribute immediately.

---

## 1. Project Overview

**slmcode** is a Go-based, SLM-first coding harness. It orchestrates specialist LLM agents through a config-driven pipeline to plan, implement, review, correct, and test code changes with an emphasis on **small local models (7B–30B)**. It works with any OpenAI-compatible endpoint.

| Concept | Purpose |
|---------|---------|
| **Pipeline** | YAML-configurable execution graph of 16 phases |
| **Agents** | 15 built-in role experts with SLM-optimized prompts |
| **Blocks** | Marketplace-ready YAML packages: pipelines, agents, quality, packs |
| **Stacks** | Provider/model presets (omlx-local, deepseek, openai, …) |
| **Skills** | Claude Code–compatible `SKILL.md` convention packs |
| **Studio** | Vite/React/TypeScript web UI + SSE API server at `http://127.0.0.1:7420` |

---

## 2. Architecture & Package Map

```
cmd/slmcode/          CLI (cobra), embedded Studio UI (go:embed ui/)
web/                  Vite + React + TypeScript Studio SPA source
├── src/              React components, API client, types, styles
├── dist/             Vite build output (synced to cmd/slmcode/ui/ via make ui-react)
├── vite.config.ts    Vite configuration
└── tailwind.config.js Tailwind CSS config
pkg/
├── agents/           Specialist prompts + custom YAML factory
├── blocks/           Building block registry + bundled YAML presets
│   └── bundled/      Built-in blocks: pipelines/, agents/, quality/, packs/
├── pipeline/         Config-driven execution graph (phases, slots, groups)
├── stacks/           Provider/model stack presets (YAML)
├── skills/           SKILL.md loader, resolver, renderer
├── orchestrator/     Pipeline runner — coordinates phases, agents, board, HITL
├── config/           Central config (config.yaml) with env/flag overrides
├── server/           HTTP/SSE server (Studio backend, REST API)
├── harness/          Top-level harness (New, Init, Run)
├── plan/             Plan/task model types + role ID constants
├── context/          Context + PROJECT.md management
├── loop/             Inner execute loop (worker → review → correct → test)
├── multipass/        Multi-pass thinking support
├── quality/          QA gate runner
├── compact/          Context compaction
├── permissions/      Shell/file permission modes
├── authstore/        Auth credential store
├── cli/              CLI formatting utilities
├── workspace/        Workspace tool definitions
├── backends/         LLM backend resolution
├── hooks/            Lifecycle hooks
├── hitl/             Human-in-the-loop (clarify, plan, continue, escalate)
├── session/          Session + turn management
├── learning/         Learning/memory distillation
├── refine/           Auto-refinement loop
├── repair/           JSON repair utilities
├── rewind/           File checkpointing and restore
├── stream/           Streaming utilities
├── eval/             Evaluation framework
├── mcp/              MCP integration
├── models/           Model discovery/catalog
├── instructions/     Instruction rendering (AGENTS.md/PROJECT.md loading)
├── knowledge/        Knowledge injection
└── retrievaL/        Embedding-based retrieval
```

The dependency graph: `cmd` → `harness` → `orchestrator` → `agents` + `pipeline` + `skills` + `loop` + `quality` → `config`.

External dependency: `github.com/piotrlaczkowski/GoLangGraph` (Go-based LangGraph-style agent framework).

The Studio frontend (`web/`) is a standalone Vite + React + TypeScript SPA. Build with `make ui-react` (runs `npm run build` in `web/`, copies output to `cmd/slmcode/ui/`). The `cmd/slmcode/ui/` directory is embedded into the Go binary via `go:embed all:ui`.

---

## 3. Building Blocks System

### 3.1 Overview

Blocks are YAML-configurable, marketplace-ready units. Four kinds:

| Kind | Schema type | Purpose |
|------|------------|---------|
| `pipeline` | `PipelineBlock` | Phase graph, loop agents, insertable slots |
| `agent` | `AgentBlock` | Custom specialist definition or builtin override |
| `quality` | `QualityBlock` | Format/lint/test/build commands per language |
| `pack` | `PackBlock` | Composes pipeline + quality + agents + skills |

### 3.2 Discovery Order (first ID wins per kind)

1. **Project** — `.slmcode/blocks/{pipelines,agents,quality,packs}/*.yaml`
2. **User** — `~/.slmcode/blocks/…` or `$XDG_CONFIG_HOME/slmcode/blocks/…`
3. **Extra** — `$SLMCODE_BLOCKS` env var, walk-up `blocks/` dirs
4. **Builtin** — embedded in `pkg/blocks/bundled/` (compiled into binary via `go:embed`)

### 3.3 Common Schema (`Meta`)

Every block YAML shares this header:

```yaml
api_version: blocks/v1    # required
kind: pipeline            # pipeline|agent|quality|pack
id: my-block              # lowercase, [a-z][a-z0-9_-]+
name: My Block
description: A reusable block
version: "1.0.0"
author: UnicoLab
license: MIT
language: go
tags: [go, worker]
icon: "🐹"
shareable: true
```

### 3.4 Creating a Pipeline Block

Place YAML in `pkg/blocks/bundled/pipelines/` (builtin) or `.slmcode/blocks/pipelines/` (project):

```yaml
api_version: blocks/v1
kind: pipeline
id: my-lang
name: My Language Pipeline
version: "1.0.0"
language: rust
tags: [rust, pipeline]
icon: "🦀"
spec:
  version: 1
  order: [init, skills, context, explore, plan, split, coord, execute, learn, test, memory, done]
  groups:
    - {id: prepare, label: Prepare, steps: [init, skills, context, explore]}
    - {id: design, label: Design, steps: [plan, split]}
    - {id: build,  label: Build,  steps: [coord, execute, learn]}
    - {id: verify, label: Verify, steps: [test]}
    - {id: finish, label: Finish, steps: [memory, done]}
  phases:
    init:    {agent: "",       when: always, label: Init}
    context: {agent: context,  when: always, label: Context}
    explore: {agent: explorer, when: auto,   label: Explore}
    plan:    {agent: planner,  when: always, label: Plan}
    split:   {agent: splitter, when: always, label: Split}
    coord:   {agent: coordinator, when: always, label: Coord}
    execute: {agent: worker,   when: always, label: Execute}
    learn:   {agent: memory,   when: auto,   label: Learn}
    test:    {agent: tester,   when: always, label: Test}
    memory:  {agent: memory,   when: always, label: Memory}
    done:    {agent: "",       when: always, label: Done}
  execute:
    default_role: worker
    reviewer: reviewer
    corrector: corrector
    max_waves: 2
```

### 3.5 Creating an Agent Block

```yaml
api_version: blocks/v1
kind: agent
id: my-worker
name: My Worker
version: "1.0.0"
language: rust
icon: "🦀"
spec:
  id: my-worker
  title: My Worker
  system_prompt: |
    You are a Rust implementation specialist.
    After edits, smoke with: cargo test -p <crate>
  tools: true
  max_iter: 16
  temperature: 0.12
  max_tokens: 3072
  skills: [specialist-worker, atomic-coding]
```

### 3.6 Creating a Quality Block

```yaml
api_version: blocks/v1
kind: quality
id: my-lang
name: My Lang Quality
version: "1.0.0"
language: rust
spec:
  detect:
    files: [Cargo.toml]
    extensions: [.rs]
    priority: 20
  lint:
    - {cmd: cargo clippy -- -D warnings, label: clippy}
  test:
    - {cmd: cargo test, label: cargo test}
  build:
    - {cmd: cargo build, label: cargo build}
  smoke: cargo test --quiet
  qa_gate: cargo test
```

### 3.7 Creating a Pack Block

```yaml
api_version: blocks/v1
kind: pack
id: my-lang
name: My Language Pack
version: "1.0.0"
language: rust
spec:
  pipeline: my-lang
  quality: my-lang
  agents: [my-worker, my-tester]
  skills: [atomic-coding]
  pin_skills: true
  override_tester: my-tester
  override_worker: my-worker
```

### 3.8 Key Functions

- `blocks.Load(projectRoot)` — load registry
- `reg.Catalog(kind)` — list blocks (filtered by kind)
- `reg.GetPack/Pipeline/Quality/Agent(id)` — get by ID
- `reg.View(activePack, activePipeline)` — Studio/API response
- `reg.DetectQuality(workspaceRoot)` — auto-detect quality pack
- `blocks.ApplyPack(cfg, reg, packID, opts)` — materialize pack
- `blocks.ApplyPipelinePreset(cfg, reg, pipelineID)` — apply pipeline
- `blocks.ResolveQAGateCommand(projectRoot, workspaceRoot, activePack)` — get QA gate

---

## 4. How Predefined Pipelines Work

### 4.1 Default Pipeline (`pkg/pipeline/default.go`)

16 phases across 5 groups:

| Group | Phases |
|-------|--------|
| **Prepare** | `init` → `skills` → `context` → `explore` → `docs` |
| **Design** | `architect` → `clarify` → `plan` → `split` |
| **Build** | `coord` → `execute` → `learn` |
| **Verify** | `polish` → `test` |
| **Finish** | `memory` → `done` |

### 4.2 Language-Specific Pipelines (`pkg/blocks/bundled/pipelines/`)

| File | Key Overrides |
|------|--------------|
| `go.yaml` | `test.agent: go-tester`, `execute.default_role: go-worker`, slot with go vet/race/build reminders |
| `python.yaml` | `test.agent: python-tester`, `execute.default_role: python-worker`, slot with ruff/pytest reminders |
| `react.yaml` | `test.agent: react-tester`, `execute.default_role: react-worker`, slot with lint/tsc/build reminders |

### 4.3 Slots System

Slots are user-inserted agent calls around phase anchors:

- `before` / `after` — run before/after a phase
- `replace` — replace the phase agent entirely
- `when` — `always`, `never`, or `query_matches:<regex>`
- `input` — template with `{{query}}`, `{{exploration}}`, `{{plan}}`, `{{phase}}`
- `persist_to` — `none`, `scratch`, `context`, `memory`
- `fail_mode` — `continue` or `abort`

---

## 5. Built-in Specialists (15 total)

Defined in `pkg/agents/prompts.go` + `pkg/agents/factory.go`:

| ID | Role | Has Tools | MaxIter |
|----|------|-----------|---------|
| `coordinator` | Coordinate board & specialists | No | 2 |
| `orchestrator` | High-level orchestration | No | 4 |
| `context` | Maintain CONTEXT.md | No | 2 |
| `explorer` | Codebase explorer | Yes | 10 |
| `docs` | Documentation explorer | Yes | 8 |
| `architect` | Minimal design / approach | No | 2 |
| `planner` | High-level plan | No | 2 |
| `splitter` | Atomic task split | No | 2 |
| `worker` | Implement scoped change | Yes | 16 |
| `deep` | Deep multi-step worker | Yes | 20 |
| `reviewer` | Self-critic / approve | No | 2 |
| `corrector` | Fix review issues | Yes | 12 |
| `tester` | Verify / run tests | Yes | 12 |
| `placeholder` | Fill placeholders / flag gaps | Yes | 14 |
| `escalate` | Escalate arbitrator | No | 1 |
| `memory` | Distill MEMORY.md | No | 2 |

Custom agents can override any built-in by placing same-id YAML in `.slmcode/agents/`.

---

## 6. How Stacks Work

Stacks are YAML presets in `stacks/` directory. Built-in stacks:

| File | Provider | Model |
|------|----------|-------|
| `omlx-local.yaml` | omlx | Qwen3-Coder-30B-A3B-Instruct-MLX-4bit |
| `deepseek.yaml` | deepseek | deepseek-chat |
| `openai.yaml` | openai | gpt-4o-mini |
| `ollama-local.yaml` | ollama | qwen2.5-coder:14b |
| `openrouter.yaml` | openrouter | — |
| `google.yaml` | google | — |
| `groq.yaml` | groq | — |
| `qwen.yaml` | qwen | — |

Key functions: `stacks.List()`, `stacks.Load(name)`, `stacks.Apply(cfg, stack, opts)`.

---

## 7. How Skills Work

Skills are Claude Code–compatible `SKILL.md` packs in `<dir>/<name>/SKILL.md`:

```markdown
---
name: atomic-coding
description: Split work into tiny file-scoped tasks for SLMs
triggers: refactor, implement, fix, code
agents: worker, deep, corrector
user-invocable: true
---

# Atomic coding for SLMs

- Touch the fewest files possible.
- Prefer `ws_edit` over rewriting whole files.
```

Resolution order: explicit `@skill:name` refs → agent-targeted → global → keyword matches.

---

## 8. CLI Commands Reference

| Command | Purpose |
|---------|---------|
| (bare) | Premium interactive TUI |
| `init` | Create `.slmcode/` workspace |
| `run <query>` | Full pipeline run |
| `chat` | Interactive REPL |
| `studio` | Launch Studio UI + API |
| `studio --kill` | Force-kill existing studio on port |
| `studio --port-auto` | Auto-switch to next free port |
| `status` | Query/plan/board snapshot |
| `board` | Live kanban board |
| `config show` | Print effective config |
| `config set <key> <value>` | Update config |
| `stack list` | List available stacks |
| `stack apply <name>` | Apply a stack |
| `agent list` | List agents with effective LLM |
| `agent show <id>` | Show agent detail |
| `agent edit <id> model=…` | Patch agent fields |
| `blocks list` | List all building blocks |
| `blocks show <kind> <id>` | Show block detail |
| `blocks validate` | Validate all block YAML |
| `blocks apply <pack-id>` | Apply a language pack |
| `skills list` | List skills |
| `skills new <name>` | Create a skill |
| `doctor` | System health check |
| `diff` | Show git diff |
| `commit` | Git add -A && commit |

---

## 9. How to Run Tests

```bash
# Build UI first (required for e2e tests)
make ui-react

# Unit tests
go test ./... -count=1

# Specific packages
go test ./pkg/pipeline/... ./pkg/agents/... ./pkg/blocks/... ./pkg/stacks/... ./pkg/skills/... -v

# E2E tests (requires built UI; live oMLX for live tests)
RUN_E2E=1 go test ./test/e2e/ -count=1 -timeout 30m
```

---

## 10. How to Build

```bash
# Build embedded UI first (Vite + React → cmd/slmcode/ui/)
make ui-react

# Standard build
go build -o bin/slmcode ./cmd/slmcode

# With version info
go build -ldflags "-s -w \
  -X main.Version=0.9.0 \
  -X main.GitCommit=$(git rev-parse --short HEAD) \
  -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/slmcode ./cmd/slmcode

# Quick build + test + install
make build && make test && make install
```

---

## 11. Key Conventions

- **Normalize + Validate pattern**: Every struct has `Normalize()` (fill defaults, clean) then `Validate()` (enforce rules). Always call both before persisting.
- **YAML tags**: All serializable structs use `yaml:"…"` + `json:"…"` tags.
- **Config**: `config.Config` is the single source of truth. Use `ApplyEnv()` for env overrides.
- **Agent prompts**: SLM-optimized — short, role-locked, STRICT JSON output schema first.
- **Coding agents**: Get `workspace.ToolNames()` + `workspace.SpecialistToolNames()`.
- **Block IDs**: Lowercase kebab-case, validated against `^[a-z][a-z0-9_-]{1,63}$`.
- **Never end on a tool call**: Core agent invariant — must produce final JSON after tool use.
- **HARD SCOPE**: Workers and correctors must stay within focus files / same package.

---

## Quick Reference: Adding a Feature

1. **New agent**: Add prompt → `pkg/agents/prompts.go` → add to `Specs()` in `pkg/agents/factory.go` → optionally add YAML block in `pkg/blocks/bundled/agents/`
2. **New pipeline phase**: Add to `pkg/pipeline/default.go` `Default()` → update orchestrator logic
3. **New block kind**: Add to `pkg/blocks/meta.go` → add schema struct in `pkg/blocks/schema.go` → add to `pkg/blocks/registry.go` `ingest()` switch
4. **New stack**: Create `stacks/<name>.yaml` with provider/model/endpoint
5. **New skill**: Create `skills/default/<name>/SKILL.md` or `.slmcode/skills/<name>/SKILL.md`
6. **New CLI command**: Add Cobra command in `cmd/slmcode/` → register in `root.go`
7. **New config field**: Add to `config.Config` struct → handle in `ApplyPatch()` → add YAML/JSON tags

---

## 12. Parallel Execution Architecture (v0.12.0+)

slmcode maximizes throughput via 6 parallel execution paths, all bounded by `max_parallel` (default 4):

### 12.1 Parallel Execution Paths

| Path | Location | What Runs in Parallel |
|------|----------|----------------------|
| **Worker execution** | `loop/runner.go:runWave` | All tasks in a wave via GoLangGraph `ExecuteSubAgents` |
| **Post-worker QA** | `loop/runner.go:runPostWorkerQAParallel` | Smoke, acceptance smoke, static quality, claims gate — across all tasks |
| **Self-critique** | `loop/runner.go:runSelfCritiqueParallel` | Corrector LLM for all weak tasks simultaneously |
| **Review wave** | `loop/runner.go:reviewWave` | Reviewer+corrector for independent tasks (no shared files) |
| **Phase parallelism** | `orchestrator/parallel.go:runPhaseParallel` | context+explore in parallel, architect+clarify in parallel |
| **Speculative races** | `loop/runner.go:speculate` + `orchestrator/speculate.go` | Disk-accept vs reviewer LLM, multiple tester strategies |

### 12.2 Config Fields

```yaml
# .slmcode/config.yaml
max_parallel: 4           # Max concurrent tasks per wave (default: 4)
think_passes: 1           # Multi-pass thinking (2+ enables speculative digs)
task_timeout: 12m         # Per-task timeout
max_retries: 4            # Review/correct retries before escalate
```

### 12.3 Task Independence

Tasks are grouped by shared files for parallel review — tasks without overlapping files run concurrently. The `scheduleReady` function prioritizes:
1. Explorers/docs first (discovery)
2. Workers with files (focused)
3. Testers last (post-implementation)

### 12.4 Wave-Level Fast-Path

When ALL tasks in a wave have clean QA + disk evidence, the entire reviewer LLM phase is skipped — all tasks go directly to Done.

---

## 13. HITL (Human-in-the-Loop) Configuration

### 13.1 HITL Modes

| Setting | Values | Default | Purpose |
|---------|--------|---------|---------|
| `plan_approve` | `off` \| `auto` \| `ask` | `ask` | Human must approve plan before execute |
| `clarify_mode` | `off` \| `auto` \| `ask` | `ask` | Interview agent asks about language/stack |
| `continue_ask` | `off` \| `auto` \| `ask` | `ask` | Ask when retries/QA exhausted |
| `escalate_ask` | `off` \| `auto` \| `ask` | `ask` | Ask on max-retry escalate |
| `auto_approve` | `true` \| `false` | `false` | Global override — skip all HITL gates |

### 13.2 Pack-Level HITL Control

```yaml
spec:
  defer_plan_approve: true   # Force plan_approve=ask
  defer_clarify: true        # Force clarify_mode=ask
```

### 13.3 HITL Endpoints

The Studio frontend polls these endpoints every 2s:
- `GET /api/clarify/pending` → `POST /api/clarify/answer`
- `GET /api/plan/pending` → `POST /api/plan/approve`
- `GET /api/continue/pending` → `POST /api/continue/answer`
- `GET /api/escalate/pending` → `POST /api/escalate/answer`
- `GET /api/shell/pending` → `POST /api/shell/approve`

Each ask has a timeout; on expiry the recommended/default action is applied.

---

## 14. File Browser API

### 14.1 Endpoints

- `GET /api/workspace/tree?path=` — list directory contents (dirs first, no hidden files)
- `GET /api/workspace/file?path=` — read file content with syntax highlighting

### 14.2 Studio File Browser

The `/files` page shows a full recursive directory tree. Features:
- Expand/collapse folders with lazy loading
- Toggle between "All files" and "Modified only" (agent-changed)
- Per-line inline comments that can be sent as tasks
- Syntax highlighting for Go, Python, TypeScript, Rust, and more

---

## 15. Version History

| Version | Key Changes |
|---------|------------|
| 0.12.2 | e2e test fixes for Vite bundle output, CI builds Studio UI before Go, Homebrew formula sync, docs refresh |
| 0.12.1 | Vite/React/TypeScript Studio UI (`web/` + `make ui-react`), `fast_model` dual-model routing, smarter QA gate, improved tester |
| 0.12.0 | Engine-wide parallelization: 6 parallel paths, MaxParallel=4, phase parallelism, parallel QA, parallel self-critique, parallel review, wave fast-path |
| 0.11.0 | HITL defaults to ask, File Browser (workspace tree API), `--kill` CLI flag, single run input |
| 0.10.x | SessionStorage state persistence, blocks CLI, Studio LiveView, code review comments, SLM-optimized prompts |
