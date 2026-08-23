# 🩺 Troubleshooting

Keyed on the messages the harness actually emits. Start with `slmcode doctor`.

---

## Endpoint pre-flight

The harness probes the model server **before** starting a run and refuses to start with a
doctor-quality block, rather than marching through every phase emitting per-agent failures.
`slmcode run` exits **4** when the endpoint is unreachable.

| `cause:` | `tip:` |
|---|---|
| `connection refused` | Nothing is listening. Start your server (`ollama serve`, LM Studio, oMLX) or point `--endpoint` elsewhere. |
| `host not found` | The hostname does not resolve — check `--endpoint` / `SLMCODE_ENDPOINT` for a typo. |
| `timed out` | The endpoint accepted the connection but did not answer in time. Is the model still loading? |
| `TLS handshake failed` | Use `http://` for a local server, or fix the CA bundle. |
| `HTTP 401/403 unauthorized` | The provider rejected the API key. Set `SLMCODE_API_KEY`, or store it in `.slmcode/auth.json`. |
| `HTTP 404 — model not found` | That model id is not served by this endpoint. `slmcode config set model <id>`. |
| `HTTP 404` (no model named) | The endpoint path is wrong — most OpenAI-compatible servers need the `/v1` suffix. |
| `HTTP 429 rate limited` | Retry shortly, or lower `max_parallel`. |
| `HTTP 5xx from the provider` | The server is up but failing — check its logs. |
| `no endpoint configured` | `slmcode config set endpoint <url>` |
| `HTTP 404 on /models` (amber) | The server answered but does not list models. Fine for some backends; `slmcode doctor` runs the deeper check. |

```bash
slmcode status --json | jq .connection
slmcode doctor --json
```

---

## Edit failures

These arrive **in-band**, as the tool result the model sees. They are also what you will see in
the Live view and the run trace.

### `Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`)`

The commonest small-model failure. `ws_read` renders a `   42|` gutter for navigation; it is not
in the file. The message shows a before/after. `pkg/evolve` ships a rule
(`transform_args: strip_line_number_prefix`) that fixes this automatically after the first time.

### `Edit refused — old_str is empty (or only whitespace)`

An empty search used to pass `strings.Contains` and silently prepend `new_str`. The message names
the three real intents: create → `ws_write`; append → anchor on the last 2–3 lines; insert →
repeat the anchor at the end of `new_str`.

### `old_str found N times in <path>`

The search text is not unique. Add 2–3 surrounding lines, or pass `replace_all: true` if you
really mean every occurrence. **Not** a reason to fall back to `ws_write`.

### `Ambiguous edit refused — the search text matches N places in <path> (<strategy> match)`

One of the tolerant match strategies found several candidates. An ambiguous edit is a wrong edit,
so nothing is applied. Same fix: more context.

### `old_str not found in <path>`

None of the five strategies matched uniquely. The result includes a fuzzy hint at the closest
span. Re-read and retry with the exact text — never with `ws_write`.

### `[matched on first+last line anchors — … verify the result with ws_read]`

The edit **did** apply, but only the first and last lines of `old_str` matched; the middle
drifted. Read the file back before trusting it. Seeing this repeatedly means your model is
paraphrasing spans instead of copying them — consider `edit_format: search_replace` or a
different `fast_model`.

### `EDIT REVERTED — <path> parsed correctly before your change and does NOT parse after it`

The post-edit syntax check caught a break the edit introduced, and the file is **unchanged on
disk**. Fix the replacement text; retrying the identical edit will be reverted again. Disable
with `disable_syntax_check: true` if a checker is misbehaving on your codebase.

### `⚠ syntax check failed (<tool>) on <path>`

The file did not parse before either — the edit was applied and the parse error is reported so it
can be fixed on the next turn rather than three tool calls later.

### `No-op edit refused — old_str and new_str are identical`

Usually a model that has lost track of what it already did. Check the run trace for a loop.

### A `ws_patch` per-hunk report

Multi-hunk patches are all-or-nothing. The report names which hunks anchored
(`anchored@120..164 exact`) and which missed. A repeated multi-hunk failure is what makes
`pkg/evolve` switch `edit_format` to `search_replace`.

---

## Shell refusals

### `shell refused — "<cmd>" can execute arbitrary code, so it needs explicit operator approval`

An **executor** (`python`, `node`, `make`, `npx`, `sh`, `awk`, `go run`, …) is not auto-allowed.
This is a deliberate tightening: `python` on an allowlist is functionally identical to no
allowlist. Use an allowed verification form (`python -m pytest`, `python -m py_compile`,
`node --check`, `go test`), or allow it explicitly:

```bash
slmcode config set shell_allow '["make ","npx vitest"]'
export SLMCODE_BASH_ALLOW="make ,npx vitest"
```

### `shell refused — "<cmd>" modifies files outside the tool layer`

A **mutator** (`sed`, `cp`, `mv`, `rm`, `tee`, `patch`, `git checkout`…). Those edits could not be
checkpointed, reviewed or reverted. Use `ws_edit` / `ws_patch` / `ws_write` / `ws_mv` /
`ws_delete`.

### `shell refused — command substitution "$(" is not allowed`

`$(…)`, backticks, `<(…)` and `>(…)` hide a nested command from every safety check. Run the inner
command as its own `ws_shell` call and use its output. This one cannot be allowlisted.

### `shell refused — a bare `&` backgrounds the command`

One command per call, and wait for its output. `&&`, `2>&1` and `&>file` are fine.

### `shell whitelist: this command writes to "<path>" (<kind>)`

`shell_write_guard` caught a `cat > file` / `tee` clobber. Appends (`>>`) are allowed.

### `shell whitelist: "<cmd>" is not an allowed command`

Not in any tier. Either it is genuinely unusual (allowlist it) or the model invented it.

### `shell denied by permission mode (shell=deny)` / `shell denied by user`

`shell_permission: deny`, or you answered no at the gate. The model is told not to retry the same
command.

### `command timed out after 2m0s and was killed (whole process group)`

Output captured before the kill is included. The message suggests a narrower command
(`go test ./pkg/foo -run TestBar -short`) and warns that a command needing stdin will always time
out. Raise `shell_timeout`, or pass `timeout_sec` on the call (ceiling 15m).

---

## Write refusals

### `write refused — .slmcode/<file> is harness control state, not project source`

Tools may not write under `.slmcode/` except `.slmcode/scratch/`. This is a privilege boundary,
not a heuristic — see [Permissions](permissions.md#3-slmcode-is-harness-state-not-agent-workspace).

### `path escapes workspace: <path>`

`..` or a symlink pointing out of the tree. Use a project-relative path.

### `Write refused — <path> uses a reserved device name`

Windows device names (`nul`, `con`, `com1`…).

### `review: staged <path> → …patch.json (run \`slmcode apply\`)`

Not an error — you are in `permission: review`. Run `slmcode apply`.

---

## JSON and decoding

### The model emits prose around its JSON

The repair ladder handles fences, prose extraction, single quotes, Python literals, trailing
commas and missing braces. If it is happening constantly, the endpoint is probably at
`prompt_only`: check whether constrained decoding was negotiated at all, and see
[Constrained decoding](decoding.md#6-debugging).

### `repair: json truncated mid-string (raise max_tokens or re-ask)`

Distinct from malformation on purpose: a truncated string has no recoverable content, and
appending closing braces produces a document that parses and lies. Raise `max_tokens` (or the
role's `max_tokens` in `model_profiles`). `pkg/evolve` maps this fingerprint to
`action: raise_max_tokens`.

### `repair: unrepairable json`

The ladder ran out of rungs. Usually a model that answered in prose entirely. Check that the role
has a schema contract, and that `structured_decoding` is not `off`.

### Structured output silently got worse after working fine

A **live demotion**. When a server returns a permanent 4xx for a request that differs from a plain
one only by its constrained-decoding field, that capability is demoted for that
provider+endpoint+model key. Common with OpenAI-compatible proxies that 400 on unknown body
fields, or servers that advertise `json_schema` but reject `strict: true`.

---

## Context and budget

### The model behaves as if it cannot see the file

Check the effective budget. If no model profile matches your model, the pack falls back to
`max_context_kb` (16 KB ≈ 4K tokens) regardless of the model's real window. Set a profile:

```yaml
model_profiles:
  qwen2.5-coder:
    context_limit: 32768
```

### `context_length_exceeded`

Classified as `context_overflow` and **not retried** — the same prompt will not fit on a second
attempt either. The fix is to shrink the pack (`context_role_budget`, `repo_map_tokens`,
`excerpt_window_lines`, `skill_disclosure: cards`) or raise the window.

### Time to first token is seconds instead of milliseconds

KV-cache prefix reuse is not hitting. Anything you add to a prompt must be byte-deterministic and
stable-prefix-first. A per-turn timestamp, a randomly ordered map, or a shuffled skill list is
enough to break it for every call.

### `max_task_calls=10 used=10 blocked=review`

The per-task LLM call budget is exhausted. It replaced an unbounded worst case (~16 calls for one
task), and it is **derived from `max_retries`**, not picked: worker + self-critique +
`max_retries` × (review + correct), which is 1 + 1 + 8 = 10 at the shipped `max_retries: 4`.

So raising `max_retries` without raising `max_task_calls` does nothing — the budget caps the
retries first, and the run warns when you have configured that combination. Raise both together,
or split the task. `slmcode task show <id>` names this gate under **Gate**, with the number of
times it fired and the `used=` / `llm_requests=` counters behind it.

---

## Gates and runs

### A run finishes but nothing changed

The run says so now — the closing block reads
`⚠ no files changed — nothing was created, modified or deleted on disk` and gives a reason. Three
reasons, in order of likelihood:

1. **The edit was refused for lack of evidence.** The model returned
   `{"status":"done","files_changed":["x.go"]}` without ever writing `x.go`. The line under the
   warning says so, and `slmcode task show <id>` prints the reviewer's verdict, the gate that
   refused the task, and the (unchanged) diff of its focus files. Shrink the scope and sharpen the
   acceptance line — `slmcode task edit T1 --acceptance "…"` then `slmcode run "…"` again.
2. **`permission: review`.** The edits exist as proposals in `.slmcode/pending/`; the block says
   `N proposed edit(s) are held for review` and offers `slmcode apply`.
3. **`permission: dry-run`.** Nothing is ever written.

The change set is what *this run* did: files that were already modified before the run started and
that the run did not touch are excluded, and `.slmcode/` harness state never counts.

### `slmcode board` says a task is done but the work is not there

Look for `⚑ forced done` next to it. That marks a task closed because a human answered `[d]one` at
the escalate gate, which **overrides** the evidence gate that refused it. The run summary counts
those separately (`1 human override — you answered [d]one at the escalate gate`) and
`slmcode task show <id>` says so in the header.

### `T1 needs human review` and you do not know why

`slmcode task show T1`. It renders the scope, the acceptance criteria, the agent's last output
(with its `files_changed` claim labeled as a claim), the reviewer's verdict and issues, the gate
that refused the task, and the diff of the task's focus files — then lists what you can do from
the terminal. `slmcode board` flags the tasks worth opening and names one in its tip.

### The reviewer approves work that is not there

It should not: disk state is authoritative, hallucinated edits do not auto-approve, and repo dirt
unrelated to the task does not count as evidence. If you see it anyway, capture the run trace
(`GET /api/queries/{id}/trace`) and open an issue — that is a real defect, not a tuning problem.

### The run stops at the plan gate in CI

Default `--on-gate-timeout=stop` means a plan is **never** auto-approved in a headless run. Pass
`--on-gate-timeout=approve` to opt into the old behaviour, or `=reject` to fail closed. Exit code
**6** means a gate could not be answered.

### `slmcode apply` exits 2

`interactive review needs a TTY (use --all / --list / --json)`.

### The QA gate wants to install dependencies

`qa_bootstrap` is `ask` by default: an agent that invented a `requirements.txt` should not get an
unattended network install. Set it to `auto` if you trust the sandbox, `off` to forbid it.

---

## Studio

### `forbidden: studio only serves loopback hosts`

The request's `Host` was not `127.0.0.1` / `::1` / `localhost`. This is the DNS-rebinding guard.

### `forbidden: cross-origin request rejected`

An `Origin` that is not same-origin, or `Sec-Fetch-Site: cross-site`. Studio emits no permissive
CORS headers. For the Vite dev server, enable the dev-origin allowance
(`SLMCODE_STUDIO_DEV_CORS=1`) — it permits exactly `:5173`, nothing else.

### `unauthorized: missing or invalid studio session token`

Open the URL the CLI printed (it carries `?t=…`), or send the token as `X-SLMCode-Token` /
`Authorization: Bearer`.

### The live feed shows a gap

An explicit `event: gap {from,to}` frame means events could not be replayed from the 1500-entry
ring buffer. The run is fine; the log is incomplete. Token deltas are evicted first so the
structural timeline survives.

### Studio shows a placeholder page

`cmd/slmcode/ui/index.html` is a checked-in placeholder. Run `make bootstrap` (or `make ui-react`)
to build the real SPA.

### `port 7420 is in use`

`slmcode studio --kill` (only ever signals a process named exactly `slmcode`), or let it move to
a free port, or `--no-port-auto` to fail instead.

---

## Update

### `checksum mismatch for <asset>` / `<asset> is not listed in SHA256SUMS — refusing to install`

The self-updater downloads the release's `SHA256SUMS` first and verifies the binary against it
before replacing anything. A mismatch means the download was corrupted or tampered with — nothing
was installed.

### `installing to <path>: permission denied`

Try `sudo`, or `slmcode update --user` to install into `~/.local/bin`.

---

## Getting more detail

```bash
slmcode run --vv "…"                     # debug-level rendering
slmcode status --json
slmcode memory show --role worker        # what the model was actually told
slmcode evolve rules                     # which repairs the harness has learned
slmcode metrics show --last 10           # pass rate, edit-apply rate, calls per task
cat .slmcode/memory/REFLECTION.md        # what happened last run
```

Still stuck → [FAQ](faq.md).
