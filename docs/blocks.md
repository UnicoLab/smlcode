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

SLMCode ships **fifteen** packs. Each one is a `pack` block composing a pipeline, a
quality block and language-aware agents; `slmcode init` picks one automatically (see
[Detection](#detection-how-a-pack-is-chosen)).

| Pack | Language | Agents | Smoke | QA gate (`qa_gate_command`) |
|---|---|---|---|---|
| 🐹 `go` | go | `go-worker` `go-tester` `go-reviewer` | `go test ./... -short` | `go test ./... -race -count=1` |
| 🐍 `python` | python | `python-worker` `python-tester` `python-reviewer` | `python -m pytest -q` | `python -m pytest -q` |
| ⚛️ `react` | typescript | `react-worker` `react-tester` `react-reviewer` | `npm test --silent` | `npm test --silent` |
| 🟦 `typescript` | typescript | `ts-worker` `ts-tester` `ts-reviewer` | `npm test --silent` | `npm test --silent` |
| 🌐 `web` | html | `web-worker` `web-tester` | `test -s index.html \|\| test -s index.htm` | same |
| 🦀 `rust` | rust | `rust-worker` `rust-tester` `rust-reviewer` | `cargo check --quiet` | `cargo test --quiet` |
| ☕ `java` | java | `java-worker` `java-tester` `java-reviewer` | `mvn -q -B -DskipTests compile` | `mvn -q -B test` |
| 🟪 `kotlin` | kotlin | `kotlin-worker` `kotlin-tester` | `./gradlew compileKotlin --console=plain` | `./gradlew test --console=plain` |
| 🟣 `dotnet` | csharp | `dotnet-worker` `dotnet-tester` `dotnet-reviewer` | `dotnet build --nologo --verbosity quiet` | `dotnet test --nologo --verbosity quiet` |
| 💎 `ruby` | ruby | `ruby-worker` `ruby-tester` | `bundle exec rspec --no-color` | `bundle exec rspec --no-color` |
| 🐘 `php` | php | `php-worker` `php-tester` | `vendor/bin/phpunit --colors=never` | `vendor/bin/phpunit --colors=never` |
| 🕊️ `swift` | swift | `swift-worker` `swift-tester` | `swift build` | `swift test` |
| ⚙️ `cpp` | cpp | `cpp-worker` `cpp-tester` | `cmake --build build` | `ctest --test-dir build --output-on-failure` |
| 🧩 `shadcn` | typescript | `shadcn-worker` `shadcn-reviewer` `react-tester` | `npx tsc --noEmit` | `npx tsc --noEmit` |
| 🎨 `untitledui` | typescript | `untitledui-worker` `untitledui-reviewer` `react-tester` | `npx tsc --noEmit` | `npx tsc --noEmit` |

`slmcode blocks list` prints the live set; this table is a snapshot of it.

!!! tip "shadcn and untitledui are *methods*, not languages"
    The last two build React UI by **installing** components with the library's own
    CLI and wiring them up, rather than writing them by hand. You do not have to
    apply them: both are chosen automatically from the request and the project, and
    their agents ship registered. Applying one just pins the choice for good.
    See [Frontend: assemble or write](frontend.md).

Every pack also pins skills (`pin_skills: true`) — always `atomic-coding`, `specialist-worker`
and `specialist-tester`, plus language-specific ones: `go` adds `go-table-tests` and
`go-concurrency`, `typescript` adds `typescript-strict`, `react` adds `react-hooks` and
`typescript-strict`.

### 🐚 Shell (agents only)

`shell-worker` / `shell-tester` — for Bash/shell scripts (`bash -n` + `shellcheck`).
No standalone pack; the generic pipeline selects them when the workspace is shell.

---

## Detection — how a pack is chosen

`slmcode init` calls `blocks.DetectPack(root, root)`, and that is the **only** detection answer in
the codebase. It is deterministic and precedence-ranked, and it scores each quality block's
`detect` stanza:

| Signal | Score | Meaning |
|---|---|---|
| `detect.contains` satisfied | **+25** each | strongest: proof from a marker file's *content* |
| `detect.files` marker present in the root | +12 each | strong |
| a source file with a `detect.extensions` suffix | +2 each, capped at 3 | weak tiebreak |
| `detect.priority` | added as-is | the pack author's ranking |

Two rules make it correct on real repositories:

- **Nested sub-projects are skipped.** The extension walk does not descend into a directory that
  carries its own project marker (`go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`,
  `pom.xml`, `build.gradle{,.kts}`, …). A Go module with a Vite app in `web/` is a Go project;
  the frontend's `.ts` files no longer out-vote the backend's `go.mod`.
- **Markers outweigh stray files.** "This repo has a `pyproject.toml`" is worth much more than
  "some `.py` file exists somewhere".

### `detect.contains` — proving a language from file content

`package.json` alone cannot tell `react` from `typescript`, and a filename-only rule got it wrong
in both directions. `contains` maps a root file onto substrings that prove the language; any one
match satisfies the entry:

```yaml
spec:
  detect:
    files: [package.json]
    extensions: [.tsx, .jsx]
    contains:
      package.json: ['"react"', '"next"', '"preact"', '"react-dom"']
    priority: 14
```

At most 256 KB of each named file is read. A `contains` entry that is declared but not satisfied
scores nothing — it never counts against the block.

So: a `package.json` that declares React resolves to the `react` pack; one that does not resolves
to `typescript`; a directory of `index.html` and `.css` with no `package.json` resolves to `web`.

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

Create, edit and delete project blocks (written to `.slmcode/blocks/`):

```bash
slmcode blocks new agent my-agent --file agent.yaml
slmcode blocks new agent my-agent --name "My Agent"
slmcode blocks edit agent my-agent --file agent.yaml
slmcode blocks delete agent my-agent
slmcode blocks apply go --force        # overwrite existing agent files
```

In the TUI or the chat REPL:

```
/pack <pack-id>   — apply a language pack (any of the thirteen)
/blocks           — list all available blocks
/skills           — list loaded skills
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
    files: [Cargo.toml]           # root marker files (+12 each; globs allowed)
    extensions: [.rs]             # source suffixes (+2 each, capped at 3)
    contains:                     # content proof (+25 each) — any substring matches
      Cargo.toml: ['[package]']
    priority: 20                  # author ranking, added to the score
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

The **PipelineEditor** includes a preset selector listing every pipeline block the registry can see — the thirteen builtins plus anything under `.slmcode/blocks/pipelines/` — with one-click switching.

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
