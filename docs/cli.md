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
| `readiness` / `ready` | Score local SLM readiness; `--fix` applies safe defaults |
| `compose` | Preview the dynamic pipeline phases/agents without an LLM call |
| `init` | Create `.slmcode/` scaffolding 🌱 |
| `stack` | list / show / apply provider+model presets 📦 |
| `agent` | list / show / set per-agent LLM pins 🧩 |
| `blocks` | list / show / apply / validate building blocks 🧱 |
| `update` | Refresh binary (release) or rebuild from source ⬆️ |
| `version` | Print version metadata |

### Board & tasks 📋

| Command | Purpose |
|---------|---------|
| `board` | Show kanban |
| `watch` | Live-refreshing kanban 👀 |
| `task` | add / show / edit / move / delegate / checklist / promote |
| `status` | Query, dynamic pipeline state, plan approval gate, board counts |
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
slmcode run --dynamic "add JWT auth"      # force task-specific composition
slmcode run --no-dynamic "tiny typo fix"  # use the static pipeline
slmcode run --think-passes 2 --parallel 2 --retries 2 "…"
```

| Flag | Meaning |
|------|---------|
| `--agent` / `--mode specialist` | Single-role run |
| `--skill` | Pin a skill pack |
| `--dynamic` / `--no-dynamic` | Override `dynamic_pipeline` for this run |
| `--think-passes` | Draft → critique → refine |
| `--parallel` | Concurrent ready tasks |
| `--retries` | Critic loop stubbornness |
| `--dry-run` | Simulate writes |

Query sugar: `@skill:name`, `@file:path`, `@folder:path` (when supported by instructions loader).

## Dynamic pipeline & readiness 🎯

```bash
slmcode compose "add JWT auth"       # inspect phases, team, execute loop, SLM fit
slmcode compose --json "add JWT auth"
slmcode status                       # includes dynamic + latest composition + plan gate
slmcode readiness                    # checks provider/model and SLM-safe settings
slmcode readiness --fix              # enables safe local-model defaults where needed
```

`dynamic_pipeline` defaults on: the composer selects a task-specific subset of phases,
agents, slots, and execute-loop roles before workers run. `compose` is deterministic
inspection only; it does not call the LLM or write code. To force the static configured
pipeline, use `slmcode run --no-dynamic` or `slmcode config set dynamic_pipeline false`.

Plan approval is controlled by `plan_approve` (`off | auto | ask`) and `auto_approve`.
When a run is paused before execute, `slmcode status` reports the pending plan gate id,
task count, and timeout so you can approve in Studio or through the plan approval API.

`readiness` is the local SLM preflight: it scores provider reachability, model
availability, skills, dynamic pipeline, HITL, and other safety defaults. It exits
non-zero when required checks fail; `--fix` applies the safe config patch it recommends.

---

## `stack` & `agent` 📦

```bash
slmcode stack list
slmcode stack show deepseek
slmcode stack apply omlx-local
slmcode stack apply deepseek --clear-agent-llm   # agents inherit stack LLM
slmcode stack apply openai --agents              # also write optional role pins

slmcode agent list
slmcode agent show worker
slmcode agent set worker --model … --provider …  # pin; empty = inherit stack
```

Stacks live in `stacks/*.yaml`. DeepSeek default endpoint: `https://api.deepseek.com`
(OpenAI-compat client appends `/v1`). Details → [🔌 Providers](providers.md).

## `blocks` 🧱

```bash
# List all building blocks, grouped by kind
slmcode blocks list

# Show details of a specific block
slmcode blocks show pipeline go
slmcode blocks show agent python-worker
slmcode blocks show pack react

# Apply a language pack (writes pipeline.yaml + config)
slmcode blocks apply go
slmcode blocks apply python --materialize-agents
slmcode blocks apply react --force

# Validate all block YAML configs
slmcode blocks validate
```

Blocks are marketplace-ready YAML presets: pipelines, agents, quality packs, and language packs.
Three predefined language packs ship built-in: 🐹 Go, 🐍 Python, ⚛️ React/TypeScript.
Custom blocks go in `.slmcode/blocks/`. Details → [🧱 Blocks](blocks.md).

## `config` ⚙️

```bash
slmcode config                 # show
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set permission review
```

Prefer `.slmcode/auth.json` (or env) for keys — not committed YAML.
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
