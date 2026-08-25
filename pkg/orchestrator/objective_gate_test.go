package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/session"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// smokeGate is whatever can stand in for the objective command. It matches the
// shape of Orchestrator.qaSmoke, so anything satisfying it can be wired into a
// test orchestrator in place of a real toolchain.
type smokeGate interface {
	run(ctx context.Context, root, cmd string, timeout time.Duration) quality.SmokeResult
}

// fakeGate answers the objective command from a script and records every
// invocation. No shell, no toolchain, no model — the whole point of the seam.
type fakeGate struct {
	mu   sync.Mutex
	runs []string
	ok   bool
	out  string
}

func (f *fakeGate) run(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
	f.mu.Lock()
	f.runs = append(f.runs, cmd)
	f.mu.Unlock()
	sr := quality.SmokeResult{Ran: true, Command: cmd, OK: f.ok, Output: f.out}
	if f.ok {
		sr.Summary = quality.SmokePassedMarker + ": " + cmd
	} else {
		sr.Summary = quality.SmokeFailedMarker
	}
	return sr
}

// count reports how many times the objective command itself was executed.
func (f *fakeGate) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.runs {
		if c == cmd {
			n++
		}
	}
	return n
}

// countingExec records every agent call the harness makes. A corrective wave
// cannot happen without going through here, which is what makes "no more waves"
// assertable as a number instead of as prose.
//
// The tester answers passed:false on purpose: that is what the live run did,
// and it is what sends the harness round the corrective-wave loop the early
// finish is supposed to cut short.
type countingExec struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	c.mu.Lock()
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, r := range reqs {
		c.calls = append(c.calls, r.AgentID)
		reply := `{"status":"done","summary":"did it","files_changed":["stats.go"]}`
		if strings.Contains(r.AgentID, "tester") {
			reply = `{"passed":false,"summary":"not convinced","failures":["prove it again"]}`
		}
		out = append(out, ggagent.SubAgentResult{AgentID: r.AgentID, Output: reply})
	}
	c.mu.Unlock()
	return out, nil
}

func (c *countingExec) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *countingExec) roles() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.calls, ",")
}

const objectiveCmd = "go test ./..."

// objectiveOrch builds an orchestrator that can be driven through
// finalizeAfterExecute — and through a real board — with no model and no real
// command.
//
// exec is the SubAgentRunner interface rather than *countingExec so a test that
// needs workers which really write to disk can supply one; every existing
// caller passes a *countingExec unchanged. gate is an interface for the same
// reason: fakeGate answers with one fixed verdict for its whole life, and a
// test about work that becomes correct PART WAY THROUGH a run needs a gate that
// can change its mind (see scriptedGate).
func objectiveOrch(t *testing.T, gate smokeGate, exec loop.SubAgentRunner, tune func(*config.Config)) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.QAGate = true
	cfg.QAGateCommand = objectiveCmd
	cfg.PlaceholderPass = false
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	cfg.TaskTimeout = 5 * time.Second
	if tune != nil {
		tune(cfg)
	}
	cfg.Normalize()

	o := &Orchestrator{
		cfg:        cfg,
		store:      contextstore.New(cfg.SlmDir()),
		boardStore: plan.NewLiveStore(cfg.SlmDir()),
		shared:     ggagent.NewSharedState(),
		executor:   exec,
		onEvent:    func(Event) {},
		qaSmoke:    gate.run,
	}
	o.buildPackers(nil, 32768)

	// Memory distillation is an LLM phase and has nothing to do with the
	// question under test; leaving it on would put its call into the agent-call
	// count and say nothing about waves.
	pipe := pipeline.Default()
	if pipe.Phases == nil {
		pipe.Phases = map[string]pipeline.PhaseSpec{}
	}
	pipe.Phases["memory"] = pipeline.PhaseSpec{When: pipeline.WhenNever}
	pipe.Execute.MaxWaves = 3
	o.pipe = &pipe

	turn, err := session.BeginTurn(cfg.SlmDir(), "run-objective", "implement the thing")
	if err != nil {
		t.Fatal(err)
	}
	o.currentTurn = turn

	// Write evidence: the run changed a file, which is what makes "the objective
	// might already be met" a question worth spending a probe on.
	o.noteChangedFiles("stats.go")

	// A real Runner, so "did it schedule another corrective wave?" is answered
	// by the component that actually schedules them.
	runner := loop.NewRunner(exec, o.shared)
	runner.Store = o.boardStore
	runner.Root = cfg.Root
	runner.MaxWaves = pipe.Execute.MaxWaves
	runner.MaxRetries = 0
	runner.MaxParallel = 1
	runner.IdleWait = time.Millisecond
	runner.Timeout = 5 * time.Second
	runner.PostWorkerSmoke = false
	runner.RequireSmoke = false
	runner.StaticQuality = false
	runner.ClaimsGate = false
	runner.WorkerCritique = false
	runner.ReviewParallel = false
	runner.Log = func(string, ...interface{}) {}
	// Mirror buildRunner: the between-waves objective probe is part of the
	// production wiring, so the fixture that stands in for a real run has it.
	// TestBuildRunnerInstallsTheBetweenWavesProbe pins the real wiring.
	runner.BetweenWaves = o.objectiveMetBetweenWaves
	o.mu.Lock()
	o.activeRunner = runner
	o.mu.Unlock()
	return o
}

// doneBoard is a board whose agent work is finished.
func doneBoard() *plan.Board {
	return &plan.Board{
		QueryID: "run-objective", Query: "implement the thing",
		Plan:  plan.Plan{Summary: "implement stats"},
		Tasks: []plan.Task{{ID: "T1", Title: "implement", Column: plan.ColDone, Role: plan.RoleWorker}},
	}
}

func finalize(t *testing.T, o *Orchestrator, board *plan.Board) *Result {
	t.Helper()
	if err := o.boardStore.Replace(*board); err != nil {
		t.Fatal(err)
	}
	res, err := o.finalizeAfterExecute(context.Background(), "run-objective",
		"implement the thing", "", board, o.lastRunner(), time.Now())
	if err != nil {
		t.Fatalf("finalizeAfterExecute: %v", err)
	}
	if res == nil {
		t.Fatal("finalizeAfterExecute returned no result")
	}
	return res
}

// TestRunFinishesEarlyWhenTheObjectiveIsAlreadyMet is the regression guard for
// the defect a live SLM run exposed: a green `go test ./...` and the harness
// kept scheduling corrective waves for ~40 more minutes and 412k prompt tokens
// before reporting failure. A board whose STRONG objective gate is green must
// finish where it stands.
func TestRunFinishesEarlyWhenTheObjectiveIsAlreadyMet(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tstats\t0.2s\n"}
	exec := &countingExec{}
	o := objectiveOrch(t, gate, exec, nil)

	res := finalize(t, o, doneBoard())

	if n := exec.n(); n != 0 {
		t.Fatalf("the harness spent %d agent call(s) after the objective was already met: %s",
			n, exec.roles())
	}
	if n := o.lastRunner().CorrectiveRuns(); n != 0 {
		t.Fatalf("scheduled %d corrective wave(s) with a green objective gate", n)
	}
	// The probe REUSES the deterministic pre-test's run of the same command on
	// the same tree, so finishing early must cost no extra command runs.
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("objective command ran %d time(s), want exactly the pre-test's 1", n)
	}
	if !res.Success {
		t.Fatalf("green objective gate reported failure: %+v", res.Summary)
	}
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, OutcomeSuccess)
	}
}

// TestWeakObjectiveGateNeverFinishesEarly pins the load-bearing half of the
// rule: compileall proves a file parses, not that the objective is met, and a
// syntax-only gate must never end a run early however green it is.
func TestWeakObjectiveGateNeverFinishesEarly(t *testing.T) {
	gate := &fakeGate{ok: true}
	exec := &countingExec{}
	o := objectiveOrch(t, gate, exec, func(c *config.Config) {
		c.QAGateCommand = "python -m compileall -q ."
	})

	if _, done := o.objectiveAlreadyMet(context.Background(), doneBoard(), false, nil); done {
		t.Fatal("a syntax-only gate ended the run early")
	}
	if n := o.objectiveProbesSpent(); n != 0 {
		t.Fatalf("a weak gate spent %d probe(s) — it must be refused before it is run", n)
	}
	if n := len(gate.runs); n != 0 {
		t.Fatalf("a weak gate was executed %d time(s) for the early-finish question", n)
	}
}

// TestObjectiveGateNeverSkipsRequiredWork covers the two ways a run still owes
// something that no green command can pay off.
//
// An escalated task is deliberately NOT one of them any more — see
// TestGreenObjectiveFinishesEvenWithAnEscalatedTask for why, and
// TestCircularityGuardTesterRejectionStillBlocksAGreenObjective for the refusal
// that did survive.
func TestObjectiveGateNeverSkipsRequiredWork(t *testing.T) {
	agentWork := &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "implement", Column: plan.ColReadyToDev, Role: plan.RoleWorker}},
	}
	cases := []struct {
		name           string
		board          *plan.Board
		testerRejected bool
		wantBlocker    string
	}{
		{"agent work remains", agentWork, false, "agent work remains"},
		{"tester rejected", doneBoard(), true, "tester rejected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := objectiveBlocker(tc.board, tc.testerRejected); got != tc.wantBlocker {
				t.Fatalf("objectiveBlocker = %q, want %q", got, tc.wantBlocker)
			}
			gate := &fakeGate{ok: true}
			o := objectiveOrch(t, gate, &countingExec{}, nil)
			if _, done := o.objectiveAlreadyMet(context.Background(), tc.board, tc.testerRejected, nil); done {
				t.Fatalf("finished early past required work: %s", tc.wantBlocker)
			}
			if n := len(gate.runs); n != 0 {
				t.Fatalf("spent %d gate run(s) on a board that owes work", n)
			}
		})
	}
}

// TestObjectiveGateIsBounded pins the frequency rule: a probe is spent only when
// something has been written since the last one, and probing stays bounded.
//
// The context here carries NO deadline, which is the case the count fallback
// exists for — see maxProbesWithoutDeadline. The economic rule that governs a
// real run is pinned in TestProbeBudgetScalesWithTheCostOfAsking.
func TestObjectiveGateIsBounded(t *testing.T) {
	// Red gate, so no probe ever short-circuits the loop and every call gets as
	// far as the frequency rule.
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	board := doneBoard()
	ctx := context.Background()

	if _, done := o.objectiveAlreadyMet(ctx, board, false, nil); done {
		t.Fatal("a red gate finished the run early")
	}
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("first probe ran the command %d time(s), want 1", n)
	}

	// Nothing written since: asking the same question of the same tree cannot
	// give a different answer, so it must not be asked.
	for i := 0; i < 3; i++ {
		if _, done := o.objectiveAlreadyMet(ctx, board, false, nil); done {
			t.Fatal("a red gate finished the run early")
		}
	}
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("re-probed an unchanged tree: command ran %d time(s), want 1", n)
	}
	if n := o.objectiveProbesSpent(); n != 1 {
		t.Fatalf("probes spent = %d, want 1", n)
	}

	// New write evidence each time: every one of these is a real question about
	// a tree that really changed, so every one is asked — up to the fallback
	// ceiling, which is what stops it.
	for i := 0; i < 20; i++ {
		o.noteChangedFiles("more" + string(rune('a'+i)) + ".go")
		if _, done := o.objectiveAlreadyMet(ctx, board, false, nil); done {
			t.Fatal("a red gate finished the run early")
		}
	}
	if n := o.objectiveProbesSpent(); n != maxProbesWithoutDeadline {
		t.Fatalf("probes spent = %d, want the no-deadline ceiling %d",
			n, maxProbesWithoutDeadline)
	}
	if n := gate.count(objectiveCmd); n != maxProbesWithoutDeadline {
		t.Fatalf("objective command ran %d time(s), want at most %d",
			n, maxProbesWithoutDeadline)
	}
}

// TestOperatorTestSlotBlocksEarlyFinish: a user-authored agent on the test
// phase is an explicit instruction about how this project verifies itself, and
// a cost optimization must not route around it.
func TestOperatorTestSlotBlocksEarlyFinish(t *testing.T) {
	gate := &fakeGate{ok: true}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	o.mu.Lock()
	o.pipe.Slots = append(o.pipe.Slots, pipeline.Slot{
		ID: "house-tests", Agent: "tester", Before: "test",
	})
	o.mu.Unlock()

	if _, done := o.objectiveAlreadyMet(context.Background(), doneBoard(), false, nil); done {
		t.Fatal("finished early past an operator-configured test slot")
	}
	if n := len(gate.runs); n != 0 {
		t.Fatalf("spent %d gate run(s) on a question that was already settled", n)
	}
}

// TestObjectiveGateNeedsWriteEvidence: with nothing written there is no
// implementation that could already be correct, so no probe is worth its cost.
func TestObjectiveGateNeedsWriteEvidence(t *testing.T) {
	gate := &fakeGate{ok: true}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	o.resetChangedFiles()

	if _, done := o.objectiveAlreadyMet(context.Background(), doneBoard(), false, nil); done {
		t.Fatal("finished early with no write evidence at all")
	}
	if n := len(gate.runs); n != 0 {
		t.Fatalf("spent %d gate run(s) with nothing written", n)
	}
}

// TestObjectiveGateRejectsNoTests: a toolchain that found nothing to run has
// verified nothing. That exit keeps the QA gate's "code compiles" escape hatch
// at the finish line, but it must not end a run early.
func TestObjectiveGateRejectsNoTests(t *testing.T) {
	gate := &fakeGate{ok: false, out: "?   example/pkg\t[no test files]\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)

	g, done := o.objectiveAlreadyMet(context.Background(), doneBoard(), false, nil)
	if done {
		t.Fatal(`"no test files" ended the run early`)
	}
	if !g.NoTests || g.Green {
		t.Fatalf("classification wrong: %+v", g)
	}
}

// TestObjectiveGateReusesThePreTestRun: the pre-test has just run the same
// command on the same tree, so the probe must fold that result in rather than
// pay for an identical second run.
func TestObjectiveGateReusesThePreTestRun(t *testing.T) {
	gate := &fakeGate{ok: true}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	pre := quality.SmokeResult{OK: true, Ran: true, Command: objectiveCmd, Output: "ok"}

	g, done := o.objectiveAlreadyMet(context.Background(), doneBoard(), false, &pre)
	if !done {
		t.Fatalf("green reused pre-test did not finish early: %+v", g)
	}
	if n := len(gate.runs); n != 0 {
		t.Fatalf("re-ran the command %d time(s) instead of reusing the pre-test", n)
	}
}

// TestGreenObjectiveWithAFailedTaskSucceedsAndSaysSo is the verdict case. A run
// whose only blemish is a failed subsidiary task must not report flat failure
// when the strong objective gate is green — and the failure must stay visible.
func TestGreenObjectiveWithAFailedTaskSucceedsAndSaysSo(t *testing.T) {
	gate := &fakeGate{ok: true}
	exec := &countingExec{}
	o := objectiveOrch(t, gate, exec, nil)

	board := doneBoard()
	// A subsidiary bookkeeping task that failed but is parked with the humans,
	// so it is neither open agent work nor an escalation.
	board.Tasks = append(board.Tasks, plan.Task{
		ID: "T2", Title: "update the changelog", Column: plan.ColScoped,
		Role: "docs", Error: "docs agent returned nothing",
	})

	res := finalize(t, o, board)

	if res.FailedTasks != 1 {
		t.Fatalf("FailedTasks = %d, want the failure still counted (1)", res.FailedTasks)
	}
	if !res.Success {
		t.Fatal("a green strong objective gate with one failed subsidiary task reported flat failure")
	}
	if res.Outcome != OutcomeSuccessWithFailures {
		t.Fatalf("Outcome = %q, want %q — the caller must be able to tell this from a clean run",
			res.Outcome, OutcomeSuccessWithFailures)
	}
	if !strings.Contains(res.Summary, "1 task(s) failed") {
		t.Fatalf("the failure was swallowed instead of surfaced: %q", res.Summary)
	}
}

// TestRunOutcomeNamesTheVerdict pins the three states Result.Outcome carries.
func TestRunOutcomeNamesTheVerdict(t *testing.T) {
	cases := []struct {
		success bool
		failed  int
		want    string
	}{
		{true, 0, OutcomeSuccess},
		{true, 2, OutcomeSuccessWithFailures},
		{false, 0, OutcomeFailure},
		{false, 3, OutcomeFailure},
	}
	for _, tc := range cases {
		if got := runOutcome(tc.success, tc.failed); got != tc.want {
			t.Fatalf("runOutcome(%v,%d) = %q, want %q", tc.success, tc.failed, got, tc.want)
		}
	}
}

// ── between-waves probe ─────────────────────────────────────────────────────

// writingExec is a worker that really writes its task's file, so the review
// fast path approves on disk evidence and no reviewer LLM is involved. It also
// records which TASKS were dispatched — "wave 2 never ran" is a set of task ids,
// not a sentence.
type writingExec struct {
	root string
	mu   sync.Mutex
	ids  []string
}

func (e *writingExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		e.mu.Lock()
		e.ids = append(e.ids, req.TaskID)
		n := len(e.ids)
		e.mu.Unlock()
		name := strings.ToLower(req.TaskID) + ".go"
		_ = os.WriteFile(filepath.Join(e.root, name),
			[]byte(fmt.Sprintf("package main\n\n// rev %d\n", n)), 0o600)
		out = append(out, ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: fmt.Sprintf("Observation: ws_edit edited %s (1 replacement(s))\n", name) +
				fmt.Sprintf(`{"status":"done","summary":"done","files_changed":[%q]}`, name),
		})
	}
	return out, nil
}

func (e *writingExec) dispatched() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ids...)
}

func (e *writingExec) sawTask(id string) bool {
	for _, got := range e.dispatched() {
		if got == id {
			return true
		}
	}
	return false
}

// midBoard is the shape of the run that motivated this fix: 4 planned tasks,
// parallel=2, so the board needs two waves.
func midBoard() *plan.Board {
	tasks := make([]plan.Task, 0, 4)
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		tasks = append(tasks, plan.Task{
			ID: id, Title: "build " + id, Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "write " + strings.ToLower(id) + ".go",
			Acceptance:  "file written",
			Files:       []string{strings.ToLower(id) + ".go"},
		})
	}
	return &plan.Board{QueryID: "run-objective", Query: "implement the thing",
		Plan: plan.Plan{Summary: "implement stats"}, Tasks: tasks}
}

// runBoardLive drives a REAL board through the REAL loop with the between-waves
// probe wired as production wires it. `prep` runs after the orchestrator exists
// and before the board starts, so a test can attach an event sink and see the
// stop event the operator would see.
func runBoardLive(t *testing.T, gate *fakeGate, board *plan.Board,
	prep func(*Orchestrator)) (*Orchestrator, *loop.Runner, *writingExec) {
	t.Helper()
	exec := &writingExec{}
	o := objectiveOrch(t, gate, exec, nil)
	exec.root = o.cfg.Root
	if prep != nil {
		prep(o)
	}
	r := o.lastRunner()
	r.MaxParallel = 2

	if err := o.boardStore.Replace(*board); err != nil {
		t.Fatal(err)
	}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	return o, r, exec
}

// runMidBoard drives a REAL board through the REAL loop with the between-waves
// probe wired as production wires it, then runs the finish path over whatever
// board it ended with.
func runMidBoard(t *testing.T, gate *fakeGate) (*Orchestrator, *loop.Runner, *writingExec, *plan.Board) {
	t.Helper()
	board := midBoard()
	o, r, exec := runBoardLive(t, gate, board, nil)
	return o, r, exec, board
}

// TestBoardStopsMidRunWhenObjectiveBecomesGreen is the regression guard for the
// measured defect.
//
// The live 9B run's fixture had a green `go test ./...` at 23:15 and the harness
// kept grinding corrective rounds until the 20-minute ceiling at 23:30, because
// one task kept getting rejected: the board never drained, so the post-drain
// probe never fired. The probe has to be taken BETWEEN waves, and a board whose
// gate is green after wave 1 must not run wave 2.
func TestBoardStopsMidRunWhenObjectiveBecomesGreen(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tstats\t0.2s\n"}
	o, r, exec, board := runMidBoard(t, gate)

	if got := r.Waves(); got != 1 {
		t.Fatalf("waves=%d, want 1 — wave 2 ran after the objective was already met", got)
	}
	if n := len(exec.dispatched()); n != 2 {
		t.Fatalf("dispatched %d agent call(s) (%v), want only wave 1's 2",
			n, exec.dispatched())
	}
	// Which two ran is the scheduler's business; that the other two never did
	// is this test's.
	skipped := 0
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		if !exec.sawTask(id) {
			skipped++
		}
	}
	if skipped != 2 {
		t.Fatalf("%d task(s) went unexecuted, want 2: dispatched=%v", skipped, exec.dispatched())
	}
	stopped, reason, left := r.EarlyStop()
	if !stopped {
		t.Fatal("the board ran to its bound instead of stopping on the green objective")
	}
	if !strings.Contains(reason, "objective already met") {
		t.Fatalf("stop reason=%q", reason)
	}
	if left != 2 {
		t.Fatalf("unexecuted=%d, want the 2 tasks that never ran", left)
	}
	// One probe, one command run. The finish path must then REUSE that answer:
	// nothing wrote to the tree after the board stopped, so a pre-test re-run
	// would only re-prove it at the price of a whole suite.
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("objective command ran %d time(s), want exactly the 1 probe", n)
	}

	res := finalize(t, o, board)
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("the finish path re-ran the objective command (%d total) instead of "+
			"reusing the between-waves answer", n)
	}
	if !res.Success || res.Outcome != OutcomeSuccess {
		t.Fatalf("green objective reported success=%v outcome=%q: %s",
			res.Success, res.Outcome, res.Summary)
	}
	if n := o.lastRunner().CorrectiveRuns(); n != 0 {
		t.Fatalf("scheduled %d corrective wave(s) with a green objective gate", n)
	}
}

// TestResultReportsTheTasksItNeverExecuted: stopping mid-board abandons planned
// work, so the Result has to say so — in a number a caller can branch on and in
// words a human will read.
//
// It also pins the shape of the abandonment: the two tasks stay OPEN on the
// board, because a task whose files were never written is not eligible for the
// green-gate promotion. So the run ends with a green objective and visible
// unfinished work, and reports success anyway — which is the honest reading,
// not a hidden one.
func TestResultReportsTheTasksItNeverExecuted(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\n"}
	o, _, exec, board := runMidBoard(t, gate)
	res := finalize(t, o, board)

	if res.UnexecutedTasks != 2 {
		t.Fatalf("Result.UnexecutedTasks = %d, want 2", res.UnexecutedTasks)
	}
	if !strings.Contains(res.Summary, "2 task(s) not executed") {
		t.Fatalf("the abandoned work was swallowed by the summary: %q", res.Summary)
	}
	if !res.Success || res.Outcome != OutcomeSuccess {
		t.Fatalf("a green objective with deliberately abandoned work reported "+
			"success=%v outcome=%q — the harness contradicted the gate it obeyed",
			res.Success, res.Outcome)
	}
	open := map[string]bool{}
	for _, tk := range res.Board.Tasks {
		if tk.Column != plan.ColDone {
			open[tk.ID] = true
		}
	}
	if len(open) != 2 {
		t.Fatalf("%d task(s) open on the final board, want the 2 that never ran: %v", len(open), open)
	}
	for id := range open {
		if exec.sawTask(id) {
			t.Fatalf("%s ran but is still open — the wrong tasks were left behind", id)
		}
	}
}

// TestBoardWithoutAGreenObjectiveRunsToItsNormalBound is the no-behavior-change
// control: a run whose gate never goes green must execute every planned task.
func TestBoardWithoutAGreenObjectiveRunsToItsNormalBound(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	_, r, exec, _ := runMidBoard(t, gate)

	if got := r.Waves(); got < 2 {
		t.Fatalf("waves=%d — a red gate must not shorten the board", got)
	}
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		if !exec.sawTask(id) {
			t.Fatalf("%s never ran on a board with a red objective gate: %v", id, exec.dispatched())
		}
	}
	if stopped, _, left := r.EarlyStop(); stopped || left != 0 {
		t.Fatalf("EarlyStop reported stopped=%v left=%d on a red gate", stopped, left)
	}
	// One probe per wave that wrote something is the most this board can ask
	// for; beyond that the unchanged-tree rule would have stopped holding.
	if n := gate.count(objectiveCmd); n > r.Waves() {
		t.Fatalf("probed %d time(s) across %d wave(s) — the unchanged-tree rule "+
			"stopped holding", n, r.Waves())
	}
}

// TestBetweenWavesProbeRespectsEveryRefusal: the probe fires in a new place, so
// every rule that forbids finishing early has to hold there too. The first case
// is the positive control — without it the table could pass by refusing
// everything.
func TestBetweenWavesProbeRespectsEveryRefusal(t *testing.T) {
	inFlight := func(b *plan.Board) *plan.Board {
		b.Tasks[0].Column = plan.ColInReview
		return b
	}
	escalated := func(b *plan.Board) *plan.Board {
		b.Tasks = append(b.Tasks, plan.Task{
			ID: "T9", Title: "risky", Column: plan.ColBlocked, Role: plan.RoleWorker,
			Notes: "ESCALATED: needs human review",
		})
		return b
	}
	cases := []struct {
		name     string
		tune     func(*config.Config)
		setup    func(*Orchestrator)
		board    func(*plan.Board) *plan.Board
		wantStop bool
		wantRuns int
	}{
		{name: "green strong gate with ready work remaining", wantStop: true, wantRuns: 1},
		// An escalation is REPORTED, not obeyed: it sits in this table as a stop,
		// not a refusal, because the run it used to veto is exactly the expensive
		// one the probe exists to save.
		{name: "escalated task is reported, not obeyed", wantStop: true, wantRuns: 1, board: escalated},
		{name: "weak gate", wantRuns: 0,
			tune: func(c *config.Config) { c.QAGateCommand = "python -m compileall -q ." }},
		{name: "tester rejected", wantRuns: 0,
			setup: func(o *Orchestrator) { o.noteTesterRejected(true) }},
		{name: "task still in flight", wantRuns: 0, board: inFlight},
		{name: "operator-configured test slot", wantRuns: 0,
			setup: func(o *Orchestrator) {
				o.pipe.Slots = append(o.pipe.Slots, pipeline.Slot{
					ID: "house-tests", Agent: "tester", Before: "test"})
			}},
		{name: "no write evidence", wantRuns: 0,
			setup: func(o *Orchestrator) { o.resetChangedFiles() }},
		{name: "qa_gate off", wantRuns: 0,
			tune: func(c *config.Config) { c.QAGate = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := &fakeGate{ok: true, out: "ok\n"}
			o := objectiveOrch(t, gate, &countingExec{}, tc.tune)
			if tc.setup != nil {
				tc.setup(o)
			}
			board := midBoard()
			if tc.board != nil {
				board = tc.board(board)
			}
			stop, reason := o.objectiveMetBetweenWaves(context.Background(), board)
			if stop != tc.wantStop {
				t.Fatalf("stop=%v (%q), want %v", stop, reason, tc.wantStop)
			}
			if n := len(gate.runs); n != tc.wantRuns {
				t.Fatalf("objective command ran %d time(s), want %d", n, tc.wantRuns)
			}
		})
	}
}

// TestBetweenWavesProbeRejectsNoTests: a toolchain that found nothing to run has
// verified nothing, so it cannot stop a board either.
func TestBetweenWavesProbeRejectsNoTests(t *testing.T) {
	gate := &fakeGate{ok: false, out: "?   example/pkg\t[no test files]\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)

	if stop, _ := o.objectiveMetBetweenWaves(context.Background(), midBoard()); stop {
		t.Fatal(`"no test files" stopped the board`)
	}
}

// TestBetweenWavesProbeBudgetIsBounded: the probe can now be asked after EVERY
// wave, so its cost is what has to be bounded. Two separate rules do it — the
// command runs only when something has been written since the last ask, and
// only while asking is affordable — and this pins the first one, which no
// amount of runway relaxes.
//
// Ten of these twenty waves wrote nothing. Those ten must cost nothing, because
// re-asking the same question of the same tree cannot give a different answer.
func TestBetweenWavesProbeBudgetIsBounded(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	board := midBoard()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Twenty waves, half of which wrote nothing new.
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
		}
		if stop, _ := o.objectiveMetBetweenWaves(ctx, board); stop {
			t.Fatal("a red gate stopped the board")
		}
	}
	// The fake is instant, so a 20-minute runway affords every ask that has a
	// changed tree behind it — and exactly those.
	if n := gate.count(objectiveCmd); n != 10 {
		t.Fatalf("objective command ran %d time(s) across 20 waves of which 10 "+
			"wrote, want exactly those 10", n)
	}
	if n := o.objectiveProbesSpent(); n != 10 {
		t.Fatalf("probes spent = %d, want 10", n)
	}
}

// TestSpentBudgetStillLetsTheFinishPathReuseAFreeAnswer: between-waves probes
// can now arrive at the finish line having spent the whole budget. The budget
// bounds what the run PAYS FOR, and a result the pre-test already produced
// costs nothing — refusing it would buy nothing and cost a full verification
// phase.
func TestSpentBudgetStillLetsTheFinishPathReuseAFreeAnswer(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	ctx := context.Background() // no deadline: the count fallback, fully spendable

	for i := 0; i < maxProbesWithoutDeadline; i++ {
		o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
		if stop, _ := o.objectiveMetBetweenWaves(ctx, midBoard()); stop {
			t.Fatal("a red gate stopped the board")
		}
	}
	if n := o.objectiveProbesSpent(); n != maxProbesWithoutDeadline {
		t.Fatalf("probes spent = %d, want the budget fully spent", n)
	}

	pre := quality.SmokeResult{OK: true, Ran: true, Command: objectiveCmd, Output: "ok"}
	before := len(gate.runs)
	g, done := o.objectiveAlreadyMet(ctx, doneBoard(), false, &pre)
	if !done {
		t.Fatalf("a free green answer was refused for budget: %+v", g)
	}
	if len(gate.runs) != before {
		t.Fatal("reusing an already-run result executed the command again")
	}
}

// TestBuildRunnerInstallsTheBetweenWavesProbe pins the production wiring: the
// fixture above mirrors it, and a mirror is only worth what the original is.
func TestBuildRunnerInstallsTheBetweenWavesProbe(t *testing.T) {
	o := testOrch(t, nil)
	o.shared = nil
	if r := o.buildRunner("q", "run-1", ""); r.BetweenWaves == nil {
		t.Fatal("buildRunner did not install the between-waves objective probe")
	}
}

// TestClassifySmokeIsTheOneDefinitionOfGreen: the mid-run answer and the finish
// path's answer come from this function, so they cannot disagree.
func TestClassifySmokeIsTheOneDefinitionOfGreen(t *testing.T) {
	cases := []struct {
		name             string
		cmd              string
		sr               quality.SmokeResult
		green, noTests   bool
		weak, shouldHave bool
	}{
		{name: "strong pass", cmd: objectiveCmd,
			sr: quality.SmokeResult{Ran: true, OK: true}, green: true},
		{name: "strong fail", cmd: objectiveCmd,
			sr: quality.SmokeResult{Ran: true, Output: "--- FAIL: X\nFAIL\n"}},
		{name: "no test files", cmd: objectiveCmd,
			sr: quality.SmokeResult{Ran: true, Output: "?\tx\t[no test files]\n"}, noTests: true},
		{name: "weak pass", cmd: "python -m compileall -q .",
			sr: quality.SmokeResult{Ran: true, OK: true}, green: true, weak: true},
		{name: "never ran", cmd: objectiveCmd, sr: quality.SmokeResult{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := classifySmoke(tc.cmd, tc.sr)
			if g.Green != tc.green || g.NoTests != tc.noTests || g.Weak != tc.weak {
				t.Fatalf("classifySmoke = %+v, want green=%v noTests=%v weak=%v",
					g, tc.green, tc.noTests, tc.weak)
			}
		})
	}
}

// ── a green objective over work that failed or escalated ────────────────────

// escalatedTask is the task that motivated this fix: reviews kept turning it
// down until the board parked it for a human.
func escalatedTask() plan.Task {
	return plan.Task{
		ID: "T9", Title: "flaky bit", Role: plan.RoleWorker, Column: plan.ColBlocked,
		Error: "review rejected after max retries",
		Notes: "ESCALATED: needs human review",
	}
}

// escalatedMidBoard is midBoard with an escalation already on it — the shape the
// measured runs were in when they still had ~15 minutes to burn.
func escalatedMidBoard() *plan.Board {
	b := midBoard()
	b.Tasks = append(b.Tasks, escalatedTask())
	return b
}

// TestGreenObjectiveFinishesEvenWithAnEscalatedTask is the regression guard for
// the measured defect.
//
// Three runs of one fixture, one model, one 20-minute ceiling. The only run that
// finished was the one with failed_tasks == 0: 998s, five checks pass. The two
// with failed_tasks == 1 ground on to the ceiling and died on `context deadline
// exceeded`, because the probe refused whenever anything had escalated — and a
// task escalates BECAUSE reviews kept failing, i.e. on exactly the run that
// cannot afford the refusal. The board is a planner's guess at a decomposition;
// the gate is the acceptance criterion the user stated, measured against the
// tree that exists. The measurement wins — loudly, never silently.
func TestGreenObjectiveFinishesEvenWithAnEscalatedTask(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tstats\t0.2s\n"}
	log := &eventLog{}
	board := escalatedMidBoard()

	o, r, exec := runBoardLive(t, gate, board, log.attach)

	// 1. The board stopped where it stood instead of grinding to the ceiling.
	if got := r.Waves(); got != 1 {
		t.Fatalf("waves=%d, want 1 — the escalated task vetoed the early finish again", got)
	}
	if n := len(exec.dispatched()); n != 2 {
		t.Fatalf("dispatched %d agent call(s) (%v), want only wave 1's 2", n, exec.dispatched())
	}
	stopped, reason, left := r.EarlyStop()
	if !stopped {
		t.Fatal("a green strong gate did not stop a board carrying an escalated task")
	}
	if left != 2 {
		t.Fatalf("unexecuted=%d, want the 2 tasks that never ran", left)
	}
	// 2. The stop event names what the run walked past.
	if !strings.Contains(reason, "1 task(s) failed or escalated") {
		t.Fatalf("the stop event does not name the escalation: %q", reason)
	}

	res := finalize(t, o, board)

	// 3. Loud, not silent: a success, but never a BARE one.
	if !res.Success {
		t.Fatalf("green objective with an escalated task reported failure: %s", res.Summary)
	}
	if res.Outcome == OutcomeSuccess {
		t.Fatalf("reported a clean %q over an escalated task — a caller cannot tell "+
			"this from a run that owed nobody anything", OutcomeSuccess)
	}
	if res.Outcome != OutcomeSuccessWithFailures {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, OutcomeSuccessWithFailures)
	}
	if res.FailedTasks == 0 {
		t.Fatal("FailedTasks was zeroed — the failure must survive the early finish")
	}
	if res.UnexecutedTasks != 2 {
		t.Fatalf("Result.UnexecutedTasks = %d, want the 2 tasks that never ran", res.UnexecutedTasks)
	}
	if !strings.Contains(res.Summary, "1 task(s) failed or escalated") {
		t.Fatalf("the summary does not name the escalation: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "left on the board for inspection") {
		t.Fatalf("the summary does not say where the escalation went: %q", res.Summary)
	}

	// 4. And it really is still on the board for a human to look at.
	var seen, open bool
	for _, tk := range res.Board.Tasks {
		if tk.ID == "T9" {
			seen, open = true, tk.Column != plan.ColDone
		}
	}
	if !seen {
		t.Fatal("the escalated task vanished from the final board")
	}
	if !open {
		t.Fatal("the escalated task was promoted to done instead of left for inspection")
	}

	// 5. The finish event tells the same story the summary does.
	msgs := log.scoped("objective_met")
	if len(msgs) == 0 {
		t.Fatal("no objective_met event was emitted")
	}
	named := false
	for _, m := range msgs {
		if strings.Contains(m, "1 task(s) failed or escalated") {
			named = true
		}
	}
	if !named {
		t.Fatalf("the objective_met event does not name the escalation: %v", msgs)
	}
}

// TestCircularityGuardTesterRejectionStillBlocksAGreenObjective pins the refusal
// that did NOT get relaxed.
//
// The tester is the harness's own verification role and the objective gate is
// the thing it is supposed to be checking, so letting the gate overrule it is
// the gate clearing its own examiner — circular. The first half is the positive
// control: the identical board finishes early WITHOUT the rejection, so this
// fails if the refusal is either too loose or too strict.
func TestCircularityGuardTesterRejectionStillBlocksAGreenObjective(t *testing.T) {
	ctx := context.Background()

	control := &fakeGate{ok: true, out: "ok\n"}
	oc := objectiveOrch(t, control, &countingExec{}, nil)
	if stop, _ := oc.objectiveMetBetweenWaves(ctx, escalatedMidBoard()); !stop {
		t.Fatal("control: a green strong gate refused to finish over an escalated task")
	}

	gate := &fakeGate{ok: true, out: "ok\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	o.noteTesterRejected(true)

	if stop, reason := o.objectiveMetBetweenWaves(ctx, escalatedMidBoard()); stop {
		t.Fatalf("between waves: the objective gate cleared its own examiner (%q)", reason)
	}
	drained := doneBoard()
	drained.Tasks = append(drained.Tasks, escalatedTask())
	if _, done := o.objectiveAlreadyMet(ctx, drained, true, nil); done {
		t.Fatal("after drain: the objective gate cleared its own examiner")
	}
	if got := objectiveBlocker(drained, true); got != "tester rejected" {
		t.Fatalf("objectiveBlocker = %q, want %q", got, "tester rejected")
	}
	if got := betweenWavesBlocker(escalatedMidBoard(), true); got != "tester rejected" {
		t.Fatalf("betweenWavesBlocker = %q, want %q", got, "tester rejected")
	}
	if n := len(gate.runs); n != 0 {
		t.Fatalf("spent %d gate run(s) on a board the tester had already turned down", n)
	}
}

// TestGreenObjectiveStillRefusesWhatItCannotTrust is the narrowness proof: the
// escalation veto is gone and NOTHING else went with it.
//
// Every row carries an escalated task, at both probe points, so the first row —
// which must STOP — fails the moment the veto comes back, and the rest fail the
// moment a refusal about the gate's own trustworthiness is relaxed along with
// it.
func TestGreenObjectiveStillRefusesWhatItCannotTrust(t *testing.T) {
	inFlight := func(b *plan.Board) *plan.Board {
		b.Tasks[0].Column = plan.ColInReview
		return b
	}
	cases := []struct {
		name     string
		gate     func() *fakeGate
		tune     func(*config.Config)
		setup    func(*Orchestrator)
		board    func(*plan.Board) *plan.Board
		wantStop bool
		wantRuns int
	}{
		{name: "control: strong green gate over an escalated task", wantStop: true, wantRuns: 1},
		{name: "weak gate", wantRuns: 0,
			tune: func(c *config.Config) { c.QAGateCommand = "python -m compileall -q ." }},
		{name: "no test files", wantRuns: 1, gate: func() *fakeGate {
			return &fakeGate{out: "?   example/pkg\t[no test files]\n"}
		}},
		{name: "tester rejected", wantRuns: 0,
			setup: func(o *Orchestrator) { o.noteTesterRejected(true) }},
		{name: "task still in flight", wantRuns: 0, board: inFlight},
		{name: "operator-configured test slot", wantRuns: 0,
			setup: func(o *Orchestrator) {
				o.pipe.Slots = append(o.pipe.Slots, pipeline.Slot{
					ID: "house-tests", Agent: "tester", Before: "test"})
			}},
	}
	points := []struct {
		name  string
		base  func() *plan.Board
		probe func(*Orchestrator, *plan.Board) bool
	}{
		{name: "between waves", base: midBoard, probe: func(o *Orchestrator, b *plan.Board) bool {
			stop, _ := o.objectiveMetBetweenWaves(context.Background(), b)
			return stop
		}},
		{name: "after drain", base: doneBoard, probe: func(o *Orchestrator, b *plan.Board) bool {
			_, done := o.objectiveAlreadyMet(context.Background(), b, o.testerRejectedNow(), nil)
			return done
		}},
	}
	for _, tc := range cases {
		for _, pt := range points {
			t.Run(tc.name+"/"+pt.name, func(t *testing.T) {
				gate := &fakeGate{ok: true, out: "ok\n"}
				if tc.gate != nil {
					gate = tc.gate()
				}
				o := objectiveOrch(t, gate, &countingExec{}, tc.tune)
				if tc.setup != nil {
					tc.setup(o)
				}
				board := pt.base()
				board.Tasks = append(board.Tasks, escalatedTask())
				if tc.board != nil {
					board = tc.board(board)
				}
				if got := pt.probe(o, board); got != tc.wantStop {
					t.Fatalf("stop=%v, want %v", got, tc.wantStop)
				}
				if n := len(gate.runs); n != tc.wantRuns {
					t.Fatalf("objective command ran %d time(s), want %d", n, tc.wantRuns)
				}
			})
		}
	}
}

// TestEscalationStillFailsARunThatDidNotFinishOnTheGate keys the carve-out.
//
// The license to report success over an escalation belongs to finishObjectiveMet
// alone — "this run ENDED because the strong objective command measured green".
// A run that reached the finish line the ordinary way still fails on an
// escalation, even with the same strong command in hand, because "qa_gate is on
// and did not fail" is not the same claim as "measured green": it is also true
// of a gate that never ran.
func TestEscalationStillFailsARunThatDidNotFinishOnTheGate(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	board := doneBoard()
	board.Tasks = append(board.Tasks, escalatedTask())

	run := func() *Result {
		t.Helper()
		res, err := o.completeRun(context.Background(), "run-objective", "implement the thing",
			"", board, "", false, false, objectiveCmd, time.Now())
		if err != nil {
			t.Fatalf("completeRun: %v", err)
		}
		return res
	}

	res := run()
	if res.Success || res.Outcome != OutcomeFailure {
		t.Fatalf("an escalation outside the early finish reported success=%v outcome=%q",
			res.Success, res.Outcome)
	}
	if !strings.Contains(res.Summary, "escalated tasks need human review") {
		t.Fatalf("summary = %q", res.Summary)
	}

	// Same board, same command — but now the run really did end on the gate.
	o.noteObjectiveEarlyFinish(objectiveGate{Cmd: objectiveCmd, Ran: true, Green: true})
	res = run()
	if !res.Success {
		t.Fatalf("a run that ended on the gate still reported failure: %s", res.Summary)
	}
	if res.Outcome != OutcomeSuccessWithFailures {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, OutcomeSuccessWithFailures)
	}
	if res.FailedTasks != 1 {
		t.Fatalf("FailedTasks = %d, want the escalated task still counted", res.FailedTasks)
	}
}

// TestWeakGateNeverLicensesAnEscalation: noteObjectiveEarlyFinish records only a
// gate that ran, went green and is strong, so a syntax-only command can never
// become the license completeRun reads.
func TestWeakGateNeverLicensesAnEscalation(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{ok: true}, &countingExec{}, nil)
	for _, g := range []objectiveGate{
		{Cmd: "python -m compileall -q .", Ran: true, Green: true, Weak: true},
		{Cmd: objectiveCmd, Ran: true, NoTests: true},
		{Cmd: objectiveCmd, Green: true},
	} {
		o.noteObjectiveEarlyFinish(g)
		if o.finishedOnObjectiveGate() {
			t.Fatalf("gate %+v licensed an escalation", g)
		}
	}
	o.noteObjectiveEarlyFinish(objectiveGate{Cmd: objectiveCmd, Ran: true, Green: true})
	if !o.finishedOnObjectiveGate() {
		t.Fatal("a strong green gate did not license the finish")
	}
}

// TestRedObjectiveGateWithAnEscalatedTaskIsUnchanged is the no-behavior-change
// control: nothing about the relaxation touches a run whose gate never goes
// green. Every planned task still runs and no early stop is recorded.
func TestRedObjectiveGateWithAnEscalatedTaskIsUnchanged(t *testing.T) {
	gate := &fakeGate{out: "--- FAIL: TestStats\nFAIL\n"}
	board := escalatedMidBoard()

	o, r, exec := runBoardLive(t, gate, board, nil)

	if got := r.Waves(); got < 2 {
		t.Fatalf("waves=%d — a red gate must not shorten the board", got)
	}
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		if !exec.sawTask(id) {
			t.Fatalf("%s never ran on a board with a red objective gate: %v", id, exec.dispatched())
		}
	}
	if stopped, _, left := r.EarlyStop(); stopped || left != 0 {
		t.Fatalf("EarlyStop reported stopped=%v left=%d on a red gate", stopped, left)
	}
	if o.finishedOnObjectiveGate() {
		t.Fatal("a red gate recorded an objective-gate finish")
	}
	// One probe per wave that wrote something, and no more.
	if n := gate.count(objectiveCmd); n > r.Waves() {
		t.Fatalf("probed %d time(s) across %d wave(s) — the unchanged-tree rule "+
			"stopped holding", n, r.Waves())
	}
}

// TestUnfinishedForReviewCountsEachTaskOnce is the arithmetic behind every
// sentence above: the count never drops below Board.FailedCount, an escalation
// note with no error of its own is still counted, and a task that is both is
// counted once.
func TestUnfinishedForReviewCountsEachTaskOnce(t *testing.T) {
	cases := []struct {
		name  string
		tasks []plan.Task
		want  int
	}{
		{"clean board", []plan.Task{{ID: "T1", Column: plan.ColDone}}, 0},
		{"failed and escalated is one task", []plan.Task{
			{ID: "T1", Column: plan.ColDone},
			escalatedTask(),
		}, 1},
		{"escalation note with no error of its own", []plan.Task{
			{ID: "T1", Column: plan.ColDone},
			{ID: "T2", Column: plan.ColReadyToDev, Notes: "ESCALATED: needs human review"},
		}, 1},
		{"a plain failure", []plan.Task{
			{ID: "T1", Column: plan.ColScoped, Error: "docs agent returned nothing"},
		}, 1},
		{"historical note on a done task", []plan.Task{
			{ID: "T1", Column: plan.ColDone, Notes: "ESCALATED once, then fixed"},
		}, 0},
		{"both kinds at once", []plan.Task{
			escalatedTask(),
			{ID: "T2", Column: plan.ColReadyToDev, Notes: "needs human"},
			{ID: "T3", Column: plan.ColDone},
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &plan.Board{Tasks: tc.tasks}
			got := unfinishedForReview(b)
			if got != tc.want {
				t.Fatalf("unfinishedForReview = %d, want %d", got, tc.want)
			}
			if got < b.FailedCount() {
				t.Fatalf("unfinishedForReview (%d) fell below FailedCount (%d)", got, b.FailedCount())
			}
		})
	}
	if unfinishedForReview(nil) != 0 {
		t.Fatal("a nil board owes nothing")
	}
	if escalationNotice(0) != "" {
		t.Fatal("nothing to report must produce no sentence")
	}
}
