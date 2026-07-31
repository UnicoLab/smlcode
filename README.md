<p align="center">
  <img src="assets/slmcode-logo.png" alt="SLMCode" width="180" />
</p>

<h1 align="center">⚡ SLMCode</h1>

<p align="center">
  <strong>The coding harness built for small local models — not cloud giants.</strong><br/>
  Plan → atomic tasks → parallel specialists → self-critic → learn<br/>
  Powered by <a href="https://github.com/piotrlaczkowski/GoLangGraph">GoLangGraph</a> · defaults to <strong>oMLX</strong> on Apple Silicon
</p>

<p align="center">
  <a href="https://github.com/UnicoLab/smlcode/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/UnicoLab/smlcode/ci.yml?branch=main&style=flat-square&label=CI" /></a>
  <img alt="version" src="https://img.shields.io/badge/version-0.5.16-0f6e8c?style=flat-square" />
  <img alt="go" src="https://img.shields.io/badge/go-1.23+-00ADD8?style=flat-square&logo=go&logoColor=white" />
  <img alt="license" src="https://img.shields.io/badge/license-MIT-0ea5e9?style=flat-square" />
  <img alt="platform" src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-2dd4bf?style=flat-square" />
</p>

---

## 🌅 The pitch

LLMs are incredible. Coding with them — inside a well-adapted harness — feels like magic.

And the industry noticed. **Claude Code**, **Antigravity**, **Pi**, and a growing wave of specialized coding agents were all designed around frontier models: huge context windows, strong tool-calling, and enough “judgment” to survive messy repos.

That is fantastic… until you run out of tokens.
And eventually, **you will**.

Then you try the same harness on an **SLM** — a 7B–30B local model — and the magic evaporates. The model wanders. JSON breaks. Context overflows. Reviewers hallucinate green lights. The loop that made the big models shine becomes a liability for the small ones.

**SLMCode exists to fill those gaps.**

It is a public baseline for reaching *the same quality of outcome* with small models — sometimes with longer passes, tighter scopes, and extra feedback loops — motivated by a very personal need: **actually shipping work with SLMs over the summer**, offline, private, and cheap.

If you believe local models deserve a first-class coding loop — not a watered-down clone of a cloud agent — this repo is for you. Fork it. Break it. Push the idea further. 🚀

---

## 🎯 Why SLMs need a different loop

Cloud coding agents assume a frontier model that can swallow a whole repo.
Small local models need structure, memory, and ruthless focus.

| 🐘 Large-model habit | 🐭 SLMCode approach |
|----------------------|---------------------|
| Stuff the repo into chat | Incremental `.slmcode/*.md` memory |
| One free-form agent | Plan → atomic tasks → specialists |
| Re-scan every turn | Reuse CONTEXT/MEMORY; skip deep explore |
| Hope the model self-corrects | Reviewer ↔ corrector + multipass |
| Opaque progress | Live CLI + Studio stream (agent / scope / output) |
| Burn tokens until it sticks | Early-exit streams, lean packs, speculative cancel |

---

## ✨ What you get

- 🧭 **Planning + atomic split** sized for ~30B models
- 🗂️ **Coordinator** supervising a live custom kanban board
- 🧩 **14 specialists** — explorer, docs, architect, worker/deep, reviewer, corrector, tester, memory, and more
- 🔁 **Self-critic loop** with auto-correct retries
- 🧠 **Shared evolving context** — later agents inherit CONTEXT / MEMORY / skills
- 🦋 **Skills flywheel** — auto-updated `SKILLS.md` + `skills/learned/`
- ⚡ **Token-stream early-exit** — cancel wasted decode once JSON / tool-call args are complete
- 🛠️ **SLM JSON repair** — trailing commas, single quotes, truncated braces, KV fallbacks
- 📊 **Phase latency telemetry** — plan/split/execute/worker timings in logs + TUI `/stats`
- 🔐 **Shell permission modes** — `allow` | `ask` | `deny` for `ws_shell`
- 🖥️ **Premium TUI** — `/compact`, `/sessions`, `/permission`, `/stats`, agent CRUD, stop/resume
- 🎨 **Offline Studio GUI** (vendored React) at `http://127.0.0.1:7420`
- 🔌 **Any OpenAI-compatible endpoint** — oMLX, Ollama, LM Studio, vLLM, OpenRouter, …

---

## 🧬 Pipeline

```text
query → skills → context → explore|reuse → [docs] [architect]
      → plan → split → coordinator → parallel execute
      → review/correct → learn → test → memory → evolve skills
```

More passes. More evidence. Same ambition as the big harnesses — tuned for models that think smaller.

---

## 📦 Install

```bash
# From this repo
make install-system          # → Homebrew /usr/local (or /opt/homebrew)
# or user-local:
make install                 # → ~/.local/bin/slmcode

omlx start
slmcode doctor
slmcode version
```

Then from **any** project:

```bash
cd ~/any-repo
slmcode                      # premium TUI (default; also: slmcode tui)
slmcode init
slmcode run -v "fix the bug"
slmcode chat                 # classic REPL
slmcode studio               # http://127.0.0.1:7420
```

> Docs sometimes spell the project **smlcode** — the installed binary is **`slmcode`**.

### Update

```bash
slmcode update               # rebuild from the checkout recorded at install
slmcode update --check       # dry compare installed vs source
make update                  # from the repo (= install-system)
```

Uninstall: `make uninstall-system` or `./scripts/install.sh --uninstall --system`

---

## 🚀 Quick start

```bash
cd your-project
slmcode init
# edit .slmcode/PROJECT.md

slmcode run -v "add validation to the login handler"
slmcode board
slmcode watch
slmcode studio
```

Useful flags:

```bash
slmcode run --think-passes 2 --parallel 3 --retries 2 "…"
slmcode run --agent explorer "Where is auth handled?"
slmcode run --skill atomic-coding "Refactor helpers"
slmcode config set dry_run false
```

### Provider & model (any OpenAI-compatible endpoint)

Defaults target local oMLX, but nothing is hard-wired. Switch freely via flags, env, config, or Studio Settings:

```bash
# Ollama
slmcode run --provider ollama --model qwen2.5-coder:14b \
  --endpoint http://127.0.0.1:11434 "…"

# LM Studio / vLLM / any OpenAI-compat gateway
slmcode run --provider lmstudio --model local-coder \
  --endpoint http://127.0.0.1:1234/v1 "…"

# Env overrides (applied on every command)
export SLMCODE_PROVIDER=ollama
export SLMCODE_MODEL=qwen2.5-coder:14b
export SLMCODE_ENDPOINT=http://127.0.0.1:11434

# Persist in the project
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set endpoint http://127.0.0.1:11434
slmcode doctor               # shows active provider + model + reachability
```

`provider` may be any name: known presets (`omlx`, `ollama`, `openai`, `lmstudio`, `openrouter`, `vllm`, `litellm`, `together`, `groq`, `deepseek`, …) or a custom id.

---

## ⌨️ CLI

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + active provider/model health |
| `run -v` | Full pipeline with live agent stream + latency |
| `tui` / bare `slmcode` | Premium interactive TUI (default) |
| `chat` | Classic REPL |
| `board` / `watch` | Colored kanban |
| `task …` | add / show / edit / move / delegate / check / promote |
| `context` / `docs` / `plan` / `skills` | Markdown memory |
| `studio` | GUI + SSE API |
| `update` | Rebuild & reinstall from source |

TUI slash commands worth knowing: `/compact`, `/sessions`, `/stats`, `/permission`, `/agents`, `/stop`, `/resume`.

Mid-run **Ctrl+C** or `/stop` checkpoints the board plus ReAct message history under `.slmcode/queries/<id>/react/`; `/resume` continues from that history when present (not a cold replan).

---

## 🎨 Studio

| Panel | Purpose |
|-------|---------|
| Query bar | Start / stop runs |
| Pipeline strip | Live phases including coord / learn |
| Live tab | Current **@agent**, **scope**, **file patches**, **output** |
| Kanban | Drag-and-drop; edit mid-run |
| Docs | Markdown edit / preview / split |
| Settings | Provider + model + endpoint, QA gate, think passes |

---

## 🧪 Develop & test

```bash
make tidy && make lint && make test   # format check + vet + unit tests + Studio JS smoke
make e2e                              # API/UI interaction + isolated board sandbox
make install / make install-system
RUN_E2E=1 make e2e                    # also live multi-agent + oMLX pipeline
```

Pre-commit hooks (optional, recommended):

```bash
pip install pre-commit   # or: brew install pre-commit
pre-commit install
pre-commit run --all-files
```

Engine notes (0.5.16+): GoLangGraph `v0.2.1` tiktoken (`cl100k_base`) usage estimates; speculative cancel for reviewer/tester races; optional `price_preset=local|openai|anthropic|…` (or `price_*_per_mtok`) — TUI shows tokens-only until configured.

Dependency: `github.com/piotrlaczkowski/GoLangGraph@v0.2.1` (set `GOPRIVATE=github.com/piotrlaczkowski/*` if the module proxy rejects the capital path).

---

## 🧱 Embed

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

---

## 📚 Docs

| Doc | When to read |
|-----|----------------|
| **[TESTING](docs/TESTING.md)** | Start here — smoke test, Studio, chat, e2e |
| **[GUIDE](docs/GUIDE.md)** | Daily CLI / Studio workflow |
| **[STUDIO](docs/STUDIO.md)** | GUI panels + HTTP/SSE API |
| **[AGENTS](docs/AGENTS.md)** | Specialist roster & coordinator actions |
| **[ARCHITECTURE](docs/ARCHITECTURE.md)** | Internals, streaming, knowledge flywheel |

Full index: [docs/README.md](docs/README.md)

---

## 🤝 Contributing

This is intentionally a **public baseline**. Bring better prompts, tighter gates, smarter scheduling, new specialists, and SLM-specific evaluation. PRs that make small models more reliable are especially welcome.

1. Fork & branch
2. `make lint && make test`
3. Keep commit messages conventional and human (`feat:`, `fix:`, `docs:`, …)
4. Open a PR

---

## 📜 License

[MIT](LICENSE) — use it, remix it, ship with it.

Built because summer coding with SLMs should feel like a superpower, not a compromise. ☀️
