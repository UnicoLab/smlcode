# 🧰 Tool reference (the ACI)

The **agent–computer interface** is the surface a model actually has to succeed at. A frontier
model tolerates a sloppy one; a 7B does not. Everything here is designed around one principle:
**a tool must either do the right thing or explain exactly how to retry**.

All tools are defined in `pkg/workspace`. `ws_skill` is registered by the orchestrator.

| Tool | Writes? | One-line contract |
|---|---|---|
| `ws_read` | — | Read a windowed slice of a file as numbered lines |
| `ws_write` | ✅ | Create a new file (overwrite needs a prior read) |
| `ws_edit` | ✅ | Replace `old_str` with `new_str`, uniquely |
| `ws_patch` | ✅ | Apply a unified diff or SEARCH/REPLACE block |
| `ws_mv` | ✅ | Rename/move (uses `git mv` when available) |
| `ws_delete` | ✅ | Delete a file |
| `ws_list` | — | List a directory |
| `ws_glob` | — | Find files by pattern (`**` supported) |
| `ws_grep` | — | Regex search over file contents |
| `ws_shell` | — | Run one command (bounded) |
| `ws_todo` | — | Write/replace a short checklist, echoed back |
| `ws_skill` | — | Pull a skill's full body on demand |
| `git_status`, `git_diff` | — | Read-only git |

`workspace.ToolNames()` returns the coding set; `workspace.SpecialistToolNames()` adds the
meta-tools `find_models` and `mcp_call`.

---

## Universal rules

**Every result is capped.** `DefaultMaxToolChars` is 8000 characters (~2k tokens), configurable
with `max_tool_chars`. Truncation keeps head and tail and appends steering text naming the total
size — a single oversized result must never evict the rest of the conversation.

**Paths are project-relative and jailed.** `..` escapes are refused. Symlinks are resolved
against the real workspace root, so a symlink inside the tree cannot point out of it.

**`.slmcode/` is off limits.** Tools may not write anywhere under `.slmcode/` except
`.slmcode/scratch/`. This holds even when the focus guard is disabled — it is a privilege
boundary, not a heuristic: an agent that could drop a `hooks.json` would have arbitrary shell on
the next run, and one that could rewrite `config.yaml` could disable its own guards.

```
write refused — .slmcode/hooks.json is harness control state, not project source.
Files under .slmcode/ (hooks.json, config.yaml, pending/, checkpoints/) configure the
harness itself and are never edited by tools.
If you need scratch space, write under .slmcode/scratch/ instead.
```

**Loop guard.** Repeated identical calls are detected and answered with an intervention nudge.
The tracker is isolated per task, so one task's repetition history cannot poison another's.

---

## `ws_read`

```json
{"path": "pkg/foo/bar.go", "offset": 1, "limit": 120}
```

Returns a **120-line window** by default (`read_window_lines`), formatted as `%6d|line`. A
second hard ceiling caps any single read at roughly 15% of the context window.

When the window does not cover the whole file the result ends with:

```
[showing lines 1–120 of 480 in pkg/foo/bar.go; use offset= to see more]
Next page: ws_read {"path":"pkg/foo/bar.go","offset":121,"limit":120}. To jump straight to a symbol use ws_grep first.
```

The `   42|` gutter is **display only**. Including it in `old_str` is the single most common
small-model edit failure, so `ws_edit` detects and rejects it by name rather than reporting a
generic miss.

Failure messages point at the recovery tool: a missing path suggests `ws_glob`/`ws_list`, a
directory suggests `ws_list`, an out-of-range offset gives the valid range.

## `ws_write`

```json
{"path": "pkg/foo/new.go", "content": "…", "allow_shrink": false}
```

Creates new files. Overwriting an existing file is refused unless it was read this session
(`read_before_edit`), and the refusal spells out the `ws_edit` recipe instead.

A **catastrophic-truncation guard** refuses rewriting a large file as a tiny one; repeat with
`"allow_shrink": true` if that really is the intent. Windows reserved device names
(`nul`, `con`, `com1`…) are refused.

## `ws_edit`

```json
{"path": "calc.go", "old_str": "…", "new_str": "…", "replace_all": false}
```

### The match ladder

Small models drift on trailing whitespace, indentation and blank lines when they re-emit a span
they just read. Rather than failing outright, `ws_edit` walks a fixed ladder and stops at the
**first strategy producing exactly one match**:

| # | Strategy | Note appended on success |
|---|---|---|
| 1 | `exact` | *(none)* |
| 2 | `trailing-whitespace-insensitive` | `[matched ignoring trailing whitespace — your old_str had different line endings]` |
| 3 | `indentation-normalized` | `[matched after normalizing indentation — your old_str was indented differently]` |
| 4 | `blank-line-insensitive` | `[matched ignoring blank lines — your old_str had different blank-line spacing]` |
| 5 | `anchored-first-last-line` | `[matched on first+last line anchors — the middle of your old_str did not match exactly; verify the result with ws_read]` |

A strategy producing **two or more** candidates is never applied — an ambiguous edit is a wrong
edit. The indentation strategy re-applies the file's own leading whitespace to the replacement.

Reporting which rung matched is deliberate: it is how the model learns its `old_str` drifted,
and how `pkg/evolve` learns which drift your model has.

### Refusals

| Situation | Response |
|---|---|
| `old_str` empty or whitespace-only | Refused. Empty search used to pass `strings.Contains` and silently prepend. The message names the three real intents: create → `ws_write`; append → anchor on the last 2–3 lines; insert → repeat the anchor in `new_str`. |
| `old_str` carries the `   42\|` gutter | Refused by name, with a before/after example. |
| `old_str == new_str` | `No-op edit refused — old_str and new_str are identical.` |
| Exact match found N>1 times | `old_str found N times … pass replace_all:true, or include more surrounding context … Do NOT use ws_write.` |
| A ladder strategy matched N>1 times | `Ambiguous edit refused — the search text matches N places … (strategy) match.` |
| No strategy matched | Not-found guidance plus a fuzzy hint at the closest span. |
| Whole-file-style rewrite through `ws_edit` | Refused by the over-edit guard (`over_edit_guard`). |

Success: `edited pkg/foo/bar.go (1 replacement(s))` plus any strategy note and syntax note.

## `ws_patch`

```json
{"path": "pkg/foo/bar.go", "patch": "@@ -10,3 +10,4 @@\n …"}
```

Accepts a unified diff with `@@` hunks, a `<<<<<<< SEARCH / ======= / >>>>>>> REPLACE` block, or
a bare `-`/`+` block treated as one anchorless hunk.

Multi-hunk diffs are applied **hunk by hunk**, each anchored on its `@@` line numbers within a
±20-line window (`AnchorWindowLines`), with earlier hunks' line delta carried forward. The same
match ladder runs inside that window, so a hunk whose context drifted slightly still lands.

Application is **all-or-nothing**: if any hunk misses, nothing is written and you get a per-hunk
report naming which hunks applied, where they anchored (`anchored@120..164 exact`) and which
failed. Partial patches are how a file ends up half-migrated and compiling wrong.

## Post-edit syntax checking

After a successful write, edit or patch the harness runs a **file-local** parse check:

| Extension | Checker |
|---|---|
| `.go` | `gofmt -e -l` |
| `.py` | `python3 -c 'compile(...)'` (falls back to `python`) |
| `.js`, `.mjs`, `.cjs` | `node --check` |
| `.json` | `python3 -c 'json.load(...)'` |

TypeScript is deliberately not checked — `tsc --noEmit` needs the whole program and routinely
takes 10s+, far too slow to sit inside a tool call. A missing runtime is *skipped*, never read as
broken, and a timed-out check is skipped too.

Two outcomes:

1. **Was broken, still broken** → the error is appended to the result in-band, so the model fixes
   it on the very next turn:
   ```
   ⚠ syntax check failed (gofmt) on pkg/foo/bar.go:
   pkg/foo/bar.go:41:2: expected '}', found 'EOF'
   FIX THIS NOW with ws_edit before doing anything else …
   ```
2. **Parsed before, does not parse now** → the edit **is reverted** and the model is told exactly
   what it broke:
   ```
   EDIT REVERTED — pkg/foo/bar.go parsed correctly before your change and does NOT parse after it (gofmt):
   …
   The file is unchanged on disk. Fix the syntax in your replacement text and retry:
     • check brackets/parens/quotes are balanced in new_str
     • check indentation matches the surrounding block
   Do NOT retry the identical edit — it will be reverted again.
   ```

Disable with `disable_syntax_check: true`.

## `ws_grep`, `ws_glob`, `ws_list`

`ws_grep` takes a **real RE2 regular expression**. If the pattern does not compile it is used as
a literal substring and the result says so, rather than failing. Narrow with `glob=` and `path=`.
At most 50 hits; the cap is announced.

`ws_glob` supports `**` for any number of directories (`pkg/**/*_test.go`), capped at 200 hits.

`ws_list` returns an explicit message when the directory is empty or missing, rather than an
empty string the model has to interpret.

## `ws_mv` / `ws_delete`

`ws_mv` prefers `git mv` when a `.git` directory is present, otherwise renames. It is the
supported way to rename — rewriting a file and leaving the old one is a common small-model
failure mode.

`ws_delete` is irreversible except through the checkpoint store (`file_checkpoints`, on by
default).

## `ws_shell`

```json
{"command": "go test ./pkg/foo -short", "timeout_sec": 120}
```

One command per call. No command substitution, no backgrounding.

- **Timeout**: 2 minutes by default (`shell_timeout`), overridable per call but capped by the
  harness. On timeout the whole **process group** is killed, so a test runner cannot leave
  orphaned children holding the terminal.
- **Bounded output buffer**: output is capped while the command runs, not after, so a runaway
  command cannot exhaust memory.
- **Safety**: see [Permissions & safety](permissions.md) for the whitelist tiers, the
  substitution ban and the write-redirection guard.
- Empty output is reported explicitly: `(command succeeded with no output: …)`.

## `ws_todo`

```json
{"todos": ["read pkg/x/y.go", "[x] add nil check", "run go test ./pkg/x"]}
```

Writes or replaces a short checklist and echoes it back, which is the point: the plan stays in
*recent* context, where a small model's attention actually is, instead of scrolling away.

## `ws_skill`

Progressive skill disclosure means most matched skills are rendered as **cards** (name +
description) rather than full bodies — multiple simultaneous behavioural directives measurably
degrade small-model instruction following. `ws_skill {"name": "atomic-coding"}` pulls a body on
demand. An unknown name returns the list of skills that *are* available.

See [Skills](skills.md) and [Context engineering](context.md).

---

## The edit-format contract

Every tool-using specialist inherits the same contract (`agents.EditContract`):

- `old_str` must match the file byte-for-byte, indentation included. `ws_read` first.
- Strip `ws_read`'s `   42|` prefix — it is display only and never matches.
- Make `old_str` unique; include 2–3 surrounding lines when a short span repeats.
- `ws_write` creates **new** files; change existing files with `ws_edit` or `ws_patch`.
- **A failed match is always answered by re-reading and retrying, never by `ws_write`.**

The prompt ships a worked example and a worked *repair* (the line-number-prefix failure and its
fix), because for a small model a demonstration outperforms a rule.

Two further invariants come from `pkg/agents`:

- **One tool call per turn.** The harness truncates an assistant message to its first tool call,
  so a model that ignores the instruction simply loses the extra calls.
- **Never end on a tool call.** An agent must produce its final JSON after tool use.

Which edit format a run uses is one of the arms the bandit in `pkg/evolve` learns over
(`search_replace`, `unified_diff`, `whole_file`), keyed on model family and language — see
[Self-improvement](self-improvement.md).
