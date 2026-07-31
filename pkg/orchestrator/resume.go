package orchestrator

import (
	"context"
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
	runner.FailureHandler = loop.NewEnhancedFailureHandler(o.cfg.Root)
	runner.Log = func(format string, args ...interface{}) {
		o.emit("execute", fmt.Sprintf(format, args...), "")
	}
	runner.OnEvent = func(kind, agent, taskID, msg, scope, output string) {
		o.emitFull("execute", kind, agent, taskID, msg, scope, output)
	}
	runner.OnUsage = func(u llm.Usage, estimated bool, _, _ string) {
		o.recordUsage(u, estimated)
	}
	runner.AfterWave = func(ctx context.Context, board *plan.Board, wave []plan.Task) {
		o.evolveAfterWave(ctx, query, skillPack, board, wave)
		o.coordinate(ctx, query, board, "after-wave")
	}
	runner.BuildInput = func(t plan.Task) string {
		lean := loop.StripScopedPack(t.Description)
		docs := contextstore.LeanDocsForRole(t.Role)
		tp, _ := o.packer.Build(t.Role, query, docs, t.Files, o.skillPackFor(t.Role, query))
		tp.TaskID = t.ID
		tp.TaskTitle = t.Title
		t.Description = tp.Render() + "\n## Task instructions\n\n" + lean
		return formatWorkerPromptFor(t)
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

	// Deterministic rename acceptance before LLM tester (avoids false rewrite).
	if renameDiskOK(o.cfg.Root, query, board) {
		promoteRenameTasksDone(board)
		o.persistBoard(board)
		o.emit("test", "rename acceptance satisfied on disk — skipping LLM tester reject path", "")
		testOut := `{"passed":true,"summary":"rename verified on disk","commands":[],"failures":[]}`
		_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)
		return o.completeRun(ctx, runID, query, skillPack, board, testOut, false, start)
	}

	o.emitAgent("test", plan.RoleTester, "", "verification pass", "", "")
	_, tasksMD := board.ToMarkdown()
	testPack, _ := o.packer.Build("tester", query, contextstore.DefaultDocsForRole("tester"), nil, o.skillPackFor("tester", query))
	testPrompt := testPack.Render() + "\nTasks:\n" + truncate(tasksMD, 4000) +
		"\n\nVerify THIS query's work. Return STRICT JSON: " +
		`{"passed":true|false,"commands":["..."],"summary":"...","failures":["..."]}` +
		"\nIf anything does not work, set passed=false and list concrete failures. Do not approve broken work."
	testOut, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPrompt)
	testerRejected := false
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
				} else if o.applyTesterFeedback(ctx, query, board, testOut2) {
					snap = o.boardStore.Snapshot()
					board = &snap
				} else {
					testerRejected = false
				}
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return o.checkpointInterrupt(board, session.PhaseTest, err)
	}

	qaFailed := o.runQAGate(ctx, query, board)
	if qaFailed {
		testerRejected = true
		fake := `{"passed":false,"summary":"qa_gate command still failing","failures":["qa_gate red"]}`
		_ = o.applyTesterFeedback(ctx, query, board, fake)
		snap := o.boardStore.Snapshot()
		board = &snap
	}

	return o.completeRun(ctx, runID, query, skillPack, board, testOut, testerRejected, start)
}

func (o *Orchestrator) completeRun(ctx context.Context, runID, query, skillPack string, board *plan.Board, testOut string, testerRejected bool, start time.Time) (*Result, error) {
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
	success := failed == 0 && board.AllDone() && !testerRejected
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: success, FailedTasks: failed,
		Duration: time.Since(start), Summary: summarize(board, board.Plan),
		Backend: o.cfg.Backend, LatencyMs: o.snapshotLatency(),
		Usage: o.snapshotUsage(),
	}
	if testerRejected {
		res.Success = false
		res.Summary = res.Summary + " (tester/QA rejected — plan/tasks rewritten)"
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
