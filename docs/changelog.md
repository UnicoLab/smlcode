# Changelog

## v0.15.0 — Production SLM Harness, HITL UX & Studio Control Plane

### Highlights
- **Production readiness diagnostics** — richer status/readiness commands and Studio
  checks surface backend health, provider state, dynamic pipeline fit, and actionable
  warnings before a run starts.
- **User-validation UX** — plan approval, clarification, continuation, escalation, and
  shell asks now use structured pending-state handling with timeouts/default actions,
  making HITL decisions visible and resumable from Studio.
- **SLM information sharing** — shared briefs, session event summaries, and composer
  fit analysis help specialized agents pass compact task context without exhausting
  local-model context windows.
- **Dynamic pipeline visibility** — the Live page now shows selected agents, composed
  phases, SLM-fit hints, execute-loop roles, phase progress, current stage, recent
  agent activity, and readable long labels with hover/full-detail access.
- **Board task control** — the Kanban board supports adding tasks to any column,
  editing all primary task fields, moving tasks across custom columns, viewing long
  descriptions/outputs without clipping, and deleting tasks from the board UI.
- **Studio UX polish** — agent cards, Live task views, run history, readiness panels,
  and event logs are more readable for long local-model names, prompts, task titles,
  and diagnostics.

## v0.14.0 — Dynamic Pipeline, Broad Language Support & Live Log Severity

### Highlights
- **Dynamic pipeline is now the default** — the `composer` specialist assembles a
  task-specific pipeline (phases, team, tools, skills) before every run; disable via
  `dynamic_pipeline: false`, `slmcode run --no-dynamic`, or the Studio Settings toggle.
  The composer deterministically upgrades generic `worker`/`tester` to the matching
  language specialist, enforces that `execute` + `test` always run, and falls back to a
  safe generic pipeline for any unknown language.
- **Six new language packs** — `web` (static HTML/CSS/JS), `rust`, `java`, `cpp`
  (full pipeline + quality + worker/tester), plus `shell` worker/tester agents.
  `DetectProjectLanguage`/`langHint`/splitter guidance now cover Go, Python, JS/TS,
  Rust, Java, C/C++, HTML, and shell.
- **Static-web deliverables are guaranteed** — HTML/browser/game queries always get an
  `index.html` (or the splitter's chosen `.html`) entrypoint injected; a pile of
  disconnected `.js` files with no page can no longer happen.
- **QA gate is workspace-aware** — a stale `active_pack` (e.g. `python` from a prior
  run) no longer forces pytest onto a Go/JS/HTML workspace; virtualenv/cache dirs are
  skipped during quality detection; a lone `.go` file without `go.mod` uses a
  module-free `gofmt -e` smoke, and the `go test`→`go vet` fast-path no longer emits
  an invalid `-short` flag.
- **Live log severity** — events now carry a `level` (`info|warning|error|success|problem`);
  the Studio Event Log renders severity badges, colors, and a Problems filter with
  error/warning/success counts.
- **Tester finalize is more forgiving** — an explicit `passed:true` anywhere in the
  finalize (not just inside the parsed JSON object) is now honored, reducing false
  "missing passed:true" rejections.
- **Same-file task collapse** — many worker tasks editing one self-contained file are
  collapsed into a single worker (fixes the "7 tasks editing index.html" grind that
  caused review/correction loops).
- **Shell whitelist** extended for cargo/mvn/gradle/ctest/gcc/clang/shellcheck.

## v0.13.1 — 2026-08-10

- Automate Homebrew formula checksum sync in the release pipeline
- Make LiveStore onChange callback synchronous (fixes flaky TempDir cleanup race in CI)
- Normalize 'v' prefix on update-check latest tag (no double-v in notices)
- Sync Homebrew formula checksums with v0.13.0 release assets, fix install.ps1 typo in release body
## v0.13.0 — Block CRUD, Studio GUI Editing, Language Pinning & Live Feedback

### Highlights
- **Block CRUD API + Studio GUI** — Create / edit / delete building blocks (pipeline,
  agent, quality, pack) from the Blocks page with kind-aware visual editors; editing a
  builtin creates a project override; deleting a builtin is protected.
- **Pipeline Library + visual builder** — Browse, select, create, edit, and delete
  pipelines in the GUI; visual editors for groups, phases, execute loop, and slots with
  agent pickers; deleted phases archive as `when: never` (restorable) instead of
  resurrecting from defaults.
- **Agent blocks as runtime roles** — `go-tester` / `go-worker` / `python-tester` and
  every registry agent block are real registered roles; `execute.default_role` is
  honored; unknown roles fall back to generics with a warning.
- **Language pinning (no more pytest in Go runs)** — Project language is injected into
  tester / worker / reviewer / QA-gate / placeholder / interview prompts and knowledge
  cards are filtered by language; `when: never` / `enabled: false` is honored for all
  13 agent-driven phases.
- **Live feedback** — Send free-form steering from the Studio Live page or the TUI
  (`/feedback <text>`); it is injected into the next agent call as highest-priority
  instructions. New `GET/POST/DELETE /api/feedback`.
- **Skills ↔ Agents cross-linking** — Attach skills to agents and select agents in
  skills with visual multi-select chips in the GUI.
- **Update notifications** — TUI banner, `slmcode version`, `slmcode update --check`,
  and a Studio banner notify when a newer release is available
  (`GET /api/update`).
- **CLI parity** — `slmcode blocks new|edit|delete <kind> <id>`.
- **Hardening** — pipeline validation (duplicate groups, unknown steps), HTTP tests for
  the block API, skills edit modal, agent editor as a modal.

## v0.10.1 — LiveView Pipeline Progress, Task Management & Context Injection

### Highlights
- **Pipeline Progress Strip** — Visual tracker showing 5 groups (Prepare→Design→Build→Verify→Finish)
  with 16 colored phase dots, active pulse animations, and completed checkmarks.
- **Stats Dashboard** — Real-time phases completed, active agent, tasks in-flight, events count.
- **Active Agent Panel** — Current agent with description + recent events during runs.
- **LiveTaskPanel** — Tabbed right sidebar with full task CRUD (add/edit/delete),
  context injection (CONTEXT.md editor), and worker precision temperature slider.
- **Collapsible Event Log** — Toggle to show/hide the streaming event feed.
- **Tabbed Right Sidebar** — Tasks tab + Results tab for better information organization.
- All existing functionality preserved: SSE streaming, run/stop, specialist picker,
  config badges, event scrolling, result summary.

### SLM Optimizations

- **JSON repair improvements** — The repair engine now handles Python-style boolean
  literals (`True`/`False`) by normalizing them to JSON `true`/`false` before parsing.
  Trailing text after closing braces — a common SLM artifact where the model appends
  commentary after completing JSON output — is stripped automatically, reducing parse
  failures on partial or over-eager completions.
- **Tester gate robustness** — Regex-based pass detection scans tester output for 20+
  known shell/test-framework success markers (`PASS`, `ok`, `success`, `tests passed`,
  `All tests passed`, `0 failures`, green check variants). This makes the tester agent
  reliably recognize passing test runs regardless of framework (Go test, pytest, Jest,
  etc.) or output format quirks.
- **Worker status detection** — Smarter heuristics for detecting worker completion
  status from partial, malformed, or tool-chain-terminated output. Reduces false
  "incomplete" classifications when the worker produced valid changes but the final
  JSON was truncated or blocked by a tool call.
- **Improved tester prompt** — Tuned tester agent system prompt for better SLM
  adherence: explicit pass/fail criteria, shell command expectations, and a
  structured output schema that small models can follow more consistently.
- **LiveView enhancements** — Pipeline progress strip now reflects slot insertions
  (before/after/replace) with distinct styling. Group labels remain sticky during
  scroll in long runs. Event cards include tool-call metadata (tool name, args
  summary) inline.
- **HITL popup** — New modal overlay in Studio for escalate, clarify, continue, and
  shell-permission decisions. Replaces the inline prompt pattern with a focused,
  non-dismissible dialog that includes a visible countdown timer, action buttons,
  and contextual metadata (affected task, agent, retry count).
- **File inspector** — New Studio page for browsing workspace files with syntax
  highlighting, line numbers, and a diff view against the last checkpoint snapshot.
  Supports read-only inspection of any file in the project tree during or after
  runs — useful for verifying worker edits without leaving the Studio.

---

## v0.10.0 — Building Blocks, Language Packs & One-Click Pipeline Switching

### Highlights
- **Building Blocks system** — Marketplace-ready YAML presets: pipelines, agents, quality packs,
  language packs. Four block kinds with `api_version: blocks/v1` schema.
- **Predefined language packs** — 🐹 Go, 🐍 Python, ⚛️ React/TypeScript ready to use.
  Each pack includes tuned pipeline, language-specific worker/tester agents, and quality gates.
- **Blocks CLI** — `slmcode blocks list|show|apply|validate` — full block lifecycle management.
- **Chat REPL commands** — `/blocks` lists all blocks, `/pack <id>` applies a language pack.
- **BlockManager UI** — New Studio page: tabbed browser for all blocks, one-click apply,
  active indicators, source badges (builtin/custom).
- **PipelineEditor enhancement** — Preset selector for one-click switching between
  Go/Python/React pipelines directly from the pipeline editor.
- **PackSelector in Settings** — Switch language packs from the Settings page,
  alongside the existing Stack Selector.
- **Active config indicators** — LiveView and Sidebar now show active pack, pipeline,
  and stack badges during runs.
- **AGENTS.md** — Comprehensive 416-line contributor guide at project root.
- **18 blocks tests** — Full test coverage: registry loading, validation, catalog filtering,
  quality detection, QA gate resolution, meta validation, edge cases.

### Fixes
- Import cycle resolved: `blocks → agents → workspace → quality → blocks`.
  Quality smoke detection now delegates to `blocks.ResolveQAGateCommand` in orchestrator layer.
- `active_pack` and `active_pipeline` fields added to config schema and Studio Config type.

---

## v0.9.0 — Stacks, auth store & strict-provider ReAct

### Highlights
- **Stacks CLI + Studio** — `slmcode stack list|show|apply`, `GET/POST /api/stacks…`,
  Settings → Model Stack; hierarchy `stack → agent pin → runtime`
- **Per-agent LLM pins** — `slmcode agent …`, `effective_model` / `effective_provider` on APIs
- **Models catalog** — `GET /api/models`, `find_models` tool, costs + enabled_models allowlist
- **Auth store** — `.slmcode/auth.json` via `/auth`, `PUT /api/auth` (keys out of config.yaml)
- **Prime-agent ports** — LLM compaction engines, session `events.jsonl`, MCP status,
  auto-refine after waves, config schema (`GET /api/config/schema`)
- **DeepSeek** — default endpoint `https://api.deepseek.com` (client appends `/v1`)
- **GoLangGraph v0.2.2** — ReAct appends `role=tool` messages; no finalize race that
  skipped `act` (fixes DeepSeek 400 “insufficient tool messages”)
- **Success semantics** — historical `ESCALATED…` notes on **done** tasks no longer fail a green run

### Live smoke
- `RUN_E2E=1 go test ./test/e2e/ -run TestLiveStacksOMLXAndDeepSeek` (omlx-local + deepseek)

---

## v0.8.3 — Studio React style fix

### Fixes
- Studio Quick-start footer used HTML string `style` attrs → React error #62 crash
- `make lint` gofmt ignores `.slmcode/` workspace artifacts

---

## v0.7.3 — Incomplete finalize recovery

### Highlights
- Detect empty finalize and synthetic `model ended on a tool call` blocked JSON
- Up to two finish-steer corrector passes (demand status JSON, stop tool chains)
- Provisional done from disk/tool evidence when finalize still fails
- Knowledge cards: Python / Go project bars for language expectations

---

## v0.7.2 — Escalate timeout → SLM arbitrator

### Highlights
- On escalate HITL timeout, dedicated **@escalate** agent decides
  `retry` / `re_scope` / `abort` / `mark_done` (override via `escalate_timeout_agent`)
- Heuristic fallback when the LLM is unavailable (stubs → retry, vague → re_scope)
- Docs: Studio / TUI / Agents / Architecture / Config cover escalate + runnable QA bar
- CI: trailing-whitespace fix in `docs/pipeline.md`

---

## v0.7.1 — Runnable quality bar + escalate HITL

### Highlights
- Greenfield Python QA defaults to **pytest** (fail closed), not `compileall`
- **Acceptance smoke** — whitelisted commands from task acceptance run after each worker
- Worker critique loops until smoke/static/acceptance green (bounded by `max_retries`)
- Run success requires a **strong** QA gate — syntax-only cannot rubber-stamp incomplete boards
- **Escalate HITL** — Studio modal + TUI `/escalate`; options: re-scope / retry / mark done / abort

### API
- `GET` / `POST` `/api/escalate/pending|answer`

---

## v0.7.0 — Pipeline control + reference quality

### Highlights
- **Config-driven pipeline** (`.slmcode/pipeline.yaml`) — bind any agent to any phase,
  configure execute-loop reviewer/corrector, insert slots before/after/replace phases
- **Studio Pipeline tab** — edit phases/slots live; progress header follows config dynamically
- **Reference-quality bar** — project completeness gate blocks TestSLMs-style false success
- **Real-query eval suite** — offline harness + optional live LangGraph / FastAPI / CLI cases
- **Loop feedback** — SSE `kind=loop` with wave reasons + continue/abort/flag HITL
- **Placeholder polish** — `@placeholder` pass + precise gap flagging
- Custom agents accepted in specialist mode and board role dropdowns

### API
- `GET` / `PUT` `/api/pipeline`
- `POST` `/api/pipeline/reset`
- `GET` / `POST` `/api/continue/pending|answer`

### Docs
- New [Pipeline](pipeline.md) guide; updates to Studio, Agents, Config, Architecture

---

## v0.6.0 — SLM quality ports + eval

Harness gates, interventions, turn meter, `slmcode eval`, and Studio/TUI polish.
