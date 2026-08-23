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
- rewrite `config.yaml` to disable its own guards;
- forge `pending/*.patch.json` entries that a human would then apply.

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

**Read-only** — cannot mutate the tree:
`ls` `cat` `head` `tail` `wc` `pwd` `echo` `printf` `date` `which` `type` `env` `printenv`
`uname` `whoami` `id` · `git log|status|diff|show|branch|remote|stash list|tag|ls-files|rev-parse`
· `find` `grep` `rg` `ag` `fd` `tree` · `pip show` `pip list` `npm list` `npm ls`
`cargo metadata` · `df` `du` `free` `top -bn` `ps` · `curl -I` `curl --head` · `mkdir` `touch`
`true` `false` `test` `[` · `sort` `uniq` `cut` `diff` `stat` `file` `basename` `dirname`

**Build/test** — the runners a worker is expected to use:
`go test|build|vet|fmt|mod|list` `gofmt` · `pytest` `python -m pytest|py_compile|compileall|unittest`
· `node --check` · `npm test|run|ci|install` · `cargo test|build|clippy|fmt|check` ·
`mvn` `./mvnw` `gradle` `./gradlew` · `ctest` `cmake` · `bash -n` `shellcheck` ·
`tsc` `eslint` `ruff` `mypy` `black` `flake8` · `gcc` `g++` `clang` `clang++` ·
`uv run pytest` `uv sync` `uv pip`

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

**Without a human attached**, gates resolve immediately using `--on-gate-timeout`:

| Value | Effect |
|---|---|
| `stop` *(default)* | take the gate's non-TTY default; a plan is never auto-approved headless |
| `approve` | auto-approve (the old permissive behaviour) |
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

`hooks_enabled` (on) loads `.slmcode/hooks.json` — lifecycle commands run by the harness. See
`.slmcode-hooks.example.json` in the repo root. Because hooks execute shell, `.slmcode/` is not
tool-writable; that is the whole reason for §3.

## 11. Secrets

API keys resolve in this order: explicit config → `SLMCODE_API_KEY` → `.slmcode/auth.json` →
provider-specific env (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `OMLX_API_KEY`, …).

`slmcode init` writes a `.slmcode/.gitignore` covering `auth.json`, `pending/` and `sessions/`,
because `slmcode commit` runs `git add -A`.

## 12. Studio

Studio is a local agent with file-read, config-write, API-key-write and run-start capability. Its
own security model — loopback enforcement, same-origin enforcement, session tokens — is in
[Studio](studio.md#security-model).
