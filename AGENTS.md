# AGENTS.md — slmcode

Go, SLM-first coding harness. Deeper: `CONTRIBUTING.md`, `docs/`.

## Build & test

- `make bootstrap` — build Studio UI once per clone. `go build` alone embeds a **placeholder**.
- `make check` — the single gate: `tidy-check`, gofmt, vet, lint, `cover` (`go test ./...` + the coverage floor), `-race ./pkg/...`, web lint+build. CI runs this. The two steps that need the network (module proxy, npm registry) **skip with a named reason** instead of failing, so it runs offline.
- Lint baseline is **zero**: `make lint` (and `make lint-strict`, its alias) fail on any finding. Fix it, or add `//nolint:<linter> // <why this site is a false positive>`.
- `make e2e` offline; `RUN_E2E=1 make e2e` adds live-model tests. Two suites run under plain `make test` and are the ones to keep green: `test/e2e/harness_smoke_test.go` drives the harness in-process against a fake OpenAI server, and `test/e2e/binary_acceptance_test.go` builds the **real binary** plus `test/fakemodel` and drives `init → doctor → run → task show → diff → apply` against a Go and a TypeScript fixture, asserting the bytes on disk.

## Layout

`cmd/slmcode` → `harness` → `orchestrator` (phases) → `loop` (worker/review/correct), over `agents`, `workspace` (tools), `context`+`repomap`, `schema`+`backends`, `memory`+`evolve`, `server`. `web/` builds into `cmd/slmcode/ui/`, `go:embed`ed.

## Non-default conventions

- **Normalize → Validate**: config/pipeline/block structs default in `Normalize()`, enforce in `Validate()`. Call both before persisting.
- `config.Config` is the only source of truth (`ApplyEnv`, `ApplyPatch`). Precedence: defaults → user file → project file → `SLMCODE_*` → flags.
- **ANTI-WANDER / HARD SCOPE**: `agents.AntiWanderCore` — workers/correctors touch only focus files and same-package siblings. Tests assert both markers verbatim.
- Every structured role needs a `pkg/schema` contract (`TestPromptContractsMatchSchemaAndGrammar`).
- **Never end a turn on a tool call** — emit final JSON after tool use. One tool call per turn.

## Gotchas

- `cmd/slmcode/ui/index.html` is **tracked** (placeholder); `ui/assets/` is gitignored output.
- Budget context in **tokens** (`pkg/context`), never bytes — bytes starved a 32K model to ~3.2K.
- Prompt assembly stays byte-deterministic, stable prefix first, or KV-cache reuse dies.
- Every tool result goes through the cap in `pkg/workspace`; never return unbounded output.
- `.slmcode/` is gitignored runtime state; tools are denied writes into it.

## Frontend <!-- paths: web/**, **/*.ts, **/*.tsx -->

`react-hooks/exhaustive-deps` is an **error**; run `npm run lint && npm test` in `web/`.
