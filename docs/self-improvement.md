# Self-improvement & memory

> *"This harness should be self-evolving and improving all the time so it only
> fails on one thing once and repairs itself, then evolves and gets better over
> time — like a self-improving loop or reinforcement learning. Plus long-term
> and short-term memory."*

That is the requirement. This page explains how it is implemented, what it
writes to disk, which knobs exist, and how to inspect or delete all of it.

Two packages do the work:

| Package | Responsibility |
|---|---|
| `pkg/memory` | Four layers of memory: working, episodic, semantic, procedural |
| `pkg/evolve` | Failure fingerprinting, repair rules, a policy bandit, reflection, regression checks |
| `pkg/eval/metrics` | Per-run metrics, baseline-vs-current comparison, offline replay |

Three rules apply to every part of it:

1. **Deterministic core, optional LLM.** Nothing here needs a model. Distillation
   and reflection accept an optional summarizer and are merely *better* with
   one — never dependent on a small model getting a summary right.
2. **Bounded, prunable, safe to be wrong.** Every collection has a hard cap,
   every store has a prune policy, and every read path returns a usable zero
   value on corrupt data. A memory system that grows without limit or crashes
   on a bad record is worse than none.
3. **Fully inspectable and reversible.** Plain JSON, JSONL and Markdown.
   `rm -rf .slmcode/memory .slmcode/evolve` is a supported operation.

---

## 1. Memory

### Working memory (short-term, run-scoped)

Lives in process for the duration of a run. Holds the current task, its focus
files, the last 24 tool calls with outcomes, open failures, decisions taken and
a compact rolling summary.

`RecordTool` is on the hot path after every tool call, so it does no I/O, no
token counting and no regular expressions — a handful of slice appends. Token
counting happens only when the block is rendered.

The rendered block projects onto `pkg/compact`'s `MustPreserve` schema, so a
compacted run and a fresh run present the same headings in the same order:
files read → files edited → commands and exit status → failed calls →
decisions.

```go
w := store.Working()
w.Start(runID, task, role)
w.Focus("pkg/http/client.go")
w.RecordTool(memory.ToolEvent{Tool: "ws_edit", Path: "…", OK: false, Error: "…"})
w.Resolve(fingerprint, "re-read then retried", "rule:rule_ab12")
block := w.Render(700) // tokens
```

Caps: 24 tool events, 12 files read, 12 files edited, 5 commands, 8 open
failures, 8 resolved failures, 5 decisions, 16 focus files.

### Episodic memory (long-term, per project)

One append-only JSONL record per completed task or turn: the query, plan, files
changed, tools used, commands run, failures and how each was resolved, gate
outcomes, tokens, wall time, model and a success verdict.

Recall uses a **BM25F-style lexical scorer** over the structured fields, not
embeddings. The reasoning: recall runs on every task start, must be
deterministic under CI, must work with zero embedding calls, and scores fields
(paths, tool names, tags) where exact token overlap is the signal — a path
token like `runner.go` is worth far more than its cosine similarity to
anything. Embeddings would also need a cache keyed on a model that can change
between runs, which is exactly the silent staleness this subsystem exists to
avoid. `pkg/retrieval` remains the right tool for prose-heavy code chunks;
episodes are not prose.

Precision is enforced two ways, because for a 7B model an
irrelevant-but-plausible memory is worse than no memory:

* a **coverage gate** — an episode must contain at least 34 % of the distinct
  query terms. (A raw BM25 threshold cannot do this: its scale depends on
  corpus size, so on a fresh project every term looks common and every score
  collapses. Coverage is corpus-independent.)
* a **relative floor** — matches scoring below 45 % of the best match are
  dropped, so one strong hit does not drag in three weak ones for company.

Scores are then decayed by recency (45-day half-life) and nudged up 15 % for
episodes that ended in success.

### Semantic memory (long-term, distilled)

Durable, deduplicated, confidence-scored facts about *this* project: build and
test commands that actually worked, the real module layout, conventions
observed, gotchas, per-file summaries.

Distillation is pure counting — no model:

| Fact kind | Derived from |
|---|---|
| `command` | commands seen ≥ 2 times with at least one success, plus their success ratio |
| `layout` | the directories that change most often |
| `file` | per-file change frequency |
| `gotcha` | resolved failures, keyed by fingerprint, with the fix that worked |
| `convention` | edit-format apply rate observed in this repo |
| `dependency` | the project's primary language |

Confidence is a Beta(1,1) posterior mean: `(support+1)/(support+contradict+2)`.
A single sighting scores 0.67, not 1.0 — a hint, not a law. Observing the same
subject with a *different* claim is a contradiction; once contradictions
outweigh support the fact's text is replaced and its counters reset. That is
how a fact decays when the project changes under it.

Numeric drift is not a contradiction: "works here (2/2 runs)" and "works here
(7/8 runs)" are recognised as the same claim with fresher arithmetic. Without
that, re-distilling would thrash the store back and forth forever.

Facts with `"pinned": true` are user-authored: never overwritten, never
refuted, never pruned. Edit `facts.json` by hand to add one.

The rendered block targets ≤ 400 tokens, grouped action-first (commands and
gotchas before layout and files), at most 6 facts per kind.

### Procedural memory (cross-project, user-scoped)

Under `~/.slmcode/memory/`: what works for a given **model family** and
**language**. Namespaced by both, so a Python project's lessons never pollute a
Go one and a lesson about `qwen2.5-coder` never leaks into `gpt-4o-mini`.

Model ids are folded to families by dropping quantization, parameter count and
serving-format suffixes at the first such token:

```
Qwen3-Coder-30B-A3B-Instruct-MLX-4bit → qwen3-coder
qwen2.5-coder:7b-instruct-q4_K_M      → qwen2.5-coder
deepseek-chat                         → deepseek
gpt-4o-mini                           → gpt-4o-mini
```

`Best(topic, family, language)` requires at least 3 observations before it will
recommend anything. Lookup widens (family, language) → (family, \*) → (\*,
language) → (\*, \*), never across languages before it has widened across
models.

---

## 2. Evolve: fail once, then never again

### Failure fingerprinting

Any failure becomes a stable `Fingerprint`:

1. **Normalize** the message — strip ANSI, cut stack traces, then replace
   timestamps, durations, URLs, IPs, hex addresses, hashes, paths, line:column
   pairs, quoted payloads and bare numbers with placeholders. Lowercase,
   collapse whitespace, cap at 300 bytes.
2. **Classify** into one of 23 classes (`edit_not_found`, `edit_ambiguous`,
   `edit_line_numbers`, `file_not_read`, `malformed_json`, `truncated_output`,
   `compile_error`, `test_failure`, `timeout`, `context_overflow`,
   `provider_error`, `no_progress`, `permission_denied`, …). Needle matching
   uses word boundaries for bare words, so the identifier `waveTimeout` is not
   mistaken for a network timeout.
3. **Hash** class + tool + language + model family + a *salient* string.

The salient string is the interesting part. For **structural** classes — every
`old_str not found` is the same problem regardless of which file or which text
missed — the message is excluded from the hash entirely, so superficially
different messages collapse to one fingerprint. For **content** classes
(compile errors, test failures) the normalized message participates, so
`undefined: alpha` and `undefined: beta` stay distinct while the same
`undefined: alpha` in two different files collapses.

### Repair rules

A rule is `{Fingerprint, Trigger, Repair, Evidence, Successes, Failures,
Confidence, CreatedAt, LastUsed, Scope}`.

`Repair` is a small typed union, not free text, because the point is for the
harness to *apply* a remembered fix rather than describe it to a model and hope:

| Kind | Effect | Costs an LLM call? |
|---|---|---|
| `guidance` | inject text into the next prompt | yes (a targeted one) |
| `transform_args` | rewrite the failed call's arguments with a named transform | **no** |
| `switch_tool` | retry with a different tool | no |
| `edit_format` | switch edit format for the retry | no |
| `config` | change a config knob | no |
| `shell` | propose a fixup command (run by the harness under the permission system) | no |
| `action` | a named recovery: re-read, compact, raise max_tokens, split, back off… | no |

Named argument transforms: `strip_line_number_prefix`, `set_replace_all`,
`trim_trailing_whitespace`, `unfence_code`, `repair_json`, `shrink_old_str`.

**Confidence** is a Beta posterior mean. Seeded rules start at Beta(4,1) ≈ 0.80
— believed, because they encode failure modes we already understand.
Synthesized rules start at Beta(1,2) ≈ 0.33 — below the 0.45 apply bar, so a
guess is *suggested* but not *applied* until it has proved itself. Rules gain
confidence on success and lose it on failure; below 0.18 **and** with at least
4 samples they retire themselves. The sample floor is the guardrail: one
unlucky early result cannot silently kill a good repair.

Lookup is exact-fingerprint first, then trigger patterns, ordered by confidence
then trigger specificity.

#### The shipped rule set

These make the harness useful on day one:

| Failure | Repair |
|---|---|
| `ws_read` line-number gutter leaked into `old_str` | `transform_args: strip_line_number_prefix`, retry |
| `old_str` not found | `action: reread_file` — re-read, copy 2–3 lines verbatim, retry with a smaller uniquely-anchored span |
| `old_str` missed on whitespace only | `transform_args: trim_trailing_whitespace`, retry |
| `old_str` found N times | `guidance` — add surrounding context for a unique anchor; `replace_all` only if you mean it |
| `old_str` empty | `guidance` — `ws_write` to create, anchor on the last lines to append |
| No-op edit (`old_str == new_str`) | `guidance` — make a real change or finish |
| File edited before being read | `action: reread_file`, retry |
| Multi-hunk diff failure | `edit_format: search_replace` — then whole file as a last resort |
| JSON truncated by `max_tokens` | `action: raise_max_tokens` — never guess past a truncation |
| Malformed JSON | `transform_args: repair_json`, retry |
| Context overflow | `action: compact_context`, retry |
| Repeated identical tool call | `action: force_different_action` |
| Reviewer rejected repeatedly | `action: split_task` |
| Shell command not permitted | `guidance` — propose the allowed equivalent, do not retry |
| Path does not exist | `action: reread_file` — list before assuming a layout |
| Missing tool/module | `guidance` — report it, do not reimplement it |
| Rate limited | `action: backoff_retry` |
| Timeout | `action: split_task` |
| Go: `declared and not used` | `guidance` |
| Go: `undefined: X` | `action: reread_file` — grep before inventing |
| Python: indentation error | `transform_args: unfence_code`, retry |

To disable a shipped rule, set `"retired": true` on it in `rules.json`.
*Deleting* it does not work — seeds are re-merged on every load.

### Policy learning: a bandit over harness choices

`pkg/evolve` runs a contextual multi-armed bandit keyed on
`(decision, model family, language)` over the harness's discrete choices: edit
format, which model handles a role, thinking passes, whether to run the explore
phase, retry-ladder ordering, review strictness.

**Thompson sampling over Beta posteriors**, not UCB1. Four reasons:

* the reward is naturally a bounded [0,1] score, which makes Beta conjugate —
  an O(1) update and two floats per arm, both legible in the JSON you are
  invited to read;
* the sample counts are tiny. One developer on one project produces tens of
  observations per arm, not thousands. UCB1's confidence bound is only
  meaningful once every arm has been pulled and over-explores badly in the
  low-n regime — which here means deliberately using an edit format you already
  know applies 60 % of the time;
* warm starting is exactly expressible — a prior *is* "pretend we already saw α
  successes and β failures", so shipped defaults and learned evidence live on
  the same scale;
* deterministic mode is a one-line change (argmax of the posterior mean), and
  CI must be reproducible.

**Reward function**, in [0,1]:

```
correctness = 0.60·applied + 0.25·gate_passed + 0.15·(1 − min(retries,3)/3)
cost        = 0.50·token_efficiency + 0.50·time_efficiency
reward      = 0.85·correctness + 0.15·cost
hard failure ⇒ reward capped at 0.10
```

A gate that did not run scores 0.5 (neutral). Unknown budgets score 0.5, never
a bonus and never a penalty. Correctness outweighs cost roughly six to one on
purpose: the harness must never learn to prefer a cheaper option that produces
broken code. Cost exists only to break ties between options that work equally
well.

The Beta update is `α += r; β += 1 − r`, which keeps the posterior mean an
unbiased estimate of expected reward for a bounded reward.

**Warm start.** Shipped priors, worth a handful of pseudo-observations each, so
a fresh install behaves sensibly immediately:

```
edit_format:  search_replace β(8,2)   unified_diff β(3,5)   whole_file β(4,4)
think_passes: 1 β(5,3)   2 β(5,4)   3 β(3,5)
explore:      on β(6,3)   off β(4,4)
review:       normal β(6,3)   strict β(4,4)   lenient β(3,5)
```

**Guardrails against locking in a bad arm:**

* prior pseudo-counts are never removed, so no arm can be driven to certainty
  by a handful of samples;
* every arm must be pulled twice (`MinPulls`) before sampling takes over;
* an explicit ε starts at 0.20 and decays with a 40-pull half-life, but never
  below 0.02 — a model upgrade or a refactor can change the answer, so a little
  exploration is permanent;
* once a key passes 200 pulls its posterior is decayed halfway back toward its
  prior, keeping it responsive and bounding the numbers on disk.

**Deterministic mode** (`EngineOptions{Deterministic: true}`, the `--no-explore`
knob) replaces sampling with a greedy argmax and disables ε entirely. Runs are
then bit-for-bit reproducible.

**Explaining a choice:**

```
$ (via Bandit.Why)
edit_format (model qwen2.5-coder, language go) — 37 observations
→ search_replace       91% ±4%   (28 pulls, α=26.4 β=2.6)
  whole_file           62% ±11%  (6 pulls,  α=5.1 β=3.1)
  unified_diff         41% ±13%  (3 pulls,  α=3.4 β=4.9)
mode: Thompson sampling, ε=0.11
```

### Reflection

After each run, `Reflect(RunReport)` deterministically compares intent with
outcome — tasks planned vs done, gates passed, retries, tokens, wall time, and
every failure with how it was resolved — and emits:

* an `Episode` for `pkg/memory`;
* **candidate repair rules**, synthesized only from failures that were fixed
  *without* an existing rule (those are the ones we do not yet know);
* **bandit rewards** for the choices the run made;
* **regression checks** for failures that were fixed;
* `.slmcode/memory/REFLECTION.md`, a human-readable report.

An optional summarizer appends a "Model commentary" section labelled *advisory
only*. It is strictly additive — an error, a timeout or an empty answer leaves
the computed report byte-for-byte unchanged.

### Regression memory

Every fixed failure is recorded with, where one exists, a cheap way to prove it
has not come back: a command, a "file contains", a "file absent", or a "file
exists" assertion. `Regressions().Checks()` hands them to the harness.

`evolve` never executes a command itself — the harness runs those under the
permission system. `RunOffline(root)` evaluates only the file-based checks,
which are safe.

---

## 3. Measurement

`pkg/eval/metrics` writes one record per run to `.slmcode/metrics/runs.jsonl`:

* task pass rate
* **edit-format apply rate** — first-class, because for a small model
  edit-format compliance *is* the bottleneck: a plan that is right and an edit
  that will not apply produce exactly zero working code. Aider's leaderboard
  reports "% of responses using the correct edit format" next to task success
  for the same reason.
* tool error rate, redundant-call rate
* LLM calls per task, tokens in/out, wall time
* gate outcomes
* repair-rule hit rate
* how many failures were resolved **from memory** vs from a **fresh LLM
  round-trip**

`Compare(baseline, current)` renders a Markdown delta. Rates are *pooled* (sum
of numerators over sum of denominators), not averaged per run — averaging rates
over runs of different sizes silently overweights the small ones. A metric with
no data on either side reports "no data" rather than a fabricated zero.

```
## Metrics: 12 baseline run(s) → 12 current run(s)

| Metric | Baseline | Current | Change |
|---|---:|---:|---:|
| task pass rate | 58.3% | 75.0% | +16.7 pp ✅ |
| edit-format apply rate | 61.0% | 92.0% | +31.0 pp ✅ |
| failures fixed from memory | 0.0% | 68.0% | +68.0 pp ✅ |
| LLM calls per task | 7.20 | 4.90 | −2.30 ✅ |

**Verdict: improved.**
```

### Offline replay

A stored **trajectory** is a recording of what a model actually emitted — tool
calls, arguments, results — plus, for each failed step, the arguments that
eventually worked. Replaying it against a `Repairer` (satisfied by
`*evolve.Rules`) answers one precise question with no live model:

> how many of these failures would this repair store have fixed
> deterministically, and how many would still have cost a round-trip?

```go
fixtures, _ := metrics.LoadTrajectories("testdata/trajectories")
cmp := metrics.ABTest(fixtures, rules, "qwen2.5-coder")
fmt.Println(cmp.Render())
```

Both arms must land the same edits — the repair saves cost, it does not change
correctness. If it changed correctness the A/B would be measuring two things.

---

## 4. On-disk layout

Everything is human-readable and safe to edit, version-control or delete.

```
<project>/.slmcode/
├── memory/
│   ├── episodes.jsonl          one JSON object per completed task/turn
│   ├── episodes.index.json     searchable projection + byte offsets
│   ├── facts.json              semantic memory (distilled, confidence-scored)
│   ├── SEMANTIC.md             human-readable mirror of facts.json
│   ├── WORKING.md              last run's short-term state (debug only)
│   └── REFLECTION.md           last run's intent-vs-outcome report
├── evolve/
│   ├── rules.json              project-scoped + builtin repair rules
│   └── regressions.json        fixed failures and their re-checks
└── metrics/
    └── runs.jsonl              one metrics record per run

~/.slmcode/
├── memory/
│   ├── procedures.json         cross-project: what works per model + language
│   └── PROCEDURES.md           human-readable mirror
└── evolve/
    ├── rules.json              user-scoped repair rules (model-level lessons)
    └── policy.json             bandit posteriors
```

All writes go through `pkg/internal/atomicfile` (temp file + rename), except
the two append-only JSONL logs, which use a single `write(2)` per record — on
POSIX a sub-`PIPE_BUF` append is atomic, so a crashed run leaves whole records,
never a spliced one.

A file that fails to parse is moved aside to `<name>.corrupt` and the store
starts clean, with the problem reported through `Warnings()`. A corrupt line in
a JSONL log is skipped; the records either side of it survive. A stale index is
detected and rebuilt from the log.

### Bounds

| Store | Cap | Prune policy |
|---|---|---|
| Episodes | 300 records | also drops anything older than 180 days; the JSONL log is rewritten so the file shrinks too |
| Facts | 200 | drops confidence < 0.25 and anything unseen for a year; pinned facts are exempt |
| Procedures | 400 | drops entries unused for a year |
| Repair rules | 400 | drops retired and unused-after-a-year learned rules; seeded rules are never removed |
| Bandit keys | 300 | least-used first |
| Regression checks | 200 | oldest first |
| Metrics log | 2000 runs | oldest first |

---

## 5. Knobs

| Knob | Where | Effect |
|---|---|---|
| `evolve` | config / `--evolve` / `--no-evolve` | turn the whole subsystem on or off (default on) |
| `deterministic` | config / `--no-explore` | greedy policy, no exploration — for CI and reproducible runs; `dry_run` implies it |
| `memory_tokens` | config | token budget for the injected memory block (default 300) |
| `regression_checks` | config | replay stored regression checks around the QA gate |
| `EngineOptions.Deterministic` | `evolve.OpenWith` | the library-level form of `deterministic` |
| `EngineOptions.Seed` | `evolve.OpenWith` | reproducible exploration |
| `EngineOptions.ReadOnly` | `evolve.OpenWith` | open every store without writing |
| `EngineOptions.ProjectPolicy` | `evolve.OpenWith` | keep bandit posteriors in the project instead of `~` |
| `EngineOptions.NoSeedRules` | `evolve.OpenWith` | start with no shipped repair rules |
| `memory.Limits` | `memory.OpenWith` | per-store caps and per-layer token budgets |
| `memory.PrunePolicy` | `Store.Prune` | ages and counts |
| `evolve.RulePolicy` | `Rules.Prune` | rule-store bounds |
| `Query.MinCoverage` / `MinScore` | `RecallEpisodes` | recall precision |
| `"pinned": true` | `facts.json` | a fact you wrote that must never be overwritten or pruned |
| `"retired": true` | `rules.json` | disable a repair rule (including a shipped one) |

---

## 6. Inspecting and resetting

### From the CLI

```bash
slmcode memory show --role worker      # the memory block a role actually receives
slmcode memory show --budget 500       # …at a different token budget
slmcode memory episodes 20             # the most recent runs the harness remembers
slmcode memory facts --kind command    # distilled semantic facts, filtered by kind
slmcode memory forget episodic --yes   # working|episodic|semantic|procedural|project|all

slmcode evolve rules                   # repair rules with confidence and hit counts
slmcode evolve rules --all             # include seeded-but-unused and retired rules
slmcode evolve why edit_format         # the posterior table behind a learned choice
slmcode evolve regressions             # stored regression checks and their status
slmcode evolve regressions --run       # replay the offline (file-based) checks now
slmcode evolve reset --yes             # rules, policy, regressions and memory

slmcode metrics show                   # the latest run
slmcode metrics show --last 10         # …plus an aggregate over the last 10
slmcode metrics compare 12             # newest 12 runs vs the 12 before them
```

Every one of these takes `--json`.

### From the shell

```bash
cat .slmcode/memory/SEMANTIC.md        # distilled project facts
cat .slmcode/memory/REFLECTION.md      # what happened last run
cat ~/.slmcode/memory/PROCEDURES.md    # what works for your model
jq . .slmcode/evolve/rules.json        # repair rules and their confidence
jq . ~/.slmcode/evolve/policy.json     # bandit posteriors
jq -s 'length' .slmcode/metrics/runs.jsonl
```

Forget selectively, in code:

```go
store.Forget(memory.ScopeWorking)     // this run only
store.Forget(memory.ScopeEpisodic)    // the run log
store.Forget(memory.ScopeSemantic)    // distilled facts
store.Forget(memory.ScopeProcedural)  // cross-project model lessons
store.Forget(memory.ScopeProject)     // episodic + semantic
store.Forget(memory.ScopeAll)         // everything, including ~/.slmcode
engine.Forget(memory.ScopeAll)        // the above plus rules, policy, regressions
```

Or by hand — this is fully supported and breaks nothing:

```bash
rm -rf .slmcode/memory .slmcode/evolve .slmcode/metrics
rm -rf ~/.slmcode/memory ~/.slmcode/evolve
```

The next run starts from the shipped repair rules and the shipped bandit priors
— which is to say, it behaves exactly like a fresh install, and then starts
learning again.
