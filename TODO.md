## Fix agents in the studio
**Done.** `GET /api/agents/{id}` returns full config including built-in `system_prompt`. Studio loads detail on row click / Edit. TUI `/agent show|edit` seeds from the same path. Verified live via Studio API.

## Tester agent
**Done.** Tester prompt/skill require real `ws_shell` execution. Soft-pass without `commands[]` is rejected. Per-task tester finish schema fixed. Finalize mandates commands. Live oMLX run executed `python main.py --help` / smoke checks.

## Project plan
**Done.** `ParsePlanJSON` accepts string or object steps; never dumps raw JSON into Summary. Unparsed output goes to a fenced Raw appendix. Live PLAN.md stayed structured.

## Spec / functional code quality
**Done.** Clarifier + greenfield harness (`requirements.txt` / pytest smoke) + auto tester task.
Deterministic `post_worker_smoke` (`py_compile` / `go test`) before review; pre-test smoke before LLM tester;
QA gate uses compileall/pytest (never `--help`). Soft-pass needs real Observation/smoke evidence.
Live oMLX greenfield e2e PASS (~98s).

## TUI improvements
**Done.** Live throttled redraw during runs + mid-run board refresh. `KindDebug` for runner internals (hidden by default). Studio Debug toggle. Compact mode default on. SSE prefers latest event under backpressure.

## Check the current failure and improve
**Done.** Full `go test ./...` green. Offline quality e2e + live `TestLiveQualityGreenfieldPython` (oMLX) PASS — produced working `main.py` with real execution evidence.

## Planning / scoping (PRD interview)
**Done.** Claude Code AskUserQuestion + pi-clarify style interviewer (`clarify_mode`: auto|ask|off),
locked PRD in CONTEXT, Studio ask modal + `POST /api/clarify/answer`, post-split `scope_judge`
enriching every task with concrete acceptance/checklist before execute.

## Claude Code gap ports
**Done.** Plan approve gate · real CONTEXT `/compact context` · interactive shell ask ·
wave rewind snapshots · hooks.json Pre/PostToolUse · thin read-only MCP (`mcp_call`) ·
wired `auto_approve`.

## little-coder SLM harness ports
**Done.** Write-guard · read-before-edit · shell-write guard · tool/knowledge inject ·
quality-monitor · reserved device names · path normalize · tool-output truncate.

## Code quality > giant LLMs
**Done.** Numbered ws_read + offset/limit + auto-trim · fuzzy edit recovery · static gate ·
require smoke · finalize-warn + thinking-budget · text tool-call detection · JS smoke ·
knowledge cards · stricter reviewer/corrector · cooler temps.

**Also done.** Over-edit guard (refuse whole-file-style edits) · no-op edit refuse ·
claims gate (hallucinated `files_changed`) · worker self-critique on weak output ·
algorithm cheat-sheets (binary search, DP, two pointers, BFS/DFS, backtracking, sort).
Config: `claims_gate`, `worker_critique`, `over_edit_guard` (default ON).

## little-coder quality ports (round 4)
**Done.** Worker multipass when `think_passes≥2` (incomplete JSON → critique/refine) ·
finalize mid-run steer on ReAct resume · react context-watchdog (conversation compact at
80% of `max_context_kb` with #68 hysteresis). Config: `react_compact`,
`react_compact_at_percent` (default ON / 80).

## little-coder quality ports (round 5)
**Done.** Mid-ReAct loopguard (refuse verbatim repeated tool calls via wrappers) ·
output-parser extract + arg-rich text-tool nudge · edit/write/patch failure recovery
cards in tool results. `quality_monitor` now also wraps coding tools at register time.

## little-coder quality ports (round 6)
**Done.** Remaining knowledge cards (hash/tree/BFS-state/io-wrapper/string-rules/zipper) ·
bash SAFE_PREFIXES whitelist (`shell_whitelist`, `shell_allow`) · thinking-budget hard
abort recovery (post-turn) · first-write-wins file checkpoints · per-model profiles
(`model_profiles`) · malformed_args quality detection.

## UX + eval (v0.6.0)
**Done.** `KindIntervention` / `KindTurn` SSE · TUI banners + turn meter + progress strip ·
prompt history · `/clear` · `/plan` · richer `/help` · Studio intervention banner + turn chip ·
read trim by context % · `slmcode eval` + `pkg/eval` harness · Studio UI markers aligned.

## Quality: no more false success on placeholder scaffolds (TestSLMs)
**Done.** Root cause: `promoteBoardOnQAGreen` + weak `compileall` QA rubber-stamped
escalated/blocked tasks; static gate missed `# Placeholder implementation`; existence-only
acceptance / already-satisfied skipped real work; Studio had no escalate intervention.

Fixes:
- Never promote escalated / `status=blocked` / missing files / static stubs on QA green
- Weak QA (`compileall` / `py_compile`) does not clear tester rejection or escalate backlog
- Run success requires no escalated tasks left
- Static quality catches Placeholder comments + stub constant returns
- Review-time static insurance + Studio `KindIntervention` on escalate / stub refuse
- Vague acceptance rejects "exists and contain…", "tool evidence", `pytest --collect-only`
- Architect/splitter/reviewer/tester/worker prompts require functional code, not stubs
- Greenfield harness now catches "setup/template/folder structure" + LangGraph class agents
  (auto-adds requirements.txt, main.py, pytest) + LangGraph knowledge card
- Studio Live: overview grid (status / activity / enrich / deps) + dedicated `LiveLogs` panel
- Studio pipeline header redesigned: Prepare/Design/Build/Verify/Finish groups, track fill,
  live agent/task, board %, harness intervention chip
- Static gate rejects `from langgraph import Graph`; clarifier/PRD defaults force runnable
  LangGraph acceptance; e2e `TestLangGraphTemplateQueryPath` locks the harness path
- Placeholder specialist (`@placeholder`) + project-wide stub scan after execute (polish phase);
  fills real code or flags precise `path:line` gaps; Studio continue-ask when retries/QA
  exhausted (`continue` / `stop` / `flag_only`) via `/api/continue/*`

## Pipeline config + reference-quality bar (v0.7.0)
**Done.** Config-driven pipeline (`.slmcode/pipeline.yaml`) with phase agent bindings,
execute-loop reviewer/corrector, and insertable slots (before/after/replace) with full
prompt/params control. Studio Pipeline tab + dynamic progress header follow the same config.
`GET/PUT /api/pipeline`, custom agents usable in specialist mode and board roles.

Also: project completeness reference bar (LangGraph/FastAPI/CLI), real-query eval suite,
loop SSE (`kind=loop`) with continue/abort UX, preserve harness tasks when capping to 8,
docs for pipeline + updated agents/studio/config/architecture.
