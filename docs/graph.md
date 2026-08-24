# Knowledge graph

The harness has always written down how its records relate to each other. A
distilled fact names the episodes it came from. A repair rule names its
evidence. An episode names the files it changed, the failures it hit and the
run it belonged to. Every one of those references was stored as a string that
nothing followed — `Fact.Sources`, `Rule.Evidence`, `Episode.FilesChanged`,
`FailureNote.ResolvedBy`, `Episode.RunID` — so the obvious question, *what has
broken in this file before and what fixed it*, could not be asked at all.

`pkg/graph` materializes those references as typed edges and gives them a
bounded, deterministic traversal. It is one package, one directory on disk, and
one command: `slmcode graph`.

---

## 1. What this is, and what it is not

**It is an index over records the harness already wrote.** The graph invents no
data. Every edge in it exists because some episode, fact or rule already
contained the reference; `slmcode graph backfill` reads those records and turns
each reference into an edge. Delete the whole store and one backfill brings it
back, byte for byte.

**It is not entity extraction over your source code.** Nothing here parses your
files, resolves imports, or builds a call graph. A `file:` node is a path that
appeared in an episode's `files_changed` list — evidence that the harness
touched it, not a claim about what is in it. If the harness has never run over a
file, the graph knows nothing about it, and says so.

**It deliberately does no fuzzy matching and no entity resolution.** A node is an
opaque typed string and edges join exact ids only: no aliasing, no similarity
threshold, no "these two are probably the same thing". That is the design
decision that makes the classic knowledge-graph failure mode — the false merge,
where two unrelated entities are silently fused and every query downstream
inherits the lie — structurally impossible here. The cost is real: a reference
the backfill cannot resolve exactly is **dropped**, so the graph will sometimes
be missing an edge you know should exist. That is the trade taken on purpose. A
missing edge is a gap you can see. A wrong edge is a lie that gets believed
forever, by a model, in every prompt after it.

The same three rules that govern `pkg/memory` and `pkg/evolve` apply
([self-improvement](self-improvement.md)): deterministic core with no LLM
anywhere in it, bounded and prunable, fully inspectable and reversible.

---

## 2. Why it earns its cost

Because the interesting questions span stores, and no single store can answer
them.

"What failure classes have we seen in `pkg/loop/runner.go`, and which rule fixed
them" requires joining four things that live in three different files: the
episodes are in `memory/episodes.jsonl`, the files each one changed are a list
inside those records, the failure fingerprints are another list inside the same
records, and the rules are in `evolve/rules.json`. Answering it by hand means
scanning every episode for the path, collecting fingerprints, then grepping the
rule store for each one. That is `O(episodes)` per question, it is written
freshly and slightly differently by every caller that wants it, and it is
exactly the kind of code that quietly stops matching the record format.

The graph does that join once, as a traversal:

```
file: <-touched- episode: -produced-> failure: -resolved_by-> rule:
```

and `graph.KnownAboutFile(store, path)` is the whole query. Every hop checks the
node kind at the far end, so a hand-added edge of the right type between the
wrong kinds of node cannot smuggle a run id into the episode list.

The second reason is the harness's own retrieval. Cross-store recall — "before
you edit this file, here is what has gone wrong in it" — is a traversal, and a
traversal is a thing you can bound. Text search over the same records is not.

---

## 3. Node ids

A node id is `<kind>:<value>`. Exact, typed, opaque. Ids are minted by
constructors (`FileNode`, `EpisodeNode`, …), never assembled by hand, and are
capped at 512 bytes — an id is a path, a hash or a record id, never prose.

| Kind | Id shape | Minted from |
|---|---|---|
| `file` | `file:<repo-relative path>` | `Episode.FilesChanged` |
| `episode` | `episode:<episode id>` | `Episode.ID`, `Fact.Sources`, `Rule.Evidence` |
| `run` | `run:<run id>` | `Episode.RunID`, `Rule.Evidence` |
| `task` | `task:<run id>/<task id>` | a reference that is already a node id |
| `attempt` | `attempt:<run id>/<task id>/<n>` | a reference that is already a node id |
| `rule` | `rule:<rule id>` | `FailureNote.ResolvedBy` |
| `fact` | `fact:<fact id>` | `Fact.ID` |
| `failure` | `failure:<fingerprint>` | `FailureNote.Fingerprint` |
| `symbol` | `symbol:<file>#<name>` | nothing yet — vocabulary for callers |
| `commit` | `commit:<sha>` | nothing yet — vocabulary for callers |
| `artifact` | `artifact:<path>` | nothing yet — vocabulary for callers |

Paths are canonicalized but never resolved: `pkg/x.go` and `./pkg/x.go` are the
same node, `pkg/x.go` and an absolute path to the same file are **not**. This is
the exact-identity rule again — canonicalization is a syntactic rewrite that
cannot be wrong, resolution touches the filesystem and can be.

A failure fingerprint *is* a failure class. `pkg/evolve` hashes the class, tool,
language, model family and salient message into it, so two `failure:` nodes on a
file mean two distinct ways that file has broken.

---

## 4. Edge vocabulary

An edge is one typed, directed, dated link. It is a pointer between two records,
never a payload: 20 000 of them have to load instantly.

| Type | Direction | Meaning |
|---|---|---|
| `derived_from` | fact → episode, rule → episode/run | where a claim came from. Follow it backwards to audit one |
| `resolved_by` | failure → rule | what fixed this failure class |
| `touched` | episode → file | this record changed that file |
| `produced` | episode → failure/artifact | this record created that |
| `parent_of` | run → episode | containment (run → task → attempt) |
| `contradicts` | claim → claim | two claims that cannot both hold |
| `supersedes` | record → record | this one replaces that one |
| `evaluated_by` | work → gate/check | what judged it |
| `depends_on` | file/symbol → file/symbol | a code dependency |
| `mentions` | record → anything | a weaker `touched`: referenced, not changed |

The first five are what `Backfill` emits today. The last five are vocabulary:
`Store.Add` accepts any non-empty type, so a caller can record something new
without a release, but nothing in the shipped harness writes them yet and
`slmcode graph stats` will never show them on a store built purely by backfill.
Notably **`depends_on` is not populated** — the graph is not a code index.

Edge fields:

| Field | Notes |
|---|---|
| `from`, `to` | node ids. Blank ends and self edges are rejected |
| `type` | lowercased |
| `at` | UTC, truncated to the second |
| `run_id` | the run that observed the edge, when known |
| `confidence` | `[0,1]`; **0 means unset**, not "certainly false". Backfilled edges leave it unset — they are facts about the log, not beliefs about the world |
| `note` | free-form, ≤ 240 bytes, for humans reading the JSONL |

**Edges are content-addressed on `(from, to, type)`.** The id is
`e_` + the first 12 hex characters of the SHA-256 of those three fields.
Re-observing an edge is not a new edge, it is the same edge seen again: the
timestamp, confidence, run id and note are refreshed in the index and no new
line is written to the log. That is precisely what lets a run backfill after
every single turn without the file growing.

---

## 5. What the backfill materializes

`Backfill(root)` reads the stores and derives:

| Record field | Edge |
|---|---|
| `Episode.RunID` | `run` → `episode`, `parent_of` |
| `Episode.FilesChanged` | `episode` → `file`, `touched` |
| `Episode.Failures[].Fingerprint` | `episode` → `failure`, `produced` (note: the failure class) |
| `FailureNote.ResolvedBy` | `failure` → `rule`, `resolved_by` (note: the resolution) |
| `Fact.Sources` | `fact` → `episode`/`run`, `derived_from` (note: the fact kind) |
| `Rule.Evidence` | `rule` → `episode`/`run`, `derived_from` |

Four details worth knowing, because each one is a case where the backfill
declines to guess:

- **`ResolvedBy` only yields an edge when it names a rule.** The field also holds
  `llm`, `retry` or `human` when no rule was involved; those produce nothing. A
  failure class that shows up with no `resolved_by` edge has genuinely never been
  fixed by a rule, and `slmcode graph file` calls that out.
- **Rule evidence is matched exactly against records actually seen.** It is
  documented as an episode or task id, but `pkg/evolve` also writes *run* ids into
  it. Classifying by shape would mint `episode:run-1724…` nodes that name
  nothing, so references are resolved against the ids this backfill really saw,
  falling back only to the two unambiguous id prefixes (`ep_`, `run-`). Anything
  else is dropped.
- **Seeded repair rules are skipped.** A shipped rule has no evidence, so merging
  the seed set in would only add work.
- **Only project records are read.** The user-scoped memory directory is pointed
  at the project on purpose: nothing the backfill needs lives under `$HOME`, and
  a derived project index should not read — or, on a corrupt file, quarantine —
  your global store.

Backfill is idempotent by construction and runs automatically at the end of a
turn, best-effort: the graph is derived data, so losing it costs one backfill and
must never cost a run.

---

## 6. On-disk layout

```
<project>/.slmcode/graph/
├── edges.jsonl        append-only edge log — the source of truth
└── edges.index.json   rebuildable adjacency index
```

The log is one JSON object per line, appended with a single `write(2)` per
record: on POSIX a sub-`PIPE_BUF` append is atomic, so a crashed run leaves whole
records, never a spliced one. The index is written through
`pkg/internal/atomicfile` (temp file + rename).

The index is a *lossless* projection — unlike an episode, an edge is small
enough that splitting it between index and log would buy nothing and cost a seek
per lookup. It keeps the byte offset anyway, because that is what makes a stale
index detectable and a prune able to rewrite the log. It also stores the
adjacency lists, so the file is directly greppable:

```bash
jq '.in["file:pkg/loop/runner.go"]' .slmcode/graph/edges.index.json
jq -s 'length' .slmcode/graph/edges.jsonl
jq 'select(.type == "resolved_by")' .slmcode/graph/edges.jsonl
```

They are recomputed from the entries on load, which is O(n) with no I/O and
cannot disagree with the entries they are built from.

### Durability contract

A `Store` is always usable. `Open` never fails because of a corrupt or unwritable
store: it degrades to in-memory operation and records the problem in
`Warnings()`, which every subcommand prints.

| Damage | What happens |
|---|---|
| Corrupt line in `edges.jsonl` | skipped; the records either side survive; counted in a warning |
| Line longer than 32 KB | treated as corrupt, so a damaged file cannot exhaust memory |
| Unparseable `edges.index.json` | moved aside to `edges.index.json.corrupt`, rebuilt from the log |
| Stale index (log written behind its back) | detected by spot-checking the newest entry's offset, rebuilt from the log |
| Hand-edited index with duplicates | deduplicated on load |
| Directory unwritable | the graph disables itself and warns; nothing else fails |
| `rm -rf .slmcode/graph` | **supported**; the next backfill rebuilds every edge |

Read paths never write. `slmcode graph stats|file|neighbors|walk` open the store
read-only, which means no directory creation, no append, no index flush and no
quarantine — a corrupt index is reported and rebuilt *in memory*, and the file on
disk is left exactly as it was for you to look at. The `.corrupt` rename happens
on the next command that writes.

### Bounds

| Axis | Default | Behavior |
|---|---|---|
| Edges | 20 000 | `Prune` keeps the newest; a hard backstop prunes automatically if a caller that never prunes reaches 2× the cap |
| Age | 180 days | edges not re-observed within the window are dropped |
| Node id | 512 bytes | clipped |
| Note | 240 bytes | clipped |
| Log line | 32 KB | longer lines are read as corrupt |
| Walk depth | 3, hard cap 6 | past six hops in a graph this small everything reaches everything |
| Walk nodes | 200, hard cap 2 000 | the origin included |

A prune **rewrites the JSONL log** rather than marking entries dead, so the file
actually shrinks; the rewrite is atomic, and a crash mid-prune leaves the
previous log intact. An append-only log that is never compacted is an unbounded
log, which would make every claim about the ceiling a lie.

Pruning is safe in a way that pruning episodes is not: every dropped edge is
derivable again from the record that implied it, so `slmcode graph backfill`
restores anything whose source record still exists.

---

## 7. Traversal, and why it is bounded

`Walk` is breadth-first, cycle-safe (each node is reached once, by the shortest
path found first) and deterministic: neighbors are expanded in sorted order, and
`Out`/`In` sort by the far end then the type, so the same store and the same
flags print the same paths in the same order regardless of the order the edges
were written in. The origin is not returned — a zero-length path to yourself is
noise.

Both axes are capped and the caps are not negotiable from the outside. This
traverses a graph the harness derived from its own logs, where one wrong edge
would otherwise drag a result — and then a prompt — through the entire store.
The default depth of 3 is not arbitrary: it is exactly the chain that matters,
file → episode → failure → rule.

---

## 8. The CLI

```bash
slmcode graph stats                      # edge counts by type, nodes, file size, warnings
slmcode graph file pkg/loop/runner.go    # what the harness knows about a file
slmcode graph neighbors <node>           # one hop, with --dir and --type
slmcode graph walk <node>                # bounded traversal, with --depth and --type
slmcode graph backfill                   # materialize edges from memory + evolve records
slmcode graph prune --max-age 720h       # bound the store; rewrites the log
slmcode graph forget --yes               # delete .slmcode/graph
```

Every subcommand takes `--json`. Every read path opens the store read-only, so
inspecting the graph can never change it.

`<node>` is a node id (`episode:ep_7f3a91`, `failure:fp_2c81d0`) or a
repo-relative path, which is read as `file:<path>`. `--type` is repeatable and is
validated against the vocabulary: a typo would silently return nothing, which
looks exactly like an empty graph, so it is rejected instead.

`--dir` defaults to `either` on `neighbors` and `walk`. Most node kinds are
reached from one side only — every edge on a file node is *incoming* — so an
outgoing-only listing of a file would always be empty, and the walk renderer
writes `<-touched-` for a hop followed backwards so the arrow never states the
reverse of what the store holds.

### An empty graph

Not an error, and not an empty table. A fresh workspace has nothing to index yet:

```
$ slmcode graph stats

▸ Graph
─────────
  Typed edges over records the harness already wrote: which episode touched
  which file, what failed there, which rule resolved it. Derived data — the
  store can be deleted and `slmcode graph backfill` rebuilds every edge.

  dir             /home/you/project/.slmcode/graph
  edges           0 of 20000 max
  nodes           0
  log size        0 B

  (no edges yet — the graph is derived from memory and evolve records)
  run `slmcode graph backfill` to materialize them, or `slmcode run` once
```

### Filling it

```
$ slmcode graph backfill
✔ materialized 13 new edge(s) — 13 total

$ slmcode graph backfill
✔ graph up to date — 13 edge(s), nothing new to materialize
```

The second invocation is the point: edges are content-addressed, so re-running
adds nothing and leaves the log byte-identical.

```
$ slmcode graph stats

▸ Graph
─────────
  …

  dir             /home/you/project/.slmcode/graph
  edges           13 of 20000 max
  nodes           10
  log size        1.7 KB

  derived_from     4
  resolved_by      1
  touched          3
  produced         3
  parent_of        2
```

### What do we know about this file

The command the package exists for:

```
$ slmcode graph file pkg/loop/runner.go

▸ Graph: pkg/loop/runner.go
─────────────────────────────
  episodes       2
      ep_7f3a91
      ep_a02e55

  failures       2
      fp_2c81d0                seen 2×
        fixed by rule_reread_file
      fp_9b4417                seen 1×
        ⚠ no rule has resolved this class

  rules          rule_reread_file
  inspect one with `slmcode evolve rules`
```

Two failure classes, two distinct ways this file has broken. One of them the
harness can now repair without a model round-trip; the other has happened and
nothing has ever fixed it, which is the line worth a human's attention. The ids
are bare (`ep_7f3a91`, not `episode:ep_7f3a91`) so they can be handed straight to
`slmcode memory episodes` and `slmcode evolve rules`.

`--json` carries the same join, plus how often each class was seen:

```json
{
  "episodes": ["ep_7f3a91", "ep_a02e55"],
  "failures": ["fp_2c81d0", "fp_9b4417"],
  "file": "pkg/loop/runner.go",
  "fixed_by": { "fp_2c81d0": ["rule_reread_file"] },
  "node": "file:pkg/loop/runner.go",
  "rules": ["rule_reread_file"],
  "seen_counts": { "fp_2c81d0": 2, "fp_9b4417": 1 },
  "warnings": null
}
```

A fingerprint in `failures` but absent from `fixed_by` has never been fixed by a
rule. That asymmetry is the useful part of the payload.

### One hop

```
$ slmcode graph neighbors episode:ep_a02e55

▸ Neighbors: episode:ep_a02e55
────────────────────────────────
  direction       either
  types           -

  ← derived_from   fact:f_1d9c22  2d ago
      gotcha
  → produced       failure:fp_2c81d0  2d ago
      edit_apply
  → produced       failure:fp_9b4417  2d ago
      gate_qa
  → touched        file:pkg/loop/runner.go  2d ago
  ← derived_from   rule:rule_reread_file  5d ago
  ← parent_of      run:run-1755760000  2d ago

  6 edge(s), 6 distinct neighbor(s)
```

Read the incoming arrows as provenance: the fact and the repair rule both cite
this episode as their evidence. `--type derived_from` alone answers "what did
this run teach the harness".

### Traversal

```
$ slmcode graph walk pkg/loop/runner.go --depth 2

▸ Walk: file:pkg/loop/runner.go
─────────────────────────────────
  direction       either
  depth           2 (max 6)
  types           -

  1 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91
  1 file:pkg/loop/runner.go <-touched- episode:ep_a02e55
  2 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91 <-derived_from- fact:f_1d9c22
  2 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91 -produced-> failure:fp_2c81d0
  2 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91 -touched-> file:pkg/loop/retry.go
  2 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91 <-derived_from- rule:rule_reread_file
  2 file:pkg/loop/runner.go <-touched- episode:ep_7f3a91 <-parent_of- run:run-1755590000
  2 file:pkg/loop/runner.go <-touched- episode:ep_a02e55 -produced-> failure:fp_9b4417
  2 file:pkg/loop/runner.go <-touched- episode:ep_a02e55 <-parent_of- run:run-1755760000

  9 node(s) reached
```

The leading number is the path depth. `pkg/loop/retry.go` at depth 2 is the
co-change signal — a file that keeps being edited in the same turn as this one.
Asking for more than the ceiling says so rather than silently obeying:

```
$ slmcode graph walk failure:fp_2c81d0 --depth 12
  …
  depth           6 (max 6)
⚠ --depth clamped to the package ceiling of 6
```

### Bounding and deleting

```
$ slmcode graph prune --max-age 48h
✔ dropped 13 edge(s) — 0 remaining

$ slmcode graph backfill
✔ materialized 13 new edge(s) — 13 total
```

Pruning is not destruction: the records that implied those edges are untouched,
so a backfill restores them. Deleting the store is the same story, and needs the
same confirmation flag `slmcode evolve reset` does:

```
$ slmcode graph forget --yes
✔ edge index deleted — `slmcode graph backfill` rebuilds it
```

`slmcode graph forget --json` refuses without `--yes`: a scripted invocation has
no prompt to answer. By hand, this is equivalent and equally supported:

```bash
rm -rf .slmcode/graph
```

---

## 9. When this is not worth it

Honest limits, in the spirit of the rest of these docs:

- **On a repo with a handful of runs, it tells you nothing you cannot see.**
  Below roughly a dozen episodes, `slmcode memory episodes 20` shows the same
  information by eye and the graph is a slower way to read it. The join only pays
  once there are enough episodes that scanning them is work.
- **It needs failures to be interesting.** The `failure → rule` chain is the part
  that earns its keep, and it only accumulates if the harness has actually hit
  and repaired failures in this project. On a repo where every run succeeds
  first try, the graph is a list of which files were edited together — real, but
  thin.
- **It says nothing about code the harness has not run over.** No AST, no
  imports, no call graph; `depends_on` and `symbol:` exist in the vocabulary and
  nothing populates them. If you want "what depends on this function", this is
  the wrong tool and always will be.
- **It is derived, so it is never the thing to back up.** Version-controlling
  `.slmcode/graph` is wasted diff noise. Back up `memory/` and `evolve/`; the
  graph is one command away from those.
- **`rm -rf` is a legitimate answer.** If the store looks wrong, do not debug the
  index — delete it and backfill. That path is tested, and it is why the index is
  allowed to be a cache rather than a database.

The costs, so you can weigh them: roughly 130 bytes per edge on disk (13 edges,
1.7 KB in the worked example above), a full load of the index into memory at
open, and one pass over already-loaded records at the end of a turn. The ceiling
is 20 000 edges — a few megabytes at most, prunable, and rebuildable from
scratch.
