# Contributing to SLMCode

Public baseline on purpose. The most valuable contributions are the ones that make **small
models** more reliable: tighter tool contracts, better prompts, evals, gates that fail closed.

## Build

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make bootstrap        # builds the Studio UI (npm ci + vite build) → cmd/slmcode/ui/
make build            # → ./bin/slmcode
```

`make bootstrap` is not optional on a fresh clone if you care about Studio. `cmd/slmcode/ui/`
is embedded with `go:embed all:ui`, and only `cmd/slmcode/ui/index.html` is tracked in git —
a checked-in **placeholder** page so `go build` always succeeds. Without `make bootstrap`
(or `make ui-react` to rebuild), `slmcode studio` serves that placeholder, not Studio.

Needs Go 1.23+ and a Node with `npm ci` support (the SPA is React 18 + Vite + TypeScript).
`make bootstrap` fails with a clear message if `npm` is missing. `make install-user` puts the binary in
`~/.local/bin`; `make install-system` installs system-wide.

## `make check` — the one gate

```bash
make check
```

It runs, in order: `make tidy`, `make lint` (gofmt check → `go vet ./...` → golangci-lint →
embedded-UI smoke check), `make test` (`go test ./...`), `make race`
(`go test -race -count=1 ./pkg/...`), then `npm run lint && npm run build` in `web/`.

CI's lint-test job and `.pre-commit-config.yaml` run exactly this, so local and CI cannot
diverge. If you want to know whether a PR will pass CI, run `make check`.

Other targets worth knowing:

| Target | What it does |
|---|---|
| `make cover` | coverage with a total floor (`scripts/coverage-check.sh`, floor `COVERAGE_FLOOR`, currently 51.0%) |
| `make e2e` | offline e2e (`test/e2e/`) + `scripts/e2e_prime_smoke.sh` |
| `RUN_E2E=1 make e2e` | additionally runs `TestLiveOMLX` / `TestIsolatedMultiAgent` against a live model |
| `make govulncheck` | vulnerability scan |
| `make docs-build` | strict MkDocs build |
| `make docs-serve` | docs at <http://127.0.0.1:8000> |

## The lint ratchet

`make lint` runs golangci-lint **non-blocking**: it prints findings but does not fail. The
project is mid-ratchet against a captured baseline recorded at the top of `.golangci.yml`
(95 issues: errcheck 29, gosec 23, staticcheck 22, unused 8, misspell 6, bodyclose 3,
ineffassign 4).

The rules of the ratchet:

- `make lint-strict` (or `LINT_STRICT=1 ./scripts/lint.sh`) fails on any finding. Use it
  while fixing a category.
- Fix findings, then **lower the baseline numbers** in the `.golangci.yml` comment.
- Do **not** add exclusion presets to get a green run. `.golangci.yml` deliberately sets no
  default exclusion presets — with them on, errcheck alone drops from 29 findings to 5, which
  is a pre-filtered view, not progress.
- `_test.go` files are exempt from gosec only. Nothing else is exempt.
- `gofmt` and `go vet` are always blocking.

The goal is for `make lint-strict` to become what CI runs.

## Test layout

- **Unit tests** live next to the code as `*_test.go` in each `pkg/...` package. This is where
  almost everything belongs; most engine behaviour is testable without a model.
- **Race tests**: `go test -race ./pkg/...`. The parallel wave, the SSE hub and the memory
  stores are all concurrent — new concurrency needs a race test.
- **E2E** lives in `test/e2e/`, split into offline tests (always run) and live tests gated on
  `RUN_E2E=1`. `scripts/e2e_prime_smoke.sh` covers the Studio/stack/auth/MCP surface.
- **Frontend**: Vitest + Testing Library in `web/src/**/*.test.tsx`.
- Determinism matters more than coverage here: an offline test that depends on a model's
  wording is worse than no test. Prefer fixtures and fakes over live calls.

## Extending the harness

### A new block (pipeline / agent / quality / pack)

Blocks are YAML, discovered project → user → `$SLMCODE_BLOCKS` → builtin, first id wins per
kind. Built-in blocks live in `pkg/blocks/bundled/{pipelines,agents,quality,packs}/` and are
`go:embed`ed; project blocks go in `.slmcode/blocks/…`. Every block carries the shared `Meta`
header (`api_version: blocks/v1`, `kind`, `id`, `version`, …). Validate with
`slmcode blocks validate`. Full schemas: [docs/blocks.md](docs/blocks.md).

To add a new block **kind**: `pkg/blocks/meta.go` → a schema struct in `pkg/blocks/schema.go`
→ the `ingest()` switch in `pkg/blocks/registry.go`.

### A new agent (built-in role)

1. Add the prompt to `pkg/agents/prompts.go`. Tool-using roles must embed `AntiWanderCore`.
2. Add a `RoleSpec` to `specs()` in `pkg/agents/factory.go` (tools, `MaxIter`, temperature,
   `MaxTokens`, and `SchemaRole` when the id does not match a `pkg/schema` contract).
3. If the role emits structured JSON, register its contract in `pkg/schema/spec.go` — GBNF is
   generated from it. Keep the schema inside the supported subset (see the package doc).
4. Optionally ship a YAML agent block so it can be overridden per project.

Registry agent blocks are also registered as runtime roles via `agents.Factory.ExtraCustoms`;
on-disk `.slmcode/agents/{id}.yaml` wins on id clash.

### A new skill

`skills/default/<name>/SKILL.md` (shipped, embedded) or `.slmcode/skills/<name>/SKILL.md`
(project). Frontmatter: `name`, `description`, `triggers`, `agents`, `user-invocable`. Skills
are progressively disclosed — the description is what most specialists ever see, so write it
as a card. See [docs/skills.md](docs/skills.md).

### A new stack

`stacks/<name>.yaml` with provider/model/endpoint (and optional per-role `agents:` defaults).

### A new CLI command

Add a Cobra command in `cmd/slmcode/`, register it in `root.go` under the right group
(`run` / `review` / `config` / `inspect`). Honour the non-interactive contract in
`cmd/slmcode/doc.go`: no prompting without a TTY, `--json` writes one document to stdout with
colour off, failures return a `codedError` with the documented exit code.

### A new config field

Add it to `config.Config` with both `yaml:` and `json:` tags → default it in `Default()` →
normalize it in `Normalize()` → handle it in `ApplyPatch()` → document it in
[docs/config.md](docs/config.md).

## Package ownership map

| Package | Owns |
|---|---|
| `cmd/slmcode` | CLI, TUI entry, embedded Studio assets, exit codes |
| `pkg/harness` | top-level `New` / `Init` / `Run` façade |
| `pkg/orchestrator` | phase graph execution, HITL gates, project instructions, scope |
| `pkg/loop` | inner execute loop: worker → review → correct → test, evidence and call budget |
| `pkg/plan` | task/board model, role ids, sanitization |
| `pkg/agents` | specialist prompts, role specs, decoding normalization, custom agents |
| `pkg/workspace` | the tool layer (ACI): `ws_*` tools, guards, edit ladder, shell safety |
| `pkg/context` | token budget, task packs, excerpts, project docs |
| `pkg/repomap` | symbol extraction, reference graph, PageRank ranking |
| `pkg/compact` | ReAct compaction, must-preserve digest, elision |
| `pkg/skills` | SKILL.md loading, matching, progressive disclosure |
| `pkg/instructions` | AGENTS.md / CLAUDE.md loading with path-glob gating |
| `pkg/retrieval` | embeddings, chunking, score calibration, cache |
| `pkg/schema` | JSON Schema contracts + GBNF generation |
| `pkg/backends` | provider registration, capability probe, structured decoding, retry policy |
| `pkg/repair` | the JSON repair ladder and its counters |
| `pkg/memory` | working / episodic / semantic / procedural memory |
| `pkg/evolve` | fingerprints, repair rules, bandit, reflection, regressions |
| `pkg/eval`, `pkg/eval/metrics` | eval harness, per-run metrics, `Compare`, replay |
| `pkg/blocks`, `pkg/pipeline`, `pkg/stacks` | YAML building blocks, phase graph, presets |
| `pkg/permissions`, `pkg/hitl`, `pkg/hooks` | write/shell policy, human gates, lifecycle hooks |
| `pkg/server` | Studio HTTP/SSE API, security policy, review API |
| `pkg/cli` | terminal rendering: diffs, gates, REPL input, colour, width |
| `web/` | Studio SPA |

## Pull requests

1. Fork and branch.
2. `make check` green.
3. `make docs-build` if you touched `docs/` or `mkdocs.yml`.
4. Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`…). Human commit messages — no tool
   trailers, no ANSI art.
5. No secrets. Keys belong in `.slmcode/auth.json` or the environment, never in committed YAML.

## Docs

Docs are MkDocs Material in `docs/`, published to GitHub Pages. `mkdocs.yml` nav entries must
resolve to real files — `make docs-build` runs strict and will fail otherwise. If you change
behaviour, change the page that documents it in the same PR; a doc that overstates what the
code does is worse than a missing one.
