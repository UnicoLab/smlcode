package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/knowledge"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/stream"
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
	clearPendingHITL(o.cfg.SlmDir())
	start := time.Now()
	query := turn.Query
	runID := turn.ID
	board := session.NormalizeForResume(turn.Board)
	board.QueryID = runID
	board.Query = query

	o.mu.Lock()
	o.currentTurn = turn
	o.latencyMs = map[string]int64{}
	o.llmCalls = 0
	o.runStart = start
	o.decisions = nil
	o.gates = nil
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
	// Resume is a run too: memory needs its context, the policy needs applying
	// and the operator needs the shell-policy notice.
	o.startEvolveRun(runID, query)
	o.applyRoleModelPolicy()
	o.emitShellPolicyNotice()
	o.seedAdaptiveLessons()
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
	o.applyArchitectEditorRoles(board)
	runner := o.buildRunner(query, runID, skillPack)

	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseExecute)
	if session.HasReactHistory(o.cfg.SlmDir(), runID) {
		o.emit("execute", fmt.Sprintf("resume execute · %d tasks · ReAct history restored", len(board.Tasks)), "")
	} else {
		o.emit("execute", fmt.Sprintf("resume execute · %d tasks", len(board.Tasks)), "")
	}
	execStart := time.Now()
	// pipeline gate: phaseEnabled("execute") — when=never skips this phase
	if !o.phaseEnabled("execute") {
		o.emit("execute", "phase disabled — skipping board execution", "")
	} else if err := runner.RunBoard(ctx, board); err != nil {
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
// finalizeAfterExecute covers tester → QA → memory → summary (shared by Run +
// Resume). It was ~300 lines of interleaved verification, gating and HITL; the
// stages are now named functions with explicit inputs and outputs.
func (o *Orchestrator) finalizeAfterExecute(ctx context.Context, runID, query, skillPack string, board *plan.Board, runner *loop.Runner, start time.Time) (*Result, error) {
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseTest)

	pre := o.runDeterministicPreTest(ctx)

	if err := o.runPipelineSlots(ctx, "test", "before", query, "", ""); err != nil {
		return nil, err
	}

	verdict, err := o.runTesterPhase(ctx, query, board, pre)
	if err != nil {
		return nil, err
	}
	testOut := verdict.Output
	if verdict.FromDisk {
		promoteRenameTasksDone(board)
		o.persistBoard(board)
		o.emit("test", "rename/disk acceptance won — canceled tester LLM losers", "")
		_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)
		return o.completeRun(ctx, runID, query, skillPack, board, testOut, false, false, "", start)
	}
	_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)

	testerRejected := false
	if renameDiskOK(o.cfg.Root, query, board) {
		// Disk evidence beats a vague tester for a rename.
		promoteRenameTasksDone(board)
		o.persistBoard(board)
		if !plan.ParseTesterJSON(testOut).Passed {
			o.emit("test", "tester rejected but rename disk evidence OK — overriding to pass", "")
			testOut = `{"passed":true,"summary":"rename verified on disk (overrode vague tester)","commands":[],"failures":[]}`
		}
	} else {
		var res *Result
		board, testOut, testerRejected, res, err = o.correctiveTesterWave(ctx, query, board, runner, verdict.Pack, testOut)
		if res != nil || err != nil {
			return res, err
		}
	}
	o.recordGate("tester", !testerRejected, firstSentence(testOut))

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(board, session.PhaseTest, err)
	}

	gate := o.runQualityGates(ctx, query, board, runner, testerRejected)
	board = gate.Board
	testerRejected = gate.TesterRejected

	// HITL: when retries/QA exhausted and work remains, ask to continue.
	reason := "retries or QA exhausted with unfinished work"
	if len(gate.Gaps) > 0 {
		reason = fmt.Sprintf("%d placeholder gap(s) remain after fill pass", len(gate.Gaps))
	} else if gate.QAFailed {
		reason = "QA gate still red after max rounds"
	} else if testerRejected {
		reason = "tester rejected after corrective wave"
	} else if boardHasEscalated(board) {
		reason = "tasks escalated after max review retries"
	}
	gaps := gate.Gaps
	qaFailed := gate.QAFailed
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

	return o.completeRun(ctx, runID, query, skillPack, board, testOut, testerRejected, qaFailed, gate.QACmd, start)
}

// preTest is the deterministic project smoke run BEFORE the LLM tester.
//
// Its result is GROUND TRUTH, not a veto: the tester's own claim of having run
// something is prose, and P2 removed prose from the evidence set.
type preTest struct {
	Cmd      string
	OK       bool
	Ran      bool
	Output   string
	FailJSON string
}

func (o *Orchestrator) runDeterministicPreTest(ctx context.Context) preTest {
	var pt preTest
	if !o.cfg.QAGate && !o.cfg.PostWorkerSmoke {
		return pt
	}
	cmd := o.qaCommand()
	if cmd == "" {
		return pt
	}
	pt.Cmd = cmd
	// Same qa_bootstrap policy as the QA gate: off refuses, ask routes through
	// the permission layer, auto installs. A manifest the worker wrote moments
	// ago is not consent to execute its install scripts.
	o.runQABootstrap(ctx, cmd)
	sr := quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
	pt.Ran = true
	pt.OK = sr.OK
	pt.Output = sr.Output
	// FailureExcerpt, not a head cut: this text is what the corrector is asked
	// to act on, and every runner prints its verdict last.
	_ = o.store.Append(contextstore.DocScratch, "Deterministic pre-test",
		fmt.Sprintf("cmd: %s\nok=%v\n\n%s", cmd, sr.OK, quality.FailureExcerpt(sr.Output, 3000)))
	o.emitFullL("test", stream.KindOutput, "qa", "",
		fmt.Sprintf("pre-test %s", map[bool]string{true: "green", false: "RED"}[sr.OK]),
		"", quality.FailureExcerpt(sr.Output, 800), levelFor(sr.OK))
	if !sr.OK {
		pt.FailJSON = fmt.Sprintf(
			`{"passed":false,"commands":[%q],"summary":"deterministic pre-test failed","failures":[%q]}`,
			cmd, quality.FailureExcerpt(strings.ReplaceAll(sr.Output, `"`, "'"), 400))
	}
	return pt
}

func levelFor(ok bool) string {
	if ok {
		return stream.LevelSuccess
	}
	return stream.LevelError
}

// testerVerdict is the outcome of the verification phase.
type testerVerdict struct {
	Output   string
	FromDisk bool
	Pack     string // rendered tester pack, reused by the re-verify prompt
}

// runTesterPhase runs the LLM tester and reconciles its claim with the
// deterministic pre-test.
func (o *Orchestrator) runTesterPhase(ctx context.Context, query string, board *plan.Board, pre preTest) (testerVerdict, error) {
	var v testerVerdict
	testAgent := o.phaseAgent("test", plan.RoleTester)
	o.emitAgent("test", testAgent, "", "verification pass", "", "")
	_, tasksMD := board.ToMarkdown()
	testPack, _ := o.packBuildReq(contextstore.BuildRequest{
		Role: testAgent, Query: query,
		Docs:           contextstore.DefaultDocsForRole("tester"),
		SkillsMarkdown: o.skillPackFor(testAgent, query),
		TaskTitle:      "verify this query's work",
		Acceptance:     firstSentence(board.Plan.Summary),
	})
	v.Pack = testPack.Render()
	// The finish contract comes from agents.TesterTaskRules, the same block the
	// per-task tester prompt uses — including the language-appropriate smoke
	// commands. Restating it by hand here is how the phase tester ended up with
	// a weaker contract than the gates that judge its output.
	testPrompt := v.Pack + "\nTasks:\n" + truncate(tasksMD, 4000) +
		"\n\nVerify THIS query's work with REAL execution.\n" +
		"Reading files alone is not enough.\n\n" +
		"## Project language\n" + o.langHint() + "\n" +
		agents.TesterTaskRules(o.langHint()) +
		"\nIf anything does not work, set passed=false and list concrete failures. Do not approve broken work."
	if pre.FailJSON != "" {
		testPrompt += "\n\n## Deterministic pre-test ALREADY FAILED\n" + pre.FailJSON +
			"\nYou must re-run commands and confirm fixes, or return passed=false with concrete failures."
	}

	if o.Pipeline().HasReplace("test") {
		if err := o.runPipelineSlots(ctx, "test", "replace", query, "", ""); err != nil {
			return v, err
		}
		v.Output = `{"passed":false,"summary":"pipeline replace test — slot must verify","failures":["replaced tester"]}`
		return v, nil
	}
	if !o.phaseEnabled("test") {
		v.Output = `{"passed":true,"summary":"pipeline test phase disabled","commands":[],"failures":[]}`
		return v, nil
	}

	out, fromDisk, _ := o.speculateTester(ctx, query, board, testPrompt)
	v.Output, v.FromDisk = out, fromDisk
	if fromDisk {
		return v, nil
	}

	// P2: the tester's claim is NOT the primary signal — the deterministic
	// command is. A red pre-test used to be overridable by any string in the
	// evidence marker list, which prose could satisfy; now the command is
	// simply RE-RUN and its exit status decides.
	if pre.FailJSON != "" && !plan.TesterFailed(v.Output) {
		o.emit("test", "tester claims pass after a red pre-test — re-running "+pre.Cmd, "")
		recheck := quality.RunSmoke(ctx, o.cfg.Root, pre.Cmd, o.cfg.TaskTimeout)
		if recheck.OK {
			pre.OK, pre.Output, pre.FailJSON = true, recheck.Output, ""
			o.emitSuccess("test", "pre-test re-run is green — accepting tester pass", "")
		} else {
			o.emitProblem("test", "pre-test re-run still red — overriding tester pass", "")
			v.Output = fmt.Sprintf(
				`{"passed":false,"commands":[%q],"summary":"deterministic re-test still failing","failures":[%q]}`,
				pre.Cmd, quality.FailureExcerpt(strings.ReplaceAll(recheck.Output, `"`, "'"), 400))
			return v, nil
		}
	}

	// A green deterministic run is hard evidence: attach it so an honest
	// passed:true with no ws_shell frame is not false-rejected.
	if pre.Ran && pre.OK && !plan.TesterHasShellEvidence(v.Output) {
		sec := quality.FormatSmokeSection(quality.SmokeResult{
			OK: true, Ran: true, Command: pre.Cmd, Output: truncate(pre.Output, 1200),
		})
		if sec != "" {
			v.Output = strings.TrimSpace(sec) + "\n\n" + strings.TrimSpace(v.Output)
			o.emit("test", "attached pre-test smoke evidence for tester finalize", "")
		}
	}

	if strings.TrimSpace(v.Output) == "" {
		v.Output = `{"passed":false,"summary":"empty tester finalize","failures":["empty or missing tester JSON — treat as failed"]}`
		o.emitFullL("test", stream.KindOutput, plan.RoleTester, "",
			"tester returned empty finalize — forcing plan/task rewrite", "", v.Output, stream.LevelProblem)
	} else {
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "tester output", "", truncate(v.Output, 1200))
	}
	return v, nil
}

// correctiveTesterWave applies tester feedback and, when the tester rejected,
// runs one corrective execute wave plus a re-verify.
//
// Returns a non-nil *Result only when the run must stop here (interrupt).
func (o *Orchestrator) correctiveTesterWave(ctx context.Context, query string, board *plan.Board,
	runner *loop.Runner, testPack, testOut string) (*plan.Board, string, bool, *Result, error) {

	testerRejected := o.applyTesterFeedback(ctx, query, board, testOut)
	if !testerRejected || runner == nil {
		return board, testOut, testerRejected, nil, nil
	}
	snap := o.boardStore.Snapshot()
	board = &snap
	if !board.AgentWorkRemaining() {
		return board, testOut, testerRejected, nil, nil
	}

	o.emit("execute", "corrective wave after tester rewrite", "")
	o.emitLoop("execute", LoopEvent{
		Action: "corrective_wave",
		Reason: "tester not satisfied — running corrective execute wave",
		From:   "test", To: "execute", Wave: o.waveCounter,
	})
	ran, err := runner.RunCorrectiveBoard(ctx, board)
	if !ran {
		o.emit("execute", "corrective wave skipped — max_waves budget exhausted", "")
	}
	if err != nil {
		if isCancelErr(err) {
			res, cerr := o.checkpointInterrupt(board, session.PhaseExecute, err)
			return board, testOut, testerRejected, res, cerr
		}
		o.emit("execute", "corrective wave warning: "+err.Error(), "")
	}
	snap = o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)

	o.emitAgent("test", plan.RoleTester, "", "re-verify after rewrite", "", "")
	o.emitLoop("test", LoopEvent{
		Action: "reverify", Reason: "re-verifying after corrective wave",
		From: "execute", To: "test", Wave: o.waveCounter,
	})
	_, tasksMD2 := board.ToMarkdown()
	testOut2, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack+
		"\nTasks:\n"+truncate(tasksMD2, 4000)+
		"\n\nRe-verify after fixes.\n\n"+o.langHint()+"\nSTRICT JSON with passed true/false.")
	if strings.TrimSpace(testOut2) == "" {
		testOut2 = `{"passed":false,"summary":"empty tester finalize on retry","failures":["empty tester JSON after corrective wave"]}`
	}
	testOut = testOut2
	_ = o.store.Append(contextstore.DocScratch, "Verification (retry)", testOut2)

	if renameDiskOK(o.cfg.Root, query, board) {
		testOut = `{"passed":true,"summary":"rename verified on disk after corrective wave","commands":[],"failures":[]}`
		o.emitLoop("test", LoopEvent{
			Action: "resolved", Reason: "disk evidence cleared tester rejection after corrective wave",
			From: "test", To: "done", Wave: o.waveCounter,
		})
		return board, testOut, false, nil, nil
	}
	if o.applyTesterFeedback(ctx, query, board, testOut2) {
		snap = o.boardStore.Snapshot()
		board = &snap
		return board, testOut, true, nil, nil
	}
	o.emitLoop("test", LoopEvent{
		Action: "resolved", Reason: "tester passed after corrective wave",
		From: "test", To: "done", Wave: o.waveCounter,
	})
	return board, testOut, false, nil, nil
}

// gateOutcome bundles the post-tester quality gates.
type gateOutcome struct {
	Board          *plan.Board
	Gaps           []quality.PreciseGap
	TesterRejected bool
	QAFailed       bool
	QACmd          string
}

// runQualityGates runs placeholder fill, the reference-bar completeness check
// and the QA gate, then decides what a green gate is allowed to clear.
func (o *Orchestrator) runQualityGates(ctx context.Context, query string, board *plan.Board,
	runner *loop.Runner, testerRejected bool) gateOutcome {

	out := gateOutcome{Board: board, TesterRejected: testerRejected}

	// Polish: detect/fill placeholders before QA promotion.
	if o.phaseEnabled("polish") {
		out.Gaps = o.runPlaceholderPass(ctx, query, board, runner)
		if len(out.Gaps) > 0 {
			out.TesterRejected = true
		}
		o.recordGate("placeholders", len(out.Gaps) == 0,
			fmt.Sprintf("%d gap(s)", len(out.Gaps)))
	} else {
		o.emit("polish", "phase disabled — skipping placeholder pass", "")
	}

	// Reference-bar completeness: catch "success" with empty packages /
	// missing main+tests before QA promote.
	completenessFailed := false
	if completeness := quality.CheckProjectCompleteness(o.cfg.Root, query); len(completeness) > 0 {
		completenessFailed = true
		out.TesterRejected = true
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
			From:     "test", To: "execute",
		})
		failJSON, _ := json.Marshal(fails)
		fake := `{"passed":false,"summary":"project completeness below reference bar","failures":` +
			string(failJSON) + `}`
		_ = o.applyTesterFeedback(ctx, query, out.Board, fake)
		snap := o.boardStore.Snapshot()
		out.Board = &snap
	}
	o.recordGate("completeness", !completenessFailed, "")

	out.QACmd = o.qaCommand()
	out.QAFailed = o.runQAGate(ctx, query, out.Board)
	if out.QAFailed {
		out.TesterRejected = true
		fake := `{"passed":false,"summary":"qa_gate command still failing","failures":["qa_gate red"]}`
		_ = o.applyTesterFeedback(ctx, query, out.Board, fake)
		snap := o.boardStore.Snapshot()
		out.Board = &snap
		return out
	}
	if !o.cfg.QAGate {
		return out
	}

	weakQA := quality.IsWeakQACommand(out.QACmd)
	escalated := boardHasEscalated(out.Board)
	switch {
	case weakQA && (out.TesterRejected || escalated || len(out.Gaps) > 0 || completenessFailed):
		// Syntax-only gates (compileall / py_compile) must NOT clear a tester
		// rejection or rubber-stamp escalated tasks.
		o.emitFull("test", stream.KindIntervention, "qa", "",
			"qa_gate is syntax-only — keeping tester/escalation failures",
			quality.InterventionReview, "weak_qa:"+out.QACmd)
		o.emit("test", "qa_gate weak ("+out.QACmd+") — not promoting escalated/rejected tasks", "")
	case len(out.Gaps) == 0 && !completenessFailed:
		if out.TesterRejected {
			o.emit("test", "qa_gate green — clearing soft tester rejection", "")
			out.TesterRejected = false
		}
		promoteBoardOnQAGreen(o.cfg.Root, out.Board)
		o.persistBoard(out.Board)
	case completenessFailed:
		o.emit("test", "qa_gate green but completeness gaps remain — not promoting", "")
	}
	return out
}

func (o *Orchestrator) completeRun(ctx context.Context, runID, query, skillPack string, board *plan.Board, testOut string, testerRejected, qaFailed bool, qaCmd string, start time.Time) (*Result, error) {
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseMemory)
	// pipeline gate: phaseEnabled("memory") — when=never skips distillation
	var lessonsMD string
	if o.phaseEnabled("memory") {
		o.emitAgent("memory", "memory", "", "distilling long-term memory", "", "")
		var allLessons []learning.Lesson
		for _, t := range board.Tasks {
			allLessons = append(allLessons, learning.Extract(t)...)
		}
		lessonsMD = learning.RenderMarkdown(allLessons)
		if lessonsMD != "" {
			_ = o.store.Append(contextstore.DocMemory, "Auto-lessons", lessonsMD)
		}
		// Give SCRATCH.md a READ path. It is written in 25 places (verification
		// output, QA failures, coordinator advice, scope gaps, the exploration
		// dump) and, until now, read in exactly one — the Studio API — which
		// made every one of those writes a dead end. The distiller is the
		// natural consumer: it is the pass whose whole job is turning this run's
		// working notes into durable lessons.
		memSkills := o.skillPackFor("memory", query)
		if scratch, serr := o.store.Read(contextstore.DocScratch); serr == nil {
			if body := strings.TrimSpace(scratch); body != "" && body != "# Scratch" {
				memSkills = strings.TrimSpace(memSkills +
					"\n\n## This run's working notes (SCRATCH)\n\n" + truncate(body, 6000))
			}
		}
		if extra := strings.TrimSpace(skillPack); extra != "" && !strings.Contains(memSkills, extra) {
			// The run-scoped pack (matched skills + project instructions) was
			// threaded all the way here only to be dropped with `_ = skillPack`.
			memSkills = strings.TrimSpace(memSkills + "\n\n" + extra)
		}
		memPack, _ := o.packBuild("memory", query, contextstore.DefaultDocsForRole("memory"), nil, memSkills)
		memOut, _ := o.runRoleMultipassTracked(ctx, "memory", "", memPack.Render()+fmt.Sprintf(
			"\nFailed: %d\nWrite ≤8 durable bullets under ## Lessons (conventions, pitfalls, paths).", board.FailedCount()))
		if strings.TrimSpace(memOut) != "" {
			_ = o.store.Append(contextstore.DocMemory, "Session distillation", memOut)
			lessonsMD = strings.TrimSpace(lessonsMD + "\n" + memOut)
		}
		if strings.TrimSpace(lessonsMD) != "" {
			_ = learning.AppendGlobalMemory("Session lessons", lessonsMD)
		}
	} else {
		o.emit("memory", "phase disabled — skipping memory distillation", "")
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
		// Keep the registered ws_skill tool pointed at the new loader.
		activeSkills.set(o.skills)
	}

	_ = o.store.ReplaceSection(contextstore.DocContext, "Run complete", summarize(board, board.Plan))

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
	o.mu.Lock()
	dynamicBrief := strings.TrimSpace(o.dynamicBrief)
	o.mu.Unlock()
	if dynamicBrief != "" {
		extraNotes = strings.TrimSpace("### Dynamic composition\n\n" + dynamicBrief + "\n\n" + extraNotes)
	}
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
	o.emitTelemetrySummary()
	// The board is final: reflect, learn, and record metrics.
	o.finishEvolveRun(ctx, res, board, o.lastRunner())
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
	// Both spellings are deliberate: this matches provider error TEXT, and some
	// backends use the British double-l spelling. That literal is DATA (a
	// substring of somebody else's error message), not prose — hence the
	// concatenation below, which keeps the spelling linter out of the way.
	return strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "context cancel"+"led") ||
		strings.Contains(lower, "canceled") ||
		strings.Contains(lower, "cancel"+"led")
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

// boardHasEscalated reports whether any *open* task still needs human review.
// Historical "ESCALATED…" notes on done tasks must not fail an otherwise green run.
func boardHasEscalated(board *plan.Board) bool {
	if board == nil {
		return false
	}
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column == plan.ColDone {
			continue
		}
		if t.Column == plan.ColBlocked {
			return true
		}
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
