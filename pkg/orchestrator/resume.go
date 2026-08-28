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

	o.resetObjectiveProbes()
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

	// Resume reaches the continue/escalate/shell gates, so it gets the same
	// run-start gate resolution a fresh run does (headless.go). Placed after
	// the no-tasks branch above so the Run() fallback does not log it twice.
	if err := o.preflightGates(); err != nil {
		return nil, err
	}
	o.emitGateDecisions()

	_ = o.store.SetQuery(query)
	// Resume is a run too: memory needs its context, the policy needs applying
	// and the operator needs the shell-policy notice.
	o.startEvolveRun(runID, query)
	o.applyRoleModelPolicy()
	o.emitShellPolicyNotice()
	o.seedAdaptiveLessons(query)
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
		return o.checkpointInterrupt(ctx, board, session.PhaseExecute, err)
	}
	if runner.ResumedReact {
		o.emit("execute", "continued from ReAct message checkpoint (no cold replan)", "")
	}
	o.recordLatency("execute", time.Since(execStart))
	snap := o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(ctx, board, session.PhaseExecute, err)
	}

	return o.finalizeAfterExecute(ctx, runID, query, skillPack, board, runner, start)
}

// finalizeAfterExecute covers tester → QA → memory → summary (shared by Run + Resume).
// finalizeAfterExecute covers tester → QA → memory → summary (shared by Run +
// Resume). It was ~300 lines of interleaved verification, gating and HITL; the
// stages are now named functions with explicit inputs and outputs.
func (o *Orchestrator) finalizeAfterExecute(ctx context.Context, runID, query, skillPack string, board *plan.Board, runner *loop.Runner, start time.Time) (*Result, error) {
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseTest)

	// EARLY FINISH #0 — a BETWEEN-WAVES probe already ran the objective command
	// on this exact tree and stopped the board on the strength of it. RunBoard
	// returned immediately, so nothing has been written since: re-running the
	// command here (which runDeterministicPreTest would) could only re-prove the
	// same answer, at the price of a whole test suite.
	if g, ok := o.objectiveMetEarly(); ok {
		return o.finishObjectiveMet(ctx, runID, query, skillPack, board, g, "between waves", start)
	}

	pre := o.runDeterministicPreTest(ctx)

	// EARLY FINISH #1 — the execute board has drained and the deterministic
	// pre-test has just run the objective command on this exact tree. If it is
	// green on a STRONG gate the objective is already met, and everything below
	// (the LLM tester, its corrective wave, the placeholder wave, the QA gate's
	// own rounds, the continue waves) can only re-prove it. A live SLM run paid
	// ~40 minutes and 412k prompt tokens for that re-proof.
	//
	// `pre.Smoke` is handed over so this costs ZERO extra command runs: the
	// probe reuses the result the pre-test just produced rather than shelling
	// out again.
	if g, done := o.objectiveAlreadyMet(ctx, board, false, pre.Smoke); done {
		return o.finishObjectiveMet(ctx, runID, query, skillPack, board, g, "before verification", start)
	}

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
	// Publish the verdict to the run, so any board the harness still runs sees
	// the same "tester rejected" refusal the probes below are handed directly.
	o.noteTesterRejected(testerRejected)

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(ctx, board, session.PhaseTest, err)
	}

	// EARLY FINISH #2 — the corrective tester wave above may have written to the
	// tree, which is new evidence the first probe never saw. Ask again before
	// runQualityGates spends the placeholder wave, the completeness reopen, the
	// QA gate's diagnose/fix rounds and the continue waves. The probe is skipped
	// for free when nothing was written since #1 (same fingerprint) or when the
	// tester rejected — see objectiveAlreadyMet's frequency rule.
	if g, done := o.objectiveAlreadyMet(ctx, board, testerRejected, nil); done {
		return o.finishObjectiveMet(ctx, runID, query, skillPack, board, g, "after the corrective wave", start)
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
	// Smoke is the raw result, kept so the early-finish probe can REUSE this
	// run of the objective command instead of paying for an identical second
	// one. Nil when the pre-test did not run.
	Smoke *quality.SmokeResult
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
	sr := o.runSmoke(ctx, cmd)
	sr.Command = cmd // the reuse check matches on it; RunSmoke sets it too
	pt.Ran = true
	pt.OK = sr.OK
	pt.Output = sr.Output
	pt.Smoke = &sr
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
		recheck := o.runSmoke(ctx, pre.Cmd)
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
			// Model text and harness text become one string here and the tester
			// gates scan the result. Stamp the harness's half so it keeps its
			// authority, defuse the model's half so it never gains any: this
			// branch runs precisely when the tester produced NO execution
			// frame, so anything frame-shaped in v.Output is the model's own
			// prose or something it pasted out of the repository.
			v.Output = strings.TrimSpace(quality.StampHarnessSection(sec)) + "\n\n" +
				strings.TrimSpace(quality.DefuseModelText(v.Output))
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
	o.noteTesterRejected(testerRejected)
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
			res, cerr := o.checkpointInterrupt(ctx, board, session.PhaseExecute, err)
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
	if rejected, restaffed := o.applyTesterFeedbackRestaffed(ctx, query, board, testOut2); rejected {
		snap = o.boardStore.Snapshot()
		board = &snap
		// The project manager just moved this ticket to a different specialist
		// and told it what to do differently. Stopping now would do the whole
		// analysis and throw it away: the user sees "finished" over a defect
		// still on disk and a ticket nobody touched, which is worse than never
		// having triaged at all.
		//
		// Bounded twice over — a ticket is re-staffed at most once (see
		// reassignedMarker), and RunCorrectiveBoard still refuses past
		// max_waves — so this cannot become the loop it exists to end.
		if restaffed > 0 {
			board, testOut, rejected = o.runRestaffedTickets(ctx, query, board, runner, testPack, restaffed)
		}
		return board, testOut, rejected, nil, nil
	}
	o.emitLoop("test", LoopEvent{
		Action: "resolved", Reason: "tester passed after corrective wave",
		From: "test", To: "done", Wave: o.waveCounter,
	})
	return board, testOut, false, nil, nil
}

// finishObjectiveMet ends the run because the strong objective gate is already
// green, and says so loudly enough that the saving is visible rather than
// mysterious.
//
// `when` names the decision point ("between waves" / "before verification" /
// "after the corrective wave"). The board is promoted exactly as a green strong
// gate promotes it in runQualityGates, so the finished board and the verdict
// tell the same story, and the gate is recorded under the same qa_gate name the
// finish path would have used — this IS that gate, evaluated earlier.
//
// Promotion is not blanket: promoteEligible refuses a task whose declared files
// are not on disk — and refuses an escalated one outright — so the tasks a
// between-waves stop abandoned stay open in ready_to_dev and the escalations
// stay open for a human. That is the honest outcome, and it is why the counts
// below, Result.UnexecutedTasks and Result.FailedTasks exist rather than a
// silent green board.
func (o *Orchestrator) finishObjectiveMet(ctx context.Context, runID, query, skillPack string,
	board *plan.Board, g objectiveGate, when string, start time.Time) (*Result, error) {

	// Tell completeRun this run is ending on the gate, which is the one thing
	// that licenses reporting success over an escalated task.
	o.noteObjectiveEarlyFinish(g)
	promoteBoardOnQAGreen(o.cfg.Root, board)
	o.persistBoard(board)
	o.recordGate("qa_gate", true, g.Cmd)
	o.recordGate("objective_met_early", true, when+": "+g.Cmd)

	skipped := o.remainingWaveBudget()
	msg := fmt.Sprintf("objective already met (%s green) — finishing early, %d wave(s) not needed", g.Cmd, skipped)
	// A between-waves stop abandoned planned work. Say how much, in the event
	// the operator watches as well as in the Result they read afterwards.
	if left := o.unexecutedTaskCount(); left > 0 {
		msg = fmt.Sprintf("%s · %d task(s) planned but never executed", msg, left)
	}
	// An escalation is a signal that a human should look, so walking past one is
	// never silent: the same sentence goes into the stop event, this finish
	// event and the Result summary.
	if notice := escalationNotice(unfinishedForReview(board)); notice != "" {
		msg = msg + " · " + notice
	}
	o.emitSuccess("test", msg, "")
	o.emitLoop("test", LoopEvent{
		Action: "objective_met",
		Reason: msg + " · checked " + when,
		From:   "test", To: "done", Wave: o.waveCounter,
	})
	_ = o.store.Append(contextstore.DocScratch, "Objective gate",
		fmt.Sprintf("%s\ncmd: %s\nchecked %s\n\n%s", msg, g.Cmd, when, truncate(g.Output, 2000)))

	testOut := fmt.Sprintf(
		`{"passed":true,"summary":"objective gate green (%s) — finished early","commands":[%q],"failures":[]}`,
		when, g.Cmd)
	return o.completeRun(ctx, runID, query, skillPack, board, testOut, false, false, g.Cmd, start)
}

// unexecutedTaskCount reports how many planned tasks the board loop abandoned
// when a between-waves probe stopped it. Zero on every other path.
func (o *Orchestrator) unexecutedTaskCount() int {
	r := o.lastRunner()
	if r == nil {
		return 0
	}
	stopped, _, left := r.EarlyStop()
	if !stopped {
		return 0
	}
	return left
}

// remainingWaveBudget reports how many corrective waves the run still had left
// to spend, i.e. what finishing early saved. Best-effort: 0 when the runner is
// gone or the budget is unbounded.
func (o *Orchestrator) remainingWaveBudget() int {
	r := o.lastRunner()
	if r == nil || r.MaxWaves <= 0 {
		return 0
	}
	if left := r.MaxWaves - r.CorrectiveRuns(); left > 0 {
		return left
	}
	return 0
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
	// Keep the run's mirrored verdict in step with whatever these gates decide.
	// There are three exits; a defer covers all of them.
	defer func() { o.noteTesterRejected(out.TesterRejected) }()

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

	// Squad integration runs BEFORE the QA gate: it is the more specific
	// question ("do the two halves fit?") and its failure output names the seam,
	// which is far more useful than the generic gate's when both are red.
	integrationFailed := o.runSquadIntegration(ctx, out.Board)

	out.QACmd = o.qaCommand()
	qaFailed := o.runQAGate(ctx, query, out.Board)
	out.QAFailed = qaFailed || integrationFailed
	if out.QAFailed {
		out.TesterRejected = true
		// An integration failure has already raised its own ticket, carrying
		// the command, the output that names the seam, the contract clauses at
		// stake and the team that owes them. Re-entering the tester path with
		// a synthetic `qa_gate red` verdict would throw all of that away and
		// stack a second, generic ticket on top of the specific one — and its
		// reopen pass would reopen halves that are green by definition, which
		// is what "every squad passed and the seam is wrong" means.
		if qaFailed {
			fake := `{"passed":false,"summary":"qa_gate command still failing","failures":["qa_gate red"]}`
			_ = o.applyTesterFeedback(ctx, query, out.Board, fake)
		}
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

// Outcome values for Result.Outcome.
//
// Success stays a bool because every existing consumer (exit codes, headless
// JSON, the e2e scenarios) reads it and the question it answers — "did the user
// get what they asked for?" — is genuinely binary. What a bool cannot express
// is that a run reached a green objective gate while a subsidiary task failed,
// so that lands here rather than being folded into Success or dropped.
const (
	// OutcomeSuccess: the objective is met and the board is clean.
	OutcomeSuccess = "success"
	// OutcomeSuccessWithFailures: the strong objective gate is green and no
	// tester rejection stands — but subsidiary task(s) failed along the way, or
	// escalated and were left on the board for a human. Result.FailedTasks
	// carries the failure count; the summary names the review count, which also
	// covers an escalation that carries no error of its own.
	//
	// This is the outcome a run that walked past an escalation MUST report: a
	// caller must be able to tell it from a clean board, and OutcomeSuccess
	// cannot.
	OutcomeSuccessWithFailures = "success_with_failures"
	// OutcomeFailure: the run did not meet its objective.
	OutcomeFailure = "failure"
	// OutcomeUnverified: the run made changes and the objective command is
	// green — but it was ALREADY GREEN before the run wrote anything, so that
	// command has verified nothing about the work.
	//
	// This exists because the other three cannot say it. "Success" claims the
	// objective was met; "failure" claims it was not; the truth here is that
	// nothing measured either way, and a harness that rounds that to success is
	// fabricating completion.
	//
	// MEASURED: Qwen3-Coder-Next on the honest-failure scenario — a deliberately
	// impossible task against a repo whose suite passes from the start — was
	// reported as engine_success=true with failed_tasks=0. The model edited a
	// file, the suite still passed (as it always had), and every signal the
	// harness owns said "green".
	//
	// Success stays TRUE: the work completed and nothing failed. What is
	// missing is EVIDENCE, not achievement, and a control run proved the
	// difference matters — respects-scope changed exactly the right file, left
	// every frozen file untouched, passed six of six checks, and would have
	// been reported as a failure (and a non-zero exit code) by a rule that
	// folded verification into Success.
	OutcomeUnverified = "unverified"
)

// runOutcome names the verdict. FailedTasks stays authoritative for the count;
// this only says which of the two green shapes (or neither) the run has.
func runOutcome(success bool, failed int) string {
	switch {
	case !success:
		return OutcomeFailure
	case failed > 0:
		return OutcomeSuccessWithFailures
	default:
		return OutcomeSuccess
	}
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
			// The model's bullets are lessons too: parse them back so they
			// enter the fact store beside the deterministic ones, where they
			// can be confirmed, contradicted and pruned like anything else.
			allLessons = append(allLessons, learning.ParseMarkdown(memOut)...)
		}
		if strings.TrimSpace(lessonsMD) != "" {
			_ = learning.AppendGlobalMemory("Session lessons", lessonsMD)
		}
		// Route the run's lessons into typed semantic memory. MEMORY.md above
		// stays the human mirror; this is what gives a lesson confidence,
		// contradiction handling, provenance and a prune policy.
		o.recordLessonFacts(allLessons)
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
	// objectiveWon is the ONE license to report success over an escalation, and
	// it is not "the QA gate happened to be green": it is set only by
	// finishObjectiveMet, i.e. only when this run ENDED because the strong
	// objective command was measured green on the tree that exists. Every other
	// route to the finish line leaves it false and the old refusal stands.
	//
	// Why an escalation may be walked past at all: the board is a planner's
	// guess at a decomposition, the gate is the acceptance criterion the user
	// stated, and when the guess and the measurement disagree the measurement
	// wins. A task escalates because reviews kept failing — the run that is
	// burning its budget — so refusing there helped the cheap runs and abandoned
	// the expensive ones. Nothing is swallowed: FailedTasks keeps the count,
	// Outcome below refuses to say plain "success", and the summary names it.
	objectiveWon := o.finishedOnObjectiveGate()
	escalatedBlocks := escalatedLeft && !objectiveWon
	// Never mark success when escalated/blocked tasks remain, even if a weak
	// compileall QA gate was green (TestSLMs false-success regression).
	success := !testerRejected && failed == 0 && board.AllDone() && !escalatedLeft
	// Soft success from QA alone requires a *strong* gate (pytest / go test / …).
	// Syntax-only compileall must not rubber-stamp an incomplete board.
	//
	// `failed == 0` is deliberately NOT part of this clause any more. A live run
	// produced byte-correct code with a green `go test ./...`, owed nothing to a
	// human, and still reported flat failure because one subsidiary bookkeeping
	// task had failed — the user's objective was met and the harness said no.
	// The failure is not swallowed: it stays on Result.FailedTasks, it is named
	// in the summary, and Outcome below distinguishes this from a clean board.
	//
	// The last clause has one carve-out. A DELIBERATE early stop is not agent
	// work left dangling: the board loop stopped because the objective gate
	// said the remaining tasks could not add anything, and that probe had
	// already refused to fire on an in-flight task or an outstanding tester
	// rejection. Those tasks are not promotable — their files were never
	// written — so they sit in ready_to_dev and would otherwise make the
	// harness report failure for obeying its own gate. Nothing is hidden by the
	// carve-out: UnexecutedTasks carries the number and the summary says it in
	// words.
	//
	// The escalation refusal is now `escalatedBlocks` rather than
	// `escalatedLeft`, for the reason given at objectiveWon above: measured
	// green beats a planner's guess, and the escalation is reported instead.
	unexecuted := o.unexecutedTaskCount()
	workLeft := board.AgentWorkRemaining() && unexecuted == 0
	softSuccess := !success && qaGreen && !weakQA && !testerRejected && !escalatedBlocks && !workLeft
	if softSuccess {
		success = true
	}
	// A GREEN THAT WAS GREEN BEFORE VERIFIES NOTHING — but that is a fact about
	// the EVIDENCE, not a verdict on the work, and the two must not be confused.
	//
	// This first shipped as a downgrade of Success itself, and a control run
	// showed why that is wrong. On respects-scope — a legitimate task against a
	// green repo — the model changed exactly the right file, left every frozen
	// file untouched, and the suite passed: six of six checks. Reporting
	// Success=false for that is telling someone their correct change failed,
	// and Success drives the exit code, so it would fail their CI too. Most real
	// work runs against a repository whose tests already pass.
	//
	// So Outcome carries it and Success does not. The run says plainly that the
	// project's own test command did not exercise this change — which is useful,
	// actionable and true — without inventing a failure.
	//
	// What this does NOT do is decide that an impossible task failed. The
	// harness cannot tell "the requirement was unachievable" from "the
	// requirement is not covered by the tests"; both look identical from a
	// command that is green either way. Claiming otherwise by way of this flag
	// would be picking the answer that suits one scenario.
	outcome := runOutcome(success, failed)
	if success && o.objectiveUnverified() {
		outcome = OutcomeUnverified
	}
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: success, FailedTasks: failed,
		Outcome:         outcome,
		UnexecutedTasks: unexecuted,
		Duration:        time.Since(start), Summary: summarizeWithRepairs(board, board.Plan, &o.repairs),
		Repairs: o.repairs.snapshot(),
		Backend: o.cfg.Backend, LatencyMs: o.snapshotLatency(),
		Usage: o.snapshotUsage(),
	}
	// A run that ended ON the objective gate with a failed or escalated task is
	// never a BARE success. The escalation is exactly the signal that a human
	// should look, so the verdict a caller branches on has to keep saying so
	// even though Success is true.
	if res.Success && objectiveWon && (failed > 0 || escalatedLeft) {
		res.Outcome = OutcomeSuccessWithFailures
	}
	if testerRejected || escalatedBlocks {
		res.Success = false
		res.Outcome = OutcomeFailure
		if testerRejected {
			res.Summary = res.Summary + " (tester/QA rejected — plan/tasks rewritten)"
		} else {
			res.Summary = res.Summary + " (escalated tasks need human review)"
		}
	} else if qaGreen && weakQA && !success {
		res.Summary = res.Summary + " (qa_gate is syntax-only — need pytest/tests for success)"
	} else if qaGreen && success && !board.AllDone() {
		res.Summary = res.Summary + " (qa_gate green)"
	}
	// Say it out loud wherever the verdict is read: a green objective with a
	// failed or escalated task is a success with an asterisk, never a silent
	// one. The count is unfinishedForReview's, the same one the stop event and
	// the finish event quote.
	if res.Outcome == OutcomeSuccessWithFailures {
		if notice := escalationNotice(unfinishedForReview(board)); notice != "" {
			res.Summary = fmt.Sprintf("%s (objective met — %s)", res.Summary, notice)
		}
	}
	// Same rule for the other asterisk: a run that stopped mid-board on a green
	// objective gate left planned work undone, and the promoted board no longer
	// shows it. Say it where the verdict is read.
	if res.UnexecutedTasks > 0 {
		res.Summary = fmt.Sprintf("%s (objective met between waves — %d task(s) not executed)",
			res.Summary, res.UnexecutedTasks)
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

// checkpointInterrupt saves the board and, when the run was really interrupted,
// turns that into a resumable Result.
//
// The authority on "was this run interrupted" is the RUN CONTEXT, not the text
// of err. It used to be the text, and the text lies: pkg/loop races several
// reviewers against each other and cancels the losers on purpose, so a healthy
// run routinely produced errors reading
// `chat failed: …: context canceled`. Every one of those was checkpointed as a
// user interrupt — the run stopped with "interrupted at execute" and exit 130
// while the user was watching it work. ctx.Err() cannot be wrong about this:
// it is non-nil exactly when the caller (SIGINT, /stop, a parent timeout) went
// away.
func (o *Orchestrator) checkpointInterrupt(ctx context.Context, board *plan.Board, phase string, err error) (*Result, error) {
	if board != nil {
		o.persistBoard(board)
	}
	if o.currentTurn != nil && board != nil && ctx != nil && ctx.Err() != nil && isCancelErr(err) {
		_ = session.MarkInterrupted(o.cfg.SlmDir(), o.currentTurn, *board, phase)
		// `/resume <id>` is a REPL slash command, and this string is persisted:
		// it reaches a CLI run, a headless JSON consumer and a saved session
		// summary, none of which have a REPL to type it into. Emit the run id
		// as DATA and let each renderer phrase its own instruction.
		saved := "board saved"
		if session.HasReactHistory(o.cfg.SlmDir(), o.currentTurn.ID) {
			saved = "ReAct history + board saved"
		}
		msg := fmt.Sprintf("interrupted at %s — %s", phase, saved)
		o.emitFullDataL("stop", stream.KindPhase, "", "", msg, "", "", stream.LevelWarn,
			map[string]any{"resume_id": o.currentTurn.ID, "phase": phase, "saved": saved})
		res := &Result{
			ID: o.currentTurn.ID, Query: o.currentTurn.Query, Board: *board,
			Success: false, FailedTasks: board.FailedCount(), Outcome: OutcomeFailure,
			Summary: fmt.Sprintf("interrupted at %s — %s, resumable as %s",
				phase, saved, o.currentTurn.ID),
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
	// The text arm lives in ONE place (loop.IsContextCancelErr) so the harness
	// cannot end up with two different opinions about what a cancellation looks
	// like. The bare word "canceled" is deliberately NOT enough and used to be:
	// any message that merely contained it — a provider reporting an upstream
	// job cancellation, a model quoting the harness's own prompt back — read as
	// a context cancellation and could abort a healthy run.
	return loop.IsContextCancelErr(err)
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

// taskEscalated reports whether ONE open task is parked for a human.
// Historical "ESCALATED…" notes on done tasks must not fail an otherwise green
// run, so a done task is never escalated whatever its notes say.
func taskEscalated(t plan.Task) bool {
	t.Normalize()
	if t.Column == plan.ColDone {
		return false
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
	return t.Column == plan.ColToScope && strings.TrimSpace(t.Error) != ""
}

// boardHasEscalated reports whether any *open* task still needs human review.
func boardHasEscalated(board *plan.Board) bool {
	if board == nil {
		return false
	}
	for _, t := range board.Tasks {
		if taskEscalated(t) {
			return true
		}
	}
	return false
}

// unfinishedForReview counts the tasks a human should look at when a run ends on
// a green objective gate: everything Board.FailedCount already counts, plus any
// OTHER open task boardHasEscalated flags — an escalation note on a task that
// carries no error of its own. Counted per task, so one that is both failed and
// escalated counts once, and never below FailedCount.
//
// This is the number the stop event, the finish event and the Result summary all
// quote, so the three cannot drift apart.
func unfinishedForReview(board *plan.Board) int {
	if board == nil {
		return 0
	}
	n := board.FailedCount()
	for _, t := range board.Tasks {
		t.Normalize()
		// Skip everything FailedCount has already counted, and everything that
		// finished.
		if t.Column == plan.ColDone || t.Column == plan.ColBlocked ||
			t.Status == plan.StatusFailed || strings.TrimSpace(t.Error) != "" {
			continue
		}
		if taskEscalated(t) {
			n++
		}
	}
	return n
}

// escalationNotice is the one sentence a green-objective finish uses to report
// what it walked past. Empty when there is nothing to report.
func escalationNotice(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d task(s) failed or escalated and were not completed — "+
		"left on the board for inspection", n)
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

// runRestaffedTickets executes the tickets the project manager just moved, and
// re-verifies.
//
// This is the step that turns triage from an observation into a fix. Everything
// here is best effort: a wave that cannot run (budget spent, canceled context)
// leaves the board exactly as triage left it, which is still a correctly
// staffed ticket a human or the next run can pick up.
func (o *Orchestrator) runRestaffedTickets(ctx context.Context, query string, board *plan.Board,
	runner *loop.Runner, testPack string, restaffed int) (*plan.Board, string, bool) {

	failed := `{"passed":false,"summary":"tester rejected after the re-staffed wave","failures":["unresolved after reassignment"]}`
	if runner == nil || !board.AgentWorkRemaining() {
		return board, failed, true
	}
	o.emit("execute", fmt.Sprintf("re-staffed wave: %d ticket(s) moved by the project manager", restaffed), "")
	o.emitLoop("execute", LoopEvent{
		Action: "restaffed_wave",
		Reason: "the project manager moved the ticket to a different specialist",
		From:   "plan", To: "execute", Wave: o.waveCounter,
	})
	ran, err := runner.RunCorrectiveBoard(ctx, board)
	if !ran {
		o.emit("execute", "re-staffed wave skipped — max_waves budget exhausted", "")
		return board, failed, true
	}
	if err != nil {
		if isCancelErr(err) {
			return board, failed, true
		}
		o.emit("execute", "re-staffed wave warning: "+err.Error(), "")
	}
	snap := o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)

	o.emitAgent("test", plan.RoleTester, "", "re-verify after the re-staffed wave", "", "")
	_, tasksMD := board.ToMarkdown()
	out, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack+
		"\nTasks:\n"+truncate(tasksMD, 4000)+
		"\n\nRe-verify after the reassignment.\n\n"+o.langHint()+"\nSTRICT JSON with passed true/false.")
	if strings.TrimSpace(out) == "" {
		return board, failed, true
	}
	if !plan.ParseTesterJSON(out).Passed {
		// Still red, and the ticket has now spent its one handoff. Say so once
		// rather than reassigning again: a third agent guessing at work two
		// others could not do is a scoping problem, not a staffing one.
		o.emitLoop("test", LoopEvent{
			Action: "unresolved", Reason: "still failing after the project manager's reassignment",
			From: "test", To: "plan", Wave: o.waveCounter,
		})
		return board, out, true
	}
	o.emitSuccess("test", "resolved after the project manager reassigned it", "")
	o.emitLoop("test", LoopEvent{
		Action: "resolved", Reason: "the re-staffed specialist fixed it",
		From: "test", To: "done", Wave: o.waveCounter,
	})
	return board, out, false
}
