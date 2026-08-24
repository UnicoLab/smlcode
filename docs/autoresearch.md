# Autoresearch: tuning the harness on itself

`pkg/evolve` learns from runs that were going to happen anyway. **Autoresearch
is different in kind**: it deliberately changes the harness's own prompts and
knobs, measures what happened against a fixed evaluator, and keeps only what
survives.

```
snapshot → propose ONE change → apply → evaluate → keep if better,
else restore → record → repeat
```

Every clause in that line is load-bearing, and this page explains why, what it
writes to disk, which knobs exist, and how to inspect, undo or delete all of it.

Three rules apply, the same three that govern `pkg/memory` and `pkg/evolve`:

1. **Deterministic core, optional LLM.** The seeded proposer is the product. The
   prompt-rewriting model is strictly additive: with no model configured the
   engine runs on the deterministic proposer alone, and a model that errors or
   answers with nonsense falls back to it rather than failing the run.
2. **Bounded, prunable, safe to be wrong.** Three simultaneous budgets, a capped
   journal, a capped surface, and a stated reason for every stop.
3. **Fully inspectable and reversible.** Plain JSONL and Markdown.
   `rm -rf .slmcode/autoresearch` is a supported operation, and so is
   `slmcode autoresearch --restore`.

!!! warning "Opt-in, twice"
    This subsystem rewrites files you wrote. It is off by default and running
    `slmcode autoresearch` with no flags is a **dry run**. Applying takes both
    `--apply` *and* `autoresearch: true` in `.slmcode/config.yaml`.

---

## 1. Why this is possible at all

The loop needs three things, and the harness already had all three.

| Requirement | What supplies it |
|---|---|
| A mutable experimental surface | `.slmcode/agents/*.yaml` and `.slmcode/config.yaml` are **data**, not code |
| A fixed evaluator | `pkg/eval` (cases) + `pkg/eval/metrics` (pooled comparison) |
| Reversibility | file snapshot and restore — deliberately **not** git |

That last row is a decision, not an omission. `git stash` is three lines and
already installed, and it is wrong here: slmcode has no git integration, the
project directory belongs to the user, and a research loop that starts
committing, stashing or resetting somebody's working tree as a side effect of an
experiment is a data-loss bug wearing a convenience costume. A user with
uncommitted work, a dirty index, or no repository at all has to be able to run
this safely.

---

## 2. The surface: an allow-list

`Surface` is the declarative description of everything an experiment may mutate
— and, by omission, everything it may not. It is an **allow-list**. A knob is
immutable unless `pkg/autoresearch/whitelist.go` names it, gives it a domain to
move in, and records why moving it is safe.

### Agent fields

Reflected from every `.slmcode/agents/*.yaml`. Both file shapes are handled: a
project agent file puts the fields at the top level, a bundled block nests them
under `spec:`.

| Field | Domain |
|---|---|
| `temperature` | 0.00 – 1.00, step 0.05 |
| `max_tokens` | 512 – 8192, step 512 |
| `max_iter` | 1 – 24, step 1 |
| `system_prompt` | free text, ≤ 8000 chars — **LLM proposer only** |

`system_prompt` has no enumeration, which is exactly what keeps it out of the
deterministic proposer's reach: a text domain returns no candidates, so only the
optional model proposes rewrites of it.

A non-scalar field is never a knob. Flattening an agent's `skills:` list into a
string would corrupt the file, so lists and nested maps are skipped.

### Config knobs

| Key | Domain |
|---|---|
| `context_slack_percent` | 5 – 25, step 5 |
| `excerpt_window_lines` | 5 – 60, step 5 |
| `max_retries` | 0 – 6 |
| `max_task_calls` | 4 – 16, step 2 |
| `max_tokens` | 1024 – 8192, step 512 |
| `memory_tokens` | 100 – 800, step 50 |
| `react_compact_at_percent` | 50 – 95, step 5 |
| `repo_map_tokens` | 0 – 2000, step 100 |
| `skill_disclosure` | `auto` \| `cards` \| `full` |
| `skill_max_expanded` | 0 – 4 |
| `structured_decoding` | `auto` \| `off` |
| `temperature` | 0.00 – 1.00, step 0.05 |
| `think_passes` | 1 – 3 |
| `worker_critique` | `false` \| `true` |

Every one of these is a behavioral tuning knob whose worst case is a slower or
dumber harness — never a *wider* one.

### What is deliberately immutable

Nothing on the surface can change who the harness talks to, what it may run,
where it may write, or what it may read. These keys are disqualified and a unit
test asserts that each of them stays that way:

| Key | Why it can never be an experiment |
|---|---|
| `api_key`, `embedding_api_key` | provider credential |
| `provider`, `endpoint`, `embedding_endpoint` | chooses who the harness talks to |
| `model`, `fast_model` | changes the measured system, not a knob on it |
| `backend`, `claude_code_bin` | swaps the engine / names an executable to run |
| `permission`, `shell_permission`, `shell_whitelist`, `shell_allow` | the write and shell policies |
| `hooks_enabled` | runs repository-supplied shell commands on every tool call |
| `mcp_servers` | spawns subprocesses and opens network connections |
| `skills_dirs`, `retrieval_cache_dir` | filesystem roots |
| `listen` | the address the server binds |
| `auto_approve`, `dry_run` | skips human gates / disables writes |
| `write_guard`, `read_before_edit`, `shell_write_guard` | workspace safety invariants |

`slmcode autoresearch --surface --json` prints this table alongside the mutable
one, so "what can this thing touch" is answerable without reading the source.

---

## 3. The proposer

### Deterministic (required)

**Cyclic coordinate descent** from a seed. A seeded permutation fixes the knob
order and a second seeded permutation fixes each knob's value order; the walk
always moves the **least-tried** knob next.

The cyclic part matters more than it looks. A run has a budget of a dozen
experiments and a surface of twenty-odd knobs, so an exhaustive line search per
coordinate would spend the whole budget on `temperature`'s twenty-one values and
never touch the other nineteen knobs.

Three properties fall out of this, all required:

* the same seed produces the same experiment sequence, so **a run replays**;
* the space is finite, so `surface exhausted` is a fact the proposer can report
  rather than a loop it never leaves;
* a small budget still sees breadth.

The proposer holds no state between calls: the sequence is a pure function of
`(seed, surface, history)`, which is why replay survives a restarted process.

**One change per iteration.** There is no plural form of `Change` anywhere in the
package. Two simultaneous edits produce a measurement nobody can attribute, and
a sequence of those is a random walk with a changelog.

### LLM (optional)

`LLMProposer` rewrites a `system_prompt` with a model. Optional is *enforced*,
not documented: with a nil `Rewriter` it **is** the deterministic proposer, and
every failure mode of a small model — an error, an empty answer, an unchanged
answer, an answer past the length cap, an answer wrapped in a code fence it was
told not to use — falls back rather than failing the run or writing nonsense.

By default the model is asked on one proposal in three; the numeric sweep stays
the backbone, because prompt rewrites are expensive to evaluate and easy to
overfit.

---

## 4. The evaluator

`Evaluator` is one method: `Evaluate(ctx) (Score, error)`. It must be **fixed** —
the whole method depends on the yardstick not moving while the thing being
measured does.

`EvalEvaluator` is the default and wraps `pkg/eval` + `pkg/eval/metrics`. A
failing case is *data*, not an error: a change that breaks two cases should score
badly and be reverted, which cannot happen if it aborts the run instead. Only a
canceled context is an error.

`Score` carries the primary metric plus the guarded ones, all using
`pkg/eval/metrics`' convention that **`-1` means "no data"**, distinct from zero:

| Field | Source | Direction |
|---|---|---|
| `Primary` | task pass rate | higher is better |
| `TokensPerTask` | pooled tokens ÷ tasks | lower |
| `SecondsPerTask` | pooled wall ms ÷ tasks | lower |
| `ToolErrorRate` | tool errors ÷ tool calls | lower |
| `EditApplyRate` | edits applied ÷ attempted | higher |

Rates are **pooled** (sum of numerators over sum of denominators), because
`metrics.Aggregate` pools: averaging rates over cases of different sizes silently
overweights the small ones.

---

## 5. The anti-gaming guard

> *A ratchet improves the metric it can see.*

Left alone, "raise the pass rate" is solved perfectly well by spending ten times
the tokens, or by turning off whatever was producing tool errors. So a change is
retained only when **the primary metric improves AND no guarded metric regresses
beyond its tolerance**.

Default guard set, on by default:

| Guard | Direction | Tolerance |
|---|---|---|
| tokens per task | lower is better | 5 % of baseline |
| wall seconds per task | lower is better | 10 % of baseline |
| tool error rate | lower is better | 2 percentage points |
| edit-format apply rate | higher is better | 2 percentage points |

Tolerances are tight for the two rates (2 pp is noise, not a trend) and looser
for the two cost metrics, where a real improvement often does legitimately cost
a little.

**The guard runs twice, against two different baselines**, and the second one is
the interesting one:

1. against the current **champion** — the ordinary pairwise check;
2. against the run's own **baseline** — because checking only the champion lets a
   sequence of individually-tolerable regressions add up to a large one. Five
   steps each 4.9 % worse on tokens pass every pairwise check and land 27 %
   worse than where the run started.

A guard whose metric is unknown on either side is **skipped**. A change cannot be
blamed for a number nobody measured, and it must not be credited for one either.

The guard set is configurable (`RatchetOptions.Guards`). Nil means the defaults;
turning guarding off takes an explicit empty slice, never an omission.

---

## 6. Budgets and stopping honestly

A run is capped on three axes **at once**, checked before every experiment:

| Axis | Default | Why one axis is not enough |
|---|---|---|
| `--max-experiments` | 12 | a slow evaluator would otherwise run all night |
| `--budget` (wall clock) | 30m | a cheap-looking run would otherwise burn a month of tokens |
| `--max-tokens` | 2,000,000 | neither of the above stops a hanging evaluator |

Whichever binds first is the one the result names. Exhausting a budget returns
**the best artifact so far plus a stated reason for stopping**:

```
stopped   experiment budget spent — the surface was NOT exhausted,
          so more remains untried (12/12 experiments)
```

That sentence is not decoration. A score table alone cannot distinguish
"converged" from "ran out of money", and those are entirely different results.
The stop reasons are `max-experiments`, `max-wall-clock`, `max-tokens`,
`surface-exhausted`, `canceled`, `evaluation-failed`, `dry-run` and
`empty-surface`.

---

## 7. Reversibility

Reversal is by **file copy**, bounded to exactly the files the surface can write
— no more (snapshotting the tree would be a rewind system, which is a different
feature) and no less.

Two layers, covering two different failures:

* **Per-trial, in memory.** Before a change is applied the file is captured;
  the restore sits in a `defer` next to a `recover`, so an evaluator that
  panics, errors or has its context canceled still leaves
  `.slmcode/agents/*.yaml` byte-for-byte as it was. A test hashes every surface
  file before and after and asserts the hashes match for all three cases.
* **Per-run, on disk.** A pre-run snapshot is persisted under
  `.slmcode/autoresearch/snapshot/` **before the baseline evaluation**, because
  an evaluator is allowed to be slow and a run killed during the baseline must
  still be undoable. This is the layer that covers what no `defer` can: SIGKILL,
  a crashed machine, a closed laptop.

```bash
slmcode autoresearch --restore    # undo everything the last applied run kept
```

A snapshot records each file's bytes, mode and SHA-256, plus whether the file
existed at all — restoring a never-existed file **removes** it rather than
leaving an empty one the next run would load.

---

## 8. On-disk layout

Everything is human-readable and safe to edit, version-control or delete.

```
<project>/.slmcode/autoresearch/
├── trials.jsonl              one JSON object per experiment
├── BEST.md                   what was retained, what a guard rejected, why it stopped
└── snapshot/
    ├── manifest.json         paths, modes, hashes, existed-before flags
    └── 000-worker.yaml       the pre-run copies
```

`trials.jsonl` is append-only with one `write(2)` per record: on POSIX a
sub-`PIPE_BUF` append is atomic, so a crashed run leaves whole records rather
than a spliced one. **Every other write in the package goes through
`pkg/internal/atomicfile`** (temp file + rename).

One trial record:

```json
{"seq":3,"at":"2026-08-24T10:31:02Z","seed":7,
 "knob_id":"agent:worker.max_tokens","before":"3072","after":"8192",
 "origin":"deterministic",
 "baseline":{"primary":0.5,"tokens_per_task":1000,...},
 "score":{"primary":0.95,"tokens_per_task":2500,...},
 "kept":false,"guard":"tokens per task",
 "reason":"reverted: tokens per task regressed vs champion: 1000.0000 → 2500.0000 (allowed 50.0000)",
 "duration_ms":41233}
```

### Durability

A corrupt line in `trials.jsonl` is **skipped and counted**, never fatal — a
half-written record must not cost the history either side of it, and the count
surfaces through `Journal.Warnings()`. An unparseable `manifest.json` is moved
aside to `manifest.json.corrupt` and reported, matching the rest of the harness.
A manifest whose stored copies were deleted underneath it fails loudly rather
than restoring empty files over live content.

### Bounds

| Thing | Cap |
|---|---|
| Trial journal | 2000 records, oldest pruned first, file rewritten so it shrinks |
| JSONL line | 64 KB — longer is treated as corrupt, not buffered |
| Stored `before`/`after` per trial | 600 bytes (the snapshot is the backup, not the log) |
| Stored reason / error | 400 bytes |
| Snapshotted file | 1 MiB |
| Agent files on the surface | 64 |
| Knobs on the surface | 512 |
| Values enumerated per numeric domain | 64 |
| Rewritten prompt | 8000 chars |

---

## 9. Knobs

| Knob | Where | Effect |
|---|---|---|
| `autoresearch` | config | allow `--apply` to write. **Default false** |
| `--apply` | CLI | actually write; needs the config knob too |
| `--dry-run` | CLI | propose and print, apply nothing (also the no-flag default) |
| `--surface` | CLI | list the mutable knobs and their domains, then exit |
| `--restore` | CLI | restore the files from the last run's snapshot, then exit |
| `--seed` | CLI | fixes the experiment sequence; the same seed replays the run |
| `--max-experiments` / `--budget` / `--max-tokens` | CLI | the three budgets |
| `--deterministic` | CLI | never use the optional prompt-rewriting model |
| `--real` | CLI | score against the real-user query suite instead of the default cases |
| `--json` | CLI | machine-readable output |
| `RatchetOptions.Guards` | library | the guard set; nil = defaults, `[]Guard{}` = off |
| `RatchetOptions.MinImprovement` | library | how far the primary must move to count |
| `Options.NoAgents` / `Options.NoConfig` | library | narrow the reflected surface |

A dry run calls the evaluator **zero times**. `slmcode autoresearch` with no
flags is a dry run, so the default invocation never spins up a model.

---

## 10. Using it

```bash
# What can this thing touch? Ask before you run it.
slmcode autoresearch --surface

# What would it try? No model, no writes, instant.
slmcode autoresearch --seed 7 --max-experiments 6

# For real. Needs the config opt-in as well as the flag.
slmcode config set autoresearch true
slmcode autoresearch --apply --seed 7 --max-experiments 6 --budget 20m

# Read what it decided.
cat .slmcode/autoresearch/BEST.md
jq -s 'map(select(.kept))' .slmcode/autoresearch/trials.jsonl
jq -s 'map(select(.guard != null)) | length' .slmcode/autoresearch/trials.jsonl

# Undo the whole run.
slmcode autoresearch --restore
```

Every one of these takes `--json`.

### Resetting

```bash
rm -rf .slmcode/autoresearch     # fully supported; breaks nothing
```

The next run starts from the harness as it currently stands, with no memory of
past experiments. Note that this deletes the *snapshot* too — reset after you
are happy with what a run kept, not before.

---

## 11. Library use

```go
surface, err := autoresearch.Reflect(autoresearch.Options{Root: root})

r, err := autoresearch.New(autoresearch.RatchetOptions{
    Surface:     surface,
    Evaluator:   autoresearch.NewEvalEvaluator(eval.DefaultCases(), cfg),
    Proposer:    autoresearch.NewDeterministicProposer(7),
    Budget:      autoresearch.Budget{MaxExperiments: 6, MaxWallClock: 20 * time.Minute},
    Seed:        7,
    Journal:     autoresearch.OpenJournal(root),
    SnapshotDir: autoresearch.SnapshotDir(root),
})

res, err := r.Run(ctx)
fmt.Println(res.StopDetail)   // always say why it stopped
fmt.Println(res.RenderBest())
```

To add the optional prompt rewriter, wrap the deterministic proposer:

```go
rewrite := func(ctx context.Context, prompt string) (string, error) { /* your model */ }
proposer := autoresearch.NewLLMProposer(rewrite, autoresearch.NewDeterministicProposer(7))
```

`Rewriter` has the same shape as `memory.Summarizer`, so an orchestrator's
existing adapter fits with no glue.

---

## 12. What this does not do

* It does not change the model, the provider or the backend. Those change the
  system being measured rather than tuning a knob on it, and a "result" that
  came from swapping the model is not a harness result.
* It does not touch anything outside `.slmcode/`. Bundled blocks under
  `pkg/blocks/bundled/agents/` are read-only: they ship with the binary and are
  not a project's state.
* It does not commit, stash, reset or otherwise touch git.
* It does not run unless asked, and it does not write unless asked twice.
