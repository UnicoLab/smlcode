# 🧭 Code conventions

The rules a contributor (human or agent) has to know that are *not* guessable from the code. The
two-kilobyte version is [`AGENTS.md`](https://github.com/UnicoLab/smlcode/blob/main/AGENTS.md);
this is the long form. Build, test and ownership live in
[`CONTRIBUTING.md`](https://github.com/UnicoLab/smlcode/blob/main/CONTRIBUTING.md).

---

## Normalize → Validate

Every serializable struct — `config.Config`, `pipeline.Config`, every block schema — has:

- `Normalize()` — fill defaults, clean and canonicalize. Idempotent.
- `Validate()` — enforce rules, return an error.

**Call both before persisting.** `pipeline.Config.Validate()` rejects empty or duplicate group
ids, group steps referencing unknown phases, and phases assigned to multiple groups.

A corollary that has bitten before: `pipeline.Config.Normalize()` merges missing default phase
keys back in, so a *hard delete* of a phase resurrects it on the next load. GUI pipeline editors
"delete" a phase by persisting `when: never, enabled: false` and removing it from groups and
order — which is restorable, and survives normalization.

## Config

`config.Config` is the single source of truth.

- Both `yaml:` and `json:` tags on every field. Studio's Settings page renders from
  `config schema`, so an untagged field is an invisible field.
- `ApplyEnv()` handles `SLMCODE_*`; `ApplyPatch(Patch)` handles partial updates from the API and
  the CLI.
- Layering, lowest first: defaults → user file → project file → env → flags. `prov` records which
  layer supplied each key, for `config show --origin`.
- `Root` is never persisted (`yaml:"-"`): an absolute path in a config file is not portable
  between machines or checkouts, and `Load` would honour the stale value.
- `config_version` marks the schema generation; `migrate.go` moves older files forward.

## Prompts and schemas

- Prompts are SLM-optimized: short, role-locked, output contract stated **first**.
- Every tool-using specialist inherits `agents.AntiWanderCore`. It is deliberately three lines so
  it can prepend to any prompt without crowding the task:

  ```
  ANTI-WANDER — HARD SCOPE, three rules:
  SCOPE: touch only the task's focus files and same-package siblings; …
  NOTHING EXTRA: no new helpers, files, refactors, or "nice to have" additions.
  GROUNDED: reference only paths you have read; use ws_glob/ws_grep when unsure.
  ```

  `pkg/agents` tests assert the literal strings `ANTI-WANDER` and `HARD SCOPE`, and
  `pkg/server` and `pkg/orchestrator` build `## Focus files (HARD SCOPE)` sections that the same
  tests check. **Do not reword these markers.**
- Every structured role needs a contract in `pkg/schema`. `TestPromptContractsMatchSchema` fails
  when a prompt promises a field the schema does not have.
- Coding agents get `workspace.ToolNames()` + `workspace.SpecialistToolNames()`.
- **Never end a turn on a tool call** — an agent must produce final JSON after tool use.
- **One tool call per turn.** `RoleSpec.SerialTools` truncates an assistant message to its first
  tool call, so a model that ignores the instruction loses the extra calls rather than confusing
  the loop.
- `NormalizeDecoding` derives a role's schema role, `JSONOnly` flag and stop sequences from its
  id, so a new role usually only declares its tools. See
  [Constrained decoding](decoding.md#3-decoding-directives-per-role).

## Determinism

Two things must be byte-deterministic, and both have silently regressed before:

1. **Prompt assembly.** Stable prefix first, volatile content last. `TaskPack` renders from
   explicit `DocOrder` / `FileOrder` slices, never by ranging a map — Go randomizes map
   iteration, so the old renderer produced a different byte sequence for identical inputs on
   every call, and local KV-cache prefix reuse never hit.
2. **CI.** `EngineOptions{Deterministic: true}` (config `deterministic`) makes the bandit greedy
   and disables exploration. `dry_run` implies it.

## Budgets

Everything that reaches a prompt is budgeted **in tokens**, and every collection is bounded.

- `pkg/context` derives the pack budget from the model's real window minus reserves. Never
  reintroduce a byte budget.
- Every tool result goes through `pkg/workspace`'s cap; never return unbounded output.
- Every memory store has a cap and a prune policy; every rendering has a token budget.
- Search tools announce their own truncation (`MaxGrepHits`, `MaxGlobHits`).

## Failure handling

- **Fail closed at gates.** Truncated reviewer JSON is a rejection. The QA gate cannot report
  green when tests failed. A HITL gate with a human attached blocks rather than expiring.
- **Disk is authoritative.** A claimed edit that is not on disk is not evidence. Repo dirt
  unrelated to the task is not evidence either.
- **A subsystem failure must never wedge a run.** Memory, evolve, repo-map and retrieval are all
  best-effort: a corrupt file is moved aside to `<name>.corrupt`, the store starts clean, and the
  problem is surfaced through `Warnings()` rather than an abort.
- **Tool failures are information, not errors.** A shell timeout, a failed match and a syntax
  break are returned to the model as a result it can act on, with the recovery spelled out.

## Runtime roles and phase gating

- **Agent blocks are runtime roles.** `agents.Factory.ExtraCustoms` registers every registry agent
  block (bundled `go-tester`/`go-worker`/`python-tester` … plus project and user blocks) as a real
  role. On-disk `.slmcode/agents/{id}.yaml` wins on id clash. `GET /api/agents` merges both.
- **`execute.default_role` is consumed**: tasks with an empty or `implementer` role use the
  pipeline's `execute.default_role` (e.g. `go-worker`).
- **Role fallbacks**: a phase agent missing from the registry falls back to the default agent with
  a warning; unknown task roles map to generics (`go-tester` → `tester`, `python-worker` →
  `worker`). The same folding gives block-defined agents their schema contract automatically.
- **Phase gating**: `when: never` / `enabled: false` is honoured for the agent-driven phases
  (`context`, `explore`, `docs`, `architect`, `clarify`, `plan`, `split`, `coord`, `execute`,
  `learn`, `polish`, `test`, `memory`). `init`, `skills` and `done` are engine-structural and
  always run.
- **Language pinning**: the detected project language is injected into tester/worker/review/QA
  prompts ("Project language: Go — NEVER run pytest…"). `PromptTester` and `PromptTaskSplitter`
  stay language-neutral so the hint is the only source of truth.

## Naming and identifiers

- Block ids: `^[a-z][a-z0-9_-]{1,63}$`, lowercase kebab-case.
- Schema role ids are *output contract* names, not agent ids. Several agents may share one; an
  agent whose id does not match a contract names it with `SchemaRole`.
- Block discovery order, first id wins per kind: project `.slmcode/blocks/` → user
  `~/.slmcode/blocks/` or `$XDG_CONFIG_HOME/slmcode/blocks/` → `$SLMCODE_BLOCKS` and walk-up
  `blocks/` dirs → builtin (`pkg/blocks/bundled/`, `go:embed`ed).

## Adding things

| To add | Do |
|---|---|
| An agent | prompt in `pkg/agents/prompts.go` → `RoleSpec` in `specs()` → `pkg/schema` contract if it emits JSON → optional YAML block |
| A pipeline phase | `pkg/pipeline/default.go` `Default()` → orchestrator wiring → group assignment |
| A block kind | `pkg/blocks/meta.go` → struct in `pkg/blocks/schema.go` → `ingest()` switch in `pkg/blocks/registry.go` |
| A stack | `stacks/<name>.yaml` |
| A skill | `skills/default/<name>/SKILL.md` or `.slmcode/skills/<name>/SKILL.md` |
| A CLI command | Cobra command in `cmd/slmcode/` → register in `root.go` under a group → honour `cmd/slmcode/doc.go`'s non-interactive contract |
| A config field | struct field with both tags → `Default()` → `Normalize()` → `ApplyPatch()` → [config reference](config.md) |

## The build

- `cmd/slmcode/ui/` is a `go:embed all:ui` directory whose **only tracked file is `.gitkeep`** —
  it keeps the directory (and therefore the embed pattern) alive on a fresh clone. `index.html`,
  `assets/` and `vendor/` there are gitignored build output; with none of them present the server
  serves a placeholder page from `pkg/server/placeholder.go`.
  `make bootstrap` builds the real SPA; `make ui-react` rebuilds it.
- `.slmcode/` is gitignored runtime state.
- Lint findings are ratcheted against a baseline in `.golangci.yml`, never excluded. See
  [CONTRIBUTING.md](https://github.com/UnicoLab/smlcode/blob/main/CONTRIBUTING.md#the-lint-ratchet).
