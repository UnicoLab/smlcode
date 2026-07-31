<p align="center">
  <img src="assets/slmcode-logo.png" alt="SLMCode" width="180" />
</p>

<h1 align="center">⚡ SLMCode</h1>

<p align="center">
  <strong>A coding harness that loves SLMs — and works with any LLM.</strong><br/>
  Plan → atomic tasks → parallel specialists → self-critic → learn<br/>
  Powered by <a href="https://github.com/piotrlaczkowski/GoLangGraph">GoLangGraph</a>
  · defaults to <strong>oMLX</strong> · plug in Ollama, OpenAI, OpenRouter, vLLM, …
</p>

<p align="center">
  <a href="https://unicolab.ai"><img alt="UnicoLab" src="https://img.shields.io/badge/Made%20with%20%E2%99%A5%20by-UnicoLab-0f6e8c?style=flat-square" /></a>
  <a href="https://unicolab.github.io/smlcode/"><img alt="docs" src="https://img.shields.io/badge/docs-MkDocs-0ea5e9?style=flat-square" /></a>
  <a href="https://github.com/UnicoLab/smlcode/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/UnicoLab/smlcode/ci.yml?branch=main&style=flat-square&label=CI" /></a>
  <a href="https://github.com/UnicoLab/smlcode/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/UnicoLab/smlcode?style=flat-square&color=2dd4bf" /></a>
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

## 🎯 Why this loop exists

| 🐘 Large-model habit | 🐭 SLMCode approach |
|----------------------|---------------------|
| Stuff the repo into chat | Incremental `.slmcode/*.md` memory |
| One free-form agent | Plan → atomic tasks → specialists |
| Re-scan every turn | Reuse CONTEXT/MEMORY; skip deep explore |
| Hope the model self-corrects | Reviewer ↔ corrector + multipass |
| Opaque progress | Live CLI + Studio stream |
| Burn tokens until it sticks | Early-exit streams, lean packs, speculative cancel |

---

## ✨ Highlights

- 🧭 Planning + atomic split sized for ~30B models (works for larger ones too)
- 🗂️ Coordinator + live kanban board
- 🧩 14 specialists (explorer, docs, architect, worker/deep, reviewer, corrector, tester, …)
- 🔁 Self-critic loop with auto-correct retries
- 🧠 Evolving CONTEXT / MEMORY / skills flywheel
- ⚡ Token-stream early-exit + SLM JSON repair
- 🖥️ Premium TUI + offline Studio GUI (`http://127.0.0.1:7420`)
- 🔐 Shell permission modes: `allow` | `ask` | `deny`

---

## 🧬 Pipeline

```text
query → skills → context → explore|reuse → [docs] [architect]
      → plan → split → coordinator → parallel execute
      → review/correct → learn → test → memory → evolve skills
```

---

## 🚀 Quick start

```bash
cd your-project
slmcode init
# edit .slmcode/PROJECT.md

slmcode                      # premium TUI
slmcode run -v "add validation to the login handler"
slmcode board
slmcode studio               # http://127.0.0.1:7420
```

Useful knobs:

```bash
slmcode run --think-passes 2 --parallel 3 --retries 2 "…"
slmcode run --agent explorer "Where is auth handled?"
slmcode run --skill atomic-coding "Refactor helpers"
slmcode config set dry_run false
```

---

## ⌨️ CLI cheat sheet

| Command | Purpose |
|---------|---------|
| `init` / `doctor` / `config` | Workspace + provider health |
| `run -v` | Full pipeline + live stream |
| `tui` / bare `slmcode` | Premium interactive TUI |
| `chat` | Classic REPL |
| `board` / `watch` | Colored kanban |
| `studio` | GUI + SSE API |
| `update` | Refresh install (binary or source) |

TUI: `/compact`, `/sessions`, `/stats`, `/permission`, `/agents`, `/stop`, `/resume`.

---

## 📚 Docs

**Site (MkDocs → GitHub Pages):** [unicolab.github.io/smlcode](https://unicolab.github.io/smlcode/)

| Page | When |
|------|------|
| [Install](docs/install.md) | One-liners, brew, Windows, uninstall |
| [Quick start](docs/quickstart.md) | First green run in ~60s |
| [Providers](docs/providers.md) | Any LLM — presets, keys, per-agent |
| [User guide](docs/guide.md) | Daily CLI / Studio workflow |
| [Studio](docs/studio.md) | GUI + HTTP/SSE API |
| [Agents](docs/agents.md) | Specialist roster |
| [Testing](docs/testing.md) | Smoke test, Studio, e2e |
| [Architecture](docs/architecture.md) | Internals |

Local preview: `make docs-serve` → http://127.0.0.1:8000

---

## 🧪 Develop

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make tidy && make lint && make test
make docs-build              # MkDocs strict build
make install-system          # build from source onto PATH
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
2. `make lint && make test`
3. Conventional commits (`feat:`, `fix:`, `docs:`, …)
4. Open a PR

---

## 📜 License

[MIT](LICENSE) — use it, remix it, ship with it.

<p align="center">
  <br/>
  Made with ♥ by <a href="https://unicolab.ai"><strong>UnicoLab</strong></a><br/>
  <sub>Summer coding with SLMs should feel like a superpower, not a compromise. ☀️</sub>
</p>
