# ⌨️ CLI reference

Binary: **`slmcode`**. Every command has `--help`; `slmcode --help` groups them.

```bash
slmcode --help
slmcode <command> --help
```

---

## Non-interactive contract

Every command is safe to call from a script, a CI job or another agent.

- **Colour.** ANSI escapes are emitted only when stdout is a terminal, `TERM` is not `dumb`, and
  `NO_COLOR` is unset. `slmcode status | cat` is plain text. Override with
  `--color=auto|always|never` or `FORCE_COLOR=1`.
- **JSON.** `--json` is available on `status`, `doctor`, `readiness`, `board`, `version`, `apply`,
  `compose`, `task show`, `blocks list`, `hooks list`, `auth list`, `auth get`, every `config`
  subcommand except `config set`, and every `memory` / `evolve` / `metrics` subcommand. It writes
  a single JSON document to stdout with colour forced off; diagnostics go to stderr.
- **Prompts.** Nothing prompts without a TTY. `slmcode apply` refuses interactive review (exit 2)
  and points at `--all`/`--list`/`--json`; `slmcode` with no workspace refuses to scaffold
  (exit 3); `slmcode update` needs `--yes`.
- **HITL gates.** With a TTY on **stdin and stdout** they render inline and **block** until
  answered — they never expire into an automatic decision. Without a TTY the decision is taken at
  **run start, before the first model call**, and logged: with `--on-gate-timeout` unset the
  plan / clarify / continue / escalate gates answer themselves with "yes"; an explicit
  `--on-gate-timeout=stop|reject` refuses the run **at the door** (exit 6) rather than planning
  for minutes and discarding the result. `shell_permission=ask` is a safety gate and never
  auto-approves — headless it refuses up front.
- **Errors.** A failure is reported exactly once, on stderr, prefixed with `✖`.

### Exit codes

| Code | Meaning |
|---:|---|
| 0 | success |
| 1 | generic failure |
| 2 | usage error / invalid argument / a TTY was required |
| 3 | workspace not initialized |
| 4 | provider check failed — `run` pre-flight found nothing listening, or `doctor` found an unreachable endpoint, a rejected key or a missing model |
| 5 | the run completed but tasks failed |
| 6 | a human-in-the-loop gate could not be answered |
| 130 | interrupted (SIGINT/SIGTERM); a second interrupt force-quits |

---

## Global flags

| Flag | Notes |
|---|---|
| `--root` | Project root (default: cwd) |
| `--provider` | `SLMCODE_PROVIDER`. Never clobbers an endpoint set by flag, env, or an explicit non-default config value |
| `--model` | `SLMCODE_MODEL` |
| `--endpoint` | `SLMCODE_ENDPOINT` |
| `--api-key` | `SLMCODE_API_KEY` / provider-specific |
| `--backend` | `slmcode` (default) or `claude-code` |
| `-v` / `--verbose` | same as `--log-level=info` |
| `--vv` | same as `--log-level=debug` |
| `--log-level` | `error\|warn\|info\|debug` — what the CLI renders |
| `--color` | `auto` (default) `\| always \| never` |
| `--dry-run` | do not write code files |
| `--parallel` | max parallel workers |
| `--retries` | review/correct retries |
| `--think-passes` | multi-pass think loops |
| `--on-gate-timeout` | `approve\|reject\|stop` — what a HITL gate does with no TTY. **Unset** + no TTY: convenience gates auto-approve and say so. An explicit `stop`/`reject` refuses the run before any model call |
| `--no-explore` | greedy bandit, no exploration — reproducible runs (config: `deterministic`) |
| `--evolve` / `--no-evolve` | force the self-improvement engine on/off for this run |
| `--max-task-calls` | per-task LLM call budget (config: `max_task_calls`) |
| `--architect-editor` | enable the describer→editor role pair (config: `architect_editor`) |
| `--structured-decoding` | `auto\|off` — constrained decoding policy |
| `--no-banner` | hide the ASCII banner — help, `studio`, the TUI and `version` |
| `--version` | print the version and exit; `slmcode version` is the detailed form (`--check` queries GitHub) |

### Configuration layering

Lowest precedence first: built-in defaults → user file → project file (`.slmcode/config.yaml`) →
`SLMCODE_*` environment → command-line flags. `slmcode config show --origin` attributes each
effective value to `default` | `user` | `project` | `env SLMCODE_X` | `flag --x`.

The user file is discovered by `pkg/config`, so the layer applies to Studio, the TUI and any
embedder as well as the CLI. Candidates, most specific first: `$SLMCODE_USER_CONFIG`,
`$XDG_CONFIG_HOME/slmcode/config.yaml`, `~/.slmcode/config.yaml`,
`~/.config/slmcode/config.yaml`. Write to it with `slmcode config set --user <key> <value>`.

A saved `config.yaml` records **intent**: only the keys that differ from what the project would
otherwise inherit, plus a `config_version` stamp. So `config show --origin` can tell a choice from
an inherited default, a new release's improved default reaches existing projects, and no absolute
path is embedded in a file that may be committed. Older files are migrated forward on load, and
`config show` says when that happened.

---

## Commands

### Run & steer

| Command | Purpose |
|---|---|
| `slmcode` / `tui` | Interactive TUI (default with no subcommand) |
| `init` | Create `.slmcode/` memory, board, config, and a `.slmcode/.gitignore` |
| `run [query…]` | Full pipeline, or a single specialist |
| `chat` | Classic REPL |
| `studio` | Studio web UI + SSE API |
| `watch` | Live-refreshing kanban |

### Review changes

| Command | Purpose |
|---|---|
| `apply [path…]` | Review and apply pending agent writes (`permission: review`) |
| `reject [path…]` | Discard pending proposals |
| `diff [path…]` | Working-tree diff |
| `commit` | `git add -A` + commit helper |

### Configure

| Command | Purpose |
|---|---|
| `config` | `show` · `get` · `set` · `unset` · `schema` · `path` |
| `stack` | `list` · `show` · `apply` · `edit` · `new` — provider/model presets |
| `agent` | `list` · `show` · `edit` · `clear-llm` — per-agent LLM pins |
| `blocks` | `list` · `show` · `new` · `edit` · `delete` · `apply` · `validate` |
| `skills` | `list` · `show` · `new` · `edit` · `path` |
| `hooks` | `list` · `trust` · `untrust` — inspect and approve `.slmcode/hooks.json` |
| `update` | Refresh the binary release or rebuild from source |

### Inspect

| Command | Purpose |
|---|---|
| `status` | Query, provider, board counts, plan gate, connection probe, pending count |
| `board` | Kanban snapshot; flags tasks that need a human and names the one to inspect |
| `task show <id>` | **Why a task stopped** — scope, acceptance, last output, review verdict and issues, the gate that blocked it, and the diff of its focus files (`--json`, `--no-diff`) |
| `compose [query…]` | Preview the dynamic pipeline — no LLM call, no writes |
| `readiness` / `ready` | Score local-SLM readiness; `--fix` applies safe defaults |
| `task` | `add` · `show` · `edit` · `move` · `delegate` · `check` · `uncheck` · `promote` · `rm` |
| `context` | Show / append to `CONTEXT.md` |
| `docs` | List / show / edit markdown memory |
| `plan` | Show `PLAN.md` |
| `session` | `list` · `show` · `resume` |
| `doctor` | Provider / model / endpoint / workspace health |
| `eval` | Evaluation harness |
| `memory` | `show` · `episodes` · `facts` · `forget` |
| `evolve` | `rules` · `why` · `regressions` · `reset` |
| `metrics` | `show` · `compare` |
| `version` | Version metadata (`--check` queries GitHub) |
| `completion` | `bash\|zsh\|fish\|powershell` |

---

## `run`

```bash
slmcode run -v "add JWT auth"
slmcode run --agent explorer "Where is auth handled?"
slmcode run --mode specialist --agent worker "…"
slmcode run --skill atomic-coding "Refactor helpers"
slmcode run --dynamic "add JWT auth"          # force task-specific composition
slmcode run --no-dynamic "tiny typo fix"      # force the static pipeline
slmcode run --think-passes 2 --parallel 2 --retries 2 "…"
slmcode run "…"                               # headless: gates auto-approve, logged
slmcode run --on-gate-timeout=stop "…"        # headless: refuse up front instead
```

| Flag | Meaning |
|---|---|
| `--mode` | `full` \| `specialist` (overrides config) |
| `--agent` | run a single specialist |
| `--skill` | pin/load a skill by name (repeatable); `@skill:name` in the query also works |
| `--dynamic` / `--no-dynamic` | override `dynamic_pipeline` for this run |

`dynamic_pipeline` defaults on: the composer selects a task-specific subset of phases, agents,
slots and execute-loop roles before workers run.

### What a run prints at the end

The last block of every run — successful or not — answers "what changed and what do I do now".

When files changed:

```
  duration        41.2s
  tasks           2/3 done  ·  1 awaiting a human
  board           .slmcode/board.json

Changes
  3 files · +47 −12
    pkg/auth/jwt.go                     +31 -2  ++++++++++++++++
    pkg/auth/jwt_test.go                +14 -0  +++++++
    README.md                           +2 -10  ++--------

Next
  slmcode diff             the full patch, file by file
  slmcode commit -m "…"    keep it
  slmcode task show T3     why T3 stopped: verdict, gate and diff
```

When nothing changed — the most common outcome of a small local model, and the one the CLI used
to be silent about:

```
Changes
  ⚠ no files changed — nothing was created, modified or deleted on disk
  the model's edits were refused before they reached the tree (usually: an edit was claimed but never made)

Next
  slmcode task show T1    why T1 stopped: verdict, gate and diff
  slmcode run --vv "…"    re-run with the full agent transcript
```

Details worth knowing:

- The change set is what **this run** did. Files that were already dirty when the run started and
  that the run did not touch are excluded, and `.slmcode/` harness state never counts.
- `permission: review` stages edits instead of writing them, so the block says
  `N proposed edit(s) are held for review and have NOT been written yet` and offers
  `slmcode apply` first.
- `tasks` separates *verified* done from **human overrides**: answering `[d]one` at the escalate
  gate closes a task the evidence gate refused, and the summary says
  `1 human override — you answered [d]one at the escalate gate` rather than folding it into the
  done count. `slmcode board` marks the same task `⚑ forced done`.
- `errors  .slmcode/errors/errors.md` appears only when that file actually holds something.
- The same block prints when a run fails, dies at a gate or is interrupted — "did my files
  change?" is a more urgent question after a failure than after a success.

## `compose`

```bash
slmcode compose "add JWT auth"
slmcode compose --json "add JWT auth"
```

Deterministic inspection only — it does not call the LLM and does not write code. Shows the
phases, the team, the execute loop and the SLM-fit assessment the run would use.

## `readiness`

```bash
slmcode readiness            # scores provider/model reachability + safe SLM defaults
slmcode readiness --fix      # apply the safe config patch it recommends
slmcode readiness --no-probe # skip the endpoint/model availability check
slmcode readiness --json
```

Exits non-zero when required checks fail.

## `apply` / `reject`

`slmcode apply` is **interactive by default**: each pending change is rendered as a coloured
unified diff and you choose what happens to it.

| Key | Action |
|---|---|
| `a` (or `y`) | apply this file |
| `s` (or `n`, or Enter) | skip — stays pending |
| `r` | reject — discard the proposal |
| `e` | open the proposal in `$EDITOR`, re-diff, ask again |
| `v` | view the full diff (no line cap) |
| `A` | apply this and everything after it |
| `q` | stop and summarise |

File modes are preserved when a proposal is written.

```bash
slmcode apply --list        # summary of what is waiting
slmcode apply --json        # machine-readable pending set (implies no prompts)
slmcode apply --all         # apply everything without prompting
slmcode apply pkg/x.go      # only files matching a path prefix
slmcode reject pkg/x.go
slmcode reject --all
```

Without a TTY, `slmcode apply` exits 2 and names the three non-interactive options.

## `task show`

The answer to "why did T1 stop?". `T1 needs human review` is the most common terminal state of a
local-SLM run, and this is where it stops being a dead end.

```bash
slmcode task show T1            # scope, verdict, gate, and the diff of its focus files
slmcode task show T1 --no-diff  # skip the diff
slmcode task show T1 --json     # task, verdict, gate, gate reason, answer
```

It renders, in the order a human needs them:

| Section | What it answers |
|---|---|
| header | the column, the role, retry count, focus files — and `← forced done by a human` when someone overrode the evidence gate |
| **Scope** | what the task was asked to do |
| **Acceptance criteria** | how it was going to be judged |
| **Last output** | the agent's final JSON, with `files_changed` labeled as a *claim* |
| **Review verdict** | approved/rejected, score, summary, and each issue |
| **Gate** | which gate refused it, how many times, the escalate question and how it was answered |
| **Diff of focus files** | what those files actually look like now — or an explicit "no change on disk" |
| **Next** | the commands that move it forward from this terminal |

Repeated engine notes are collapsed (`… ×200`), and Studio-only advice in engine-authored text is
rewritten into a command this binary has.

`slmcode board` flags the tasks worth opening (`⚑ needs you`, `⚑ blocked`, `⚑ forced done`) and
names one in its tip.

## `studio`

```bash
slmcode studio                    # default port 7420, auto-picks a free one if busy
slmcode studio --listen :9000
slmcode studio --no-port-auto     # fail instead of moving to a free port
slmcode studio --kill             # terminate an existing slmcode on that port first
slmcode studio --dev-cors         # allow the Vite dev server (npm run dev in web/)
slmcode studio --no-auth          # drop the session token (loopback enforcement stays)
```

Studio mints a per-run session token and prints it in the URL (`http://127.0.0.1:7420/?t=…`) —
open **that** URL, or `/api/*` returns 401. `--kill` only ever signals a process whose executable
is exactly `slmcode`. `Ctrl+C` shuts down gracefully. See [Studio](studio.md).

## `hooks`

`.slmcode/hooks.json` runs shell commands around tool calls, and it lives inside the project — so
the harness **fails closed** on it. Nothing runs until both `hooks_enabled: true` and an explicit
per-content approval from you.

```bash
slmcode hooks list        # every command the file would run, and whether it is trusted
slmcode hooks list --json
slmcode hooks trust       # prints the commands, then asks; -y skips the prompt
slmcode hooks untrust     # withdraw approval
```

The listing always prints the commands **before** asking, and never executes them. Approvals are
keyed on `(absolute path, SHA-256 of the file)` and stored in your OS config directory, never in
the repository — so a repo cannot ship its own approval, and editing `hooks.json` revokes it.

`SLMCODE_TRUST_HOOKS=1` force-trusts every hooks file on the machine (for CI images that generate
their own). `hooks list` says so when it is set. Details → [Permissions §10](permissions.md#10-hooks).

| Exit | Meaning |
|---:|---|
| 0 | listed / trusted / untrusted |
| 1 | the hooks file does not parse, or declares no commands |
| 3 | there is no hooks file to trust |
| 6 | you answered "no" at the approval prompt |

## `stack` & `agent`

```bash
slmcode stack list
slmcode stack show deepseek
slmcode stack apply omlx-local
slmcode stack apply deepseek --clear-agent-llm   # agents inherit the stack LLM
slmcode stack apply openai --agents              # also write optional role pins
slmcode stack apply openai --agents --force-agents

slmcode agent list                               # agents with their effective LLM
slmcode agent show worker
slmcode agent edit worker model=… provider=…     # pin; empty = inherit the stack
slmcode agent clear-llm worker
```

Shipped stacks (13, `stacks/*.yaml`): `omlx-local`, `mlx-qwen-coder`, `ollama-local`,
`ollama-qwen-coder`, `ollama-qwen3-coder`, `lmstudio-local`, `vllm-local`, `openai`,
`openrouter`, `deepseek`, `groq`, `google`, `qwen`. `slmcode stack list` prints the live set.
Details → [Providers](providers.md).

## `blocks`

```bash
slmcode blocks list
slmcode blocks list --json
slmcode blocks show pipeline go
slmcode blocks apply go
slmcode blocks apply python --materialize-agents
slmcode blocks apply react --force
slmcode blocks new agent my-worker --file ./my-worker.yaml
slmcode blocks validate
```

Details → [Blocks](blocks.md).

## `config`

```bash
slmcode config show
slmcode config show --origin        # where each effective value came from
slmcode config show --json
slmcode config get max_parallel
slmcode config set max_parallel 6
slmcode config unset fast_model
slmcode config set --user model qwen2.5-coder:14b   # write the user-level layer
slmcode config schema               # machine-readable field schema
slmcode config path
```

Bare `slmcode config` prints help — use `config show`. Full field list →
[Config reference](config.md).

Keys belong in `.slmcode/auth.json` or the environment, not in committed YAML.

## `calibrate`

Measures what the configured `(model, endpoint)` pair can actually do —
concurrency knee, latency baseline, decode rate, context window — instead of
guessing it from the provider name.

```bash
slmcode calibrate                  # measure (or reuse) the active pair
slmcode calibrate --force          # re-measure regardless of the cache
slmcode calibrate --show           # print stored profiles, probe nothing
slmcode calibrate --json
slmcode calibrate --model Qwen3.5-9B-MLX-4bit
```

It runs automatically, once, when a run meets an unseen pair. The full level
table is printed so the chosen `max_parallel` is checkable rather than magic,
and values you have set explicitly are never overridden. Details →
[Calibration](calibration.md).

## `memory`, `evolve`, `metrics`

```bash
slmcode memory show --role worker      # the memory block a role actually receives
slmcode memory episodes 20             # recent runs the harness remembers
slmcode memory facts --kind command    # distilled semantic facts
slmcode memory forget episodic --yes

slmcode evolve rules                   # repair rules with confidence + hit counts
slmcode evolve rules --all             # include seeded-but-unused and retired rules
slmcode evolve why edit_format         # the posterior table behind a learned choice
slmcode evolve regressions --run       # replay the offline regression checks
slmcode evolve reset --yes

slmcode metrics show --last 10
slmcode metrics compare 12             # newest 12 runs vs the 12 before them
```

All take `--json`. Details → [Self-improvement & memory](self-improvement.md).

## `autoresearch`

Tunes the harness's *own* agent prompts and a whitelist of safe config knobs against
the eval suite, keeping a change only when the primary metric improves and no guarded
metric (tokens, wall clock, tool errors, edit-format apply rate) regresses.

```bash
slmcode autoresearch --surface         # what is mutable, and in what range
slmcode autoresearch                   # DRY RUN: what it would try — no model, no writes
slmcode autoresearch --apply --seed 7 --max-experiments 6 --budget 20m
slmcode autoresearch --restore         # undo the last applied run
```

It writes nothing unless asked twice: `--apply` **and** `autoresearch: true` in
`.slmcode/config.yaml`. Everything it does is reversible from the snapshot it takes
first, and everything it records lives under `.slmcode/autoresearch/`.
Details → [Autoresearch](autoresearch.md).

## `doctor`

Reports the active provider / model / endpoint, reachability with latency, the embedding mode,
and workspace / board / skills sanity. `--json` for scripts. Exit code 4 means the provider check
failed — an unreachable endpoint, a rejected or missing API key, or a model the endpoint does not
serve; the message names which.

## Completions

```bash
slmcode completion zsh > "$(brew --prefix)/share/zsh/site-functions/_slmcode"
slmcode completion bash
slmcode completion fish
slmcode completion powershell
```

---

## Environment

| Variable | Effect |
|---|---|
| `SLMCODE_<KEY>` | **every** config key has one, mechanically: `SLMCODE_MAX_PARALLEL`, `SLMCODE_QA_BOOTSTRAP`, `SLMCODE_ESCALATE_ASK_TIMEOUT`, … `slmcode config schema` lists them all with types and defaults |
| `SLMCODE_PROVIDER` `SLMCODE_MODEL` `SLMCODE_ENDPOINT` `SLMCODE_API_KEY` | provider selection |
| `SLMCODE_BACKEND` | `slmcode` \| `claude-code` |
| `SLMCODE_USER_CONFIG`, `XDG_CONFIG_HOME` | user-level config layer location |
| `SLMCODE_BASH_ALLOW` | extra shell allowlist prefixes (comma-separated) |
| `SLMCODE_BLOCKS`, `SLMCODE_STACKS` | extra block / stack search paths |
| `SLMCODE_TRUST_HOOKS=1` | force-trust every `.slmcode/hooks.json` (CI images that write their own; see `slmcode hooks`) |
| `SLMCODE_TRUST_PROJECT_MCP=1` | honour `mcp_servers` from a **project** config file — normally a user-layer-only key, because each entry is spawned as a child process at startup |
| `SLMCODE_TUI=0`, `CI=true` | force the non-interactive path |
| `SLMCODE_NO_QUIET=1` | do not filter dependency stderr during engine construction |
| `SLMCODE_SKIP_UPDATE_CHECK=1` | never contact GitHub |
| `SLMCODE_EMBEDDING_*` | embedding backend overrides |
| `SLMCODE_STUDIO_TOKEN`, `SLMCODE_STUDIO_NO_AUTH`, `SLMCODE_STUDIO_DEV_CORS` | Studio security profile |
| `SLMCODE_SRC`, `SLMCODE_UPDATE_REPO` | `slmcode update` source resolution |
| `NO_COLOR`, `FORCE_COLOR`, `TERM` | colour resolution |

---

## TUI

Bare `slmcode` opens the interactive TUI: a non-blocking REPL with an append-only transcript and
a sticky status footer (it does not clear the screen on repaint).

| Key | Action |
|---|---|
| `Esc` | interrupt the running phase and redirect it mid-run |
| `↑` / `↓` | prompt history |
| `Ctrl-R` | reverse history search |
| `Tab` | complete a slash command |
| `/` | fuzzy command picker |
| `Ctrl-A/E/K/U/W` | line editing |
| `Ctrl-C` | cancel; twice to quit |

Slash commands: `/help` `/run` `/stop` `/resume` `/plan` `/board` `/status` `/diff`
`/apply` `/reject` `/rewind` `/compact` `/agents` `/agent` `/model` `/models` `/provider`
`/permission` `/auth` `/schema` `/mcp` `/skills` `/blocks` `/pack` `/sessions` `/history`
`/stats` `/errors` `/feedback` `/escalate` `/doctor` `/studio` `/refresh` `/clear` `/q`
(aliases `/quit`, `/exit`).

→ [TUI & chat](tui.md)
