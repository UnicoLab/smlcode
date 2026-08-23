# 🚚 Migration notes

Behaviour changes that affect existing workspaces and scripts. Nothing here requires a config
migration — `.slmcode/config.yaml` is migrated forward automatically — but several defaults are
now more conservative, and a few of them will change what your scripts see.

---

## 1. The shell whitelist is tiered — interpreters and file mutators are refused

**Before:** `shell_whitelist` was a flat allowlist; anything on it ran.

**Now:** three tiers. Only read-only commands and build/test runners auto-run. Two new tiers are
refused unless explicitly allowed:

| Tier | Examples |
|---|---|
| **Executors** (can run arbitrary code) | `python` `python3` `node` `deno` `bun` `perl` `ruby` `php` `npx` `yarn` `pnpm` `make` `go run` `cargo run` `sh` `bash` `eval` `exec` `source` `xargs` `sudo` `ssh` `awk` |
| **Mutators** (edit files behind the tool layer) | `sed` `cp` `mv` `rm` `install` `truncate` `rsync` `ln` `chmod` `chown` `dd` `tee` `patch` `git checkout\|reset\|clean\|apply\|stash` |

The reasoning in one clause: `python` on an allowlist is functionally identical to no allowlist.

Specific verification forms stay auto-allowed, because the tier is decided by prefix:
`python -m pytest`, `python -m py_compile`, `python -m unittest`, `node --check`, `npm test`,
`npm run`, `go test`, `bash -n`.

**If a run of yours depended on `make` or `python -c`:**

```yaml
# .slmcode/config.yaml
shell_allow:
  - "make "        # trailing space = word-boundary match
  - "python -c"
```

```bash
export SLMCODE_BASH_ALLOW="make ,python -c"
```

Or turn the gate off entirely with `shell_whitelist: false`, leaving `shell_permission` as the
only control.

Also newly refused, and **not** allowlistable: command substitution (`$(…)`, backticks, `<(…)`,
`>(…)`) and a bare `&`. Both hide a second command from every static check.

→ [Permissions & safety](permissions.md#5-the-shell-whitelist-shell_whitelist-default-true)

## 2. Studio: CORS `*` is gone, and there is a session token

**Before:** Studio sent `Access-Control-Allow-Origin: *`, so any web page you happened to have
open could read its responses and start runs.

**Now:**

- **Loopback only** — a non-loopback `Host` gets 403 (DNS-rebinding guard).
- **Same-origin only** — no permissive CORS headers at all; a cross-origin `Origin` or
  `Sec-Fetch-Site: cross-site` is refused. When an origin is allowed, only that exact origin is
  echoed, never `*`.
- **Session token** — `slmcode studio` now mints a random 256-bit token per launch and prints it
  in the URL (`http://127.0.0.1:7420/?t=…`). **Every** request must carry it — including `GET /`,
  the HTML shell — as `X-SLMCode-Token`, `Authorization: Bearer …`, or `?t=…` (for `EventSource`,
  which cannot set headers). Presenting it once mints an HttpOnly, `SameSite=Strict` session
  cookie (`slmcode_studio`), and the SPA strips `?t=` from the address bar.

**Open the URL the CLI prints**, not a bare `http://127.0.0.1:7420` — the latter now returns 401:
a static "open the URL the CLI printed" page for a navigation, a bare 401 for `/api/*`.

!!! warning "`<meta name="slmcode-token">` is gone"
    Studio used to serve `GET /` unauthenticated and inject the token into `index.html` for the SPA
    to read, which made the shell an unauthenticated token dispenser for any other local process.
    The tag and the fallback that read it were both removed. A client that scraped it must read the
    token from the CLI output or set `SLMCODE_STUDIO_TOKEN`.

**If you script against the Studio API**, reuse the printed `?t=` value, set
`SLMCODE_STUDIO_TOKEN` to a fixed value, or pass `--no-auth`.

**If you develop the SPA with `npm run dev`**, the Vite server at `:5173` is a different origin
and needs `slmcode studio --dev-cors` (or `SLMCODE_STUDIO_DEV_CORS=1`). Previously the wildcard
made this work by accident.

→ [Studio security model](studio.md#security-model)

## 3. `slmcode apply` is interactive by default

**Before:** `slmcode apply` applied everything it found.

**Now:** it renders each pending change as a coloured unified diff and asks per file:
`[a]pply` `[s]kip` `[e]dit` `[v]iew full` `[r]eject` `[A]pply all` `[q]uit`. File modes are
preserved.

**Scripts must be updated:**

```bash
slmcode apply --all      # the old behaviour
slmcode apply --list     # summary only
slmcode apply --json     # machine-readable, implies no prompts
```

Without a TTY, `slmcode apply` **exits 2** rather than guessing.

`slmcode reject [path…]` / `--all` is new: discard proposals without applying them.

## 4. HITL gates block instead of auto-approving when a human is attached

**Before:** a gate expired after its timeout and took an automatic decision — including approving
a plan — whether or not anyone was watching.

**Now:**

- **A human is attached** (TTY, or a Studio client subscribed to the event stream): the gate
  renders and **blocks until answered**. It does not expire. A gate that silently auto-approves
  after two minutes is worse than no gate, because you believed you had one. `slmcode run` on a
  terminal prompts inline and takes a single keystroke — it does not need the TUI.
- **No human attached**: the gate resolves immediately using the new `--on-gate-timeout` flag,
  which defaults to **`stop`** — a plan is never auto-approved in a headless run. A stopped run
  exits **6** and prints the flag or config key that lets it proceed unattended.

**If your CI relied on the old permissive behaviour:**

```bash
slmcode run --on-gate-timeout=approve "…"   # old behaviour
slmcode run --on-gate-timeout=reject "…"    # fail closed
```

Exit code **6** now means a gate could not be answered. `plan_approve_on_timeout` (`approve` /
`reject` / `auto`) covers the plan gate specifically; `auto` approves only when no event
subscriber was attached at all.

## 5. New state directories under `.slmcode/` and `~/.slmcode/`

The self-improvement subsystem writes new directories. All of them are plain JSON/JSONL/Markdown,
safe to read, edit, version-control or delete.

```
<project>/.slmcode/
├── memory/        episodes.jsonl · facts.json · SEMANTIC.md · WORKING.md · REFLECTION.md
├── evolve/        rules.json · regressions.json
└── metrics/       runs.jsonl

~/.slmcode/
├── memory/        procedures.json · PROCEDURES.md
└── evolve/        rules.json · policy.json
```

Bounded by design: episodes cap at 300 records / 180 days, facts at 200, procedures at 400, repair
rules at 400, bandit keys at 300, metrics at 2000 runs. A corrupt file is moved aside to
`<name>.corrupt` and the store starts clean rather than wedging a run.

**To opt out entirely:** `slmcode config set evolve false`.

**To reset:**

```bash
slmcode memory forget all --yes
slmcode evolve reset --yes
# or by hand — this is fully supported:
rm -rf .slmcode/memory .slmcode/evolve .slmcode/metrics ~/.slmcode/memory ~/.slmcode/evolve
```

`.slmcode/` is gitignored by the repo's own `.gitignore`, and `slmcode init` writes a
`.slmcode/.gitignore` covering `auth.json`, `pending/` and `sessions/` — worth checking if your
project predates it, since `slmcode commit` runs `git add -A`.

→ [Self-improvement & memory](self-improvement.md)

---

## Smaller changes worth knowing

| Change | Impact |
|---|---|
| The context pack is budgeted in **tokens**, not bytes | If no `model_profiles` entry matches your model, the pack falls back to `max_context_kb` (16 KB ≈ 4K tokens) regardless of the real window. Set `context_limit` for your model. |
| `ws_edit` refuses an empty `old_str` | It used to silently prepend `new_str` and report success. |
| An edit that breaks a previously-parsing file is **reverted** | Set `disable_syntax_check: true` to opt out. |
| `ws_read` returns a 120-line window | It used to return more; use `offset`/`limit` to page. Tune with `read_window_lines`. |
| Every tool result is capped at 8000 chars | Tune with `max_tool_chars`. |
| `ws_shell` has a 2-minute timeout and kills the process group | Tune with `shell_timeout`; per-call ceiling 15m. |
| `.slmcode/` is not tool-writable (except `.slmcode/scratch/`) | An agent can no longer write `hooks.json` or `config.yaml`. |
| A per-task LLM call budget (`max_task_calls`, default 10) | Replaces an unbounded worst case. It is derived from `max_retries` (1 + 1 + `max_retries` × 2), so raise the two together — the budget caps the retries otherwise. |
| Colour is disabled outside a TTY | `slmcode status \| cat` is plain text. Force with `--color=always` / `FORCE_COLOR=1`. |
| Documented exit codes | `2` usage/TTY · `3` no workspace · `4` provider unreachable · `5` failing tasks · `6` unanswerable gate · `130` interrupted. |
| `slmcode update` verifies a SHA-256 checksum | The release's `SHA256SUMS` is fetched and checked before the binary is replaced; a mismatch installs nothing. Replaces `curl \| bash` for updates. |
| `qa_bootstrap` defaults to `ask` | The QA gate no longer runs `pip install` / `npm install` / `go mod tidy` unattended against agent-authored manifests. |
| `structured_decoding` defaults to `auto` | Constrained decoding is negotiated per endpoint. Set `off` to force the old prompt-only behaviour. |
| Every config key now has a `SLMCODE_<KEY>` env override | `SLMCODE_MAX_PARALLEL`, `SLMCODE_QA_BOOTSTRAP`, … `slmcode config schema` lists them. An env var you previously set for an unrelated purpose could now be read as config. |
| A saved `config.yaml` records only keys that differ from the inherited default | New releases' improved defaults reach existing projects, and `config show --origin` can tell a choice from a default. Existing files are migrated forward on load. |
| A 26 KB `AGENTS.md` is now loaded into specialist prompts | Project instructions actually reach specialist prompts now. Trim yours, or gate sections with `paths:` globs — see [Context engineering](context.md#5-project-instructions-pkginstructions). |
