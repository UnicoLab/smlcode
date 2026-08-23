# 🦋 Skills

Skills are reusable instruction packs — the difference between “generic coding bot”
and “knows how *we* ship”. ✨

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">📜</span>
<p class="slm-banner__text" markdown>
<strong>Hygiene rule:</strong> vague skills make vague agents.
“Be careful” is not a skill. “Never rewrite unrelated files” is a skill.
</p>
</div>

---

## Mental model 🧠

```text
bundled skills  +  project overrides  +  learned skills
        └──────────► matched into TaskPacks 📦
```

| Kind | Where | Who writes it |
|------|-------|----------------|
| 📦 Bundled | shipped with SLMCode | maintainers |
| 🏠 Project | `.slmcode/skills/` | you |
| 🦋 Learned | `.slmcode/skills/learned/` | the flywheel |

Index file: `.slmcode/SKILLS.md` (auto-maintained — don’t micro-manage unless you must).

---

## SKILL.md shape 🧬

```yaml
---
name: atomic-coding
description: Prefer tiny diffs, clear acceptance checks
triggers: refactor, cleanup, helper
agents: worker, deep, corrector
paths: "**/*.go, cmd/**"
user-invocable: true
---

# Atomic coding

- One concern per task
- Touch only listed files unless discovery proves otherwise
- Leave tests greener than you found them
```

| Field | Meaning |
|-------|---------|
| `name` | Stable id (`@skill:name`) |
| `description` | Human + matcher hint |
| `triggers` | Keywords that boost matching |
| `agents` | Which specialists see it (`*` = all) |
| `paths` | Gate the skill on the files a run actually touches (see below) |
| `user-invocable` | Can users pin / reference it? |

### `paths:` — gating a skill on the files in scope 🎯

Context is the scarcest resource a small model has, and a Go-specific skill in a
TypeScript task's prompt is pure noise. `paths:` is a comma-separated list of globs
supporting `*`, `?`, `[…]`, `**` and a bare directory prefix:

| Situation | Result |
|---|---|
| skill has **no** `paths:` | ungated — participates exactly as before |
| skill **has** `paths:`, and at least one file in scope matches | participates |
| skill **has** `paths:`, and nothing in scope matches | left out of the prompt |
| the scope is **empty / unknown** | gating is disabled; the skill participates |

That last row matters: `slmcode skills list`, Studio's skills page and any caller that
does not yet know which files a run will touch pass an empty scope, so a gated skill is
never *hidden* from you — it is only kept out of prompts where it could not apply.

An explicit `@skill:name` in the query, or a `pinned_skills` entry in config, **always
wins**: you naming a skill outranks a heuristic about file extensions.

---

## Day-to-day commands 🛠️

```bash
slmcode skills                 # list
slmcode skills show atomic-coding
slmcode skills new my-skill --agents worker
slmcode skills edit my-skill   # project override
```

### Pin or reference 📌

```bash
slmcode run --skill atomic-coding "Refactor helpers"
slmcode run "Fix login @skill:multipass-quality"
```

Studio → **Skills** panel has pin chips. Config:

```yaml
pinned_skills:
  - atomic-coding
  - multipass-quality
```

---

## Bundled starters (taste) 🍿

| Skill | Vibes |
|-------|-------|
| `atomic-coding` | Tiny diffs, clear done ✅ |
| `multipass-quality` | Think → critique → refine 🔁 |
| `markdown-memory` | Treat CONTEXT/MEMORY as sacred 💾 |
| `engine-full-pipeline` | Full orchestrated run behavior 🏭 |
| `specialist-*` | Role-specific playbooks 🧩 |

Exact set evolves — `slmcode skills` is truth. Docs can lie; the CLI rarely does.

---

## Teaching the flywheel 🦋

After good runs, check:

```bash
slmcode docs show SKILLS.md
ls .slmcode/skills/learned/
```

Promote hard-won lessons into a **project skill** when they stop being optional.

!!! tip "🧹 Skill hygiene"
    Prefer sharp constraints over motivational posters.
    Your workers are interns with amnesia — write the checklist.

---

## Related 🔗

- [🧩 Agents](agents.md) — who consumes skills
- [🧠 Concepts](concepts.md) — flywheel
- [⚙️ Config](config.md) — `pinned_skills`, `skills_dirs`

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
