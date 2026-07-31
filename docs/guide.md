# 🧭 User guide

Daily-driving SLMCode without turning your repo into modern art. 🎨
(Unless modern art was the goal. In that case… carry on.)

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🛤️</span>
<p class="slm-banner__text" markdown>
<strong>Recommended path:</strong>
<a href="install.md">Install</a> → <a href="quickstart.md">Quick start</a> →
<a href="concepts.md">Concepts</a> → you are here. Welcome to adulthood.
</p>
</div>

---

## Day-to-day loop 🔁

```bash
cd your-project
slmcode init
# optional: AGENTS.md at repo root (be stern, be kind)
# edit .slmcode/PROJECT.md

slmcode                      # premium TUI
slmcode run -v "Add validation to the login handler"
slmcode board
slmcode studio
```

| Habit | Command | Vibe |
|-------|---------|------|
| 🔎 Explore only | `run --agent explorer "…"` | Maps, not edits |
| 🎯 Pin craft | `run --skill atomic-coding "…"` | Tiny diffs or bust |
| 👀 Watch board | `watch` | ASMR for kanban |
| 🛡️ Safe writes | `config set permission review` → `apply` | Trust, but verify |

More copy-paste → [🧪 Recipes](recipes.md).

---

## Pipeline (what happens) 🏭

```mermaid
flowchart TD
  Q[Query 🎯] --> I[Instructions + skills]
  I --> C[Context]
  C --> E{Explore?}
  E -->|reuse ♻️| P[Plan / split]
  E -->|deep 🕵️| X[Explorer] --> P
  P --> Coord[Coordinator 🧭]
  Coord --> W[Workers 🛠️]
  W --> R[Review ↔ correct 🔍]
  R --> L[Learn / test / skills 🦋]
```

Force a fresh dig: `SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"`.

Deep dive → [🧠 Concepts](concepts.md).

---

## Surfaces 🎛️

| Surface | Doc | Mood |
|---------|-----|------|
| Premium TUI + chat | [🖥️ TUI & chat](tui.md) | Keyboard wizard |
| Studio GUI + API | [🎨 Studio](studio.md) | Click enjoyer |
| Full CLI | [⌨️ CLI reference](cli.md) | Script goblin |

---

## Skills & specialists 🧩

Skills teach house style; specialists execute roles.

```bash
slmcode skills
slmcode run --skill multipass-quality "…"
slmcode run "Fix login @skill:atomic-coding"
```

→ [🦋 Skills](skills.md) · [🧩 Agents](agents.md)

---

## Project memory (the real DB) 💾

Markdown. On disk. Boring on purpose. Future-you can `grep` it. Past-you cannot gaslight it.

| File | Role |
|------|------|
| `.slmcode/PROJECT.md` | Durable facts |
| `.slmcode/CONTEXT.md` | Working focus + discoveries |
| `.slmcode/MEMORY.md` | Lessons / pitfalls |
| `.slmcode/SKILLS.md` | Index + recent lessons |
| `.slmcode/skills/learned/` | Auto-grown conventions |
| `.slmcode/sessions/` | Resumable runs |
| `.slmcode/pending/` | Staged review writes |
| `AGENTS.md` / `CLAUDE.md` / `.cursorrules` | Auto-injected instructions |

```bash
slmcode context append "Prefer table-driven tests"
slmcode docs show MEMORY.md
```

---

## Permissions 🛡️

| Mode | Behavior |
|------|----------|
| `auto` | Write now ✍️ |
| `dry-run` | Simulate 🎭 |
| `review` | Stage → `slmcode apply` 👀 |

```bash
slmcode config set permission review
slmcode run "refactor foo"
slmcode apply && slmcode diff
```

Shell: `shell_permission: allow | ask | deny` — independent of file writes.
→ [⚙️ Config](config.md)

---

## Quality knobs 🎛️

```bash
slmcode config set think_passes 2
slmcode config set retries 2
slmcode config set parallel 2
slmcode config set max_context_kb 16
```

| Symptom | Try |
|---------|-----|
| 🥴 Wanders | Lower context, pin `atomic-coding`, stern `AGENTS.md` |
| 🥴 Weak JSON | More think passes / retries; better tool-calling model |
| 🐢 Too slow | Lower parallel; explore reuse; faster provider |

→ [❓ FAQ](faq.md)

---

## Sessions & resume 🛟

```bash
slmcode session list
slmcode session resume run-…
# TUI: /stop then /resume
```

Ctrl+C mid-run is a feature, not a crime — it checkpoints.

---

## Develop from a checkout 🛠️

```bash
make lint && make test && make docs-build
RUN_E2E=1 make e2e
```

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">🧃</span>
<p markdown>
<strong>Hydration tip:</strong> if a run feels cursed, check <code>doctor</code>, then permissions,
then whether you asked for a rewrite of the universe. Usually it’s #3.
</p>
</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
