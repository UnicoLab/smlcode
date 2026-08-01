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
