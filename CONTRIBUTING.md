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

It runs, in order: `make tidy`, `make lint` (gofmt check → `go vet ./...` → golangci-lint, blocking →
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

## The lint ratchet — done, and it stays done

`make lint` runs golangci-lint **blocking**: the baseline is **zero issues**, so any new
finding fails the build. `make lint-strict` is now just an alias for `make lint`, kept
because CI and muscle memory both still say it.

Getting to zero also meant fixing how the count was measured: golangci-lint's defaults
(`max-issues-per-linter: 50`, `max-same-issues: 3`) hid most of a class once three of its
members had printed, so the "95" and later "36" baselines in this file's history were both
reading a truncated view of a real 133. `.golangci.yml` now sets both caps to 0.

The rules, now that it is green:

- Fix the finding. That is the default and it is almost always right.
- If — and only if — a finding is a genuine false positive at that site, add
  `//nolint:<linter> // <why THIS site is safe>`. A bare `//nolint` with no linter and no
  reason is not acceptable.
- One class is excluded at the config level, with the reasoning written out in
  `.golangci.yml`: gosec **G304** ("file inclusion via variable"). slmcode's purpose is
  reading files by computed path; the control that matters is the workspace jail
  (`Workspace.resolve` + `checkSymlinkEscape`), which has its own hardening tests. G301,
  G302 and G306 (directory and file permissions) stay enabled and are enforced —
  harness state under `.slmcode/` is `0o750` / `0o600`.
- Do **not** add golangci-lint's default exclusion PRESETS to get a green run. They
  blanket-suppress whole categories; the G304 exclusion above is one named rule with a
  written justification, which is a different thing.
- `_test.go` files are exempt from gosec only. Nothing else is exempt.
- `gofmt` and `go vet` are always blocking.

## Test layout

- **Unit tests** live next to the code as `*_test.go` in each `pkg/...` package. This is where
  almost everything belongs; most engine behaviour is testable without a model.
- **Race tests**: `go test -race ./pkg/...`. The parallel wave, the SSE hub and the memory
  stores are all concurrent — new concurrency needs a race test.
- **E2E** lives in `test/e2e/`, split into offline tests (always run) and live tests gated on
  `RUN_E2E=1`. `scripts/e2e_prime_smoke.sh` covers the Studio/stack/auth/MCP surface.
- **The whole-harness smoke test** is `test/e2e/harness_smoke_test.go`: it drives
  `harness` → `orchestrator` → `loop` → `pkg/workspace` against an in-process fake
  OpenAI-compatible server and asserts the four things a finished run leaves behind — the
  file on disk, a completed board, an episode in `.slmcode/memory/episodes.jsonl`, and a
  metrics row with real edit accounting. It is hermetic (it redirects `HOME`) and runs in
  well under a second under plain `make test`. If a change to any layer breaks the
  contract between layers, this is the test that says so.
- **Driving the real binary with no model**: `test/fakemodel` is the same canned
  OpenAI-compatible server, lifted out as a standalone command so a built `slmcode` can be
  exercised end to end — the CLI surface (gates, footers, exit codes, `apply`, Studio) is not
  reachable from an in-process test.

  ```bash
  go run ./test/fakemodel -addr 127.0.0.1:8099 &            # -mode 401|404|500|garbage
  cd /tmp/demo && printf 'module d\ngo 1.22\n' > go.mod
  SLMCODE_PROVIDER=openai SLMCODE_MODEL=fake-model SLMCODE_API_KEY=x \
    SLMCODE_ENDPOINT=http://127.0.0.1:8099/v1 slmcode run "add a Divide function"
  ```

  The `-mode` flag reproduces the endpoint failures `slmcode doctor` has to explain, which is
  how the doctor remedies for 401 / 404 / non-OpenAI responses are checked.
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

**Language detection has exactly one implementation.** A new language pack is picked up by
`blocks.DetectPack(root, root)` as soon as its quality block carries a `detect` stanza — do not
add a marker list anywhere else. Three call sites once kept their own (`slmcode init`, the smoke
package, the orchestrator) and they disagreed on six of thirteen languages, so `init` wrote
`active_pack: java` next to `./gradlew test`. Score with `detect.files` (+12 per present marker),
`detect.contains` (+25 per satisfied content proof — this is what separates `react` from
`typescript`), `detect.extensions` (+2 each, capped at 3) and `detect.priority`.
`TestDetectPackPerLanguageFixtures` (pkg/blocks) and `TestInitPackAgreesWithTheAppliedQualityBlock`
(cmd/slmcode) both need a fixture for the new language.

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
`slmcode stack list` shows what resolves; extra search paths come from `$SLMCODE_STACKS`.

### A new CLI command

Add a Cobra command in `cmd/slmcode/`, register it in `root.go` under the right group
(`run` / `review` / `config` / `inspect`). Honour the non-interactive contract in
`cmd/slmcode/doc.go`: no prompting without a TTY, `--json` writes one document to stdout with
colour off, failures return a `codedError` with the documented exit code.

Two rules that are easy to miss because they are about the END of a command:

- **Never leave the reader without a next step.** Anything that reports a stopped, refused or
  empty state names the command that resolves it. `slmcode board` points at `slmcode task show`;
  a run that changed nothing says so and offers `task show` / `--vv`; an unknown task id lists
  the ids that exist.
- **Engine-authored text is translated at the renderer, not printed raw.** `pkg/orchestrator`,
  `pkg/loop` and `pkg/plan` write one event stream for the TUI, Studio and `slmcode run`, and
  their advice is phrased for the richest client ("decide in Studio", "/resume run-…").
  `cli.TranslateEngineAdvice` rewrites those into commands this binary has; extend its table
  rather than teaching users a remedy they cannot use.

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
| `pkg/blocks`, `pkg/pipeline`, `pkg/stacks` | YAML building blocks, phase graph, presets, **language detection** (`DetectPack` / `DetectAll` — the only implementation) |
| `pkg/permissions`, `pkg/hitl`, `pkg/hooks` | write/shell policy, human gates, lifecycle hooks |
| `pkg/server` | Studio HTTP/SSE API, security policy, review API |
| `pkg/cli` | terminal rendering: diffs, gates, REPL input, colour, width, engine-advice translation |
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
