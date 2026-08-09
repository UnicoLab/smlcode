---
title: SLMCode Docs
hide:
  - toc
---

<div class="slm-hero" markdown>

<div class="slm-hero__eyebrow" markdown>⚡ UnicoLab · open harness · summer energy</div>

# Coding agents that still work when tokens run out ☀️

<p class="slm-hero__lead" markdown>
A **premium coding harness** tuned for SLMs — with the ambition of frontier tools —
and a clean plug for **any** OpenAI-compatible LLM. Plan, specialize, criticize, learn.
Less “paste the monorepo”. More “ship the tiny win”.
</p>

<div class="slm-hero__actions" markdown>
[🚀 Install now](install.md){ .md-button .md-button--primary }
[⏱️ 60-second start](quickstart.md){ .md-button }
[🧠 How it thinks](concepts.md){ .md-button }
</div>

<div class="slm-hero__meta" markdown>
<span class="slm-pill">🍎 oMLX default</span>
<span class="slm-pill">🖥️ Premium TUI</span>
<span class="slm-pill">🎨 Offline Studio</span>
<span class="slm-pill">🧱 Building blocks</span>
<span class="slm-pill">🔌 Any provider</span>
<span class="slm-pill">💚 UnicoLab</span>
</div>

</div>

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">🦞</span>
<p markdown>
<strong>Frontier agents are wonderful</strong> — until the meter spins like a washing machine
and your context window becomes a landfill of half-read files.
SLMCode is the harness that says: <em>“maybe don’t feed the whole turkey to a 7B model.”</em>
</p>
</div>

## Why this exists 🤔

Frontier coding agents are wonderful — until the invoice arrives wearing a tuxedo.

Small local models need a **different loop**: atomic tasks, evidence gates, markdown memory,
and a critic that looks at the **disk**, not just the vibes.

!!! tip "🎤 The pitch in one breath"
    Keep the ambition of Claude Code / Antigravity / Pi-style harnesses.
    Fill the gaps so **7B–30B models** (and bigger ones) can ship — maybe with longer passes —
    without pretending they can swallow your monorepo whole. 🦃

<div class="slm-compare" markdown>

<div class="slm-compare__col slm-compare__col--bad" markdown>
### 😵 Dump the repo
- Giant prompts
- Sleepy SLMs
- Hallucinated “fixes”
- Mystery diffs
</div>

<div class="slm-compare__col slm-compare__col--good" markdown>
### 😎 Scoped packs
- Tiny TaskPacks
- Specialists with jobs
- Disk-backed critic
- Memory that compounds
</div>

</div>

---

## Start here 🗺️

<div class="grid cards" markdown>

-   :material-download:{ .lg .middle } **📦 Install**

    ---

    One-liners for macOS, Linux, Windows + Homebrew. No Go required.
    Your `PATH` is about to feel special.

    [:octicons-arrow-right-24: Get on PATH](install.md)

-   :material-rocket-launch:{ .lg .middle } **🚀 Quick start**

    ---

    Init a playground, run a tiny task, open Studio. Feel the loop.
    First green run ≈ one coffee sip.

    [:octicons-arrow-right-24: First green run](quickstart.md)

-   :material-brain:{ .lg .middle } **🧠 Concepts**

    ---

    Why scoped packs beat “paste the repo”. Explore reuse. Self-critic. Flywheel.

    [:octicons-arrow-right-24: Mental model](concepts.md)

-   :material-connection:{ .lg .middle } **🔌 Providers**

    ---

    oMLX, Ollama, OpenAI, OpenRouter, vLLM, or your private gateway.
    Bring your own brain. We bring the loop.

    [:octicons-arrow-right-24: Plug a brain in](providers.md)

-   :material-book-open-page-variant:{ .lg .middle } **📘 Handbook**

    ---

    Daily guide, TUI, skills, Studio, agents, battle-tested recipes.
    Less theory. More “paste this and win”.

    [:octicons-arrow-right-24: User guide](guide.md)

-   :material-api:{ .lg .middle } **📚 Reference**

    ---

    Full CLI, `config.yaml`, FAQ, and “why is doctor red?” triage.
    For when things get spicy. 🌶️

    [:octicons-arrow-right-24: CLI reference](cli.md)

</div>

---

## The loop (30 seconds) 🔁

```mermaid
flowchart LR
  Q[Query 🎯] --> P[Plan + split 📋]
  P --> C[Coordinator 🧭]
  C --> W[Workers 🛠️]
  W --> R[Reviewer 🔍]
  R -->|reject 😬| X[Corrector 🔧]
  X --> R
  R -->|approve ✅| L[Learn + skills 🦋]
  L --> M[(.slmcode memory 💾)]
```

Routing stays in **Go**. Models get **tiny packs**. Memory compounds on disk.
You stay in the TUI or Studio — not in a black box labeled “trust me bro”.

---

## What “premium” means here ✨

| Promise | Reality check |
|---------|----------------|
| 👀 Live visibility | Agent, scope, patches, latency — not a spinner cult |
| 🛟 Recoverable runs | `/stop` → checkpoint → `/resume` |
| 🛡️ Safety rails | `dry-run` / `review` / shell allow·ask·deny |
| 🔌 Model-agnostic | Same harness for local SLM or cloud frontier |
| ✈️ Offline Studio | Vendored UI; cafe Wi‑Fi optional |

---

## One-liner (because of course) 🪄

=== "🍎 macOS / Linux"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

=== "🪟 Windows"

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

=== "🍺 Homebrew"

    ```bash
    brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
    ```

Then: `slmcode doctor` → [Quick start](quickstart.md). If doctor is green, you’re dangerous. 💚

---

## Pick your adventure 🎮

<div class="slm-tiles" markdown>

<div class="slm-tile" markdown>
<span class="slm-tile__emoji">🖥️</span>
<strong>Terminal vibes</strong>
<p markdown>[TUI & chat](tui.md) — board, live events, slash commands.</p>
</div>

<div class="slm-tile" markdown>
<span class="slm-tile__emoji">🎨</span>
<strong>Clicky cockpit</strong>
<p markdown>[Studio](studio.md) — kanban + memory, airplane-mode friendly.</p>
</div>

<div class="slm-tile" markdown>
<span class="slm-tile__emoji">🧪</span>
<strong>Copy-paste wins</strong>
<p markdown>[Recipes](recipes.md) — workflows that survive reality.</p>
</div>

<div class="slm-tile" markdown>
<span class="slm-tile__emoji">❓</span>
<strong>Things broke</strong>
<p markdown>[FAQ](faq.md) — the “it’s the context window” desk.</p>
</div>

</div>

<p class="slm-footer-note" markdown>
☀️ Made with ♥ by [UnicoLab](https://unicolab.ai) · Summer coding with SLMs should feel like a
**superpower**, not a compromise. Now go make `doctor` green. 💚
</p>
