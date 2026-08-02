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
slmcode studio               # Pipeline tab → edit → Save
curl -s localhost:7420/api/pipeline | jq .
```

Reset to built-ins:

```bash
curl -s -X POST localhost:7420/api/pipeline/reset
```

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

## Related

- [Agents](agents.md) — roster + custom YAML
- [Config](config.md) — provider / quality knobs
- [Studio](studio.md) — cockpit layout
- [Architecture](architecture.md) — engine flow

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
