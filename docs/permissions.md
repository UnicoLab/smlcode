# 🛡️ Permissions & safety model

An agent harness on a local machine is a program that writes files and runs commands on your
behalf. SLMCode's defaults are deliberately conservative in the places where being wrong is
expensive and permissive in the places where it is not.

---

## 1. File write policy — `permission`

| Mode | Behaviour |
|---|---|
| `auto` *(default)* | Tools write to disk immediately. Checkpointed and reversible. |
| `dry-run` | Nothing is written. Tools report `dry-run: would edit …`. |
| `review` | Writes are staged as proposals under `.slmcode/pending/` and land only when you run `slmcode apply`. |

Aliases accepted: `allow`/`yes` → `auto`; `dryrun`/`dry` → `dry-run`; `ask`/`pending` → `review`.

In `review` mode, a tool result reads
`review: staged pkg/foo/bar.go → 1712…_edit_pkg__foo__bar.go.patch.json (run \`slmcode apply\`)`.

```bash
slmcode config set permission review
slmcode apply             # interactive per-file review
slmcode apply --list      # what is waiting
slmcode apply --json      # machine-readable
slmcode apply --all       # apply everything, no prompts
slmcode reject pkg/x.go   # discard one proposal
slmcode reject --all
```

See [CLI](cli.md#apply-reject) for the interactive keys.

## 2. Path jail

- Paths are project-relative. `..` escapes are refused with a message naming the correct form.
- The workspace root is resolved through symlinks once, and every resolved path is checked
  against that real root, so a **symlink inside the tree cannot point out of it**.
- Windows reserved device names (`nul`, `con`, `com1`, `lpt1`…) are refused everywhere.

## 3. `.slmcode/` is harness state, not agent workspace

Tools may not write anywhere under `.slmcode/` except `.slmcode/scratch/`. This holds **even when
the focus guard is disabled** — it is a privilege boundary, not an anti-wander heuristic.

`.slmcode/` used to be unconditionally writable, which let an agent:

- drop a `hooks.json` — arbitrary shell on the next run;
- rewrite `config.yaml` to disable its own guards, or to add an `mcp_servers` entry that is
  spawned as a child process on the next startup;
- forge `pending/*.patch.json` entries that a human would then apply.

The two "a file in the repo names a program to run" vectors are closed a second time, so that a
**human** committing one is no better off than an agent writing one: hooks fail closed behind
`slmcode hooks trust` (§10), and `mcp_servers` is ignored outside the user config layer (§10.1).

## 4. Shell policy — `shell_permission`

| Mode | Behaviour |
|---|---|
| `allow` *(default)* | Whitelisted commands run. |
| `ask` | Every command waits for approval (inline in the terminal, or the HITL modal in Studio). Timeout: `shell_ask_timeout`, 2m. |
| `deny` | `ws_shell` is refused outright. |

`auto_approve: true` treats `ask` as `allow`.

## 5. The shell whitelist — `shell_whitelist` (default `true`)

The allowlist is **tiered**. Only the first two tiers auto-run.

### Auto-allowed

**Inspection** — reads the tree, does not modify existing files:
`ls` `cat` `head` `tail` `wc` `pwd` `echo` `printf` `date` `which` `type` `printenv`
`uname` `whoami` `id` · `git log|status|diff|show|branch|remote|stash list|tag|ls-files|rev-parse`
· `grep` `rg` `ag` `fd` `tree` · `pip show` `pip list` `npm list` `npm ls`
`cargo metadata` · `df` `du` `free` `top -bn` `ps` · `curl -I` `curl --head` ·
`true` `false` `test` `[` · `sort` `uniq` `cut` `diff` `stat` `file` `basename` `dirname`

Three entries in this tier are auto-allowed **only in their inspecting form**, and the flag audit
(`DangerousInvocation`, `pkg/workspace/shellexec.go`) refuses the rest:

| Command | Allowed | Refused |
|---|---|---|
| `env` | `env`, `env -0`, `env FOO=1` — printing the environment | `env <program>`, `env -- <program>`, `env -S …` — these *exec* a program the allowlist never sees |
| `find` | listing and filtering paths | `-exec` `-execdir` `-ok` `-okdir` `-delete` `-fprintf` `-fprint` `-fprint0` `-fls` — these run a program or delete files for every match |
| `mkdir`, `touch` | creating a path **inside** the project root | any operand that is absolute, starts with `~`, or climbs out with `..` |

`mkdir` and `touch` do create files. They are auto-allowed because inside the workspace that is
harmless and often necessary, and refused outright when the path leaves it — but they are not
read-only, and this page used to say they were.

**Build/test** — the runners a worker is expected to use:
`go test|build|vet|fmt|mod|list` `gofmt` · `pytest` `python -m pytest|py_compile|compileall|unittest`
· `node --check` · `npm test|run|ci|install` · `cargo test|build|clippy|fmt|check` ·
`mvn` `./mvnw` `gradle` `./gradlew` · `ctest` `cmake` · `bash -n` `shellcheck` ·
`tsc` `eslint` `ruff` `mypy` `black` `flake8` · `gcc` `g++` `clang` `clang++` ·
`uv run pytest` `uv sync` `uv pip`

Several of these take a flag whose **value names another program to run**, which would clear the
allowlist while executing something it never inspected. Those flags are refused per binary:

| Binary | Refused flags | Why |
|---|---|---|
| `go` | `-exec` `-toolexec` `-vettool` `-overlay` `-gcflags` `-asmflags` `-ldflags` `-compiler` | each forwards a program (or a nested `-toolexec` / `-fuse-ld` / `-fplugin`) to the toolchain |
| `go` | `go generate` | executes `//go:generate` directives chosen by the repository |
| `cmake` | `-P` `-C` | run a CMake script, and a CMake script is `execute_process` with extra steps |
| `cmake` | `-E` | cmake's command mode (`cmake -E copy`, `cmake -E rm`) — a file mutator that bypasses `ws_write` and the checkpointer |
| `cmake` | `--install` | copies build output to an arbitrary `--prefix` |
| `cmake` | out-of-tree `-S` / `-B` / `--build` paths | `cmake --build /tmp/x` writes outside the workspace |
| `ctest` | `--build-and-test` `--test-command` `--build-generator` | name a command to execute |
| `cargo` | `--config` | injects configuration that can name a runner |
| `npm` `pnpm` `yarn` | `--node-options` | passes `--require`/`--eval` to node |
| `tsc` | `--plugin` | loads arbitrary code into the compiler |
| `eslint` | `--rulesdir` `--resolve-plugins-relative-to` | load rule modules from a path of the caller's choosing |
| `mypy` | `--custom-typeshed-dir` | same |
| `pytest` | `-p` `--rootdir` | `-p` imports an arbitrary plugin module |

`cmake .`, `cmake -S . -B build`, `cmake --build build` and a plain `ctest` still run: driving the
project's own build is the point. See §5.1 for what that inherently means.

### Refused unless explicitly allowed

!!! warning "Behaviour change"
    These used to run. They no longer do, because `python` on an allowlist is functionally
    identical to no allowlist at all.

**Executors** — can run arbitrary code:
`python` `python3` `node` `deno` `bun` `perl` `ruby` `php` · `npx` `yarn` `pnpm` `make`
`go run` `cargo run` · `sh` `bash` `zsh` `ksh` `eval` `exec` `source` `.` ·
`xargs` `sudo` `su` `ssh` `nc` `telnet` `gdb` `lldb` · `awk` `gawk`

```
shell refused — "python" can execute arbitrary code, so it needs explicit operator approval
(add it to shell_allow / SLMCODE_BASH_ALLOW).
For verification use an allowed runner instead: `go test ./pkg/x -short`,
`python -m pytest -q`, `python -m py_compile <file>`, `node --check <file>`.
```

Note the asymmetry: bare `python` is refused, but `python -m pytest` and `python -m py_compile`
are auto-allowed. The tier is decided by the *prefix*, so the specific verification forms stay
available while `python -c '…'` does not.

**Mutators** — rewrite or relocate files behind the tool layer's back:
`sed` `cp` `mv` `rm` `rmdir` `install` `truncate` `rsync` `ln` `chmod` `chown` `shred` `dd`
`tee` `patch` · `git checkout|reset|clean|apply|stash`

```
shell refused — "sed" modifies files outside the tool layer, so edits cannot be
checkpointed, reviewed or reverted.
Use ws_edit / ws_patch to change a file, ws_write to create one,
ws_mv to rename, ws_delete to remove.
```

### Always refused, regardless of allowlist

- **Command substitution** — `$(…)`, backticks, `<(…)`, `>(…)`. These hide a nested command from
  every check in the package, and there is no safe way to allow them.
- **A bare `&`** — it backgrounds the command and starts a second one the chain splitter never
  saw. (`&&`, `2>&1`, `>&2`, `&>file` are fine.)
- **Write redirection to an existing file** when `shell_write_guard` is on: `cat > file`,
  `tee file`, `dd of=file`. Appends (`>>`) are allowed. This closes the `cat > file <<EOF`
  bypass of the write guard.

Every chain segment (`;`, `&&`, `||`, `|`) must independently clear the whitelist.

### Extending it

```yaml
# .slmcode/config.yaml
shell_allow:
  - "make "        # trailing space = word-boundary match
  - "python -c"
```

```bash
export SLMCODE_BASH_ALLOW="make ,npx vitest"
```

Both are merged with the built-ins. Prefix matching is literal, and a trailing space is the
idiom for "this word, not words starting with it".

Turn the whole gate off with `shell_whitelist: false` — at which point `shell_permission` is the
only thing between the model and your shell.

## 5.1 What the allowlist does **not** protect you from

Everything in §5 is a *command* allowlist. Two consequences follow from that, and neither is a
bug to be fixed — they are properties of what this tool is. Read them before pointing SLMCode at
code you did not write.

### `ws_shell` is a command allowlist, not a filesystem jail

The tool layer (`ws_read`, `ws_write`, `ws_edit`, `ws_mv`, `ws_delete`) **is** jailed to the
project root: §2 resolves symlinks and refuses `..` escapes, and every write is checkpointed.

`ws_shell` is not. It decides **which command may run**, not which files that command may touch.
An allowed `cat`, `grep`, `find`, `head`, `ls`, `stat` or `wc` reads **any path the user account
can read** — `~/.ssh/id_rsa`, `~/.aws/credentials`, `/etc/passwd`, another project's `.env` — and
the contents come back to the model as a tool result, which means they go to whatever endpoint
`provider`/`endpoint` points at.

The write side is narrower but not zero: `mkdir` and `touch` are refused outside the root, the
mutator tier (`sed`, `cp`, `mv`, `rm`, `tee`, `dd`, …) is refused entirely unless you allow it,
and shell redirection onto an existing file is refused by `shell_write_guard`. So the realistic
exposure is **read exfiltration, not out-of-tree modification** — but it is real.

One layer does hold on the read side, and it is worth being precise about its scope: every tool
result — `ws_shell` included — passes through a **secret scrub** on the way back to the model
(`pkg/workspace/redact.go`). The values it knows about are the configured `api_key`, everything
in `.slmcode/auth.json`, and the provider environment variables (`OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, …); each is replaced with
`[redacted: slmcode credential]` wherever it appears. That closes the channel for **the harness's
own credentials by value**, whatever command produced them. It does nothing for a secret the
harness has never been told about — your `~/.ssh/id_rsa`, a colleague's `.env`, a token in a
config file — because it cannot recognise one.

If that matters for your threat model, the enforcing boundaries are the operating system's, not
this tool's:

- run SLMCode as a user that can only read the project (a container, a VM, a dedicated account);
- `shell_permission: ask` — approve each command yourself;
- `shell_permission: deny` — no shell at all; the `ws_*` tools still work and stay jailed.

### Verifying a project runs the project's own code

The quality gates work by executing the project's verification commands. That is the entire
mechanism, and it cannot be made safe by inspecting the command line, because the *command* is
innocuous and the *code it runs* comes from the repository:

| Allowed command | What it executes |
|---|---|
| `npm test`, `npm run <x>`, `npm ci`, `npm install` | the `scripts` and lifecycle hooks (`preinstall`, `postinstall`) in `package.json`, plus every dependency's install scripts |
| `pytest` | `conftest.py` at import time, before a single test runs |
| `go build`, `go test` | `#cgo` directives, which invoke the system C compiler with repository-supplied flags |
| `mvn`, `./mvnw`, `gradle`, `./gradlew` | the wrapper script committed to the repo, then the build plugins the build file declares |
| `cmake --build build` | the generated build system, i.e. the rules `CMakeLists.txt` chose |
| `cargo build`, `cargo test` | `build.rs`, compiled and run as part of the build |
| `make` *(not auto-allowed)* | any recipe in the `Makefile` |

**Pointing SLMCode at an untrusted repository is equivalent to running that repository's build.**
Clone-and-run is the risk, not clone-and-inspect. If you would not run `npm install && npm test`
in that checkout by hand, do not point an agent at it either.

### Telling the two apart

The refusals catalogued in §5 (`env python -c`, `find -exec`, `go test -exec`, `cmake -P`,
`touch /etc/x`, command substitution, bare `&`) were **allowlist bugs**: each named its payload
directly on the command line, none of them is needed to build or test anything, and each has been
closed. The two risks on this page are **inherent**: they do not come from a hole in the list, and
no addition to the list removes them.

## 6. Command execution bounds

Even an allowed command is bounded:

| Bound | Value |
|---|---|
| Default timeout | 2 minutes (`shell_timeout`) |
| Per-call override ceiling | 15 minutes |
| Captured output | 256 KB in memory, then capped in the tool result at `max_tool_chars` |
| On timeout | the entire **process group** is killed |

Process-group kill matters: a test runner that spawns children would otherwise leave orphans
holding the terminal after the harness moved on. A timeout is reported to the model as
information, not raised as a harness error.

## 7. Scope and evidence guards

All default on.

| Key | Guards against |
|---|---|
| `write_guard` | writing outside the task's focus files |
| `read_before_edit` | editing a file the agent has not read this session |
| `shell_write_guard` | clobbering files through shell redirection |
| `over_edit_guard` | whole-file rewrites smuggled through `ws_edit`/`ws_patch` |
| `claims_gate` | a `files_changed` claim naming a file that was not touched |
| `static_quality` | stub / placeholder code passing as an implementation |
| `require_smoke` | a coding task approved without a smoke check |
| `quality_monitor` | empty output, tool-call loops, hallucinated tools |
| `disable_syntax_check` *(inverted)* | an edit that breaks a file that previously parsed |

Two engine-level rules back these up:

- **Disk state is authoritative.** A claimed edit that is not on disk does not count as evidence.
  Repository dirt that is unrelated to the task does not count either.
- **Gates fail closed.** Truncated reviewer JSON is a rejection, not an approval. The QA gate
  cannot report green when tests actually failed.

## 8. Human-in-the-loop gates

| Gate | Config | Default | Timeout |
|---|---|---|---|
| Clarify | `clarify_mode` | `ask` | `clarify_timeout` 2m |
| Plan approve | `plan_approve` | `ask` | `plan_approve_timeout` 2m |
| Continue | `continue_ask` | `ask` | `continue_ask_timeout` 2m |
| Escalate | `escalate_ask` | `ask` | `escalate_ask_timeout` 5m |
| Shell | `shell_permission` | `allow` | `shell_ask_timeout` 2m |

Each takes `off` / `auto` / `ask`. `auto_approve: true` bypasses all of them.

**With a human attached** (a TTY, or a Studio client subscribed to the event stream), a gate
renders and **blocks until answered**. It does not expire into an automatic decision — a gate that
silently auto-approves after two minutes is worse than no gate, because you believed you had one.

This holds for `slmcode run` as well as the TUI and Studio: on a terminal, `run` draws the same
gate card and takes a **single keystroke** (`y` / `n` / `r`, or type free text to answer with
notes). `[n]o` stops the run; `[r]eplan` sends the planner back for another attempt — they are
different answers.

**Without a human attached**, gates resolve immediately using `--on-gate-timeout`:

| Value | Effect |
|---|---|
| `stop` *(default)* | stop at the gate, once. The run ends with exit code **6** and prints the flag or config key that would let it proceed unattended. |
| `approve` | answer every gate affirmatively (the old permissive behaviour) |
| `reject` | fail closed |

`plan_approve_on_timeout` (`approve` / `reject` / `auto`) covers the plan gate specifically;
`auto` approves only when **no** event subscriber was attached — i.e. when there was no UI that
could have answered.

## 9. Reversibility

| Mechanism | Config | What it gives you |
|---|---|---|
| File checkpoints | `file_checkpoints` (on) | per-file snapshots before each write; `/rewind` in the TUI, `POST /api/rewind/{id}` in Studio |
| Wave snapshots | `wave_snapshots` (on) | a snapshot per execution wave |
| Pending proposals | `permission: review` | nothing lands until you say so |
| Git | — | `slmcode diff`, `slmcode commit` |

## 10. Hooks

`.slmcode/hooks.json` makes the harness run shell commands around tool calls (`PreToolUse`,
`PostToolUse`). See `.slmcode-hooks.example.json` in the repo root for the shape.

**It is fully opt-in, twice over**, because that file lives *inside the project*: a repository you
cloned can ship one, and `git clone && slmcode run` must not equal `bash -c <whatever the repo
wrote>`.

1. **`hooks_enabled` defaults to `false`.** Turn it on per project with
   `slmcode config set hooks_enabled true`. With it off, nothing in the file is even loaded.
2. **The file's exact contents must be trusted by you.** `pkg/hooks` fails closed: it hashes the
   file and refuses to load it unless *this* operator has approved *that* digest. The approval
   record lives in your OS config directory, never in the repository, so a repo cannot ship its
   own approval — and any edit to `hooks.json` changes the digest and needs approval again.

```bash
slmcode hooks list      # every command the file would run, plus its trust state
slmcode hooks trust     # print the commands, then approve this exact content
slmcode hooks untrust   # withdraw approval
```

`slmcode hooks list` prints the commands **before** anything is approved and without executing
them — an approval you cannot inspect is not an approval. When the harness refuses an untrusted
file it prints the same list, so you always know what did not run.

`SLMCODE_TRUST_HOOKS=1` force-trusts every hooks file on the machine. It exists for CI images that
generate their own hooks file; do not set it in a shell you use to run code you did not write.
`slmcode hooks list` says so explicitly when it is set, so a hook that fires for that reason is
never a mystery.

Because hooks execute shell, `.slmcode/` is not tool-writable; that is the whole reason for §3.

## 10.1 MCP servers are a user-layer key

The same reasoning as §10, applied to the other place a repository could name a program to run.
Every entry in `mcp_servers:` is spawned as a **child process at orchestrator startup** — before
the model says anything, before any tool runs, before any permission prompt.

`.slmcode/config.yaml` lives inside the project, so `mcp_servers` is honoured **only** from the
user config layer (`$SLMCODE_USER_CONFIG`, `$XDG_CONFIG_HOME/slmcode/config.yaml`,
`~/.slmcode/config.yaml`, `~/.config/slmcode/config.yaml`). A project file can neither add a
server, replace the list, nor clear it — the user-layer list is restored wholesale after the
project layer is applied.

Whatever the project file declared is named in a warning that `status`, `doctor` and
`config show` all print, with the exact command that was not started and where to move it.
`SLMCODE_TRUST_PROJECT_MCP=1` force-honours the project layer, for CI images that generate the
project config themselves.

Unlike hooks this needs no approval store: an MCP server is per-user by nature (the same `docs` or
`jira` server is wanted in every project), so the user layer is where it belonged anyway.

## 11. Secrets

API keys resolve in this order: explicit config → `SLMCODE_API_KEY` → `.slmcode/auth.json` →
provider-specific env (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `OMLX_API_KEY`, …).

`slmcode init` writes a `.slmcode/.gitignore` covering all 26 paths that hold credentials or run
content — `auth.json`, `credentials.json`, `sessions/`, `queries/`, `memory/`, `summaries/`,
`metrics/`, `evolve/`, `pending/`, `checkpoints/`, `waves/`, the five HITL handshake directories,
`capabilities.json`, `throughput.json`, `repomap.json`, `*.log` and more — because `slmcode
commit` runs `git add -A`. The list lives in `pkg/config` (`SlmIgnoreEntries`) and is the same one
`slmcode doctor` probes with `git check-ignore`, so the check can never cover less than `init`
writes. What is deliberately **not** ignored: `config.yaml`, `board.json`, `hooks.json`, `skills/`,
`agents/` and `blocks/` — the parts a team is meant to share and review.

## 12. Studio

Studio is a local agent with file-read, config-write, API-key-write and run-start capability. Its
own security model — loopback enforcement, same-origin enforcement, session tokens — is in
[Studio](studio.md#security-model).
