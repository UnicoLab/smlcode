# 🧠 Concepts

The ideas behind SLMCode — so the rest of the docs feel inevitable instead of magical.
Also: fewer “why is it like this?” Slack threads. 😅

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🏠</span>
<p class="slm-banner__text" markdown>
<strong>House metaphor:</strong> don’t ask the intern to also be the architect, QA, and filing cabinet.
Give them a desk, a ticket, and a checklist. Then maybe a snack.
</p>
</div>

---

## <span class="slm-kicker">01</span> Harness ≠ model 🧰

A **model** predicts tokens.
A **harness** decides *what* the model sees, *when* it acts, *how* failures recover, and *where* knowledge sticks.

Frontier tools often hide the harness behind a chat box. SLMCode makes the harness explicit — like leaving the kitchen lights on.

| Layer | Owner | Job |
|-------|-------|-----|
| 🧭 Routing / board | Go | Plan, schedule, stop/resume |
| 🧩 Specialists | Prompts + tools | One role, one pack |
| 🔍 Critic | Reviewer + disk evidence | Catch fiction |
| 💾 Memory | `.slmcode/*.md` | Compound lessons |

!!! quote "🎤 UnicoLab watercooler"
    Models are the talent. Harnesses are the stage managers.
    Never let the talent rearrange the set mid-show.

---

## <span class="slm-kicker">02</span> Scoped packs (the turkey rule) 🦃

Stuffing the whole repo into context is how small models fall asleep mid-sentence.

Each specialist receives a **TaskPack**:

- slices of PROJECT / CONTEXT / MEMORY / skills
- a few focus files
- **one** atomic task
- tool allowlists that match the role

```mermaid
flowchart TD
  Repo[Whole repo 🏢] -.->|never wholesale| Model[Model 😴]
  Pack[TaskPack 📦] --> Model2[Model 😎]
  Pack --> MD[.slmcode markdown]
  Pack --> Files[Focus files]
  Pack --> Skill[Matched skills]
  Pack --> Task[Atomic task]
```

Bigger models still benefit: less noise, clearer acceptance criteria, cheaper runs.
(Your CFO’s favorite sentence.)

---

## <span class="slm-kicker">03</span> Plan → split → coordinate 📋

```text
query
  → instructions (AGENTS.md / PROJECT.md)
  → skills match
  → context agent
  → explore OR reuse memory
  → clarify (interview: ask|auto recommended → Locked PRD)
  → scope judge (every task gets concrete acceptance / PRD)
  → planner (multipass) → splitter → sanitize (+ auto tester task)
  → coordinator advice
  → parallel execute (worker self-smoke via ws_shell)
  → review ↔ correct
  → finalize tester (real commands required)
  → QA gate (install deps + pytest/go test/smoke until green)
  → learn → evolve skills → session snapshot
```

The **coordinator** doesn't write code. It steers the kanban: promote, reassign, add tasks, note risks.
Think air-traffic control, not pilot. ✈️

---

## <span class="slm-kicker">04</span> Explore reuse ♻️

Deep exploration is expensive (especially on slow local inference).

If CONTEXT is rich, MEMORY/PROJECT exist, and discovery finds relevant paths, SLMCode **skips** the deep dive and reuses knowledge.
Your fans thank you. Your GPU fans thank you louder.

```bash
# When memory feels stale or wrong:
SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"
```

---

## <span class="slm-kicker">05</span> Self-critic with evidence 🔍

```text
worker/deep → reviewer → (reject) → corrector → reviewer …
```

Reviewers can be flaky on SLMs. Heuristics prefer:

- clear `status: done`
- `files_changed` that match disk
- rename satisfaction when paths already moved

!!! tip "📜 Disk beats vibes"
    Always. If the file says hello and the model says goodbye — trust the file.

---

## <span class="slm-kicker">06</span> Knowledge flywheel 🦋

After a run:

1. **MEMORY.md** — lessons / pitfalls
2. **CONTEXT.md** — what we touched
3. **SKILLS.md** + `skills/learned/` — conventions that stuck
4. **sessions/** — resumable snapshots

Tomorrow's run starts smarter than today's. That's the product.
(Also: please don’t `rm -rf .slmcode` for sport.)

---

## <span class="slm-kicker">07</span> Permissions are a feature 🛡️

| Mode | Use when |
|------|----------|
| `auto` | You trust the loop (or it's a playground) 🛝 |
| `dry-run` | Demos, CI dry checks, “what would you do?” 🎭 |
| `review` | Real repos — stage patches, then `slmcode apply` 👀 |

Shell is separate: `shell_permission: allow | ask | deny`.
Files and shells have different blast radii. Treat them that way.

---

## <span class="slm-kicker">08</span> Any LLM, same loop 🔌

Providers are adapters. The harness stays constant.

- 🏠 Local SLM → more `think_passes`, smaller `max_context_kb`, patience
- ☁️ Frontier → raise parallel, enjoy speed, keep inspectability

See [Providers](providers.md) and [Config](config.md).

---

## Next 🗺️

- [⏱️ Quick start](quickstart.md) — feel it
- [🧭 User guide](guide.md) — drive it daily
- [🏗️ Architecture](architecture.md) — package map for contributors

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
