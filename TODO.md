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
- [ ] Single commit → push → `make install-system` → `slmcode version`

## Studio ↔ backend wiring (was missing / broken in practice)
- [x] SSE emits immediate `connected` event + keepalive pings (Live no longer looks dead)
- [x] Config PUT accepts partial Patch only from UI (no `api_key: "***"` round-trip)
- [x] Config rebuild re-wires orchestrator `OnEvent` so Live keeps streaming
- [x] Stop run clears `running` + emits `run_stop`; board/latest never return null arrays
- [x] Connection strip + footer show API/SSE status; SSE auto-reconnects
- [x] Settings can set API key (ignored when blank / `***`)

---

### Self-use diagnosis (2026-07-29) — fixed

From `.slmcode/` board/MEMORY during "improve slmcode" run:

| Symptom | Root cause | Fix |
|---------|------------|-----|
| PROJECT.md empty | Never written by any agent | Seed + ensureProjectDoc + Evolve enrich |
| `context deadline exceeded` | 5m task timeout + 180s LLM timeout + parallel contention | 12m task / aligned LLM timeout / parallel=2 |
| `review rejected after max retries` / no actionable changes | Review trusted JSON claims; workers often skipped tools | Disk/git evidence gate; require ws_* or real diff |
| TASKS.md 654KB | Full packs+outputs persisted every wave | Lean ToMarkdown + ephemeral BuildInput packs |
| Duplicate tasks | Coordinator `add_task` spam | Dedupe + cap |
| No errors.md | `pkg/loop` did not compile; handler unwired | Fixed handler + wired FailureHandler |
