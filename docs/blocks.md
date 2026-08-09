# 🧱 Building Blocks

> Marketplace-ready YAML presets — share, remix, and discover pipelines, agents, quality packs, and language packs.

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🧱</span>
<p class="slm-banner__text" markdown>
<strong>Blocks are the foundation of extensibility.</strong> Every pipeline, every specialist agent, every quality check — all defined as portable YAML that anyone can create, share, and apply.
</p>
</div>

---

## What are Building Blocks?

Blocks are **versioned, marketplace-ready YAML packages** that define reusable configurations for the SLMCode pipeline. They come in four kinds:

| Kind | Schema | Purpose |
|------|--------|---------|
| `pipeline` | `PipelineBlock` | Phase graph, loop agents, insertable slots |
| `agent` | `AgentBlock` | Custom specialist definition or builtin override |
| `quality` | `QualityBlock` | Format/lint/test/build commands per language |
| `pack` | `PackBlock` | Composes pipeline + quality + agents + skills |

---

## Discovery Order

Blocks are discovered in a priority chain. **First ID wins per kind:**

1. **Project** — `.slmcode/blocks/{pipelines,agents,quality,packs}/*.yaml`
2. **User** — `~/.slmcode/blocks/…` or `$XDG_CONFIG_HOME/slmcode/blocks/…`
3. **Extra** — `$SLMCODE_BLOCKS` env var, walk-up `blocks/` dirs
4. **Builtin** — embedded in `pkg/blocks/bundled/` (compiled into binary)

This means **project overrides** always win. Drop a `.slmcode/blocks/pipelines/go.yaml` with your customizations, and it replaces the builtin Go pipeline for that project.

---

## Common Schema (Meta)

Every block YAML file shares this header:

```yaml
api_version: blocks/v1    # required — current schema version
kind: pipeline            # pipeline | agent | quality | pack
id: my-block              # lowercase kebab-case, 2-64 chars, [a-z][a-z0-9_-]+
name: My Block            # human-readable display name
description: A reusable block
version: "1.0.0"
author: UnicoLab
license: MIT
language: go              # lowercase language code
tags: [go, worker]
icon: "🐹"
shareable: true           # marketplace-ready flag (default: true)
```

---

## Predefined Language Packs (Builtin)

SLMCode ships with **three production-ready language packs:**

### 🐹 Go

```
Pipeline: go     | Agents: go-worker, go-tester     | Quality: go
```

- **Pipeline**: Go-aware execute phase with `go-tester` agent
- **Worker**: Module-aware, uses `go test ./<pkg> -short` after edits
- **Tester**: Full verify chain — `gofmt` → `go vet` → `go test -race` → `go build`
- **QA Gate**: `go test ./... -race -count=1`

### 🐍 Python

```
Pipeline: python | Agents: python-worker, python-tester | Quality: python
```

- **Pipeline**: Python-aware with `python-tester` agent
- **Worker**: PyProject-aware, smokes with `py_compile` + `pytest`
- **Tester**: `ruff check` → `mypy` → `pytest` (uv-aware)
- **QA Gate**: `python -m pytest -q` (or `uv run pytest -q`)

### ⚛️ React / TypeScript

```
Pipeline: react  | Agents: react-worker, react-tester   | Quality: react
```

- **Pipeline**: Frontend-aware with `react-tester` agent
- **Worker**: Vite/Next-aware, smokes with `tsc --noEmit`
- **Tester**: `npm run lint` → `tsc --noEmit` → `npm test` → `npm run build`
- **QA Gate**: `npm test --silent`

---

## CLI Commands

```bash
# List all available blocks, grouped by kind
slmcode blocks list

# Show details of a specific block
slmcode blocks show pipeline go
slmcode blocks show agent python-worker
slmcode blocks show pack react

# Apply a language pack (writes pipeline.yaml + config)
slmcode blocks apply go
slmcode blocks apply go --materialize-agents

# Validate all block YAML configs
slmcode blocks validate
```

In the interactive chat REPL:

```
/pack go       — apply the Go language pack
/pack python   — apply the Python language pack
/blocks        — list all available blocks
```

---

## Creating Custom Blocks

### Pipeline Block

Save as `.slmcode/blocks/pipelines/my-pipeline.yaml`:

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
    test:    {agent: tester,   when: always, label: Test}
    memory:  {agent: memory,   when: always, label: Memory}
    done:    {agent: "",       when: always, label: Done}
  execute:
    default_role: worker
    reviewer: reviewer
    corrector: corrector
    max_waves: 2
  slots:
    - id: quality-reminder
      agent: tester
      title: Quality reminder
      before: execute
      when: always
      persist_to: scratch
      fail_mode: continue
      input: |
        Remember quality bar: cargo clippy, cargo test, cargo build
        Query: {{query}}
```

### Agent Block

Save as `.slmcode/blocks/agents/my-worker.yaml`:

```yaml
api_version: blocks/v1
kind: agent
id: my-worker
name: My Worker
version: "1.0.0"
language: rust
tags: [rust, worker]
icon: "🦀"
spec:
  id: my-worker
  title: My Worker
  system_prompt: |
    You are a Rust implementation specialist. Stay inside HARD SCOPE.
    After edits, smoke with: cargo test -p <crate>
  tools: true
  max_iter: 16
  temperature: 0.12
  max_tokens: 3072
  skills: [specialist-worker, atomic-coding]
```

### Quality Block

Save as `.slmcode/blocks/quality/my-lang.yaml`:

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
  safe_prefixes:
    - cargo test
    - cargo build
    - cargo clippy
```

### Pack Block

Save as `.slmcode/blocks/packs/my-lang.yaml`:

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

---

## Studio Integration

The **BlockManager** page (navigate to Blocks in the sidebar) provides a visual browser for all available blocks:

- **Tabbed interface**: All, Packs, Pipelines, Agents, Quality
- **Cards** showing metadata: name, description, language, version, tags, source
- **One-click Apply** for packs and pipelines
- **Active indicators** showing which pack/pipeline is currently active

The **PackSelector** in Settings lets you switch language packs directly from the settings page, alongside the Stack Selector.

The **PipelineEditor** includes a preset selector that lets you switch between predefined pipeline configurations (Go, Python, React) with one click.

---

## Validation

```bash
slmcode blocks validate
```

Loads every block, calls `Validate()` on each, and for packs also verifies that all referenced pipelines, agents, and quality packs actually exist. Reports exact errors so you can fix YAML issues before running.

---

## Marketplace-Ready

All blocks are designed for sharing:

- **Versioned** — Semantic versioning (`"1.0.0"`)
- **Authored** — `author` and `license` fields
- **Tagged** — `tags` for discovery and filtering
- **Iconed** — `icon` for visual recognition in UI
- **Shareable** — `shareable: true` flag for marketplace listing

Drop your custom blocks into a GitHub repo's `blocks/` directory, and anyone can use them by setting the `SLMCODE_BLOCKS` environment variable.
