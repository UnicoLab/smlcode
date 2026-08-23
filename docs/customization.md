# 🛠️ Customization Guide — Everything You Can Configure

> Deep tutorials on defining agents, pipelines, blocks, stacks, skills, and project memory. Every YAML field explained with complete, copy-paste examples.

---

## Table of Contents

1. [Custom Agents](#custom-agents)
2. [Custom Pipelines](#custom-pipelines)
3. [Quality Blocks](#quality-blocks)
4. [Language Packs](#language-packs)
5. [Custom Stacks](#custom-stacks)
6. [Skills (SKILL.md)](#skills-skillmd)
7. [Project Memory Files](#project-memory-files)
8. [Config Reference](#config-reference)

---

## Custom Agents

Agents are specialist LLM roles with custom system prompts, tool access, and LLM overrides. Define them as YAML files under `.slmcode/agents/` or as blocks under `.slmcode/blocks/agents/`.

### Location & Discovery

| Location | Scope | Format |
|----------|-------|--------|
| `.slmcode/agents/<id>.yaml` | Project-level | `CustomSpec` YAML |
| `~/.slmcode/agents/<id>.yaml` | User-level (all projects) | `CustomSpec` YAML |
| `.slmcode/blocks/agents/<id>.yaml` | Project-level block | `AgentBlock` YAML |
| Builtin | Embedded in binary | Go code in `pkg/agents/` |

**Resolution**: project → user → builtin. First ID wins.

### Agent YAML Schema (CustomSpec)

Every field, with defaults and explanations:

```yaml
# ── Required ──
id: my-specialist              # lowercase, [a-z][a-z0-9_-]{1,63}
                                # Must be unique. If it matches a builtin ID
                                # (worker, reviewer, tester, …), this becomes
                                # an OVERRIDE rather than a new agent.

title: My Specialist            # Human-readable display name (Studio, CLI)

# ── Core behavior ──
system_prompt: |               # The system prompt injected before every turn.
  You are a TypeScript code reviewer.            # Keep it SLM-short (≤ 500 words).
  Focus on type safety and API boundaries.       # Use role-locked language.
  STRICT JSON OUTPUT:                            # Always define output format.
  {"approved": true|false, "issues":[], "score": 0-100}

description: >                 # Shown in Studio agent cards and CLI listing.
  Reviews TypeScript code for type safety,
  nullability, and API contract violations.

# ── Tools ──
tools: true                     # Enable coding tools (ws_read, ws_edit, ws_shell, …)
                                # true  → full tool access
                                # false → no tools (read-only reasoning)
                                # omit  → for builtin overrides: keeps base default
                                #          for new agents: defaults to true

# ── LLM configuration (per-agent overrides) ──
model: deepseek-chat            # Override the global/stack model for this agent
provider: deepseek              # Override the global/stack provider
endpoint: https://api.deepseek.com  # Override the endpoint (default: provider's endpoint)
                                # All three fields can be set independently.
                                # Empty/omitted fields inherit from stack → global config.

# ── Performance tuning ──
max_iter: 16                    # Max tool-calling iterations before forced finalize
                                # Builtin defaults: worker=16, tester=12, planner=2, …

temperature: 0.12               # LLM sampling temperature (0.0–2.0)
                                # Lower = more deterministic
                                # Typical: worker 0.12, reviewer 0.05, planner 0.2

max_tokens: 3072                # Max completion tokens per turn
                                # Typical: worker 3072, tester 2048, reviewer 768

# ── Skills ──
skills:                         # Skill packs injected into the system prompt
  - specialist-worker           # at runtime. Resolved by skills.Loader.
  - atomic-coding               # Empty/omitted = no skills pinned.
```

### Complete Example: Custom Python Worker

```yaml
# .slmcode/agents/py-engineer.yaml
id: py-engineer
title: Python Engineer
description: Implements Python features with typing and pytest coverage.
system_prompt: |
  You are a Python implementation specialist. Stay inside HARD SCOPE.
  Respect pyproject.toml layout, src/ packages, and existing type hints.

  After edits, ALWAYS smoke with:
  - python -m py_compile <files>
  - python -m pytest -q <tests> when tests exist

  Prefer from __future__ import annotations on new modules.
  STRICT JSON: {"status":"done|blocked","summary":"...","files_changed":[],"notes":""}
tools: true
max_iter: 16
temperature: 0.12
max_tokens: 3072
skills: [specialist-worker, atomic-coding]
model: gpt-4o-mini
provider: openai
```

### Overriding Built-in Agents

When your agent `id` matches a builtin (worker, reviewer, tester, coordinator, …), it becomes an **override**:

```yaml
# .slmcode/agents/worker.yaml  (overrides the builtin worker)
id: worker
title: Worker
system_prompt: |
  You are a Rust specialist. Use cargo test after every edit.
  HARD SCOPE: focus files only. No drive-by refactors.
  STRICT JSON: {"status":"done|blocked","summary":"...","files_changed":[]}
model: deepseek-chat           # Give the worker a different LLM
temperature: 0.1               # Lower temp for coding
max_iter: 20                    # More iterations for complex changes
# tools: true ← not set, keeps builtin default (coding tools ON)
```

Only fields you **set** override the builtin. Fields you leave **empty or omit** keep the builtin defaults.

### CLI Commands

```bash
# List all agents (builtins + customs, with effective LLM)
slmcode agent list

# Show one agent with full prompt and effective model
slmcode agent show worker

# Create a custom agent via CLI (opens $EDITOR)
slmcode agent edit py-engineer model=gpt-4o model=…

# Clear per-agent LLM overrides (revert to inheriting stack)
slmcode agent clear-llm py-engineer

# Delete project-level custom agent
rm .slmcode/agents/py-engineer.yaml
```

### Studio

Navigate to **Agents** in the sidebar:
- **New Agent** button creates a `CustomSpec` YAML
- **Edit/Delete** works on project-level agents
- **Built-in** agents are listed alongside customs
- **Effective model/provider** shows what LLM each agent actually uses (resolved from stack inheritance)
- **Override** badge indicates a builtin is being modified

---

## Custom Pipelines

Pipelines define the execution graph — which phases run, which agents execute them, and what slots inject extra steps. Configured in `.slmcode/pipeline.yaml`.

### Pipeline YAML Schema

```yaml
# .slmcode/pipeline.yaml
version: 1                     # Schema version (always 1)

# ── Phase ordering ──
order:                         # Execution + display order of phases
  - init                       # Must list every phase that runs.
  - skills                     # The orchestrator iterates this list in order.
  - context
  - explore
  - docs
  - architect
  - clarify
  - plan
  - split
  - coord
  - execute
  - learn
  - polish
  - test
  - memory
  - done

# ── UI grouping (optional, for Studio) ──
groups:                        # Groups phases into labeled sections in UI
  - id: prepare                # Group ID (must be unique)
    label: Prepare             # Display label
    steps:                     # Phase IDs in this group
      - init
      - skills
      - context
      - explore
      - docs
  - id: design
    label: Design
    steps: [architect, clarify, plan, split]
  - id: build
    label: Build
    steps: [coord, execute, learn]
  - id: verify
    label: Verify
    steps: [polish, test]
  - id: finish
    label: Finish
    steps: [memory, done]

# ── Phase configuration ──
phases:
  init:
    agent: ""                  # Agent ID for this phase. Empty = no LLM needed.
    when: always               # always | auto | never
    label: Init                # Display label in UI
    tip: Boot workspace        # Tooltip shown in PipelineEditor
    group: prepare             # Which group this belongs to
    enabled: true              # Toggle on/off (default: true)

  context:
    agent: context             # Use the built-in context agent
    when: always               # Always run (never skip)
    label: Context
    tip: Refresh CONTEXT / project memory
    group: prepare

  explore:
    agent: explorer            # Codebase explorer specialist
    when: auto                 # Auto-skip when memory is fresh
    label: Explore
    tip: Discover relevant files
    group: prepare

  plan:
    agent: planner             # High-level planning specialist
    when: always
    label: Plan

  execute:
    agent: worker              # Default worker agent
    when: always
    label: Execute
    tip: Workers implement + review
    group: build

  test:
    agent: python-tester       # !!! Language-specific tester !!!
    when: always               # This is how language packs customize
    label: Test                # the verification phase.
    tip: pytest + ruff verification
    group: verify

# ── Execute loop configuration ──
execute:
  default_role: python-worker  # Agent for worker tasks (language packs override this)
  reviewer: reviewer           # Who reviews worker output
  corrector: corrector         # Who fixes review issues
  max_waves: 2                 # Max review → correct → retest cycles (0 = engine default)

# ── Insertable slots ──
slots:                         # Inject agents around phases without modifying phase config
  - id: pre-plan-check         # Unique slot ID (kebab-case)
    agent: architect            # Agent to run for this slot
    title: Pre-plan check      # Display title (default: slot ID)
    before: plan               # Run BEFORE the "plan" phase
    # after: plan              # Alternative: run AFTER the phase
    # replace: plan            # Alternative: REPLACE the phase agent entirely
    when: always               # always | never | query_matches:<regex>
    input: |                   # Prompt template for the slot agent
      Review the exploration and flag gaps before planning.
      Query:
      {{query}}                # Replaced with user query
      {{exploration}}          # Replaced with exploration output
      {{plan}}                 # Replaced with current plan
      {{phase}}                # Replaced with phase name
    persist_to: scratch        # Where slot output goes: scratch|context|memory|none
    fail_mode: continue        # continue (log + proceed) | abort (stop pipeline)
    multipass: false           # Enable multi-pass thinking for this slot
    enabled: true              # Toggle on/off

  - id: quality-reminder
    agent: python-tester
    title: Python quality reminder
    before: execute
    when: always
    persist_to: scratch
    fail_mode: continue
    input: |
      Before implementing, remember Python quality bar:
      - ruff format / ruff check when available
      - python -m pytest -q (or uv run pytest -q)
      - never treat compileall alone as success for greenfield apps
      Query:
      {{query}}
```

### Phase `when` Modes

| Mode | Behavior |
|------|----------|
| `always` | Run every time. Never skip. |
| `auto` | Built-in heuristics decide. Explore/Docs/Architect skip when memory is fresh. |
| `never` | Never run. Use a `replace` slot if you want a custom agent instead. |

### Complete Example: Rust Pipeline

```yaml
# .slmcode/blocks/pipelines/rust.yaml
api_version: blocks/v1
kind: pipeline
id: rust
name: Rust Engineering Pipeline
description: Pipeline tuned for Cargo-based Rust projects.
version: "1.0.0"
language: rust
tags: [rust, cargo, pipeline]
icon: "🦀"
shareable: true
spec:
  version: 1
  order: [init, skills, context, explore, plan, split, coord, execute, test, memory, done]
  groups:
    - {id: prepare, label: Prepare, steps: [init, skills, context, explore]}
    - {id: design, label: Design, steps: [plan, split]}
    - {id: build, label: Build, steps: [coord, execute]}
    - {id: verify, label: Verify, steps: [test]}
    - {id: finish, label: Finish, steps: [memory, done]}
  phases:
    init: {agent: "", when: always, label: Init}
    skills: {agent: "", when: always, label: Skills}
    context: {agent: context, when: always, label: Context}
    explore: {agent: explorer, when: auto, label: Explore}
    plan: {agent: planner, when: always, label: Plan}
    split: {agent: splitter, when: always, label: Split}
    coord: {agent: coordinator, when: always, label: Coord}
    execute: {agent: worker, when: always, label: Execute}
    test: {agent: tester, when: always, label: Test, tip: cargo test + clippy}
    memory: {agent: memory, when: always, label: Memory}
    done: {agent: "", when: always, label: Done}
  execute:
    default_role: worker
    reviewer: reviewer
    corrector: corrector
    max_waves: 2
  slots:
    - id: rust-quality
      agent: tester
      title: Rust quality reminder
      before: execute
      when: always
      persist_to: scratch
      fail_mode: continue
      input: |
        Remember Rust quality bar:
        - cargo fmt --check
        - cargo clippy -- -D warnings
        - cargo test
        - cargo build
        Query: {{query}}
```

### CLI Commands

```bash
# View current pipeline
slmcode config show | grep pipeline
cat .slmcode/pipeline.yaml

# Apply a predefined preset
slmcode blocks apply go
slmcode blocks apply python

# Apply just a pipeline (no QA gate change)
curl -X POST localhost:7420/api/pipeline-presets/python/apply
```

### Studio

**Pipeline** tab:
- **Preset selector**: One-click switching between Go/Python/React pipelines
- **Execute loop**: Edit default_role, reviewer, corrector, max_waves
- **Phase editor**: Toggle phases, change agents, set `when` mode
- **Save/Reset**: Persist changes or revert to defaults

---

## Quality Blocks

Quality blocks define language-specific linting, formatting, testing, and build commands. They auto-detect based on project files and provide the QA gate.

### Quality Block Schema

```yaml
# .slmcode/blocks/quality/typescript.yaml
api_version: blocks/v1
kind: quality
id: typescript
name: TypeScript Quality Pack
description: ESLint + tsc + vitest + build for TypeScript projects.
version: "1.0.0"
language: typescript
tags: [typescript, eslint, vitest]
icon: "🔷"
shareable: true
spec:
  # ── Auto-detection ──
  detect:
    files:                     # Root marker files. Each present one adds +12.
      - package.json           # Globs are allowed ("*.csproj", "*.gemspec").
      - tsconfig.json
      - eslint.config.js
    extensions:                # Source suffixes. Each matching file adds +2,
      - .ts                    # capped at 3 files — a weak tiebreak, not a vote.
      - .tsx
      - .js
    contains:                  # CONTENT proof. +25 per satisfied entry — the
      package.json:            # strongest signal there is. Any one substring
        - '"typescript"'       # in the list satisfies the entry; a declared but
        - '"vitest"'           # unsatisfied entry simply scores nothing.
    priority: 20               # Author ranking, added to the score.

  # ── Formatting checks (optional) ──
  format:
    - cmd: npx prettier --check .
      label: prettier
      optional: true           # Skip if the tool is not installed

  # ── Linting (optional) ──
  lint:
    - cmd: npx eslint .
      label: eslint
      optional: true

  # ── Type checking (optional) ──
  typecheck:
    - cmd: npx tsc --noEmit
      label: tsc
      optional: true

  # ── Tests (required if no qa_gate set) ──
  test:
    - cmd: npx vitest run --silent
      label: vitest
    - cmd: npm test --silent
      label: npm test

  # ── Build (optional) ──
  build:
    - cmd: npm run build
      label: build

  # ── Quick smoke command (post-worker) ──
  smoke: npx tsc --noEmit       # Quick check after each worker edit

  # ── Full QA gate command ──
  qa_gate: npx vitest run       # Runs after board completes (iterates until green)

  # ── Safe command prefixes (for shell whitelist) ──
  safe_prefixes:                # Commands starting with these are always allowed
    - npm test
    - npm run
    - npx tsc
    - npx vitest
    - npx eslint
    - npx prettier

  # ── Hints injected into the tester's system prompt ──
  tester_hints: |
    Prefer: npx vitest run then npm run build.
    Typecheck: npx tsc --noEmit when tsconfig.json exists.
    Lint: npx eslint . when eslint config present.
```

!!! note "How detection actually resolves"
    `blocks.DetectPack(root, root)` is the single detection answer in the codebase — `slmcode
    init` calls it, and nothing else keeps a private marker list any more. Two rules beyond the
    scoring table matter in practice:

    - The extension walk **skips any directory that carries its own project marker**
      (`go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `pom.xml`, `build.gradle{,.kts}`).
      A Go module with a Vite app in `web/` stays Go however big the frontend gets.
    - `contains` is what separates packs that share a marker file. `package.json` +
      `'"react"'` → the `react` pack; `package.json` without it → `typescript`.

    Verify what a directory resolves to before committing a custom block:
    `slmcode init` prints `pack: <id> (detected)`, and `slmcode config show --all` shows the
    `active_pack` / `qa_gate_command` pair it wrote.

### Complete Example: Rust Quality Pack

```yaml
api_version: blocks/v1
kind: quality
id: rust
name: Rust Quality Pack
description: Full Rust verify pack — cargo fmt, clippy, test, build.
version: "1.0.0"
language: rust
tags: [rust, cargo, clippy]
icon: "🦀"
spec:
  detect:
    files: [Cargo.toml]
    extensions: [.rs]
    priority: 20
  format:
    - cmd: cargo fmt --check
      label: cargo fmt
  lint:
    - cmd: cargo clippy -- -D warnings
      label: clippy
    - cmd: cargo clippy --tests -- -D warnings
      label: clippy tests
      optional: true
  test:
    - cmd: cargo test
      label: cargo test
    - cmd: cargo test --doc
      label: doc tests
      optional: true
  build:
    - cmd: cargo build --release --workspace
      label: release build
      optional: true
  smoke: cargo test --quiet
  qa_gate: cargo test
  safe_prefixes:
    - cargo test
    - cargo build
    - cargo fmt
    - cargo clippy
    - cargo check
  tester_hints: |
    Prefer: cargo test (unit + integration)
    Lint: cargo clippy -- -D warnings
    Format: cargo fmt --check
    Release build: cargo build --release when configured
```

---

## Language Packs

A pack composes a pipeline, quality block, agents, and skills into one apply-able unit.

Thirteen ship built in — `go`, `python`, `react`, `typescript`, `web`, `rust`, `java`, `kotlin`,
`dotnet`, `ruby`, `php`, `swift`, `cpp` — alongside 35 language agent blocks, 29 skills and 13
provider stacks. `slmcode blocks list` prints the live set; the tables in
[Blocks](blocks.md#predefined-language-packs-builtin) name each pack's agents, smoke command and
QA gate.

### Pack Schema

```yaml
# .slmcode/blocks/packs/rust.yaml
api_version: blocks/v1
kind: pack
id: rust
name: Rust Language Pack
description: Complete Rust engineering pack.
version: "1.0.0"
language: rust
tags: [rust, cargo, pack]
icon: "🦀"
shareable: true
spec:
  pipeline: rust               # Pipeline block ID to apply
  quality: rust                # Quality block ID for QA gate
  agents:                      # Agent blocks to materialize into .slmcode/agents/
    - rust-worker
    - rust-tester
  skills:                      # Skill packs to pin in config
    - atomic-coding
    - specialist-tester
    - specialist-worker
  pin_skills: true             # Add skills to config.pinned_skills
  override_tester: rust-tester # Set phases.test.agent to this
  override_worker: rust-worker # Set execute.default_role to this
```

### Applying a Pack

```bash
# CLI — writes pipeline.yaml, sets QA gate, pins skills
slmcode blocks apply rust

# CLI — also materialize agent YAML files into .slmcode/agents/
slmcode blocks apply rust --materialize-agents

# CLI — force overwrite existing agent files
slmcode blocks apply rust --materialize-agents --force

# API
curl -X POST localhost:7420/api/packs/rust/apply \
  -H 'Content-Type: application/json' \
  -d '{"materialize_agents": true}'
```

### What Happens on Apply

1. **Pipeline** — Pipeline block's spec is written to `.slmcode/pipeline.yaml`
2. **Quality** — QA gate command is set in `.slmcode/config.yaml` (`qa_gate_command`)
3. **Agents** — If `--materialize-agents`, referenced agent blocks are written to `.slmcode/agents/`
4. **Skills** — If `pin_skills: true`, skills are added to `config.pinned_skills`
5. **Overrides** — `override_tester` sets `phases.test.agent`, `override_worker` sets `execute.default_role`
6. **Tracking** — `active_pack` and `active_pipeline` are set in config for UI display

---

## Custom Stacks

Stacks are provider/model presets that set global LLM configuration and quality knobs.

### Stack Schema

```yaml
# stacks/my-stack.yaml
label: My Custom Stack          # Display name
description: Custom stack with specific settings
icon: "🔧"                     # Emoji or text icon
color: from-cyan-500 to-blue-700  # Tailwind gradient for UI card

# ── Provider & Model ──
provider: openai               # Provider name (omlx, ollama, openai, deepseek, …)
endpoint: https://api.openai.com/v1  # API base URL (omit for provider default)
model: gpt-4o-mini             # Default model ID

# ── Performance ──
temperature: 0.15              # Global temperature (0.0–2.0)
max_tokens: 4096               # Global max tokens per turn
max_parallel: 3                # Max parallel worker tasks
max_retries: 3                 # Review/correct max retries
max_context_kb: 64             # Context window budget (KB)
think_passes: 2                # Multi-pass thinking cycles

# ── Quality Gates ──
qa_gate: true                  # Enable QA gate after board completes
qa_gate_max_rounds: 3          # Max QA gate iterations
post_worker_smoke: true        # Run smoke check after each worker
quality_monitor: true          # Monitor empty/loop/hallucinated tool calls
static_quality: true           # Reject stub/placeholder code
thinking_budget: true          # Enforce thinking token budget
worker_critique: true          # Auto self-fix pass on weak worker output

# ── Harness Invariants ──
write_guard: true              # ws_write refuses existing files
read_before_edit: true         # edit/patch require prior ws_read
shell_write_guard: true        # Block cat>/tee overwrite redirects
tool_guidance: true            # Per-turn tool skill cards
knowledge_inject: true         # Keyword knowledge injection
context_compact: true          # Mid-run CONTEXT.md summarization
react_compact: true            # Mid-run ReAct conversation compaction
wave_snapshots: true           # Per-wave file rewind points
hooks_enabled: true            # Load .slmcode/hooks.json

# ── Interaction Modes ──
clarify_mode: ask              # auto | ask | off
plan_approve: ask              # off | auto | ask
escalate_ask: ask              # ask | auto | off
continue_ask: ask              # ask | auto | off
auto_approve: false            # Skip all HITL waits (forces recommended)

# ── Model Profiles (per-model caps) ──
model_profiles:
  default:                     # Applied when no specific model profile matches
    context_limit: 65536
    max_tokens: 4096
    thinking_budget_tokens: 8192
    skill_token_budget: 400
    knowledge_token_budget: 300
    temperature: 0.15
    max_turns: 32
  gpt-4o-mini:                 # Model-specific override (key matches model field)
    context_limit: 128000
    max_tokens: 16384
    thinking_budget_tokens: 16384
    max_turns: 64

# ── Per-Agent LLM Defaults (optional) ──
agents:                        # Write these as defaults when applying stack with --agents
  worker:
    model: gpt-4o-mini
    provider: openai
  reviewer:
    model: gpt-4o              # Use a stronger model for review
    provider: openai
  tester:
    model: gpt-4o-mini
    provider: openai
```

### Applying Stacks

```bash
# Apply a stack (writes to .slmcode/config.yaml)
slmcode stack apply deepseek

# Apply with per-agent LLM defaults materialized
slmcode stack apply openai --agents

# Remove all per-agent LLM overrides (agents inherit stack)
slmcode stack apply ollama-local --clear-agent-llm

# Force overwrite existing agent pins
slmcode stack apply openai --agents --force-agents
```

---

## Skills (SKILL.md)

Skills inject conventions and guidance into specialist system prompts at runtime. Each skill is a markdown file with YAML frontmatter.

### SKILL.md Schema

```markdown
---
name: my-skill                 # Unique skill name (lowercase kebab-case)
description: >                 # Short blurb for matching and UI display
  Custom conventions for our React codebase.
triggers: react, component, typescript, frontend  # Keywords for query matching
agents: worker, corrector, reviewer               # Which specialists get this skill
                                                   # Empty or "*" = all specialists
user-invocable: true           # Allow @skill:my-skill in queries (default: true)
---

# My Skill Title

## When to use
- When working on React components
- When touching TypeScript files in the frontend
- When modifying shared UI library

## Rules

### Component Structure
- Prefer functional components with typed props
- One component per file unless tightly coupled
- Use named exports, not default exports

### Testing
- Each component must have a corresponding `.test.tsx` file
- Use `@testing-library/react` for component tests
- Mock external API calls with `msw`

### Code Quality
- Run `npx eslint .` before committing
- TypeScript strict mode is ON — no `any` types
- CSS should use Tailwind utility classes, not inline styles

### Anti-patterns
- Do NOT use `any` type — create proper interfaces
- Do NOT skip tests for "simple" components
- Do NOT import from `../../..` deep paths — use path aliases
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique skill identifier (lowercase, kebab-case) |
| `description` | Yes | Short blurb for matching and UI |
| `triggers` | No | Comma-separated keywords for query matching |
| `agents` | No | Comma-separated agent IDs this applies to. `*` or empty = all. |
| `user-invocable` | No | Whether `@skill:name` in queries activates it. Default: true |

### Skill Resolution

Skills are scored and ranked at runtime:
1. **Explicit refs** (`@skill:name` in query) → base score 1000
2. **Agent-targeted** (skill lists this agent) → base score 80
3. **Keyword matches** (query tokens appear in name/description/triggers) → +10 per match
4. **Trigger matches** (exact trigger keyword in query) → +2 per match
5. **Pinned skills** (in `config.pinned_skills`) → always included, base score 1000

The top N skills (default limit: 6) are packed into the specialist's prompt via `RenderPack()`.

### Creating Skills

```bash
# Create a new project skill
slmcode skills new my-conventions

# Create with specific agents
slmcode skills new my-conventions --agents worker,corrector

# Edit in $EDITOR
slmcode skills edit my-conventions

# List all skills
slmcode skills list
```

### Skill Paths

| Location | Purpose |
|----------|---------|
| `skills/default/<name>/SKILL.md` | Builtin skills (shipped with slmcode) |
| `.slmcode/skills/<name>/SKILL.md` | Project-level skills |
| Config `skills_dirs` entries | Additional directories |

---

## Project Memory Files

SLMCode uses markdown files in `.slmcode/` as persistent memory.

### File Reference

| File | Purpose | Auto-managed? |
|------|---------|---------------|
| `PROJECT.md` | Durable project facts (stack, patterns, conventions) | No — you write it |
| `CONTEXT.md` | Working focus + live discoveries | Yes — context agent updates it |
| `MEMORY.md` | Learned lessons and pitfalls | Yes — memory agent distills it |
| `PLAN.md` | Current execution plan | Yes — planner writes it |
| `TASKS.md` | Task board as markdown | Yes — board snapshot |
| `SCRATCH.md` | Scratchpad for specialists | Yes — agents use as scratch |
| `SKILLS.md` | Skill index + recent lessons | Yes — learning agent updates |
| `QUERY.md` | Current user query | Yes — set on each run |

### PROJECT.md Example

```markdown
# slmcode Project

## Stack
- **Language**: Go 1.23+
- **Framework**: net/http + chi router
- **Database**: PostgreSQL via pgx
- **Testing**: go test + testify
- **Linting**: golangci-lint (strict config in .golangci.yml)

## Conventions
- All new handlers go in `internal/handlers/`
- Each handler must have a corresponding test file
- Database queries use prepared statements — no string interpolation
- Error handling: always wrap with `fmt.Errorf("context: %w", err)`

## API Design
- RESTful: resources as nouns, HTTP verbs as actions
- JSON request/response bodies
- Auth via JWT in Authorization header
- All endpoints return structured errors: `{"error": "message", "code": "ERR_CODE"}`

## Known Issues
- Rate limiting not yet implemented (see #42)
- Pagination cursor is unstable under high concurrency (see #67)
```

### CONTEXT.md (Auto-managed Example)

```markdown
## Active focus
- Implementing JWT auth middleware for API routes
- Focus files: internal/auth/middleware.go, internal/auth/token.go

## Recent discoveries
- The chi router supports middleware chaining via r.Use()
- JWT parsing uses golang-jwt v5 — migrated from v4 last sprint

## Open questions
- Should we store refresh tokens in Redis or in DB?
- Need to confirm token expiration policy with product team

## Relevant paths
- internal/auth/ — auth middleware and token handling
- internal/handlers/ — API route definitions
- cmd/server/main.go — server bootstrap
```

---

## Config Reference

Complete `.slmcode/config.yaml` with every field:

```yaml
# ── Provider & Model ──
provider: omlx                 # Provider name (omlx, ollama, openai, deepseek, …)
endpoint: http://127.0.0.1:8000/v1  # API base URL
model: Qwen3-Coder-30B-A3B-Instruct-MLX-4bit  # Default model
api_key: ""                    # Prefer env vars (SLMCODE_API_KEY) or auth.json
backend: slmcode               # slmcode | claude-code

# ── Mode ──
mode: full                     # full (pipeline) | specialist (single agent)
specialist: ""                 # Agent ID when mode=specialist
pinned_skills:                 # Skills always loaded (in addition to @skill: refs)
  - atomic-coding
  - specialist-worker

# ── Stacks & Packs ──
active_stack: omlx-local       # Last applied stack ID (UI highlight)
active_pack: python            # Last applied language pack ID
active_pipeline: python        # Active pipeline block ID

# ── Performance ──
temperature: 0.2               # 0.0–2.0
max_tokens: 4096               # Max completion tokens per turn
max_retries: 4                 # Review/correct retries
max_parallel: 2                # Max parallel worker tasks
max_context_kb: 32             # Context window budget (KB)
think_passes: 1                # Multi-pass thinking cycles
task_timeout: 12m0s            # Per-task timeout

# ── Model Catalog ──
enabled_models: []             # Optional allowlist of model IDs (empty = all)
llm_retry_count: 3             # HTTP retries on LLM calls
llm_retry_delay_ms: 1000       # Delay between retries (ms)

# ── Quality Gates ──
qa_gate: true                  # Run QA gate after board completes
qa_gate_command: python -m pytest -q  # QA gate command (auto-detected if empty)
qa_gate_max_rounds: 3          # Max QA gate iterations
post_worker_smoke: true        # Run smoke after each worker

# ── Interaction Modes ──
clarify_mode: ask              # auto | ask | off
clarify_timeout: 2m0s          # Timeout for ask mode
scope_judge: true              # Post-split PRD completeness check
plan_approve: ask              # off | auto | ask
plan_approve_timeout: 2m0s
placeholder_pass: true         # Post-execute stub scan
continue_ask: ask              # ask | auto | off
continue_ask_timeout: 2m0s
escalate_ask: ask              # ask | auto | off
escalate_ask_timeout: 30s
escalate_timeout_agent: ""     # Specialist that decides on timeout

# ── Permissions ──
permission: auto               # auto | dry-run | review
shell_permission: allow         # allow | ask | deny
shell_whitelist: true          # Enforce SAFE_PREFIXES
shell_allow: []                # Extra safe prefix patterns
shell_ask_timeout: 2m0s
dry_run: false                 # No file writes
verbose: false                 # Loud agent logs
auto_approve: false            # Skip HITL waits

# ── Compact Mode ──
compact_mode: true             # Trim live events in TUI/CLI
context_compact: true          # Mid-run CONTEXT.md summarization
context_compact_engine: heuristic  # heuristic | llm | auto
react_compact: true            # Mid-run ReAct conversation compaction
react_compact_at_percent: 80   # Trigger compaction at this % of context budget

# ── Session ──
session_event_log: true        # Write events.jsonl under queries/
auto_refine: false             # Append refine notes into CONTEXT
auto_refine_max_rounds: 2      # Max refine passes

# ── Safety ──
wave_snapshots: true           # Per-wave file rewind points
file_checkpoints: true         # Snapshot files before first write
hooks_enabled: true            # Load .slmcode/hooks.json

# ── SLM Harness Invariants ──
write_guard: true              # ws_write refuses existing files
read_before_edit: true         # edit/patch require prior ws_read
shell_write_guard: true        # Block cat>/tee overwrite
tool_guidance: true            # Per-turn tool skill cards
knowledge_inject: true         # Keyword knowledge injection
quality_monitor: true          # Monitor empty/loop/hallucinated tool calls
static_quality: true           # Reject stub/placeholder code
thinking_budget: true          # Enforce thinking budget
thinking_budget_tokens: 4096   # Hard-abort threshold
finalize_warn: true            # Warn before MaxIter exhaustion
require_smoke: true            # Coding tasks need smoke for approve
claims_gate: true              # Reject hallucinated files_changed
worker_critique: true          # Auto self-fix pass
over_edit_guard: true          # Refuse whole-file-style edits
read_head_lines: 80            # Auto-trim read head
auto_text_tools: false         # Strengthen corrector recovery

# ── Model Profiles ──
model_profiles: {}             # Per-model caps (see stack example above)

# ── MCP ──
mcp_servers: []                # Thin read-only MCP connections

# ── Embedding ──
embedding_enabled: false
embedding_endpoint: ""
embedding_model: ""
embedding_api_key: ""
embedding_top_k: 5

# ── Pricing ──
price_preset: ""
price_prompt_per_mtok: 0
price_completion_per_mtok: 0

# ── Paths ──
root: /path/to/project         # Project root (auto-set from cwd)
skills_dirs: []                # Extra skill directories
listen: 127.0.0.1:7420         # Studio server bind address
```

### CLI Config Commands

```bash
# Show all config
slmcode config show

# Set individual fields
slmcode config set provider openai
slmcode config set model gpt-4o-mini
slmcode config set permission review
slmcode config set qa_gate true
slmcode config set pinned_skills atomic-coding,my-skill

# Apply a stack (writes multiple config fields at once)
slmcode stack apply deepseek

# Apply a language pack
slmcode blocks apply python
```

---

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
