# 📐 Context engineering & the repo map

A 32K model that receives 3.2K tokens of context is a 3.2K model. Getting the right text in front
of a small model, in a stable order, under a real budget, is most of what this harness does.

Packages: `pkg/context` (budget + packs + excerpts), `pkg/repomap` (symbol map),
`pkg/compact` (compaction), `pkg/skills` (progressive disclosure),
`pkg/instructions` (AGENTS.md/CLAUDE.md), `pkg/retrieval` (embeddings).

---

## 1. The budget is in tokens

The pack budget is the model's real context window **minus everything else that shares it**,
which the packer does not otherwise see:

```
available = context_limit
          − reserve_system      (500  — the specialist system prompt)
          − reserve_tools       (900  — the ws_* JSON schemas)
          − reserve_response    (2048 — the model still has to answer)
          − slack               (10%  — tokenizer disagreement + chat scaffolding)
```

`context_limit` comes from the model profile (`model_profiles`), resolved exact → family
substring → size bucket → default:

| Profile | Context limit | Max tokens | Max turns |
|---|---:|---:|---:|
| `1.5b`, `3b` | 4096 | 1536 / 1792 | 12 / 14 |
| `7b` | 8192 | 2048 | 16 |
| `default`, `14b` | 16384 | 3072 / 4096 | 20 |
| `32b`, `qwen` | 32768 | 4096 / 3072 | 24 |

Tokens are counted with tiktoken (`cl100k_base`) when available, falling back to a
dependency-free chars/4 estimate. The floor is `MinPackTokens` (512) — below that a specialist
cannot see anything useful at all.

!!! warning "Why this mattered"
    The historical packer budgeted in **bytes**: `max_context_kb` defaulted to 16, which under the
    legacy reserves capped a 32K Qwen at roughly **3.2K tokens** of context while the compaction
    watchdog believed it was at 80% capacity. `max_context_kb` still exists as a compatibility
    path (`TokensFromKB`) for when no model profile supplies a real window, but the model profile
    is what you should set.

Reserves are overridable: `context_reserve_system_tokens`, `context_reserve_tool_tokens`,
`context_reserve_response_tokens`, `context_slack_percent`.

### Per-role share

Not every role needs the same window. `context_role_budget` overrides these defaults:

| Roles | Share |
|---|---:|
| `worker`, `corrector`, `deep`, `placeholder` | 100% |
| `reviewer`, `tester` | 85% |
| `architect`, `planner`, `splitter` | 70% |
| `explorer`, `context`, `docs` | 60% |
| `coordinator`, `memory` | 50% |
| anything else | 75% |

Implementation roles get the most — a worker that cannot see the function it must edit cannot
produce an exact `old_str`. Exploratory and summarizing roles run on identifiers and docs, and
giving a small model less irrelevant text measurably improves instruction-following.

## 2. Deterministic assembly

A `TaskPack` renders in an explicit, stable order (`DocOrder`, `FileOrder`), not by ranging a Go
map. Map iteration is randomized, so the old renderer produced a **different byte sequence for
byte-identical inputs on every call** — which makes KV-cache prefix reuse impossible. On
oMLX/Ollama that is the difference between ~0.3s and ~8s time to first token.

The rule for anything added to a prompt: **stable content first, volatile content last.**

## 3. File excerpts with real line numbers

Files are not head-truncated. Each file is windowed around the identifiers that appear in the
query and task:

| Setting | Default | Meaning |
|---|---:|---|
| `excerpt_window_lines` | 25 | ± lines around each match |
| head lines | 15 | always-included prologue (package clause, imports) |
| max windows | 6 | separate regions per file |
| tail lines | 40 | fallback when nothing matches |

Excerpts carry **real file line numbers**, so a model can navigate straight to a span with
`ws_read {"offset": …}` instead of guessing. The previous behaviour — truncating every file at
2800 bytes — routinely cut the function the task was about.

Roles configured as *identifier-only* receive paths plus signatures rather than bodies, and pull
what they need with `ws_read` (just-in-time retrieval).

## 4. The repo map (`pkg/repomap`)

A compact, ranked map of the repository's symbols, so a small model can see the *shape* of a
codebase without reading it. It is the pragmatic equivalent of Aider's tree-sitter repo map, with
**no tree-sitter, no cgo, no new dependencies**:

1. **Symbol extraction** per file, with hand-written scanners for Go, Python, JavaScript/
   TypeScript, Rust and Java.
2. **A file-level reference graph** — which file mentions symbols defined in which other file.
3. **A PageRank-style pass** ranking files by how central they are to that graph.
4. **A terse list-shaped rendering** under a token budget that *shrinks as more real file bodies
   are already in the prompt* — the map exists to substitute for reading files, so it should
   yield space once the files are there.

| Setting | Default |
|---|---|
| `repo_map_tokens` | 900 (`DefaultBudgetTokens` in the package is 1000; 800–1500 is the useful band for a 32K SLM) |
| max files walked | 4000 |
| max file size | 512 KB (skips minified/vendored blobs) |
| cache | `.slmcode/repomap.json` |

One map is built per run, cached on disk. A build failure is non-fatal — the packer simply has no
symbol index.

## 5. Project instructions (`pkg/instructions`)

`AGENTS.md`, `CLAUDE.md`, `AGENT.md`, `.cursorrules`, `.cursor/rules`, `.slmcode/AGENTS.md`,
`.slmcode/PROJECT.md` — in that priority order — are loaded once per run and injected into
**every specialist's pack prefix**. Before this they were loaded and then never reached a
specialist prompt at all.

Three deliberate behaviours:

- **`README.md` is not project instructions.** A README is badges, install steps and marketing
  prose; injecting 4000 characters of it is catastrophic dilution for a 7B. Opt back in with
  `Options.IncludeReadme`.
- **Budget**: 12000 bytes total, 4000 per file, checked *after* accounting for the file just
  added.
- **De-duplication keys on the relative path**, not the lowercased basename, so
  `.slmcode/AGENTS.md` layers *under* the root `AGENTS.md` instead of silently shadowing it.

### Path-glob gating

A monorepo's AGENTS.md carries rules for Go, for the React app, for the Terraform stack. Feeding
all of them to a specialist editing one Go file is dilution. A section can declare which files it
applies to and is dropped when none are in scope:

```markdown
---
paths: pkg/**/*.go, cmd/**
---
```

or per section:

```markdown
## Frontend rules <!-- paths: web/**/*.tsx -->
```

A section with no `paths:` always applies. An empty scope list disables gating entirely, so a
caller that does not yet know its file scope loses nothing.

!!! tip "Keep your AGENTS.md short"
    A 26 KB instructions file on a 32K-context 14B burns ~8% of the window on **every turn**, and
    the model ignores half of it. This repo's own `AGENTS.md` is deliberately ≤2 KB: the
    non-guessable commands, the layout in one line, the non-default conventions, the real
    gotchas. Everything else lives in `docs/` and is pulled on demand.

## 6. Progressive skill disclosure (`pkg/skills`)

Rendering the entire `SKILL.md` body for four to six matched skills puts hundreds of tokens of
always-on behavioural directives in front of a small model, and multiple simultaneous directives
measurably degrade instruction-following. So there are two stages:

1. **Cards for every match** — name plus description, cheap, so nothing is silently dropped.
2. **Full bodies only for skills that earned it** — an explicit `@skill:` reference, a pin, or a
   high match score. `skill_max_expanded` (default 2) caps how many are ever inlined at once.

Anything that stayed a card can be pulled on demand with the `ws_skill` tool.

| `skill_disclosure` | Behaviour |
|---|---|
| `auto` (default) | cards + earned bodies |
| `cards` | never inline a body |
| `full` | inline every matched body (the historical behaviour) |

## 7. Compaction (`pkg/compact`)

### ReAct compaction (mid-run conversation)

Triggers at `react_compact_at_percent` (default 80) of the window, with a 5-point hysteresis band
— a compaction that does not open at least that much headroom pauses auto-compaction rather than
thrashing.

Two invariants:

- **Tool pairs survive.** The kept tail never begins on, or contains, an orphaned `role:"tool"`
  message. Every OpenAI-compatible server rejects that with HTTP 400 (*"messages with role 'tool'
  must be a response to a preceding message with tool_calls"*), and flattening tool calls into
  text makes a valid transcript unrecoverable. `SafeKeepStartFunc` walks backwards until the
  window is well-formed.
- **A structured must-preserve digest** is built from the messages being dropped: files read,
  files edited, commands and their exit status, failed calls, decisions — rendered list-shaped,
  in that order, because state the model can act on must come before narrative. `ResumeMessage`
  promises the model the summary preserves the work so far; the digest is what makes that true.

**Deterministic elision is tried first.** Old tool *results* are elided before any LLM
summarization is attempted — most of a long ReAct transcript is stale tool output, and dropping
it costs nothing and risks nothing.

### Document compaction

When a small model *is* asked to compress `CONTEXT.md`, its output must clear an acceptance gate
before it is allowed to overwrite real project memory. A 7B asked to "compress this context" will
happily answer *"Sure! Here is the compressed context:"* and stop, or return three bullets for a
30 KB document. `AcceptCompaction` checks length ratio, path-token retention and preamble
patterns; a failure leaves the original in place.

| Key | Default |
|---|---|
| `context_compact` | `true` |
| `context_compact_engine` | `heuristic` |
| `react_compact` | `true` |
| `react_compact_at_percent` | `80` |
| `compact_mode` | `true` |

## 8. Retrieval (`pkg/retrieval`)

Optional semantic ranking over project memory (query summaries, `MEMORY.md`, learned skills).
Off unless `embedding_enabled` is set; falls back local hashing embedder → lexical TF-IDF.

The score threshold is **calibrated, not guessed**. The old threshold of `>= 0.02` is deep inside
the noise band for signed feature hashing into 384 dimensions — pure noise was being injected as
"Retrieved prior knowledge", spending up to 3 KB of budget on nothing. Measured floors
(`TestNoiseFloorsAreCalibrated`):

| Mode | Floor |
|---|---:|
| real embeddings (`openai`) | 0.25 |
| local hashing embedder | 0.40 |
| lexical TF-IDF | 0.15 |

The effective threshold combines that absolute floor with the corpus's own measured noise
baseline (median + margin); `retrieval_min_score` overrides both. Documents are chunked by
section rather than by fixed size, and embeddings are cached (`retrieval_cache_dir`).

## 9. Inspecting what was packed

```bash
slmcode compose "add JWT auth"     # phases, team, and SLM fit — no LLM call
slmcode compose --json "…"
slmcode status --json
slmcode context                    # CONTEXT.md
cat .slmcode/repomap.json | jq '.files | length'
```

In Studio, the **Live** view streams the pack that each specialist received, and **Runs → trace**
replays a completed run.
