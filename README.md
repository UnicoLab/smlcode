<p align="center">
  <img src="assets/slmcode-logo.png" alt="SLMCode" width="180" />
</p>

<h1 align="center">⚡ SLMCode</h1>

<p align="center">
  <strong>SLM-first coding harness — blazingly fast, embarrassingly parallel.</strong><br/>
  Plan → split → parallel specialists → self-critic → test → learn<br/>
  Powered by <a href="https://github.com/piotrlaczkowski/GoLangGraph">GoLangGraph</a>
  · defaults to <strong>oMLX</strong> · works with any OpenAI-compatible endpoint
</p>

<p align="center">
  <a href="https://unicolab.ai"><img alt="UnicoLab" src="https://img.shields.io/badge/Made%20with%20%E2%99%A5%20by-UnicoLab-0f6e8c?style=flat-square" /></a>
  <a href="https://github.com/UnicoLab/smlcode/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/UnicoLab/smlcode?style=flat-square&color=2dd4bf&label=v0.12.1" /></a>
  <a href="https://github.com/UnicoLab/smlcode/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/UnicoLab/smlcode/ci.yml?branch=main&style=flat-square&label=CI" /></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.23+-00ADD8?style=flat-square&logo=go&logoColor=white" />
  <img alt="license" src="https://img.shields.io/badge/license-MIT-0ea5e9?style=flat-square" />
  <img alt="platform" src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-2dd4bf?style=flat-square" />
</p>

---

## 🌅 The pitch

LLMs are incredible. Coding with them — inside a well-adapted harness — feels like magic.

And the industry noticed. **Claude Code**, **Antigravity**, **Pi**, and a growing wave of specialized coding agents were all designed around frontier models: huge context windows, strong tool-calling, and enough judgment to survive messy repos.

That is fantastic… until you run out of tokens.
And eventually, **you will**.

Then you try the same harness on an **SLM** — a 7B–30B local model — and the magic evaporates. The model wanders. JSON breaks. Context overflows. Reviewers hallucinate green lights.

**SLMCode exists to fill those gaps** — and to stay useful when you plug a bigger model back in.

It is a public baseline for reaching *the same quality of outcome* with small models (sometimes with longer passes and extra feedback loops) — motivated by a personal need to **ship with SLMs over the summer**, offline, private, and cheap.

Fork it. Break it. Point it at whatever LLM you have. Push the idea further. 🚀

---

## 📦 Install in one line

### macOS / Linux / WSL

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
```

System-wide:

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash -s -- --system
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
```

### Homebrew

```bash
brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
```

Full matrix (CMD, pin versions, uninstall): **[docs/INSTALL.md](docs/INSTALL.md)**

```bash
slmcode version
slmcode doctor
cd your-project && slmcode init && slmcode
```

---

## 🔌 Any LLM, really

SLM-first defaults. Generic harness underneath.

| You have… | Try… |
|-----------|------|
| Apple Silicon local | `provider=omlx` (default) |
| Ollama | `--provider ollama --model qwen2.5-coder:14b` |
| LM Studio / vLLM | `--provider lmstudio --endpoint http://127.0.0.1:1234/v1` |
| OpenAI / Groq / DeepSeek / Mistral | built-in presets |
| OpenRouter / corporate gateway | any name + `--endpoint` + API key |

```bash
slmcode run --provider ollama --model qwen2.5-coder:14b \
  --endpoint http://127.0.0.1:11434 "fix the flaky test"

export SLMCODE_PROVIDER=openrouter
export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
export SLMCODE_API_KEY=…
slmcode run -v "…"
```

Deep dive: **[docs/PROVIDERS.md](docs/PROVIDERS.md)**

---

## 🧬 Pipeline (16 phases · 5 groups)

```text
┌───────── Prepare ─────────┐  ┌──── Design ────┐  ┌─── Build ───┐  ┌── Verify ──┐  ┌─ Finish ─┐
│ init → skills → context   │  │ architect       │  │ coord       │  │ polish     │  │ memory   │
│   → explore → docs        │  │   → clarify     │  │   → execute │  │   → test   │  │   → done │
│                           │  │     → plan      │  │     → learn │  │            │  │          │
│  context ∥ explore ⚡     │  │       → split   │  │             │  │            │  │          │
└───────────────────────────┘  └─────────────────┘  └─────────────┘  └────────────┘  └──────────┘
```

> ⚡ = parallel phases — `context` + `explore` run concurrently; `architect` + `clarify` run concurrently

---

## ✨ Highlights

### 🚀 Engine

| Feature | Description |
|---------|-------------|
| ⚡ **6 parallel paths** | Workers, QA, self-critique, review, phases, and speculative races all run concurrently |
| 🎯 **Atomic task split** | Plan broken into file-scoped tasks sized for 7-30B SLMs |
| 🔁 **Review ↔ correct loop** | Reviewer catches issues → corrector fixes → up to N retries → escalate to human |
| 💨 **Wave fast-path** | When ALL tasks have clean QA + disk evidence, skip reviewer LLM entirely |
| 🏎️ **`fast_model`** | Dual-model routing — 8B for light agents (reviewer, planner), 30B for heavy (worker, tester) |

### 🧩 Agents (19 specialists)

| Agent | Role | Tools | 
|-------|------|-------|
| 🧭 `explorer` | Codebase deep-dive | ✅ |
| 🏗️ `architect` | Design structure & components | ❌ |
| 📋 `planner` | High-level execution plan | ❌ |
| ✂️ `splitter` | Break plan into atomic tasks | ❌ |
| 🎤 `interviewer` | Ask clarifying questions (HITL) | ❌ |
| 🛠️ `worker` | Implement scoped changes | ✅ |
| 🔨 `deep` | Multi-step complex worker | ✅ |
| 👁️ `reviewer` | Self-critic / approve | ❌ |
| 🔧 `corrector` | Fix review issues | ✅ |
| 🧪 `tester` | Verify with real shell commands | ✅ |
| 🧩 `placeholder` | Fill stubs & flag gaps | ✅ |
| 📝 `context` | Maintain CONTEXT.md | ❌ |
| 📚 `docs` | Read documentation | ✅ |
| 🧠 `memory` | Distill MEMORY.md | ❌ |
| 🎓 `learner` | Wave lessons for future packs | ❌ |
| 🗂️ `coordinator` | Manage board & task flow | ❌ |
| 🎼 `orchestrator` | High-level coordination | ❌ |
| 🚨 `escalate` | Arbitrate max-retry failures | ❌ |

> Custom agents & per-language specialists (Go, Python, React) via YAML blocks

### 🧱 Building Blocks (marketplace-ready YAML)

| Kind | Purpose | Built-in |
|------|---------|----------|
| 📦 **Pack** | Composes pipeline + quality + agents + skills | `go`, `python`, `react` |
| ⚙️ **Pipeline** | Phase graph with language-specific slots | `go`, `python`, `react` |
| 🤖 **Agent** | Custom specialist or builtin override | `go-worker`, `python-tester`, … |
| ✅ **Quality** | Lint/test/build commands per language | `go`, `python`, `react` |

```bash
slmcode blocks list                    # browse marketplace
slmcode blocks show pipeline go        # inspect Go pipeline
slmcode blocks apply go                # apply Go language pack
slmcode blocks validate                # validate custom blocks
```

> Auto-detection on `init`: detects `go.mod` / `pyproject.toml` / `package.json` and auto-applies the right pack 🎯

### 🖥️ Studio (Web GUI)

| Page | What it does |
|------|-------------|
| 🏠 **Live** | SSE-streaming pipeline progress, event log, task board, HITL popups |
| 📋 **Board** | Full kanban — add/edit/delete tasks, inject context, set agent hints |
| ⚙️ **Pipeline** | Edit phase graph, slots, execute loop config |
| 🤖 **Agents** | Create, edit, delete custom agents with full prompt editor |
| 🧱 **Blocks** | Browse & apply pipeline/agent/quality/pack blocks |
| 📁 **Files** | Full workspace tree browser with syntax highlighting & per-line comments |
| 🧩 **Skills** | Manage SKILL.md skill packs |
| 📝 **Docs** | Edit CONTEXT.md, PLAN.md, TASKS.md, SCRATCH.md |
| ⚡ **Settings** | Provider, model, stacks, HITL modes, parallel config |

```
slmcode studio                    → http://127.0.0.1:7420 (auto-opens browser)
slmcode studio --kill             → force-kill existing + restart
slmcode studio --port-auto        → auto-switch if port is busy
```

### 👤 Human-in-the-Loop (HITL)

| Gate | Default | What it does |
|------|---------|-------------|
| 🎤 **Clarify** | `auto` | Interview agent asks about language/stack/framework |
| ✅ **Plan approve** | `auto` | Human reviews plan before workers execute |
| 🔄 **Continue** | `ask` | Ask when retries exhausted — another wave or stop? |
| 🚨 **Escalate** | `ask` | Task hit max retries — retry / re-scope / abort? |
| 🐚 **Shell** | `allow` | Approve shell commands before execution |

```yaml
# .slmcode/config.yaml — all configurable per-project
plan_approve: ask       # off | auto | ask
clarify_mode: ask       # off | auto | ask
auto_approve: false     # false = respect per-gate settings
```

### ⚙️ Config highlights

```yaml
# Speed & parallelism
max_parallel: 4           # concurrent tasks per wave
fast_model: "LFM2.5-8B"   # smaller model for light agents (3-4x faster!)
think_passes: 1           # 2+ enables speculative digs

# Quality gates
qa_gate: true             # iterate test/smoke until green
qa_gate_max_rounds: 1     # rounds before escalate
post_worker_smoke: true   # go vet / pytest after each worker

# Guardrails
write_guard: true         # prevent writes outside focus files
read_before_edit: true    # force ws_read before ws_edit
claims_gate: true         # reject hallucinated file paths
static_quality: true      # reject stub/placeholder code
```

---

## 🎯 Why this loop exists

| 🐘 Large-model habit | 🐭 SLMCode approach |
|----------------------|---------------------|
| Stuff the repo into chat | Incremental `.slmcode/*.md` memory |
| One free-form agent | Plan → atomic tasks → 19 specialists |
| Re-scan every turn | Reuse CONTEXT/MEMORY; skip deep explore |
| Hope the model self-corrects | Reviewer ↔ corrector + multipass |
| Opaque progress | Live CLI + Studio SSE stream |
| Burn tokens until it sticks | Early-exit streams, lean packs, speculative cancel |

---

## 🚀 Quick start

```bash
cd your-project
slmcode init                         # auto-detects language & applies pack
# edit .slmcode/PROJECT.md

slmcode                              # premium TUI
slmcode run -v "add JWT validation"
slmcode board                        # live kanban
slmcode studio                       # http://127.0.0.1:7420
```

Useful knobs:

```bash
slmcode stack list
slmcode stack apply deepseek         # switch to DeepSeek
slmcode config set fast_model LFM2.5-8B-A1B-MLX-4bit   # speed boost!
slmcode run --parallel 6 --think-passes 2 "refactor auth"
slmcode config set plan_approve ask  # require human plan approval
slmcode blocks apply python          # apply Python language pack
```

---

## ⌨️ CLI cheat sheet

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + provider health |
| `stack list` / `stack apply` | Model presets |
| `agent list` / `agent show` | Inspect agent specialists |
| `blocks list` / `blocks apply` | Browse & apply building blocks |
| `skills list` / `skills new` | Manage skill packs |
| `run -v` | Full pipeline + live stream |
| `tui` / bare `slmcode` | Premium interactive TUI |
| `chat` | Classic REPL |
| `board` / `watch` | Colored kanban |
| `studio` / `studio --kill` | Web GUI + SSE API |
| `diff` / `commit` | Git integration |
| `update` | Refresh install |

TUI: `/compact`, `/models`, `/mcp`, `/auth`, `/schema`, `/sessions`, `/stats`, `/permission`, `/agents`, `/stop`, `/resume`.

---

## 📊 Performance

| Feature | Capability |
|---------|-----------|
| ⚡ **Parallel execution** | 6 concurrent paths: workers, QA, critique, review, phases, speculative races |
| 🏎️ **Dual-model** | `fast_model` routes light agents (reviewer, planner) to smaller/faster LLM |
| 💨 **Wave fast-path** | Tasks with clean QA + disk evidence skip reviewer LLM entirely |
| 🔄 **QA gate** | Single-round gate, auto-fixes gofmt/ruff, skips when no test files |
| 🧪 **Smart smoke** | Uses `go vet` (instant) when no `*_test.go` files exist |
| 📦 **Auto-pack** | Detects `go.mod` / `pyproject.toml` / `package.json` on `init` |

---

## 📚 Docs

**Premium + playful site (MkDocs Material → GitHub Pages):**
☀️ [unicolab.github.io/smlcode](https://unicolab.github.io/smlcode/)

| Section | Pages |
|---------|--------|
| 🚀 Getting started | 📦 Install · ⏱️ Quick start · 🧠 Concepts · 🔌 Providers |
| 📘 Handbook | 🧭 Guide · 🖥️ TUI · 🦋 Skills · 🎨 Studio · 🧩 Agents · 🧪 Recipes |
| 📚 Reference | ⌨️ CLI · ⚙️ Config · ✅ Testing · ❓ FAQ |
| 🔧 Internals | 🏗️ Architecture · 🤝 Contributing · 📋 AGENTS.md (for AI agents) |

Local preview: `make docs-serve` → http://127.0.0.1:8000 — bring snacks. 🍿

---

## 🧪 Develop

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make ui-react                # build Vite/React Studio UI first
make tidy && make lint && make test
make docs-build              # MkDocs strict build
make install-system          # build from source onto PATH
```

The Studio UI is a **Vite + React + TypeScript** SPA in `web/`. Build it with `make ui-react` (runs `npm run build`, syncs to `cmd/slmcode/ui/`). The `cmd/slmcode/ui/` output is embedded via `go:embed` at compile time. For UI development:

```bash
cd web && npm install && npm run dev    # Vite dev server with HMR
```

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

---

## 🤝 Contributing

Public baseline on purpose. Bring better prompts, tighter gates, smarter scheduling,
new specialists, and evals — especially ones that make **small models** more reliable.

1. Fork & branch
2. `make ui-react && make lint && make test`
3. Conventional commits (`feat:`, `fix:`, `docs:`, …)
4. Open a PR

AI agents: read **[AGENTS.md](AGENTS.md)** for complete architecture, conventions, and contribution guide.

---

## 📜 License

[MIT](LICENSE) — use it, remix it, ship with it.

<p align="center">
  <br/>
  Made with ♥ by <a href="https://unicolab.ai"><strong>UnicoLab</strong></a><br/>
  <sub>Summer coding with SLMs should feel like a superpower, not a compromise. ☀️</sub>
</p>
