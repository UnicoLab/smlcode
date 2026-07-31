## Improve GUI

GUI is a nice start but let's make it much more professional and user friendly:

- [x] improve the styling of the gui, to make it more professional and good looking not with some strate colour codes like currently

- [x] main live stream needs big imrpovement on rendering we should see nice icons representing dedicated agents with animations and seeing what they are actually doing, status, live updates etc ... succeeded or failed etc ...

- [x] Maybe some kind of a visuals on how agents collaborated like the nice display of states progressbas we have on top is nice but can be drastically imrpoved !

- [x] markdown display should be much better for diplay readability and editing etc
  - Studio docs: Edit / Split / Preview with lightweight markdown renderer + styled preview

- [x] we shoudl see and be able to know what agent or sub-agent or current step recieved and input command, what it did, what it changes etc ... so we have full observability per action, step etc !!!

- [x] when everything is done for a given query we shoudl put it into archives with all the history etc, it should be a separate thread or something so we keep the history per project etc !!! 

-> [x] when executing some request we shoudl have nicely displayed plan and tasks great visually so we can track the overall progress ! Not only using text or markdown files here, but in the GUI !!! For example when a query is given to the slmcode it shoudl first get context, skills, memories all agents and entire context of the project and it's capabilities, plan everything, split into tasks etc ... then generate populate board with these taks with attributes agents etc ... live updates everywhere !!

-> [x] live stream GUI shoudl be much much better visually !!!

-> [x] GUI shoudl adapt to screan size etc, dynamic layout that adapts, resizes etc !

-> [x] I should also always see what is going on somewhere like in the status bar or in the footer, like current active agents working on ... progress per task etc ... performances ... be able to stop each process separetely etc ... hols or enrich with additional context as well !

-> [x] for all tasks we should also be able to handle their dependencies, we can display it nicely using react-flow somehow like nodes and edges etc ... or some nice method -> and this shoudl be clear for codign agents etc
  - SVG dependency graph (nodes + edged arrows) in Live — no npm build; practical react-flow equivalent

-> [x] on the live beedback live scroll to the last even shoudl be enabled by default ! We also shoudl be able to pause the current loop and add context if we want or stop it if something is not right etc ... 

-> [x] we should be able to reference files, folders and add context to the query in GUI and TUI etc (like claude code !)

## Task creation

- [x] make sure tasks creation is well integrated and will work correctly for sub-agents and everything -> currently we are creating one big file ... so maybe this is somehow a too big context for smalelr sub-agents -> or the coordinator distributes the task correctly to all agents and then just updates this bigger file . Let's make sure it works perfectly
  - Lean `TASKS.md` (truncated details)
  - Ephemeral scoped packs (not persisted into task descriptions)
  - Coordinator dedupe + task caps
  - Workers get fresh packs at execute time only


## Agents

- [x] allow user to create agents based on skills, everything should be editable and customizable by the user easily either using GUI, TUI or just by adding files etc !
  - Studio Skills tab create/edit; drop `SKILL.md` under `.slmcode/skills/` or `~/.slmcode/skills/`


## Generic skills, agents, tools and other things

- [x] We should be able to have centralized storage of skills, agents, tools and anything else allowing to specify slmcode functionality globall on the system level not only on the project level and it shoudl autodiscover and propage it gloablly !
  - Autodiscover: `~/.slmcode/skills`, `~/.config/slmcode/skills`, `$XDG_CONFIG_HOME/slmcode/skills`

## Waves

- [x] I can see some concept of "## Wave lessons ..." let's make sure that we keep coherent memories or context per project so onece we do something all further request agents and everything will have already usable context and informations like code et orgnization etc ... 
So we keep rpogressively all the knowledge about the project after each run getting better and better and not being reinitialized form scratch on every run ... but let's make sure that each agent get's correct context and info and when designing new agent we will be able to precisely controll this as well !!!
  - PROJECT.md seeded + evolved; MEMORY wave lessons; archives per run; learned skills


## Make sure we handle failures in outputs and tasks nicely

- [x] Like: **Blocked:** T1: review rejected after max retries ... we need to make sure we have correction loops, validation and everything in place to handle these kind of problems, retries or even ask user what to do if necessary !!
  - Disk/git evidence gate (no more approve-on-hallucinated JSON)
  - Escalate to `to_scope` after max retries (human can promote)
  - `.slmcode/errors/errors.md` + JSON failure records + wave_lessons.md
  - Higher default retries / timeouts for SLMs

## Errors logs
- [x] Let's make sure that we store all the logs, especially on failure with all the metadata so we can analyze them using LLM and fix all potential problems...
- [x] dedicated `errors.md` file with context by default (under `.slmcode/errors/`)

## Testing and critic

- [x] Let's imrpove testing and critics loops ... keep the global goals and tasks view ...
  - Reviewer sees disk evidence; corrector prompts reinforced; tester still runs post-board

## Project Info

- [x] I have noticed that the project info was kep empty during my entire usage of the slmcode ... which is strange -> deep dive and fix all the bugs ! Context if very important !
  - Root cause: PROJECT.md scaffold never written by any agent
  - Fix: seed from README/go.mod/layout at init + ensureProjectDoc each run + knowledge.Evolve enrich

## Testing results

- [x] on my initial test I can notice lot's of failures -> deep dive and fix all problems 
  - Fixed compile break in failure handler
  - Fixed timeouts (task/LLM alignment), empty output reviews, TASKS.md bloat, duplicate coordinator tasks, empty PROJECT.md

We need everything to be self evolving and improving all the time !!! 

## Planing

- [x] When planing what needs to be done, let's take enough time ... drastically improve this part as well in context of SLMs !!!
  - think_passes configurable; multipass planner/splitter
  - think_passes≥2: plan critique + refine; deeper explore; splitter hints for tiny SLM tasks
  - think_passes≥3: always deep explore + parallel explorer/docs digs

## TUI improvements

- [x] We need to drastically imrpove the TUI ... like the one form claude code ... cleaner ... visual overviews and status ...
  - Compact event lines + sticky status footer (`PrintEventWithStatus`)
  - Interactive chat still accepts input between runs
  - File-change events (`✎`) in the live stream

## Concepts of Waves 
- [x] This concept is quite nice, maybe we can explore it more and use it somehow in the GUI, TUI and in general ?
  - Wave chips / observability strip in Live; wave lessons in MEMORY; errors wave_lessons.md

## Core engine

- [x] All steps should initially plan, use tools, scope and delegate tasks ... validation loop with critic ...
  - Pipeline: skills → context → explore[/docs parallel] → architect → plan[+critique] → split → coord → execute/review/correct → test → QA gate → memory → evolve
  - Parallel deep-dives when think_passes≥2; evidence-gated reviews

## Coding 

- [x] streaming code updates / partial file patches for SLMs
  - Prefer `ws_edit` / new `ws_patch` (SEARCH/REPLACE + unified hunks) + disk evidence
  - Live `file_change` SSE/CLI events with path + snippet cards in Studio

## Provider / model flexibility

- [x] Users can freely change provider + model (any OpenAI-compatible endpoint)
  - Config / flags (`--provider --model --endpoint --api-key`) / env (`SLMCODE_*`, `OPENAI_BASE_URL`, `OPENAI_API_KEY`, …)
  - Registry treats unknown providers as OpenAI-compat; Ollama stays native
  - Doctor / status / Studio Settings show and switch active provider+model
  - README documents oMLX, Ollama, LM Studio, OpenAI, OpenRouter, vLLM, etc.

## REF github repository
-> it's on unicolab repository: https://github.com/UnicoLab/smlcode.git not github.com/UnicoLab/slmcode !!!
  - Module path updated to `github.com/UnicoLab/slmcode` in go.mod (repo name spelling on GitHub may still be `smlcode`)

## Context window auto management
- [x] context auto-compressed for agents / handled automatically
  - Packer budget (32KB default); lean TASKS.md; strip fat packs from persistence; truncate archive docs

## Efficiency
- [x] account for SLM structured-output failures, self-fixing loops, limited scope
  - Tool-junk recovery; evidence gate; corrector loops; escalate to human; lower idle via less think_passes default + lower parallel contention

## Testing capability steps
- [x] custom end-step testing agent / QA gate until tests pass
  - Tester specialist + configurable `qa_gate` / `qa_gate_command` / `qa_gate_max_rounds`
  - Auto-detect `go test ./... -short` (and npm/pytest/cargo/make when present)
  - Iterate: run command → diagnose (tester) → fix (corrector) → retest until green

## SLMs optimized features, loops, correct auto-correct fix etc
- [x] parallel where safe, recover/fix small edits, reduce idle time on oMLX
  - max_parallel default 2; LLM timeout aligned with task timeout; ephemeral packs; disk evidence; escalate instead of infinite block

## Anti-wander / task focus (2026-07-29 hardening)
- [x] Focus-file allowlists on `ws_write` / `ws_edit` / `ws_patch` (package-local siblings OK)
- [x] Hard-block creating root entrypoints (`main.go`, `index.js`, …) when not in focus
- [x] Evidence + scope gates reject out-of-scope `files_changed` claims
- [x] Worker/corrector/reviewer prompts reinforce HARD SCOPE
- [x] Lean execute-time packs (`LeanDocsForRole` + tighter file/skill caps)
- [x] Ready-task scheduling prefers focused implementers; less idle spin mid-wave

## Testing gaps closed
- [x] Unit tests: `agents`, `cli`, `harness`, `multipass`, `stream`, `cmd`, focus/scope
- [x] Studio UI interaction harness (HTTP Live flows + `markdown_node_test.js`)
- [x] Isolated multi-agent board sandbox e2e (temp workspace; optional `RUN_E2E=1` live)

## Studio theme revamp (0.5.8)
- [x] Professional light + dark themes via `data-theme` CSS variables (slate/teal accent)
- [x] Theme toggle in topbar with `localStorage` persistence (`slmcode-theme`)
- [x] Removed garish glow / neon gradients; clean surfaces, readable contrast
- [x] Agent avatars + Live observability polish; board progress bar; inject-context box
- [x] Archives view in Studio (`GET /api/archives`) — finished runs as history threads
- [x] `make ui-check` + `make build` embeds latest `cmd/slmcode/ui/` via `go:embed`

## Greenfield / anti-wander hardening (from LangGraph temp e2e)
- [x] Focus guard allows scaffold trees when root manifests (pyproject.toml, …) are in focus
- [x] `ReconcileFiles` keeps planned create paths under `src/` / `tests/` (no longer strips them)
- [x] Infer per-task focus files from titles for create/scaffold work
- [x] Create-task acceptance when declared files exist on disk (avoid false review rejects)
- [x] Do not collapse multi-file / greenfield / “minimal project” splits into one mega-task

---

## Production gate checklist (2026-07-30)
- [x] TODO items audited — features implemented (archives UI was the remaining GUI gap)
- [x] LangGraph temp-dir e2e green (`/tmp/slmcode-lg-ONOoTB` — 8/8 tasks, full MVP tree)
- [x] Studio UI↔API wiring hardened (SSE connected+heartbeat, patch-only config, connection strip, never-null board)
- [x] Studio UI visually verified (light + dark) from embedded binary (`conn-strip`, no `bgDrift`)
- [x] Query-scoped plan/tasks + summary.md + tester rewrite (multi-turn live e2e PASS)
- [x] Empty/malformed tester finalize forces rewrite (no silent skip / false pass)
- [x] Vague tester failures reopen narrowly (task IDs / focus files / acceptance — not whole board)
- [x] Studio Queries panel wired to `GET /api/queries` + `GET /api/queries/{id}`
- [x] Provider/model flexibility re-verified (presets, env/`OPENAI_BASE_URL`, custom OpenAI-compat gateway tests)
- [x] Single commit → push → `make install-system` → `slmcode version`

## Production ship (2026-07-30 — TUI + agents CRUD)
- [x] Premium default TUI on bare `slmcode` / `slmcode tui` (Studio-parity panels; CI non-interactive fallback)
- [x] Studio custom agents CRUD (`GET/POST/PUT/DELETE /api/agents`) + Agents tab UI
- [x] Stronger anti-wander (junk patch reject, tighter worker/corrector prompts)
- [x] Reviewer fingerprint: trust Disk evidence / ambiguous baseline + LLM fast-path (tests)
- [x] Query-scope + tester rewrite + multi-turn e2e (offline PASS; live attempted)
- [x] `go test ./...` + `go test -race ./pkg/...` green
- [x] Commit `0c31e01` → pushed `UnicoLab/smlcode` → `make install-system` → `slmcode 0.5.9`

## Studio ↔ backend wiring (was missing / broken in practice)
- [x] SSE emits immediate `connected` event + keepalive pings (Live no longer looks dead)
- [x] Config PUT accepts partial Patch only from UI (no `api_key: "***"` round-trip)
- [x] Config rebuild re-wires orchestrator `OnEvent` so Live keeps streaming
- [x] Stop run clears `running` + emits `run_stop`; board/latest never return null arrays
- [x] Connection strip + footer show API/SSE status; SSE auto-reconnects
- [x] Settings can set API key (ignored when blank / `***`)

---

## Planing and Tasks (query-scoped — 2026-07-30)

- [x] Each user query gets a **dedicated plan + tasks** (not a forever-mutating global board)
  - Live `PLAN.md` / `TASKS.md` / `board.json` are rewritten fresh at each `Run` start
  - Durable per-turn copies under `.slmcode/queries/<runID>/` (`PLAN.md`, `TASKS.md`, `board.json`, `meta.json`)
  - Board carries `query_id` + `query`; planner/splitter prompts force a brand-new plan/task list
- [x] Plans/tasks enrich project knowledge **after** the run via `summary.md` + `summaries/INDEX.md` → CONTEXT/MEMORY
- [x] Studio API: `GET /api/queries`, `GET /api/queries/{id}` (summary/plan/tasks per turn)

## Testers (rewrite on failure — 2026-07-30)

- [x] Parse tester JSON (`passed` / `failures`); never soft-accept `"does not work"`
- [x] Per-task tester gate in the review loop (`passed:false` → reject)
- [x] Post-board tester failure **rewrites** this query’s plan/tasks + opens corrective work + one re-execute wave
- [x] QA gate red ends the run as rejected and feeds the same rewrite path
- [x] Multi-turn tmp e2e (offline + live oMLX) verifies query scope + summaries
- [x] Empty / `{}` / malformed tester finalize → **failed + rewrite** (orchestrator no longer skips empty output)
- [x] Vague failures (`does not work` / unclear) reopen **narrowly** (newest done focus + tester; not unrelated blocked/docs)
- [x] Tester prompts/skills require task-ID + file-path citations in `failures[]`

## Studio Queries panel (2026-07-30)
- [x] Nav item **Queries** lists turns from `GET /api/queries`
- [x] Detail view: Summary / Plan / Tasks / Board from `GET /api/queries/{id}`
- [x] Themed to match light/dark Studio (`query-badge`, list cards)

## Closed this pass (engine)
- [x] Query-scoped turns (`pkg/session/turn.go`)
- [x] Tester-driven rewrite (`pkg/orchestrator/rewrite.go`)
- [x] Post-run `summary.md` attached to each query turn
- [x] Prior summaries enrich next-query CONTEXT (knowledge only — not live plan)
- [x] Live e2e: `RUN_E2E=1 go test ./test/e2e/ -run TestLiveMultiTurnQueryScope` (PASS ~120s)
- [x] Review auto-approve trusts disk write evidence even without `status:done` JSON (artifact lesson)
- [x] Deepened offline multi-turn stage test + queries API/UI smoke

## Per-agent settings + slow-path hardening (2026-07-30 — 0.5.10)
- [x] Custom + builtin-override agents: provider / model / endpoint / skills / tools / temp / max_tokens / max_iter / system_prompt (Studio Agents tab + YAML + API)
- [x] Runtime path: `Factory.definition` applies RoleSpec provider/model/temp/tokens/iter/prompt/tools; skills appended into system prompt
- [x] `EnsureAgentProviders` auto-registers OpenAI-compat/Ollama providers for per-agent overrides on orchestrator rebuild (no more missing-provider break)
- [x] Stop aliasing `openai`↔`omlx` (broke intentional per-agent provider selection)
- [x] Skip worker LLM when acceptance already satisfied; reviewer disk-evidence fast-path regression test (`TestReviewFastPathSkipsExecutor`)
- [x] Lean SLM defaults: skip coordinator LLM when `think_passes≤1`; tighter planning-role timeouts

## Remaining risks closed (2026-07-31 — 0.5.11)
- [x] **oMLX multi-turn latency (non-skippable work):** shorter planner/splitter/worker prompts; tighter planning max_tokens; pack cache + leaner CONTEXT; multipass JSON early-exit; skip redundant plan critique at think_passes=2; role timeouts with cancel; parallel explorer+architect; lean docs for planning roles
- [x] **Same-provider / different-endpoint sharing bug:** unique registry keys (`openai@http://host:port/v1`) + agent `Provider` points at key; unit tests for distinct backends
- [x] **TUI full agent CRUD:** `/agents`, `/agent new|edit|delete|show` (wizard + `key=value`); wires `agents.WriteCustom` / rebuild like Studio API; non-TTY list/show + inline fields

## World-class local SLM pass (2026-07-31 — 0.5.12)
- [x] **True token-stream early-exit** in GoLangGraph `CompleteStream` / `CollectStream` — cancel when complete JSON or tool-call args form; agents enable `StreamModeForced` + `DefaultEarlyExit`; live oMLX shows `finish_reason=early_exit`
- [x] **Tool / plan JSON repair** — `pkg/repair` + GenericTool trailing-comma/single-quote/close-brace fixes; ParsePlan/Tasks/Tester/Review use repair
- [x] **Phase latency telemetry** — `Result.LatencyMs`, `KindLatency` events, TUI `/stats`, execute+role timings
- [x] **oMLX auth/doctor** — clearer 401 tips; broader `~/.omlx/settings.json` key spellings; doctor shows shell permission
- [x] **Shell permission modes** — `shell_permission: allow|ask|deny` + `/permission shell=…`
- [x] **TUI UX** — `/compact`, `/sessions` picker, `/stats`, latency strip
- [x] **Live oMLX benchmark** — `TestLiveOMLXLatencyBenchmark` + `docs/omlx-latency-bench.txt` (t1 ~32s success; early-exit confirmed)
- [x] Tests: stream early-exit, repair, shell normalize; `go test ./...` + race pkgs green

### Closed this pass (2026-07-31 — 0.5.13)
- [x] **Mid-ReAct interrupt/resume** — `/stop` + Ctrl+C cancel through orchestrator→loop→CompleteStream; `session.MarkInterrupted` + `checkpoint.json`; TUI `/resume` + `slmcode session resume` continue from board/tasks (execute), not full restart when tasks exist; unit tests for cancel + normalize
- [x] **Embedding-quality memory retrieval** — `pkg/retrieval` OpenAI-compat `/v1/embeddings` + lexical TF-IDF fallback; config `embedding_*` + env; doctor visibility; CONTEXT injection ranks prior summaries/MEMORY/skills; fake-embedder tests prove relevant summaries outrank noise
- [x] **Multi-file rename reliability** — `ws_mv` / `ws_delete`; `plan.DetectRenameIntent` + `RenameSatisfied`; evidence/acceptance recognize symbol+file renames; tester override when disk rename OK; anti-wander prompts; offline e2e + unit tests

### Closed this pass (2026-07-31 — 0.5.14)
- [x] **True mid-tool-call ReAct HITL resume** — persist messages + pending tool calls + iteration/provider under `.slmcode/queries/<id>/react/`; GoLangGraph `SubAgentRequest/Result` seed+capture; `/resume` + `session resume` continue from history (no cold replan when messages exist); offline fake-executor interrupt/resume test
- [x] **Local embedding fallback** — pure-Go hashing/n-gram `LocalEmbedder`; cascade openai → local → lexical; doctor reports `openai` / `local` / `lexical`; offline ranking test without network
- [x] **Rename mid-review escalate fix** — `RenameSatisfied` before reviewer LLM; `ws_mv`/delete+create/git rename as write evidence; tester gate skips reopen when rename on disk; regression: weak tool log + disk rename → no reviewer / no escalate / Success

### Closed this pass (2026-07-31 — 0.5.15)
- [x] **GoLangGraph production dependency** — resume/early-exit/usage shipped as `v0.2.0` on `piotrlaczkowski/GoLangGraph`; slmcode `go.mod` uses the tagged module (local `replace` removed)
- [x] **Speculative parallel specialists** — explorer required + optional docs/architect; cancel losers when explorer wins; respects `max_parallel` / `think_passes`
- [x] **Stream early-exit token accounting** — estimate chars/4 when Usage empty; surface in Result + TUI `/stats` + `KindUsage` events; optional `$` only when `price_*_per_mtok` configured
- [x] Tests: speculate cancel + usage estimate; `go test ./...` + race pkgs; `make build` + `make install-system`

### Closed this pass (2026-07-31 — 0.5.16)
- [x] **Speculative cancel beyond explore** — reviewer↔acceptance race + duplicate reviewer/tester strategies; disk acceptance / first decisive JSON cancels losers; respects `max_parallel`
- [x] **True tiktoken usage** — GoLangGraph `v0.2.1` offline `cl100k_base` via tiktoken-go; heuristic fallback only if tokenizer unavailable; empty-Usage path covered in tests
- [x] **Cost presets** — `price_preset=local|omlx|openai|anthropic|openrouter|off` + explicit `price_*_per_mtok`; TUI `/stats` shows “tokens only (set price_preset or price_*_per_mtok to enable $)” when unset; never invent $ for unknown models
- [x] **GoLangGraph sibling tree** — dirty WIP discarded/reset to published main; tiktoken committed+tagged `v0.2.1` + pushed; working tree clean (no fragile `replace`)
- [x] Tests: review/tester cancel-on-win, tokenizer vs empty Usage, preset vs off; `go test ./...` + race pkgs; `make install-system` → **0.5.16**

### Honest remaining gaps vs “best in world”
- [ ] Full per-tool call cost breakdown in Studio UI charts


