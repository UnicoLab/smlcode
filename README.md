<p align="center">
  <img src="assets/slmcode-logo.png" alt="SLMCode" width="180" />
</p>

<h1 align="center">⚡ SLMCode</h1>

<p align="center">
  <strong>A coding harness built for small local models.</strong><br/>
  Constrained decoding · a real tool interface · memory that compounds · terminal + web UI<br/>
  Defaults to <strong>oMLX</strong> · works with any OpenAI-compatible endpoint
</p>

<p align="center">
  <a href="https://unicolab.ai"><img alt="UnicoLab" src="https://img.shields.io/badge/Made%20with%20%E2%99%A5%20by-UnicoLab-0f6e8c?style=flat-square" /></a>
  <a href="https://github.com/UnicoLab/smlcode/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/UnicoLab/smlcode?style=flat-square&color=2dd4bf" /></a>
  <a href="https://github.com/UnicoLab/smlcode/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/UnicoLab/smlcode/ci.yml?branch=main&style=flat-square&label=CI" /></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.23+-00ADD8?style=flat-square&logo=go&logoColor=white" />
  <img alt="license" src="https://img.shields.io/badge/license-MIT-0ea5e9?style=flat-square" />
</p>

---

## 🌅 The pitch

LLMs are incredible. Coding with them — inside a well-adapted harness — feels like magic.

And the industry noticed. **Claude Code**, **Antigravity**, **Pi**, and a growing wave of
specialized coding agents were all designed around frontier models: huge context windows,
strong tool-calling, and enough judgment to survive messy repos.

That is fantastic… until you run out of tokens.
And eventually, **you will**.

Then you try the same harness on an **SLM** — a 7B–32B local model — and the magic evaporates.
The model wanders. JSON breaks. Context overflows. Reviewers hallucinate green lights.

**SLMCode exists to fill those gaps** — and to stay useful when you plug a bigger model back in.

Fork it. Break it. Point it at whatever LLM you have. 🚀

---

## What makes it different

| | |
|---|---|
| 🔒 **Constrained decoding, negotiated per endpoint** | Every structured role has a hand-written JSON Schema and a generated GBNF grammar. At startup the harness probes your endpoint and picks the strongest mechanism it actually supports — `json_schema` → vLLM `guided_json` → llama.cpp `grammar` → `json_object` → prompt-only — caching the result and silently demoting if the server changes its mind. |
| 🧰 **A tool interface designed for small models** | `ws_edit` tries five progressively more tolerant match strategies and only ever applies a **unique** match, telling the model which rung hit. `ws_patch` anchors each hunk on its `@@` line numbers within ±20 lines and reports per-hunk status. An edit that breaks a file that previously parsed is **reverted**, in-band, on the same turn. |
| 📐 **Context budgeted in tokens, not bytes** | The pack budget is derived from the model's real context window minus system prompt, tool schemas and response reserve. Assembly is byte-deterministic with a stable prefix so local KV-cache prefixes actually hit. A tree-sitter-free repo map ranks files by PageRank over a symbol reference graph. |
| 🧠 **It gets better at your repo** | Four memory layers, failure fingerprinting, and a repair-rule store: a given failure mode costs an LLM round-trip **once**. A Thompson-sampling bandit learns which harness settings work for your model family and language. All of it is plain JSON under `.slmcode/`, and deleting it is supported. |
| 🛡️ **Gates that fail closed** | Disk state is authoritative — a claimed edit that is not on disk does not pass. Truncated reviewer JSON fails closed. The QA gate cannot report green when tests fail. A HITL gate with a human attached blocks instead of expiring into an auto-approval. |
| 🖥️ **Two front ends** | A non-blocking terminal REPL (Esc to interrupt and redirect mid-run, `/` fuzzy command picker, real unified diffs, interactive `slmcode apply`) and **Studio**, an offline React SPA with a live SSE feed, a pending-change review UI and run traces. |

---

## ⏱️ 60-second start

### Install

```bash
# macOS / Linux / WSL
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash

# Windows (PowerShell)
irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex

# Homebrew
brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
```

Full matrix (CMD, pinned versions, uninstall): **[docs/install.md](docs/install.md)**

**Locked-down work machine?** If `brew`, `go` and release downloads all 403 on you, clone and
install from the binaries carried in the repo — no Homebrew, no Go, no downloads:

```bash
git clone --depth 1 https://github.com/UnicoLab/smlcode.git
cd smlcode && ./scripts/install-offline.sh --add-to-path
```

Details: **[docs/install-offline.md](docs/install-offline.md)**

### Or from a fresh clone

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make bootstrap          # needs Node 18+: installs web/ deps and builds the Studio UI in
make install-user       # → ~/.local/bin/slmcode
```

`make bootstrap` is the one step that needs Node. It installs `web/`'s npm dependencies and runs
the Vite build into `cmd/slmcode/ui/`, which is `go:embed`ed into the binary. Note that
`web/package-lock.json` is currently out of date with `web/package.json`, so `npm ci` cannot run;
`make bootstrap` says so and falls back to `npm install`, which regenerates the lock — **commit
the regenerated `web/package-lock.json`**. Full story: [CONTRIBUTING.md](CONTRIBUTING.md#build).

No Node? `go build ./cmd/slmcode` works on its own — everything except the Studio SPA. The binary
then serves a built-in placeholder page that tells you to run `make bootstrap`, and `slmcode
studio` says the same on startup. The CLI, the TUI and the Studio API are unaffected.

### Run it

```bash
cd your-project
slmcode init                          # scaffolds .slmcode/ (memory, board, config)
                                      # detects your language and applies the matching pack
slmcode doctor                        # provider, model, endpoint, workspace — run it if init
                                      # reported that nothing answered at your endpoint
slmcode run -v "add JWT validation"   # full pipeline, live stream
slmcode                               # or: interactive TUI
slmcode studio                        # or: web UI — open the tokenised URL it prints
```

`init` is first on purpose: every other command answers from built-in defaults until a workspace
exists, and says so. `slmcode run` on a terminal pauses at the plan gate for a single keystroke;
headless it stops with exit **6** and prints the flag that lets it run unattended.

---

## 🔌 Any OpenAI-compatible endpoint

| You have… | Use |
|---|---|
| Apple Silicon, local | `provider=omlx` (default, `http://127.0.0.1:8000/v1`) |
| Ollama | `--provider ollama --model qwen2.5-coder:14b` |
| LM Studio / vLLM / llama.cpp | `--provider lmstudio --endpoint http://127.0.0.1:1234/v1` |
| OpenAI / Groq / DeepSeek / Google / Mistral / Together / Fireworks | built-in endpoint presets; `slmcode stack list` for shipped stacks |
| OpenRouter / a corporate gateway | any provider name + `--endpoint` + API key |

```bash
slmcode run --provider ollama --model qwen2.5-coder:14b \
  --endpoint http://127.0.0.1:11434 "fix the flaky test"

export SLMCODE_PROVIDER=openrouter SLMCODE_MODEL=… SLMCODE_API_KEY=…
slmcode run -v "…"

slmcode stack list && slmcode stack apply deepseek
```

Capability negotiation means a llama.cpp server gets GBNF grammars, vLLM gets `guided_json`,
OpenAI gets strict `json_schema`, and a bare endpoint falls back to prompt-only JSON plus the
repair ladder. Details: **[docs/decoding.md](docs/decoding.md)** · **[docs/providers.md](docs/providers.md)**

---

## 🧬 The pipeline

16 phases in 5 groups. `context`+`explore` and `architect`+`clarify` run concurrently.

```text
┌────────── Prepare ──────────┐ ┌───── Design ─────┐ ┌─── Build ───┐ ┌── Verify ──┐ ┌─ Finish ─┐
│ init → skills → context     │ │ architect        │ │ coord       │ │ polish     │ │ memory   │
│   → explore → docs          │ │   → clarify      │ │   → execute │ │   → test   │ │   → done │
│                             │ │     → plan       │ │     → learn │ │            │ │          │
│   context ∥ explore ⚡      │ │       → split    │ │             │ │            │ │          │
└─────────────────────────────┘ └──────────────────┘ └─────────────┘ └────────────┘ └──────────┘
```

The `dynamic_pipeline` composer (on by default) selects a task-specific subset before workers
run. `slmcode compose "…"` previews that selection without calling the LLM.

---

## ✨ Features

### Tool layer (ACI)

| Tool | Notes |
|---|---|
| `ws_read` | 120-line window with `offset`/`limit`; reports total line count; line-number gutter is display-only |
| `ws_edit` | 5-strategy match ladder (exact → trailing-whitespace → indentation-normalized → blank-line-insensitive → first/last-line anchored); unique matches only; empty `old_str` refused |
| `ws_patch` | Unified diff (`@@` anchored, ±20 lines, per-hunk report, all-or-nothing) or `SEARCH`/`REPLACE` blocks |
| `ws_write` | New files; overwriting an existing file requires a prior read; catastrophic-shrink guard |
| `ws_grep` | Real RE2 regex, falls back to literal substring and says so |
| `ws_glob`, `ws_list`, `ws_mv`, `ws_delete` | Path tools; `**` globs; `git mv` when available |
| `ws_shell` | One command, 2-minute default timeout, process-group kill, bounded output buffer |
| `ws_todo` | Short checklist echoed back, so the plan stays in recent context |
| `ws_skill` | Pull a skill's full body on demand (progressive disclosure) |
| `git_status`, `git_diff` | Read-only git |

Every result is hard-capped with steering text on truncation. Post-edit syntax checks run for
Go, Python, JavaScript and JSON and return **in-band**. Full reference with failure messages:
**[docs/tools.md](docs/tools.md)**

### Specialists

20 built-in roles: `coordinator`, `orchestrator`, `context`, `explorer`, `docs`, `architect`,
`planner`, `splitter`, `worker`, `deep`, `reviewer`, `reviewer-strict`, `corrector`, `tester`,
`placeholder`, `escalate`, `memory`, `composer`, `describer`, `editor`.

`describer`/`editor` is the architect/editor split: the describer reasons in prose with no
tools and no format constraints, the editor only formats, with constrained decoding and tools.
Their models are independently selectable, so a 32B can reason and a 7B can apply.
Custom and per-language specialists come from YAML blocks. → **[docs/agents.md](docs/agents.md)**

### Building blocks

| Kind | Purpose | Built-in |
|---|---|---|
| **Pack** | Composes pipeline + quality + agents + skills | 13 |
| **Pipeline** | Phase graph with language-specific slots | 13 |
| **Agent** | Custom specialist or built-in override | 35 (`go-worker`, `ts-reviewer`, `kotlin-tester`, …) |
| **Quality** | Lint/test/build commands per language | 13 |

The thirteen packs: `go`, `python`, `react`, `typescript`, `web`, `rust`, `java`, `kotlin`,
`dotnet`, `ruby`, `php`, `swift`, `cpp`. Also shipped: 29 skills and 13 provider stacks.

`slmcode init` picks the pack for you. Detection is scored, not first-match: a marker file in the
root counts, a `detect.contains` proof of the file's *content* counts more, stray source files
count least, and a nested sub-project's files do not count at all — so a Go module with a Vite
app in `web/` stays Go, and a `package.json` is `react` or `typescript` depending on whether it
actually declares React. Apply one explicitly with `slmcode blocks apply <pack>`.
→ **[docs/blocks.md](docs/blocks.md)**

### Safety model

| Knob | Default | Effect |
|---|---|---|
| `permission` | `auto` | `auto` writes · `dry-run` never writes · `review` stages diffs to `.slmcode/pending/` |
| `shell_permission` | `allow` | `allow` · `ask` (records, does not execute) · `deny` |
| `shell_whitelist` | `true` | Read-only and build/test commands auto-run; **interpreters and file mutators are refused** unless allowlisted |
| `write_guard`, `read_before_edit`, `claims_gate`, `static_quality`, `over_edit_guard` | `true` | Scope, evidence and stub guards |

The whitelist is tiered: `ls`/`cat`/`grep`/`go test`/`pytest`/`npm test` run; `python`, `node`,
`make`, `npx`, `sh`, `awk` (executors) and `sed`, `cp`, `mv`, `rm`, `tee` (mutators) are refused
with an explanation and a suggested allowed equivalent — because a shell that can run anything
makes every other guard decorative. Allowlist them with `shell_allow` or `SLMCODE_BASH_ALLOW`.
Flags that smuggle a second program past the list (`env python -c`, `find -exec`, `go test -exec`,
`cmake -P`, `go generate`, `pytest -p`) are refused per binary.
→ **[docs/permissions.md](docs/permissions.md)**

#### Residual risk — what is *not* enforced

The guards above are real, and they are not a sandbox. Two things remain true after every one of
them, by design rather than by oversight:

- **`ws_shell` is a command allowlist, not a filesystem jail.** The `ws_*` file tools are jailed
  to the project root; the shell is not. It decides which *command* may run, not which files that
  command may touch — an allowed `cat`, `grep` or `find` reads anything the user account can read
  (`~/.ssh/id_rsa`, `~/.aws/credentials`, another project's `.env`) and the contents go to the
  model, and therefore to whatever endpoint you configured. The write side is narrow (`mkdir` and
  `touch` are refused outside the root, mutators are refused entirely, redirection onto an
  existing file is refused), so the honest description is **read exfiltration, not out-of-tree
  modification** — but it is real. What the harness *does* enforce here is narrower and worth
  knowing: every tool result is scrubbed of the credential values it knows about (configured
  keys, `.slmcode/auth.json`, provider env vars), so those specific values do not reach the
  model even via `cat`. Any other secret in reach of the account does.
- **Verifying a project runs the project's own code.** `npm test` executes `package.json`
  scripts, `pytest` imports `conftest.py` before a single test runs, `go build` honours `#cgo`,
  `cargo build` compiles and runs `build.rs`, `./gradlew` runs a script committed to the repo.
  **Pointing slmcode at an untrusted repository is equivalent to running that repository's
  build.** If you would not run `npm install && npm test` in that checkout by hand, do not point
  an agent at it either.

What is *not* on that list, because it is closed: a repository cannot make `slmcode run` execute
code of its own choosing before the model says anything. `.slmcode/hooks.json` fails closed — it
needs `hooks_enabled: true` **and** an explicit per-content approval (`slmcode hooks trust`,
recorded in your user config, never in the repo) — and `mcp_servers` is honoured only from your
user config layer, because each entry is spawned as a child process at startup. Both refusals name
the exact command that did not run.

These are inherent to what the tool does; no addition to the allowlist removes them. The
enforcing boundary, if you need one, is the operating system's: run slmcode as a user that can
only reach the project (container, VM, dedicated account), or set `shell_permission: ask` to
approve each command, or `shell_permission: deny` to keep only the jailed `ws_*` tools.
Full detail, including every refused flag and why: **[docs/permissions.md](docs/permissions.md)**

### Human-in-the-loop

| Gate | Default | Asks about |
|---|---|---|
| `clarify_mode` | `ask` | language / stack / framework before planning |
| `plan_approve` | `ask` | the plan, before any worker runs |
| `continue_ask` | `ask` | another wave or stop, when retries are exhausted |
| `escalate_ask` | `ask` | retry / re-scope / abort for a task at max retries |
| `shell_permission` | `allow` | shell commands, in `ask` mode |

With a TTY attached these render inline and **block** until answered. Headless, the decision is
taken at **run start, before the first model call**, and logged: with `--on-gate-timeout` unset
the four convenience gates answer themselves with "yes", while an explicit `stop`/`reject`
refuses the run at the door (exit **6**) instead of planning for minutes and discarding the
result. `shell_permission=ask` is a safety gate and never auto-approves — headless it refuses up
front. A run that does stop names the retained `.slmcode/queries/<runID>/` board and the
`slmcode session resume <runID>` command.

### Studio

`slmcode studio` → **the URL it prints**, `http://127.0.0.1:7420/?t=<token>`. Live SSE feed with
resumable event ids, kanban board, pending-change review with per-file diffs and apply/reject,
run traces, pipeline and agent editors, file inspector, skills, markdown memory, settings.

Loopback-only, same-origin enforced, no permissive CORS, and a per-launch session token that
guards **everything, the HTML shell included** — a bare `http://127.0.0.1:7420/` gets a 401 page
telling you to go back to the terminal. Presenting the token once mints an HttpOnly,
`SameSite=Strict` cookie, so it stops travelling in URLs. Being honest about what that buys: the
token is printed to your stdout and lives in the server process, so it bounds other origins and
local listeners that are **not you** — it is not a sandbox against something already running as
your user. `--no-auth` drops it entirely. → **[docs/studio.md](docs/studio.md)**

### Self-improvement

`.slmcode/memory` (episodic, semantic), `~/.slmcode/memory` (procedural, per model family +
language), `.slmcode/evolve` (repair rules, regression checks), `~/.slmcode/evolve` (bandit
posteriors), `.slmcode/metrics/runs.jsonl` (per-run metrics + `Compare`). Everything is
readable JSON; `rm -rf` on any of it is supported. → **[docs/self-improvement.md](docs/self-improvement.md)**

---

## ⌨️ CLI

| Command | Purpose |
|---|---|
| `init` · `doctor` · `readiness` | Workspace scaffolding, provider health, SLM-readiness score |
| `run` · `chat` · `tui` (bare `slmcode`) | Full pipeline · classic REPL · interactive TUI |
| `apply` · `reject` · `diff` · `commit` | Review and land agent changes |
| `status` · `board` · `watch` · `compose` · `task` · `plan` | Inspect a run |
| `config` · `stack` · `agent` · `blocks` · `skills` · `hooks` | Configure (`hooks list/trust/untrust` approves `.slmcode/hooks.json`) |
| `studio` | Web UI + SSE API |
| `session` · `context` · `docs` | Sessions and markdown memory |
| `memory` · `evolve` · `metrics` | Inspect what the harness has learned |
| `update` · `version` · `completion` | Maintenance |

`--json` on `status`, `doctor`, `readiness`, `board`, `version`, `apply`, `compose`, `task show`,
`blocks list`, `hooks list`, `auth list`, and every `config` (except `set`) / `memory` / `evolve` /
`metrics` subcommand. Colour is off outside a TTY. Exit codes: `0` ok · `1` failure · `2` usage or
missing TTY · `3` no workspace · `4` provider unreachable · `5` failing tasks · `6` unanswerable
gate · `130` interrupted (a genuine cancellation — a provider error that merely says "interrupted"
does not get 130). → **[docs/cli.md](docs/cli.md)**

TUI: `/help`, `/compact`, `/models`, `/permission`, `/apply`, `/reject`, `/diff`, `/rewind`,
`/sessions`, `/stats`, `/stop`, `/resume` — `/` opens a fuzzy picker; ↑/↓ and Ctrl-R search
history; Esc interrupts a run so you can redirect it.

---

## 🎯 Why this loop exists

| 🐘 Large-model habit | 🐭 SLMCode approach |
|---|---|
| Stuff the repo into chat | Token-budgeted packs + a ranked repo map |
| Hope the model emits valid JSON | Negotiated constrained decoding, then a repair ladder |
| One free-form agent | Plan → atomic tasks → 20 specialists |
| Trust "I fixed it" | Disk is authoritative; hallucinated edits do not pass |
| Re-learn the same failure every run | Fingerprint it once, store the repair, apply it for free |
| Opaque progress | Append-only transcript + sticky footer, or Studio's SSE feed |

---

## 📚 Docs

**[unicolab.github.io/smlcode](https://unicolab.github.io/smlcode/)** — MkDocs Material.

| Section | Pages |
|---|---|
| Getting started | Install · Quick start · Concepts · Providers |
| Handbook | Guide · TUI · Skills · Studio · Agents · Blocks · Customization · Pipeline · Recipes |
| Reference | CLI · Config · Tools (ACI) · Constrained decoding · Context engineering · Permissions · Testing · Troubleshooting · FAQ |
| Internals | Architecture · Conventions · Self-improvement & memory |
| Project | Migration notes · Changelog · Contributing |

Local preview: `make docs-serve` → <http://127.0.0.1:8000>

---

## 🧪 Develop

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make bootstrap        # install web/ npm deps + build the Studio UI into cmd/slmcode/ui/
make check            # the one gate: fmt, vet, lint, tests, race, web lint+build — same as CI
```

Studio is a Vite + React + TypeScript SPA in `web/`, built to `cmd/slmcode/ui/` and embedded with
`go:embed all:ui`. Everything in `cmd/slmcode/ui/` except `.gitkeep` is gitignored build output,
so building the UI never dirties a tracked file; with none of it present the server serves a
placeholder page compiled into `pkg/server`. For UI work: `make bootstrap && cd web && npm run dev`.
See [CONTRIBUTING.md](CONTRIBUTING.md#build) — including why `web/package-lock.json` needs
regenerating and committing.

```go
import "github.com/UnicoLab/slmcode/pkg/harness"

h, _ := harness.New("/path/to/project")
_ = h.Init()
res, err := h.Run(ctx, "refactor pkg/auth")
```

Contributing guide, lint ratchet and package ownership: **[CONTRIBUTING.md](CONTRIBUTING.md)**.
Agents working on this repo: **[AGENTS.md](AGENTS.md)**.

---

## 📜 License

[MIT](LICENSE) — use it, remix it, ship with it.

<p align="center">
  <br/>
  Made with ♥ by <a href="https://unicolab.ai"><strong>UnicoLab</strong></a><br/>
  <sub>Coding with SLMs should feel like a superpower, not a compromise. ☀️</sub>
</p>
