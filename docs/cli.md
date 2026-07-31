# ⌨️ CLI reference

Binary name: **`slmcode`** (docs sometimes say *smlcode* — same project, same vibes). 💚

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🛠️</span>
<p class="slm-banner__text" markdown>
<strong>Power-user tip:</strong> every command has <code>--help</code>.
When in doubt, be loud with <code>-v</code> and green with <code>doctor</code>.
</p>
</div>

```bash
slmcode --help
slmcode <command> --help
```

---

## Global flags 🌐

| Flag | Env / notes |
|------|-------------|
| `--root` | Project root (default: cwd) |
| `--provider` | `SLMCODE_PROVIDER` |
| `--model` | `SLMCODE_MODEL` |
| `--endpoint` | `SLMCODE_ENDPOINT` / `OPENAI_BASE_URL` |
| `--api-key` | `SLMCODE_API_KEY` / provider-specific |
| `--backend` | `slmcode` (default) |
| `--parallel` | Max parallel workers |
| `--retries` | Review/correct retries |
| `--think-passes` | Multipass think loops |
| `--dry-run` | Don't write code files 🎭 |
| `-v` / `--verbose` | Loud agent logs 📢 |
| `--no-banner` | Hide ASCII banner on help |

---

## Commands 📚

### Core loop 🔁

| Command | Purpose |
|---------|---------|
| `slmcode` / `tui` | Premium interactive TUI (**default**) 🖥️ |
| `run` | Full pipeline or single specialist 🚀 |
| `chat` | Classic REPL 💬 |
| `studio` | GUI + HTTP/SSE (`--listen host:port`) 🎨 |
| `doctor` | Provider/model/workspace health 🩺 |
| `init` | Create `.slmcode/` scaffolding 🌱 |
| `update` | Refresh binary (release) or rebuild from source ⬆️ |
| `version` | Print version metadata |

### Board & tasks 📋

| Command | Purpose |
|---------|---------|
| `board` | Show kanban |
| `watch` | Live-refreshing kanban 👀 |
| `task` | add / show / edit / move / delegate / checklist / promote |
| `status` | Query + plan head + board counts |
| `plan` | Show `PLAN.md` |

### Memory & skills 💾

| Command | Purpose |
|---------|---------|
| `context` | Show / edit `CONTEXT.md` |
| `docs` | List / show / edit markdown memory |
| `skills` | List / show / new / edit 🦋 |
| `session` | list / show / resume 🛟 |

### Git helpers & safety 🛡️

| Command | Purpose |
|---------|---------|
| `diff` | Working tree diff |
| `commit` | `git add -A && commit` helper |
| `apply` | Apply `.slmcode/pending/` (review mode) |
| `config` | Show / set harness config ⚙️ |
| `completion` | Shell completion scripts |

---

## `run` deep dive 🚀

```bash
slmcode run -v "add JWT auth"
slmcode run --agent explorer "Where is auth handled?"
slmcode run --skill atomic-coding "Refactor helpers"
slmcode run --mode specialist --agent worker "…"
slmcode run --think-passes 2 --parallel 2 --retries 2 "…"
```

| Flag | Meaning |
|------|---------|
| `--agent` / `--mode specialist` | Single-role run |
| `--skill` | Pin a skill pack |
| `--think-passes` | Draft → critique → refine |
| `--parallel` | Concurrent ready tasks |
| `--retries` | Critic loop stubbornness |
| `--dry-run` | Simulate writes |

Query sugar: `@skill:name`, `@file:path`, `@folder:path` (when supported by instructions loader).

---

## `config` ⚙️

```bash
slmcode config                 # show
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set permission review
```

Full field list → [⚙️ Config reference](config.md).

---

## `doctor` reads as 🩺

- Active provider / model / endpoint
- Reachability
- Embedding mode (`openai` / `local` / `lexical`)
- Workspace / board / skills sanity

Green → ship. 💚 Red → [❓ FAQ](faq.md).

---

## Completions 🐚

```bash
slmcode completion zsh > "$(brew --prefix)/share/zsh/site-functions/_slmcode"
slmcode completion bash
slmcode completion fish
```

Installers may place these automatically on system installs.

---

## Exit philosophy 🚪

SLMCode prefers **visible failure** over silent “done” theater.
Check the board, the diff, and `doctor` when something smells off. 👃

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
