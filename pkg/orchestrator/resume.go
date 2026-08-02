package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/knowledge"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Resume continues an interrupted query turn from the last board/tasks checkpoint
// (execute phase) instead of restarting planning from zero.
func (o *Orchestrator) Resume(ctx context.Context, turnID string) (*Result, error) {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return nil, fmt.Errorf("a run is already in progress")
	}
	ctx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	o.running = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.running = false
		o.cancel = nil
		o.mu.Unlock()
		cancel()
	}()

	turn, err := session.ResolveResumeTurn(o.cfg.SlmDir(), turnID)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	query := turn.Query
	runID := turn.ID
	board := session.NormalizeForResume(turn.Board)
	board.QueryID = runID
	board.Query = query

	o.mu.Lock()
	o.currentTurn = turn
	o.latencyMs = map[string]int64{}
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.currentTurn = nil
		o.mu.Unlock()
	}()

	_ = session.ClearInterrupted(o.cfg.SlmDir(), turn)
	session.SetPhase(o.cfg.SlmDir(), turn, session.PhaseExecute)
	o.persistBoard(&board)
	from := turn.ResumeFrom
	if from == "" {
		from = session.PhaseExecute
	}
	reactMode := session.HasReactHistory(o.cfg.SlmDir(), runID)
	if reactMode {
		o.emit("init", fmt.Sprintf("resuming %s from %s (ReAct message history)", runID, from), "")
	} else {
		o.emit("init", fmt.Sprintf("resuming %s from %s", runID, from), "")
	}

	// No tasks yet → unavoidable full restart with the same query.
	if len(board.Tasks) == 0 {
		o.emit("init", "no tasks checkpoint — restarting full pipeline for same query", "")
		o.mu.Lock()
		o.running = false
		o.cancel = nil
		o.currentTurn = nil
		o.mu.Unlock()
		cancel()
		return o.Run(context.WithoutCancel(ctx), query)
	}

	_ = o.store.SetQuery(query)
	o.shared.SetGlobal("query", query)
	o.shared.SetGlobal("query_id", runID)
	o.shared.SetGlobal("root", o.cfg.Root)
	o.shared.SetGlobal("resumed", "1")
	if reactMode {
		o.shared.SetGlobal("resume_mode", "react")
	} else {
		o.shared.SetGlobal("resume_mode", "board")
	}

	skillPack := o.skillPackFor(plan.RoleWorker, query)
	return o.finishFromExecute(ctx, runID, query, skillPack, &board, start)
}

// finishFromExecute runs execute → test → memory → summary for an existing board.
func (o *Orchestrator) finishFromExecute(ctx context.Context, runID, query, skillPack string, board *plan.Board, start time.Time) (*Result, error) {
	if o.focus != nil {
		o.focus.Clear()
	}
	runner := loop.NewRunner(o.executor, o.shared)
	runner.Root = o.cfg.Root
	runner.SlmDir = o.cfg.SlmDir()
	runner.TurnID = runID
	runner.Store = o.boardStore
	runner.Focus = o.focus
	runner.MaxRetries = o.cfg.MaxRetries
	runner.MaxParallel = o.cfg.MaxParallel
	runner.Timeout = o.cfg.TaskTimeout
	runner.ReviewerRole = o.Pipeline().Execute.Reviewer
	runner.CorrectorRole = o.Pipeline().Execute.Corrector
	runner.PostWorkerSmoke = o.cfg.PostWorkerSmoke
	runner.WaveSnapshots = o.cfg.WaveSnapshots
	runner.RewindMgr = o.rewindMgr
	runner.FailureHandler = loop.NewEnhancedFailureHandler(o.cfg.Root)
	runner.Log = func(format string, args ...interface{}) {
		o.emitFull("execute", stream.KindDebug, "", "", fmt.Sprintf(format, args...), "", "")
	}
	runner.OnEvent = func(kind, agent, taskID, msg, scope, output string) {
		o.emitFull("execute", kind, agent, taskID, msg, scope, output)
	}
	runner.OnEscalate = func(ctx context.Context, board *plan.Board, t plan.Task, detail string) {
		o.runEscalateAsk(ctx, board, t, detail)
	}
	runner.OnUsage = func(u llm.Usage, estimated bool, _, _ string) {
		o.recordUsage(u, estimated)
	}
	runner.AfterWave = func(ctx context.Context, board *plan.Board, wave []plan.Task) {
		o.evolveAfterWave(ctx, query, skillPack, board, wave)
		o.maybeCompactContext(ctx)
		o.coordinate(ctx, query, board, "after-wave")
	}
	runner.QualityMonitor = o.cfg.QualityMonitor
	runner.StaticQuality = o.cfg.StaticQuality
	runner.RequireSmoke = o.cfg.RequireSmoke
	runner.ClaimsGate = o.cfg.ClaimsGate
	runner.WorkerCritique = o.cfg.WorkerCritique
	runner.ThinkPasses = o.cfg.ThinkPasses
	runner.ThinkingBudget = o.cfg.ThinkingBudget
	runner.ThinkingBudgetTokens = o.resolvedProfile().ThinkingBudgetTokens
	if runner.ThinkingBudgetTokens <= 0 {
		runner.ThinkingBudgetTokens = o.cfg.ThinkingBudgetTokens
	}
	runner.AutoTextTools = o.cfg.AutoTextTools
	runner.FinalizeWarn = o.cfg.FinalizeWarn
	runner.ReactCompact = o.cfg.ReactCompact
	runner.ReactCompactAtPercent = o.cfg.ReactCompactAtPercent
	runner.MaxContextKB = o.cfg.MaxContextKB
	runner.BuildInput = func(t plan.Task) string {
		lean := loop.StripScopedPack(t.Description)
		docs := contextstore.LeanDocsForRole(t.Role)
		tp, _ := o.packer.Build(t.Role, query, docs, t.Files, o.skillPackFor(t.Role, query))
		tp.TaskID = t.ID
		tp.TaskTitle = t.Title
		t.Description = tp.Render() + "\n## Task instructions\n\n" + lean
		return o.formatWorkerPrompt(query, t)
	}

	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseExecute)
	if session.HasReactHistory(o.cfg.SlmDir(), runID) {
		o.emit("execute", fmt.Sprintf("resume execute · %d tasks · ReAct history restored", len(board.Tasks)), "")
	} else {
		o.emit("execute", fmt.Sprintf("resume execute · %d tasks", len(board.Tasks)), "")
	}
	execStart := time.Now()
	if err := runner.RunBoard(ctx, board); err != nil {
		return o.checkpointInterrupt(board, session.PhaseExecute, err)
	}
	if runner.ResumedReact {
		o.emit("execute", "continued from ReAct message checkpoint (no cold replan)", "")
	}
	o.recordLatency("execute", time.Since(execStart))
	snap := o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(board, session.PhaseExecute, err)
	}

	return o.finalizeAfterExecute(ctx, runID, query, skillPack, board, runner, start)
}

// finalizeAfterExecute covers tester → QA → memory → summary (shared by Run + Resume).
func (o *Orchestrator) finalizeAfterExecute(ctx context.Context, runID, query, skillPack string, board *plan.Board, runner *loop.Runner, start time.Time) (*Result, error) {
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseTest)

	// Deterministic project smoke BEFORE LLM tester — hard fail if code won't compile/run.
	var preSmokeFail string
	var preSmokeOKCmd string
	var preSmokeOKOut string
	if o.cfg.QAGate || o.cfg.PostWorkerSmoke {
		cmd := strings.TrimSpace(o.cfg.QAGateCommand)
		if cmd == "" {
			cmd = quality.DetectProjectCommand(o.cfg.Root)
		}
		if cmd != "" {
			if prep := quality.BootstrapDeps(o.cfg.Root, cmd); prep != "" {
				_ = quality.RunSmoke(ctx, o.cfg.Root, prep, o.cfg.TaskTimeout)
			}
			sr := quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
			_ = o.store.Append(contextstore.DocScratch, "Deterministic pre-test",
				fmt.Sprintf("cmd: %s\nok=%v\n\n%s", cmd, sr.OK, truncate(sr.Output, 3000)))
			o.emitFull("test", stream.KindOutput, "qa", "",
				fmt.Sprintf("pre-test %s", map[bool]string{true: "green", false: "RED"}[sr.OK]),
				"", truncate(sr.Output, 800))
			if !sr.OK {
				preSmokeFail = fmt.Sprintf(
					`{"passed":false,"commands":[%q],"summary":"deterministic pre-test failed","failures":[%q]}`,
					cmd, truncate(strings.ReplaceAll(sr.Output, `"`, "'"), 400))
			} else {
				preSmokeOKCmd = cmd
				preSmokeOKOut = sr.Output
			}
		}
	}

	if err := o.runPipelineSlots(ctx, "test", "before", query, "", ""); err != nil {
		return nil, err
	}
	testAgent := o.phaseAgent("test", plan.RoleTester)
	o.emitAgent("test", testAgent, "", "verification pass", "", "")
	_, tasksMD := board.ToMarkdown()
	testPack, _ := o.packer.Build(testAgent, query, contextstore.DefaultDocsForRole("tester"), nil, o.skillPackFor(testAgent, query))
	testPrompt := testPack.Render() + "\nTasks:\n" + truncate(tasksMD, 4000) +
		"\n\nVerify THIS query's work with REAL execution.\n" +
		"You MUST call ws_shell at least once (install deps if needed, then pytest/go test/python smoke).\n" +
		"Reading files alone is not enough. Return STRICT JSON: " +
		`{"passed":true|false,"commands":["..."],"summary":"...","failures":["..."]}` +
		"\nIf anything does not work, set passed=false and list concrete failures. Do not approve broken work."
	if preSmokeFail != "" {
		testPrompt += "\n\n## Deterministic pre-test ALREADY FAILED\n" + preSmokeFail +
			"\nYou must re-run commands and confirm fixes, or return passed=false with concrete failures."
	}

	// Speculative tester race: disk/rename acceptance can cancel tester LLM;
	// duplicate tester strategies cancel on first decisive JSON.
	var testOut string
	var fromDisk bool
	if o.Pipeline().HasReplace("test") {
		if err := o.runPipelineSlots(ctx, "test", "replace", query, "", ""); err != nil {
			return nil, err
		}
		testOut = `{"passed":false,"summary":"pipeline replace test — slot must verify","failures":["replaced tester"]}`
	} else if o.phaseEnabled("test") {
		testOut, fromDisk, _ = o.speculateTester(ctx, query, board, testPrompt)
	} else {
		testOut = `{"passed":true,"summary":"pipeline test phase disabled","commands":[],"failures":[]}`
	}
	if preSmokeFail != "" && !fromDisk {
		// Never let a vague LLM pass override a failed deterministic pre-test.
		// Only accept LLM pass when it includes real shell evidence (re-run after fix).
		if !plan.TesterFailed(testOut) && !plan.TesterHasShellEvidence(testOut) {
			testOut = preSmokeFail
		}
	}
	// Pre-test green is hard evidence — attach smoke so honest passed:true from SLMs
	// (commands[] listed, but no ws_shell Observation) is not false-rejected.
	if preSmokeOKCmd != "" && !fromDisk && !plan.TesterHasShellEvidence(testOut) {
		sec := quality.FormatSmokeSection(quality.SmokeResult{
			OK: true, Ran: true, Command: preSmokeOKCmd, Output: truncate(preSmokeOKOut, 1200),
		})
		if sec != "" {
			testOut = strings.TrimSpace(sec) + "\n\n" + strings.TrimSpace(testOut)
			o.emit("test", "attached pre-test smoke evidence for tester finalize", "")
		}
	}
	testerRejected := false
	if fromDisk {
		promoteRenameTasksDone(board)
		o.persistBoard(board)
		o.emit("test", "rename/disk acceptance won — cancelled tester LLM losers", "")
		_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)
		return o.completeRun(ctx, runID, query, skillPack, board, testOut, false, false, "", start)
	}
	if strings.TrimSpace(testOut) == "" {
		testOut = `{"passed":false,"summary":"empty tester finalize","failures":["empty or missing tester JSON — treat as failed"]}`
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "",
			"tester returned empty finalize — forcing plan/task rewrite", "", testOut)
	} else {
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "tester output", "", truncate(testOut, 1200))
	}
	_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)

	// If disk rename is OK but tester is vague/negative, trust disk.
	if renameDiskOK(o.cfg.Root, query, board) {
		promoteRenameTasksDone(board)
		o.persistBoard(board)
		tj := plan.ParseTesterJSON(testOut)
		if !tj.Passed {
			o.emit("test", "tester rejected but rename disk evidence OK — overriding to pass", "")
			testOut = `{"passed":true,"summary":"rename verified on disk (overrode vague tester)","commands":[],"failures":[]}`
		}
		testerRejected = false
	} else {
		testerRejected = o.applyTesterFeedback(ctx, query, board, testOut)
		if testerRejected && runner != nil {
			snap := o.boardStore.Snapshot()
			board = &snap
			if board.AgentWorkRemaining() {
				o.emit("execute", "corrective wave after tester rewrite", "")
				o.emitLoop("execute", LoopEvent{
					Action: "corrective_wave",
					Reason: "tester not satisfied — running corrective execute wave",
					From:   "test",
					To:     "execute",
					Wave:   o.waveCounter,
				})
				if err := runner.RunBoard(ctx, board); err != nil {
					if isCancelErr(err) {
						return o.checkpointInterrupt(board, session.PhaseExecute, err)
					}
					o.emit("execute", "corrective wave warning: "+err.Error(), "")
				}
				snap = o.boardStore.Snapshot()
				board = &snap
				o.persistBoard(board)
				o.emitAgent("test", plan.RoleTester, "", "re-verify after rewrite", "", "")
				o.emitLoop("test", LoopEvent{
					Action: "reverify",
					Reason: "re-verifying after corrective wave",
					From:   "execute",
					To:     "test",
					Wave:   o.waveCounter,
				})
				_, tasksMD2 := board.ToMarkdown()
				testOut2, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
					"\nTasks:\n"+truncate(tasksMD2, 4000)+
					"\n\nRe-verify after fixes. STRICT JSON with passed true/false.")
				if strings.TrimSpace(testOut2) == "" {
					testOut2 = `{"passed":false,"summary":"empty tester finalize on retry","failures":["empty tester JSON after corrective wave"]}`
				}
				testOut = testOut2
				_ = o.store.Append(contextstore.DocScratch, "Verification (retry)", testOut2)
				if renameDiskOK(o.cfg.Root, query, board) {
					testerRejected = false
					testOut = `{"passed":true,"summary":"rename verified on disk after corrective wave","commands":[],"failures":[]}`
					o.emitLoop("test", LoopEvent{
						Action: "resolved",
						Reason: "disk evidence cleared tester rejection after corrective wave",
						From:   "test",
						To:     "done",
						Wave:   o.waveCounter,
					})
				} else if o.applyTesterFeedback(ctx, query, board, testOut2) {
					snap = o.boardStore.Snapshot()
					board = &snap
				} else {
					testerRejected = false
					o.emitLoop("test", LoopEvent{
						Action: "resolved",
						Reason: "tester passed after corrective wave",
						From:   "test",
						To:     "done",
						Wave:   o.waveCounter,
					})
				}
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(board, session.PhaseTest, err)
	}

	// Polish: detect/fill placeholders before QA promotion (additional quality step).
	gaps := o.runPlaceholderPass(ctx, query, board, runner)
	if len(gaps) > 0 {
		testerRejected = true
	}

	// Reference-bar completeness: catch TestSLMs-style "success" with empty
	// packages / missing main+tests / bad LangGraph APIs before QA promote.
	completenessFailed := false
	if completeness := quality.CheckProjectCompleteness(o.cfg.Root, query); len(completeness) > 0 {
		completenessFailed = true
		testerRejected = true
		rep := quality.FormatCompletenessReport(completeness)
		_ = o.store.Append(contextstore.DocScratch, "Project completeness", rep)
		o.emitFull("test", stream.KindIntervention, "completeness", "",
			fmt.Sprintf("reference bar: %d completeness gap(s)", len(completeness)),
			quality.InterventionReview, truncate(rep, 1200))
		fails := make([]string, 0, len(completeness))
		for _, c := range completeness {
			fails = append(fails, c.Reason)
		}
		o.emitLoop("test", LoopEvent{
			Action:   "rewrite",
			Reason:   "workspace below expert reference bar — reopening corrective work",
			Failures: trimFailures(fails, 6),
			From:     "test",
			To:       "execute",
		})
		failJSON, _ := json.Marshal(fails)
		fake := `{"passed":false,"summary":"project completeness below reference bar","failures":` +
			string(failJSON) + `}`
		_ = o.applyTesterFeedback(ctx, query, board, fake)
		snap := o.boardStore.Snapshot()
		board = &snap
	}

	qaCmd := strings.TrimSpace(o.cfg.QAGateCommand)
	if qaCmd == "" {
		qaCmd = quality.DetectProjectCommand(o.cfg.Root)
	}
	qaFailed := o.runQAGate(ctx, query, board)
	if qaFailed {
		testerRejected = true
		fake := `{"passed":false,"summary":"qa_gate command still failing","failures":["qa_gate red"]}`
		_ = o.applyTesterFeedback(ctx, query, board, fake)
		snap := o.boardStore.Snapshot()
		board = &snap
	} else if o.cfg.QAGate {
		weakQA := quality.IsWeakQACommand(qaCmd)
		escalated := boardHasEscalated(board)
		// Syntax-only gates (compileall / py_compile) must NOT clear tester
		// rejection or rubber-stamp escalated tasks — TestSLMs false success.
		if weakQA && (testerRejected || escalated || len(gaps) > 0 || completenessFailed) {
			o.emitFull("test", stream.KindIntervention, "qa", "",
				"qa_gate is syntax-only — keeping tester/escalation failures",
				quality.InterventionReview,
				"weak_qa:"+qaCmd)
			o.emit("test", "qa_gate weak ("+qaCmd+") — not promoting escalated/rejected tasks", "")
		} else if len(gaps) == 0 && !completenessFailed {
			// Hard gate green wins over soft tester evidence gaps / rewrite noise.
			// Never clear a failed reference-bar completeness check.
			if testerRejected {
				o.emit("test", "qa_gate green — clearing soft tester rejection", "")
				testerRejected = false
			}
			promoteBoardOnQAGreen(o.cfg.Root, board)
			o.persistBoard(board)
		} else if completenessFailed {
			o.emit("test", "qa_gate green but completeness gaps remain — not promoting", "")
		}
	}

	// HITL: when retries/QA exhausted and work remains, ask to continue.
	reason := "retries or QA exhausted with unfinished work"
	if len(gaps) > 0 {
		reason = fmt.Sprintf("%d placeholder gap(s) remain after fill pass", len(gaps))
	} else if qaFailed {
		reason = "QA gate still red after max rounds"
	} else if testerRejected {
		reason = "tester rejected after corrective wave"
	} else if boardHasEscalated(board) {
		reason = "tasks escalated after max review retries"
	}
	if another, b2 := o.runContinueAsk(ctx, query, board, runner, reason, gaps, testerRejected, qaFailed); another {
		board = b2
		// Re-scan + light re-test after continue wave (single extra pass).
		gaps = quality.ScanProjectPlaceholders(o.cfg.Root, board)
		if len(gaps) == 0 && !boardHasEscalated(board) {
			testerRejected = false
			qaFailed = false
		} else {
			testerRejected = true
			if len(gaps) > 0 {
				flagPreciseGaps(board, gaps)
				o.persistBoard(board)
			}
		}
	} else if b2 != nil {
		board = b2
	}

	return o.completeRun(ctx, runID, query, skillPack, board, testOut, testerRejected, qaFailed, qaCmd, start)
}

func (o *Orchestrator) completeRun(ctx context.Context, runID, query, skillPack string, board *plan.Board, testOut string, testerRejected, qaFailed bool, qaCmd string, start time.Time) (*Result, error) {
	_ = skillPack
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseMemory)
	o.emitAgent("memory", "memory", "", "distilling long-term memory", "", "")
	var allLessons []learning.Lesson
	for _, t := range board.Tasks {
		allLessons = append(allLessons, learning.Extract(t)...)
	}
	lessonsMD := learning.RenderMarkdown(allLessons)
	if lessonsMD != "" {
		_ = o.store.Append(contextstore.DocMemory, "Auto-lessons", lessonsMD)
	}
	memPack, _ := o.packer.Build("memory", query, contextstore.DefaultDocsForRole("memory"), nil, o.skillPackFor("memory", query))
	memOut, _ := o.runRoleMultipassTracked(ctx, "memory", "", memPack.Render()+fmt.Sprintf(
		"\nFailed: %d\nWrite ≤8 durable bullets under ## Lessons (conventions, pitfalls, paths).", board.FailedCount()))
	if strings.TrimSpace(memOut) != "" {
		_ = o.store.Append(contextstore.DocMemory, "Session distillation", memOut)
		lessonsMD = strings.TrimSpace(lessonsMD + "\n" + memOut)
	}

	o.emit("skills", "evolving SKILLS.md + learned skill", "")
	skillList, _ := o.skills.List()
	if ev, err := knowledge.Evolve(o.cfg.SlmDir(), query, board, lessonsMD, skillList); err == nil && ev != nil {
		o.emitFull("skills", stream.KindLearn, "memory", "",
			fmt.Sprintf("updated %s + %s", ev.SkillsIndex, ev.LearnedSkill), "", "")
		roots := []string{
			filepath.Join(o.cfg.SlmDir(), "skills"),
			filepath.Join(o.cfg.SlmDir(), "skills", "_bundled"),
		}
		roots = append(roots, globalSkillRoots()...)
		roots = append(roots, o.cfg.SkillsDirs...)
		o.skills = skills.NewLoader(roots...)
	}

	_ = o.store.Append(contextstore.DocContext, "Run complete", summarize(board, board.Plan))

	failed := board.FailedCount()
	qaGreen := o.cfg != nil && o.cfg.QAGate && !qaFailed
	weakQA := quality.IsWeakQACommand(qaCmd)
	escalatedLeft := boardHasEscalated(board)
	// Never mark success when escalated/blocked tasks remain, even if a weak
	// compileall QA gate was green (TestSLMs false-success regression).
	success := !testerRejected && failed == 0 && board.AllDone() && !escalatedLeft
	// Soft success from QA alone requires a *strong* gate (pytest / go test / …).
	// Syntax-only compileall must not rubber-stamp an incomplete board.
	if !success && qaGreen && !weakQA && !testerRejected && failed == 0 && !escalatedLeft &&
		!board.AgentWorkRemaining() {
		success = true
	}
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: success, FailedTasks: failed,
		Duration: time.Since(start), Summary: summarize(board, board.Plan),
		Backend: o.cfg.Backend, LatencyMs: o.snapshotLatency(),
		Usage: o.snapshotUsage(),
	}
	if testerRejected || escalatedLeft {
		res.Success = false
		if testerRejected {
			res.Summary = res.Summary + " (tester/QA rejected — plan/tasks rewritten)"
		} else if escalatedLeft {
			res.Summary = res.Summary + " (escalated tasks need human review in Studio)"
		}
	} else if qaGreen && weakQA && !success {
		res.Summary = res.Summary + " (qa_gate is syntax-only — need pytest/tests for success)"
	} else if qaGreen && success && !board.AllDone() {
		res.Summary = res.Summary + " (qa_gate green)"
	}
	extraNotes := lessonsMD
	if strings.TrimSpace(testOut) != "" {
		extraNotes = strings.TrimSpace(extraNotes + "\n\n### Tester\n" + truncate(testOut, 1500))
	}
	if o.currentTurn != nil {
		if spath, serr := session.WriteTurnSummary(o.cfg.SlmDir(), o.currentTurn, *board, extraNotes); serr == nil && spath != "" {
			o.emit("session", "summary "+filepath.Base(filepath.Dir(spath))+"/summary.md", "")
		}
		_ = session.ClearInterrupted(o.cfg.SlmDir(), o.currentTurn)
		session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseDone)
	}
	if path, err := session.Save(o.cfg.SlmDir(), session.Session{
		ID: runID, Query: query, Summary: res.Summary, Success: res.Success, Board: *board,
	}); err == nil {
		o.emit("session", "saved "+filepath.Base(path), "")
		if arch, aerr := session.Archive(o.cfg.SlmDir(), runID, query, res.Summary); aerr == nil && arch != "" {
			o.emit("session", "archived "+filepath.Base(arch), "")
		}
	}
	o.emitLatencySummary(res)
	o.emit("done", res.Summary, "")
	return res, nil
}

func (o *Orchestrator) checkpointInterrupt(board *plan.Board, phase string, err error) (*Result, error) {
	if board != nil {
		o.persistBoard(board)
	}
	if o.currentTurn != nil && board != nil && isCancelErr(err) {
		_ = session.MarkInterrupted(o.cfg.SlmDir(), o.currentTurn, *board, phase)
		msg := fmt.Sprintf("interrupted at %s — board saved; /resume %s", phase, o.currentTurn.ID)
		if session.HasReactHistory(o.cfg.SlmDir(), o.currentTurn.ID) {
			msg = fmt.Sprintf("interrupted at %s — ReAct history + board saved; /resume %s", phase, o.currentTurn.ID)
		}
		o.emitFull("stop", stream.KindPhase, "", "", msg, "", "")
		res := &Result{
			ID: o.currentTurn.ID, Query: o.currentTurn.Query, Board: *board,
			Success: false, FailedTasks: board.FailedCount(),
			Summary: fmt.Sprintf("interrupted at %s — resume with /resume", phase),
			Backend: o.cfg.Backend, LatencyMs: o.snapshotLatency(),
			Usage: o.snapshotUsage(),
		}
		return res, err
	}
	return nil, err
}

func isCancelErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "context cancelled") ||
		strings.Contains(lower, "canceled") ||
		strings.Contains(lower, "cancelled")
}

func renameDiskOK(root, query string, board *plan.Board) bool {
	if root == "" || board == nil {
		return false
	}
	spec := plan.DetectRenameIntent(query)
	if spec.Kind == plan.RenameNone {
		return false
	}
	var focus []string
	for _, t := range board.Tasks {
		focus = append(focus, t.Files...)
		if plan.RenameSatisfied(root, plan.DetectRenameIntent(query, t.Title, t.Description, t.Acceptance), t.Files) {
			return true
		}
	}
	if spec.OldPath != "" {
		focus = append(focus, spec.OldPath)
	}
	if spec.NewPath != "" {
		focus = append(focus, spec.NewPath)
	}
	return plan.RenameSatisfied(root, spec, focus)
}

// boardHasEscalated reports whether any task was escalated / needs human review.
func boardHasEscalated(board *plan.Board) bool {
	if board == nil {
		return false
	}
	for _, t := range board.Tasks {
		blob := strings.ToLower(t.Error + " " + t.Notes + " " + t.Review + " " + t.Output)
		if strings.Contains(blob, "escalated") ||
			strings.Contains(blob, "needs human") ||
			strings.Contains(blob, "max retries") ||
			strings.Contains(blob, `"status":"blocked"`) ||
			strings.Contains(blob, `"status": "blocked"`) {
			return true
		}
		if t.Column == plan.ColToScope && strings.TrimSpace(t.Error) != "" {
			return true
		}
	}
	return false
}

// promoteBoardOnQAGreen closes open implement/verify tasks after a *strong* QA
// gate passes, recovering from soft tester evidence false-negatives.
// Never promotes escalated / blocked / stub / missing-file tasks (TestSLMs).
func promoteBoardOnQAGreen(root string, board *plan.Board) {
	if board == nil {
		return
	}
	for i := range board.Tasks {
		t := &board.Tasks[i]
		t.Normalize()
		if t.Column == plan.ColDone {
			continue
		}
		switch t.Role {
		case plan.RoleWorker, plan.RoleCorrector, plan.RoleTester, "deep", "":
			// close
		default:
			// Still close common rewrite leftovers that have no distinct specialist role.
			if t.Column != plan.ColBlocked && t.Column != plan.ColToScope &&
				t.Column != plan.ColInProgress && t.Column != plan.ColInReview &&
				t.Column != plan.ColReadyToDev {
				continue
			}
		}
		if !promoteEligible(root, *t) {
			continue
		}
		t.Error = ""
		t.Review = "qa_gate green"
		if strings.TrimSpace(t.Output) == "" {
			t.Output = `{"status":"done","summary":"verified by qa_gate"}`
		}
		t.MoveTo(plan.ColDone)
		board.Tasks[i] = *t
	}
}

// promoteEligible is true only for soft evidence-gap failures that QA can clear.
// Hard escalations, blocked worker JSON, missing focus files, and static stubs
// must stay open for human / corrective waves.
func promoteEligible(root string, t plan.Task) bool {
	blob := strings.ToLower(t.Error + " " + t.Review + " " + t.Notes + " " + t.Output)
	if strings.Contains(blob, "escalated") ||
		strings.Contains(blob, "needs human") ||
		strings.Contains(blob, "max retries") ||
		strings.Contains(blob, `"status":"blocked"`) ||
		strings.Contains(blob, `"status": "blocked"`) {
		return false
	}
	if quality.StaticFailedInOutput(t.Output) || quality.ClaimsFailedInOutput(t.Output) {
		return false
	}
	if root != "" {
		if issues := quality.CheckStaticQuality(root, t); len(issues) > 0 {
			return false
		}
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || strings.HasSuffix(f, "/") {
				continue
			}
			if !plan.FileExists(root, f) {
				return false
			}
		}
	}
	errLower := strings.ToLower(t.Error + " " + t.Review + " " + t.Notes)
	soft := t.Error == "" ||
		strings.Contains(errLower, "smoke") ||
		strings.Contains(errLower, "tester") ||
		strings.Contains(errLower, "qa_gate") ||
		(t.Column == plan.ColReadyToDev && !strings.Contains(errLower, "review rejected"))
	return soft
}

// promoteRenameTasksDone marks implement/test tasks done when disk already matches
// a rename, so board Success isn't false after a flaky review escalate.
func promoteRenameTasksDone(board *plan.Board) {
	if board == nil {
		return
	}
	for i := range board.Tasks {
		t := &board.Tasks[i]
		t.Normalize()
		switch t.Role {
		case plan.RoleWorker, plan.RoleCorrector, plan.RoleTester, "deep":
			// ok
		default:
			if t.Column == plan.ColDone {
				continue
			}
			// Leave explorer/docs/etc. — only close if still open from this rename turn.
			if t.Column != plan.ColBlocked && t.Column != plan.ColToScope &&
				t.Column != plan.ColInProgress && t.Column != plan.ColInReview &&
				t.Column != plan.ColReadyToDev {
				continue
			}
		}
		t.Error = ""
		if strings.TrimSpace(t.Output) == "" {
			t.Output = `{"status":"done","summary":"rename verified on disk"}`
		}
		t.MoveTo(plan.ColDone)
		board.Tasks[i] = *t
	}
}
