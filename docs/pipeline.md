# 🏭 Pipeline config

The full engine graph — phases, loop agents, and insertable slots — is **data-driven**.
Studio, the progress header, and the orchestrator all read the same file:

**`.slmcode/pipeline.yaml`**

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🧩</span>
<p class="slm-banner__text" markdown>
<strong>Insert any agent anywhere:</strong> before/after/replace a phase, pick a custom or built-in
agent, control the prompt template, fail mode, and where output lands. The UI follows the config.
</p>
</div>

---

## Quick start

```bash
slmcode init                 # writes default pipeline.yaml
slmcode blocks apply go      # apply Go-optimized pipeline preset
slmcode studio               # Pipeline tab → edit → Save
curl -s localhost:7420/api/pipeline | jq .
```

Or switch presets from the Studio:

- **Pipeline tab**: Use the preset selector (every pipeline block the registry sees) for one-click switching
- **Blocks tab**: Browse all pipeline presets and apply any
- **Settings**: Use the Pack Selector to switch the entire language workflow

Reset to built-ins:

```bash
curl -s -X POST localhost:7420/api/pipeline/reset
# or via CLI:
slmcode blocks apply go   # re-apply any preset

---

## Document shape

```yaml
version: 1
order: [init, skills, context, explore, docs, architect, clarify, plan, split, coord, execute, learn, polish, test, memory, done]

groups:
  - id: prepare
    label: Prepare
    steps: [init, skills, context, explore, docs]
  # …

phases:
  context:
    agent: context
    when: always          # always | auto | never
    label: Context
    tip: Refresh CONTEXT
    group: prepare
  explore:
    agent: explorer
    when: auto
  plan:
    agent: planner
    when: always
  test:
    agent: tester
    when: always

execute:
  default_role: worker
  reviewer: reviewer      # any registered agent id (custom OK)
  corrector: corrector
  max_waves: 2

slots:
  - id: pre-plan-audit
    agent: night-auditor  # custom or builtin
    after: explore        # or before: plan  or replace: plan
    when: always          # or never | query_matches:langgraph
    input: |
      Audit exploration before planning.
      Query:
      {{query}}
      Exploration:
      {{exploration}}
    persist_to: scratch   # scratch | context | memory | none
    fail_mode: continue   # continue | abort
    multipass: false
```

### Phase `when`

| Value | Behavior |
|-------|----------|
| `always` | Run every time |
| `auto` | Built-in heuristics (explore/architect/docs) |
| `never` | Skip (or use a `replace` slot instead) |

### Slot placement

Exactly one of:

- `after: <phase>` — run after the phase finishes
- `before: <phase>` — run before the phase
- `replace: <phase>` — run instead of the built-in phase agent

### Prompt placeholders

`{{query}}` · `{{exploration}}` · `{{plan}}` · `{{phase}}`

---

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/pipeline` | Resolved config + anchors + defaults |
| `PUT` | `/api/pipeline` | Body `{ "config": { … } }` |
| `POST` | `/api/pipeline/reset` | Restore defaults |

Agents themselves stay under `/api/agents` (prompt, model, tools, …).
The pipeline only **references** agent IDs and placement.

---

## Studio

**Pipeline** tab:

1. **Execute loop** — pick reviewer / corrector / default worker
2. **Phase agents** — bind any registered agent per stage + when
3. **Slots** — insert agents around phases with a prompt template

The top progress header is driven by the same `order` / `groups` / slots — no hard-coded stage list in the UI.

Board task role dropdowns also list **all** agents from `/api/agents` (customs included).

---

## Custom agents + pipeline

1. Create `@night-auditor` in **Agents** (or `.slmcode/agents/night-auditor.yaml`)
2. Open **Pipeline** → add slot `after: explore` → agent `night-auditor`
3. Save → next full run executes the slot and streams it live

Specialist mode also accepts custom agent IDs.

---

## Predefined pipeline presets

SLMCode ships with **thirteen** built-in pipeline presets, each optimized for a specific language:

| Preset | Language | Tester Agent | Worker Agent | QA Gate |
|--------|----------|-------------|-------------|---------|
| `go` | 🐹 Go | `go-tester` | `go-worker` | `go test ./... -race -count=1` |
| `python` | 🐍 Python | `python-tester` | `python-worker` | `python -m pytest -q` |
| `react` | ⚛️ React | `react-tester` | `react-worker` | `npm test --silent` |
| `typescript` | 🟦 TypeScript / Node | `ts-tester` | `ts-worker` | `npm test --silent` |
| `web` | 🌐 Static HTML/CSS/JS | `web-tester` | `web-worker` | non-empty `index.html` entrypoint |
| `rust` | 🦀 Rust | `rust-tester` | `rust-worker` | `cargo test --quiet` |
| `java` | ☕ Java | `java-tester` | `java-worker` | `mvn -q -B test` |
| `kotlin` | 🟪 Kotlin | `kotlin-tester` | `kotlin-worker` | `./gradlew test --console=plain` |
| `dotnet` | 🟣 C# / .NET | `dotnet-tester` | `dotnet-worker` | `dotnet test --nologo --verbosity quiet` |
| `ruby` | 💎 Ruby | `ruby-tester` | `ruby-worker` | `bundle exec rspec --no-color` |
| `php` | 🐘 PHP | `php-tester` | `php-worker` | `vendor/bin/phpunit --colors=never` |
| `swift` | 🕊️ Swift | `swift-tester` | `swift-worker` | `swift test` |
| `cpp` | ⚙️ C/C++ | `cpp-tester` | `cpp-worker` | `ctest --test-dir build --output-on-failure` |

Each preset:
- Sets the **test phase agent** to a language-specific verifier
- Configures the **execute loop** with a language-aware worker
- Adds **quality gate slots** with language-specific check reminders
- Pins relevant **skills** like `atomic-coding` and `specialist-tester`

Apply any preset via CLI, API, or Studio:

```bash
# CLI
slmcode blocks apply python

# API
curl -X POST localhost:7420/api/packs/python/apply

# API — apply just the pipeline (no QA gate)
curl -X POST localhost:7420/api/pipeline-presets/python/apply
```

Custom presets can be created as YAML blocks — see [🧱 Blocks](blocks.md).

---

## What a run skips, and why

A phase that is *enabled* still only runs when it has something to do. Every
skip is announced as an info event in the phase's own lane, so the Live view
and the CLI show the reason rather than a phase that silently did not happen.

| Phase | Runs when | Skipped with |
|-------|-----------|--------------|
| `split` (the splitter) | the plan has two or more steps, or one step over more than two known files, or no known file at all | `splitter skipped — a 1-step plan over N known file(s) is one task; building the board directly`. The board is built from the plan's one step with the known files as its scope. |
| per-wave coordinator (`coord @after-wave`) | the wave that just finished failed a task, escalated one, or produced a lesson worth keeping — a failure lesson or an honored human note; the routine "this task passed its acceptance" lessons every green task yields do not count | `coordinator @after-wave skipped — all N task(s) finished green with no failure, escalation or new lesson` |
| per-wave distillation (`learn`, `think_passes >= 2`) | same rule as the coordinator | `wave distillation skipped — …` |
| `memory` (end-of-run distillation) | the run left a lesson worth keeping (same notion), changed a file, or failed a task | `memory distillation skipped — nothing to distill: no lessons, no changed files, no failed tasks`. The distiller is a single call now, not a multipass cycle. |

Two more things the pipeline no longer repeats:

- **One concurrency limit for the whole run.** `max_parallel` used to bound only
  the execute wave; context ran beside explore, architect beside clarify,
  speculative digs and review races added slots on top. Every model request —
  phase roles, workers, reviewers, correctors, critique, triage, the planner's
  multipass cycle — now takes a slot of one run-wide gate sized `max_parallel`,
  and at `max_parallel: 1` the phase pairs run one after another.
- **The objective command runs once per tree state.** The deterministic
  pre-test, each team's acceptance, the integration command and the QA gate's
  first round all ask the same question of the same tree; a result is
  remembered per (command, tree fingerprint) and reused until something is
  written — an agent write, a rewrite of an already-changed file, a formatter
  pass or a dependency install all count.

---

## Related

- [Blocks](blocks.md) — building blocks system + marketplace
- [Agents](agents.md) — roster + custom YAML
- [Config](config.md) — provider / quality knobs
- [Studio](studio.md) — cockpit layout
- [Architecture](architecture.md) — engine flow

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
