package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// realFailureTokens are the substrings that mean a test/build command actually
// FAILED, as opposed to merely reporting packages that have no tests.
//
// `go test ./...` prints `?   pkg/foo  [no test files]` for every untested
// package WHILE ALSO printing FAIL for the failing ones, so in any mixed repo
// (i.e. almost all of them, this one included) the old
// `strings.Contains(sr.Output, "?\t")` clause turned a real reproduced failure
// into "no test files found — skipping gate (code compiles)" and returned
// green. finalizeAfterExecute then promoted the board and cleared the tester
// rejection on the strength of it.
var realFailureTokens = []string{
	"FAIL",
	"build failed",
	"cannot find",
	"undefined:",
	"Error:",
	"error:",
	"panic:",
	"Traceback (most recent call last)",
}

// noTestsTokens indicate the toolchain found nothing to run.
var noTestsTokens = []string{
	"no test files",
	"no Go files",
	"no tests ran",
	"collected 0 items",
}

// qaLooksLikeNoTests reports whether a non-zero exit is only "there is nothing
// to test" and not a real failure hiding among the [no test files] lines.
func qaLooksLikeNoTests(output string) bool {
	if strings.TrimSpace(output) == "" {
		return false
	}
	for _, tok := range realFailureTokens {
		if strings.Contains(output, tok) {
			return false
		}
	}
	for _, tok := range noTestsTokens {
		if strings.Contains(output, tok) {
			return true
		}
	}
	return false
}

// runSmoke executes the QA/acceptance command through the orchestrator's own
// seam so a test can answer the objective question without a toolchain.
//
// Production leaves o.qaSmoke nil and gets quality.RunSmoke verbatim. Every
// place in this file that used to call quality.RunSmoke directly goes through
// here, so the gate a test drives is the gate a run drives.
func (o *Orchestrator) runSmoke(ctx context.Context, cmd string) quality.SmokeResult {
	var timeout time.Duration
	if o != nil && o.cfg != nil {
		timeout = o.cfg.TaskTimeout
	}
	return o.runSmokeIn(ctx, cmd, timeout)
}

// runSmokeIn is runSmoke with an explicit timeout (the build preflight uses a
// much shorter one than a whole test suite).
func (o *Orchestrator) runSmokeIn(ctx context.Context, cmd string, timeout time.Duration) quality.SmokeResult {
	root := ""
	if o != nil && o.cfg != nil {
		root = o.cfg.Root
	}
	if o != nil && o.qaSmoke != nil {
		return o.qaSmoke(ctx, root, cmd, timeout)
	}
	return quality.RunSmoke(ctx, root, cmd, timeout)
}

// objectiveGate is ONE evaluation of the project's objective: the same command
// runQAGate iterates, run once, classified the same way.
//
// It exists so the harness has exactly one definition of "the objective is
// met". The mid-run early-finish check (objectiveAlreadyMet) and the finish
// path's gate rounds both classify through evalObjectiveGate, so the answer the
// loop acts on halfway through a run cannot disagree with the answer that
// decides the verdict.
type objectiveGate struct {
	Cmd string
	// Ran is true when the command actually executed.
	Ran bool
	// Green is true when the objective is satisfied: the command exited clean.
	// A "nothing to run" exit is NoTests, never Green — it verifies nothing and
	// must not end a run early.
	Green bool
	// NoTests is a non-zero exit that means only "the toolchain found nothing
	// to run", with no real failure hiding among the [no test files] lines.
	NoTests bool
	// Weak marks a syntax-only gate (compileall / py_compile / node --check).
	// A weak gate may report Green and STILL never end a run early: a syntax
	// check cannot rubber-stamp an incomplete board. See resume.go's soft
	// success, which has refused weak gates since the TestSLMs regression.
	Weak   bool
	Output string
	// Dur is how long the command took, carried through from the SmokeResult so
	// the probe budget can price the next ask against the runway that is left.
	Dur time.Duration
}

// evalObjectiveGate runs the objective command once and classifies the result.
func (o *Orchestrator) evalObjectiveGate(ctx context.Context, cmd string) objectiveGate {
	if strings.TrimSpace(cmd) == "" {
		return objectiveGate{Cmd: cmd, Weak: quality.IsWeakQACommand(cmd)}
	}
	return classifySmoke(cmd, o.runSmoke(ctx, cmd))
}

// classifySmoke is the single definition of "the objective is met". A caller
// that has just run the same command on the same tree (the deterministic
// pre-test, the QA gate's own rounds) folds its result in here instead of
// paying for a second identical run — which is what keeps the mid-run answer
// and the finish-path answer from disagreeing.
func classifySmoke(cmd string, sr quality.SmokeResult) objectiveGate {
	g := objectiveGate{Cmd: cmd, Ran: sr.Ran, Output: sr.Output, Dur: sr.Duration,
		Weak: quality.IsWeakQACommand(cmd)}
	switch {
	case !sr.Ran:
	case sr.OK:
		g.Green = true
	case qaLooksLikeNoTests(sr.Output):
		g.NoTests = true
	}
	return g
}

// How often ONE run may spend the objective command on the mid-run "are we
// already done?" question.
//
// THIS USED TO BE A COUNT OF TWO, AND THE COUNT WAS THE BUG. The reasoning was
// sound for the finish path it was written against — one probe after the board
// drains, one after the corrective wave — but between-waves probing arrives
// much earlier, and the budget is charged for RED answers. So both probes were
// spent on the first two waves, which are the two moments in a run when "not
// yet" is most certain and least worth paying to learn. From wave three the run
// was blind: if the implementation landed at wave five, nothing would ever ask
// again, and the run burned its whole ceiling with nothing actually wrong.
// Measured: `fix-a-bug` hit its 20-minute ceiling in 1 run of 3 with
// failed_tasks == 0.
//
// A count also cannot be right for two projects at once. Two probes is
// miserly for a 200ms unit suite and profligate for a 6-minute integration
// suite; the same number means opposite things.
//
// So the bound is economic instead. Asking costs a measured T; a green answer
// saves the whole runway that is left. Ask while
//
//	runway >= probePayoffRatio * T
//
// which is the break-even of P(green) * (runway - T) > T at P ≈ 1/5, rounded to
// a ratio that stays conservative when the estimate is off. The effect is that
// a cheap gate is asked on every wave that wrote something, an expensive one
// only while there is enough time left for the answer to pay for itself, and
// neither number had to be guessed per project.
const probePayoffRatio = 6

// maxProbesWithoutDeadline bounds probing when the run's context carries no
// wall deadline — a library caller, a test, `--budget` unset. With no runway
// there is no economics to do, so this falls back to a count. Eight rather than
// two: the whole point of the change above is that the early probes are the
// least informative ones, and that is just as true without a deadline.
const maxProbesWithoutDeadline = 8

// maxProbesAbsolute is a backstop against pathology — a gate that reports zero
// duration forever, a clock that does not advance — not a budget anyone should
// reach. The economic rule is what does the real work; if this ever binds, the
// duration measurement is broken and that is the bug to fix.
const maxProbesAbsolute = 64

// objectiveProbeState is the per-run bookkeeping behind the probe budget.
//
// PER RUN, NOT PER BOARD, and that is the whole reason the old fixed ceiling of
// two was so damaging: a single run drives RunBoard repeatedly — once per
// corrective round the finish path schedules — so two probes was two for the
// entire run, not two per board. A long run spent them both inside its first
// board and never asked again.
type objectiveProbeState struct {
	// spent counts the probes this run has PAID FOR, i.e. the ones that ran the
	// objective command. A probe handed an already-run result costs nothing and
	// is not charged — see probeObjective.
	spent int
	// cost is the longest probe this run has observed, and the estimate the
	// affordability rule prices the next ask against.
	//
	// The MAXIMUM rather than the mean, because the two are wrong in different
	// directions and only one of them is safe. A test suite gets slower as the
	// tree grows, and a mean over early cheap runs would keep saying "affordable"
	// while the real cost climbed past the runway. Over-estimating costs a probe
	// that would have fit; under-estimating costs the run its remaining budget.
	cost time.Duration
	// lastFiles fingerprints the write evidence the last probe judged. Asking
	// the same question of the same tree cannot give a different answer, so a
	// probe is only worth spending once something has been written since.
	lastFiles string
	// met is the green gate a BETWEEN-WAVES probe established, kept so the
	// finish path can act on the answer it already has instead of paying for
	// the same one twice. Nil until such a probe returns green.
	met *objectiveGate
	// testerRejected mirrors the run's live verification verdict, so the
	// between-waves probe honors the same refusal the post-drain probe is
	// handed as a parameter.
	testerRejected bool
	// finishedEarly is set when the run ENDS on the objective gate itself, i.e.
	// finishObjectiveMet was reached with a strong green command. completeRun
	// reads it as the license to report success over an escalation — nothing
	// else in the harness gets that license, and a run that took the ordinary
	// route to the finish line never sets it.
	finishedEarly bool
}

// resetObjectiveProbes clears the per-run early-finish bookkeeping.
func (o *Orchestrator) resetObjectiveProbes() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.objective = objectiveProbeState{}
}

// objectiveProbesSpent reports how many probes this run has used.
func (o *Orchestrator) objectiveProbesSpent() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.objective.spent
}

// noteTesterRejected records the run's current verification verdict.
//
// The post-drain probe receives it as an argument because its caller has just
// computed it. The between-waves probe has no such caller — it is driven by the
// board loop — so the verdict has to be readable from the run instead. One
// rule, two ways of reaching it.
func (o *Orchestrator) noteTesterRejected(rejected bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.objective.testerRejected = rejected
	o.mu.Unlock()
}

// noteObjectiveEarlyFinish records that the run is ending on the objective gate
// rather than on the ordinary finish path.
//
// It only counts a gate that RAN, is Green and is not Weak — the same three
// conditions probeObjective requires before it may end a run — so the flag
// completeRun reads can never be stronger than the measurement behind it.
func (o *Orchestrator) noteObjectiveEarlyFinish(g objectiveGate) {
	if o == nil || !g.Ran || !g.Green || g.Weak {
		return
	}
	o.mu.Lock()
	o.objective.finishedEarly = true
	o.mu.Unlock()
}

// finishedOnObjectiveGate reports whether this run is ending because the strong
// objective gate was already green.
func (o *Orchestrator) finishedOnObjectiveGate() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.objective.finishedEarly
}

// testerRejectedNow reports the run's current verification verdict.
func (o *Orchestrator) testerRejectedNow() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.objective.testerRejected
}

// objectiveMetEarly returns the green gate a between-waves probe established,
// if one did.
//
// The finish path consumes this instead of re-running the objective command:
// RunBoard returns the moment the probe says green, so nothing writes to the
// tree between the two points and a second run could only re-prove the answer —
// at the price of a whole test suite, and of a probe budget the finish path may
// need for a later question.
func (o *Orchestrator) objectiveMetEarly() (objectiveGate, bool) {
	if o == nil {
		return objectiveGate{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.objective.met == nil {
		return objectiveGate{}, false
	}
	return *o.objective.met, true
}

// objectiveBlocker names the required work that forbids finishing early, or ""
// when nothing does.
//
// These are the two ways a run still owes something no green command can pay
// off: agents have work in the pipe, or verification turned the work down. A
// green test suite is evidence about the CODE; it says nothing about a board
// that is still moving, and it cannot overrule the tester.
//
// TESTER REJECTION IS NOT NEGOTIABLE. The tester is the harness's own
// verification role, and the objective gate is the thing it is supposed to be
// checking. Letting the gate clear its own examiner is circular, so a rejection
// refuses here however green the command is.
//
// AN ESCALATION IS NOT ONE OF THESE, and used to be. A task escalates BECAUSE
// reviews kept failing — precisely the run that is burning its budget, and
// therefore precisely the run this probe exists to save. The board is a
// planner's guess at a decomposition, made before anyone knew the objective was
// reachable; the gate is the acceptance criterion the user actually stated,
// measured against the tree that exists. When the guess and the measurement
// disagree, the measurement wins — the same argument probeBetweenWaves already
// makes for abandoning ready_to_dev tasks. Live measurement: three runs of the
// same fixture, same model, same 20-minute ceiling; the only run that finished
// was the one with failed_tasks == 0, and the other two ran to the ceiling and
// reported `context deadline exceeded`.
//
// What an escalation still is, is a signal that a human should look. So it is
// REPORTED rather than obeyed: see unfinishedForReview and escalationNotice,
// which put the count in the stop event, the finish event and the Result
// summary, and completeRun, which keeps such a run out of a bare success.
func objectiveBlocker(board *plan.Board, testerRejected bool) string {
	switch {
	case board == nil:
		return "no board"
	case board.AgentWorkRemaining():
		return "agent work remains"
	case testerRejected:
		return "tester rejected"
	}
	return ""
}

// betweenWavesBlocker is objectiveBlocker for a board that is still moving.
//
// It relaxes exactly ONE further refusal — "agent work remains" — because
// between waves that is true by construction: ready_to_dev tasks are precisely
// what the probe is deciding the fate of. See the note on probeBetweenWaves for
// why abandoning them is defensible.
//
// It tightens one in exchange. A task sitting in in_progress or in_review is
// work whose output nobody has judged yet, and possibly a file mid-write; the
// board is not at rest and the tree the gate just measured is not the tree the
// run will end with. RunBoard normally leaves neither column occupied between
// waves, so this costs nothing in the ordinary case and refuses in the odd one.
//
// The tester rejection carries over unchanged, for objectiveBlocker's reason:
// the gate does not get to clear the role that is checking it.
func betweenWavesBlocker(board *plan.Board, testerRejected bool) string {
	switch {
	case board == nil:
		return "no board"
	case testerRejected:
		return "tester rejected"
	case taskInFlight(board):
		return "task still in flight"
	}
	return ""
}

// taskInFlight reports whether any task is mid-execution or mid-review.
func taskInFlight(board *plan.Board) bool {
	if board == nil {
		return false
	}
	for _, t := range board.AllTasks() {
		t.Normalize()
		if t.Column == plan.ColInProgress || t.Column == plan.ColInReview {
			return true
		}
	}
	return false
}

// testPhaseIsOperatorConfigured reports whether pipeline.yaml puts a
// user-authored agent on the test phase (before / replace / after).
func (o *Orchestrator) testPhaseIsOperatorConfigured() bool {
	p := o.Pipeline()
	for _, pos := range []string{"before", "replace", "after"} {
		if len(p.SlotsAt("test", pos)) > 0 {
			return true
		}
	}
	return false
}

// objectiveAlreadyMet answers "is the objective satisfied already, so that the
// corrective wave about to be scheduled cannot add anything?"
//
// FREQUENCY RULE — the gate runs a real command (minutes of GPU/CPU on a local
// SLM run), so a probe is spent only where its answer can change what the
// harness does next, and only when there is something new to judge:
//
//  1. the board must have no ready agent work left, so a corrective wave really
//     is the next thing the loop would spend;
//  2. the run must have written something — with no write evidence there is no
//     implementation that could already be correct;
//  3. the write set must have changed since the last probe, because re-asking
//     the same question of the same tree cannot give a different answer;
//  4. asking must be affordable — the runway left has to be worth several times
//     what the last ask measurably cost (probeAffordableLocked);
//  5. `reuse` lets a caller that has JUST run the same command on the same tree
//     (the deterministic pre-test) hand its result over, so the common case
//     costs zero extra command runs.
//
// The returned gate is Green only for a STRONG gate. A weak syntax-only command
// is refused before it is even run: compileall proves the file parses, not that
// the objective is met, and it must never end a run early.
func (o *Orchestrator) objectiveAlreadyMet(ctx context.Context, board *plan.Board,
	testerRejected bool, reuse *quality.SmokeResult) (objectiveGate, bool) {

	return o.probeObjective(ctx, probeAfterDrain, board, testerRejected, reuse)
}

// objectiveProbePoint names where a probe is being taken from, which decides
// which "this run still owes work" rule applies to it.
type objectiveProbePoint string

const (
	// probeAfterDrain is the finish path: the execute board has no ready agent
	// work left, so a corrective wave really is the next thing the loop would
	// spend. objectiveBlocker applies unchanged.
	probeAfterDrain objectiveProbePoint = "after drain"
	// probeBetweenWaves is the board loop at rest between two waves — the point
	// where the ~15 minutes the motivating run wasted were actually being
	// spent. Ready tasks remain by construction, so betweenWavesBlocker applies
	// instead.
	//
	// IS IT SAFE TO STOP WITH ready_to_dev TASKS ON THE BOARD? Yes, and the
	// argument is that those tasks are a MEANS, not the end. The board was
	// planned before anyone knew whether the objective was reachable, by a
	// planner guessing at the decomposition; the objective gate is the
	// acceptance criterion the user actually stated, evaluated by running their
	// command against the tree that exists. When the two disagree, the
	// measurement wins over the guess. What is genuinely abandoned is real:
	// planned work that might have improved structure, coverage or docs the
	// gate does not measure. So the run does not pretend otherwise — the stop
	// is announced with a count, Result.UnexecutedTasks carries it, and the
	// summary says N tasks were not executed. Nothing here is silent.
	//
	// An ESCALATED task is a planning artifact by the same argument, and is
	// treated the same way: reported, not obeyed. See objectiveBlocker.
	probeBetweenWaves objectiveProbePoint = "between waves"
)

// probeObjective is the shared body of both probes. Only the "still owes work"
// rule and the bookkeeping of a green answer differ by point.
func (o *Orchestrator) probeObjective(ctx context.Context, point objectiveProbePoint,
	board *plan.Board, testerRejected bool, reuse *quality.SmokeResult) (objectiveGate, bool) {

	var g objectiveGate
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		// The objective gate IS the QA gate. With qa_gate off the harness has no
		// configured notion of "done" to check against.
		return g, false
	}
	blocked := objectiveBlocker(board, testerRejected)
	if point == probeBetweenWaves {
		blocked = betweenWavesBlocker(board, testerRejected)
	}
	if blocked != "" {
		return g, false
	}
	if o.testPhaseIsOperatorConfigured() {
		// The operator wired their own agent into the test phase. That is an
		// explicit instruction about how this project verifies itself, and a
		// cost optimization does not get to silently route around it.
		return g, false
	}
	cmd := o.qaCommand()
	if strings.TrimSpace(cmd) == "" || quality.IsWeakQACommand(cmd) {
		return objectiveGate{Cmd: cmd, Weak: true}, false
	}
	changed := o.changedFilesSnapshot()
	if len(changed) == 0 {
		return g, false
	}
	fingerprint := strings.Join(changed, "\n")
	reused := reuse != nil && reuse.Ran && strings.TrimSpace(reuse.Command) == strings.TrimSpace(cmd)

	o.mu.Lock()
	// Affordability and the fingerprint both exist to stop the run PAYING for an
	// answer, and a reused result has already been paid for by someone else.
	// Refusing it buys nothing and costs a whole verification phase — which is
	// exactly what would happen now that between-waves probes can arrive at the
	// finish line having spent the budget on a tree that has since changed.
	if !reused {
		if o.objective.lastFiles == fingerprint || !o.probeAffordableLocked(ctx) {
			o.mu.Unlock()
			return g, false
		}
		o.objective.spent++
	}
	o.objective.lastFiles = fingerprint
	o.mu.Unlock()

	if reused {
		g = classifySmoke(cmd, *reuse)
	} else {
		g = o.evalObjectiveGate(ctx, cmd)
	}
	// Price the command from BOTH paths. A reused result was paid for by someone
	// else, but it timed the same command on the same tree, so it is exactly as
	// good a price — and taking it free is what lets a run that has already run
	// its pre-test know the cost before it spends a probe of its own.
	o.noteProbeCost(g.Dur)
	// Green already excludes the "no test files" escape hatch: a toolchain that
	// found nothing to run has verified nothing, so it cannot end a run early
	// either. Weak is re-checked because qaCommand() is what was classified.
	if !g.Ran || !g.Green || g.Weak {
		return g, false
	}
	if point == probeBetweenWaves {
		// Carry the answer to the finish path. RunBoard returns the moment this
		// returns true, so nothing writes to the tree in between and this gate
		// is still the truth when finalizeAfterExecute reads it.
		met := g
		o.mu.Lock()
		o.objective.met = &met
		o.mu.Unlock()
	}
	return g, true
}

// probeAffordableLocked decides whether asking "are we done?" is worth what
// asking costs. o.mu must be held.
//
// THE FIRST ASK IS ALWAYS AFFORDABLE. Nothing has priced this project's gate
// yet, and the only way to learn what it costs is to run it once — refusing on
// an unknown cost would make the budget unreachable rather than careful. Every
// later ask is priced against the runway the run actually has left, so the rule
// tightens by itself as the ceiling approaches and needs no per-project tuning.
//
// THE WORST CASE IS THEREFORE ONE OVERSPENT PROBE, and it is worth stating
// plainly: a project whose objective command takes longer than runway/ratio
// pays for exactly one full run of it before the measurement comes back and
// every later ask is refused. That is a real cost on a slow suite, and it is
// deliberately not defended against by capping the first probe's timeout — a
// capped probe that gets killed reports a DURATION EQUAL TO ITS CAP, which
// under-prices the command it just failed to finish, and under-pricing is the
// one direction of error that lets the rule keep spending. One bounded
// overspend beats a cheap-looking estimate that is wrong forever.
//
// It is also why noteProbeCost is fed by reused results: a run whose pre-test
// already executed this command arrives here with the price already known and
// never spends that first probe blind.
func (o *Orchestrator) probeAffordableLocked(ctx context.Context) bool {
	if o.objective.spent >= maxProbesAbsolute {
		return false
	}
	est := o.objective.cost
	if est <= 0 {
		// Never priced: this ask is what prices it.
		return true
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		// No runway to reason about — fall back to a count.
		return o.objective.spent < maxProbesWithoutDeadline
	}
	// Nothing to save and nothing to spend it with: past the deadline the run
	// is over however green the tree is.
	runway := time.Until(deadline)
	return runway >= est*probePayoffRatio
}

// noteProbeCost records what the last probe cost, so the next one can be priced.
//
// A probe that ran but reported no duration is recorded at a 1ms floor rather
// than left at zero. Zero would read as "never priced", which is the one state
// that bypasses the economics entirely — a gate whose timing is broken would
// get unlimited probes instead of the bounded ones a genuinely instant command
// deserves. The floor keeps it in the priced-and-cheap case, where a real
// instant command belongs anyway.
func (o *Orchestrator) noteProbeCost(d time.Duration) {
	if o == nil {
		return
	}
	if d <= 0 {
		d = time.Millisecond
	}
	o.mu.Lock()
	if d > o.objective.cost {
		o.objective.cost = d
	}
	o.mu.Unlock()
}

// objectiveMetBetweenWaves is loop.Runner's BetweenWaves hook: it asks the
// objective gate, at the one moment the board is at rest, whether the run is
// already done. Every refusal is decided here, on the orchestrator's side of
// the seam; the loop only acts on the answer.
//
// The reason is what loop.Runner.stopBetweenWaves puts in its stop event, so
// the escalation notice is attached HERE: the operator watching the stream sees
// the same sentence the Result summary will carry.
func (o *Orchestrator) objectiveMetBetweenWaves(ctx context.Context, board *plan.Board) (bool, string) {
	g, done := o.probeObjective(ctx, probeBetweenWaves, board, o.testerRejectedNow(), nil)
	if !done {
		return false, ""
	}
	reason := fmt.Sprintf("objective already met (%s green)", g.Cmd)
	if notice := escalationNotice(unfinishedForReview(board)); notice != "" {
		reason += " — " + notice
	}
	return true, reason
}

// runQAGate iterates a project test/smoke command until green or max rounds.
// On failure it asks the tester/corrector specialists to fix, then re-runs.
// Returns true when the gate ends red (caller should rewrite plan/tasks).
func (o *Orchestrator) runQAGate(ctx context.Context, query string, board *plan.Board) bool {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return false
	}
	cmd := o.qaCommand()
	if cmd == "" {
		o.emitWarn("test", "qa_gate: no auto test/smoke command — set qa_gate_command", "")
		return false
	}
	// qaGateRounds floors config's shipped default of 1: with max==1,
	// `round == max` was true on the FIRST iteration, so the gate annotated the
	// board and returned before the tester-diagnose and corrector-fix blocks
	// ever ran — the documented "iterates until green" repair loop was
	// unreachable under the shipped default.
	max := o.qaGateRounds()

	o.runRegressionChecks(ctx, "pre-gate")

	o.runQABootstrap(ctx, cmd)

	stalled := false
	for round := 1; round <= max; round++ {
		if ctx.Err() != nil {
			return o.qaGateNoVerdict(cmd)
		}
		o.qaPreflight(ctx, round, cmd)

		o.emitFull("test", stream.KindAgentStart, "qa", "",
			fmt.Sprintf("qa_gate %d/%d: %s", round, max, cmd), "", "")
		sr := o.runSmoke(ctx, cmd)
		// classifySmoke, not a second opinion: the finish path and the mid-run
		// early-finish check must agree about what green means.
		v := classifySmoke(cmd, sr)
		// Price the command here too. This is the same command the objective
		// probe spends, so a gate round is a free measurement of it, and the
		// probe budget should not have to rediscover it.
		o.noteProbeCost(v.Dur)

		if v.NoTests && round == 1 {
			o.emitWarn("test", "qa_gate: no test files found — skipping gate (code compiles)", "")
			o.recordGate("qa_gate", true, "no tests to run")
			return false
		}
		if v.Green {
			_ = o.store.Append(contextstore.DocScratch, "QA gate",
				fmt.Sprintf("GREEN round %d\n\n%s", round, truncate(sr.Output, 2000)))
			o.emitFullL("test", stream.KindAgentEnd, "qa", "", "qa_gate green", "",
				truncate(sr.Output, 800), stream.LevelSuccess)
			o.recordGate("qa_gate", true, cmd)
			o.runRegressionChecks(ctx, "post-gate")
			return false
		}

		failText := strings.TrimSpace(sr.Output + "\n" + sr.Summary)
		// FailureExcerpt, never a head cut: every runner this harness drives
		// prints its verdict LAST, so head-only truncation handed the reader
		// collection noise with the assertion removed.
		_ = o.store.Append(contextstore.DocScratch, "QA gate failure",
			fmt.Sprintf("round %d/%d\ncmd: %s\n\n%s", round, max, cmd,
				quality.FailureExcerpt(failText, 4000)))
		o.emitFullL("test", stream.KindOutput, "qa", "",
			fmt.Sprintf("qa_gate failed round %d/%d", round, max), "",
			quality.FailureExcerpt(failText, 1500), stream.LevelError)

		// The fix pass now runs on EVERY round, the last one included. It used
		// to be skipped whenever round == max, which with max==1 meant always.
		//
		// Whether it WROTE anything is the difference between a repair loop and
		// a treadmill: re-running the same command against a tree nothing
		// touched cannot give a different answer, and on a slow suite that is
		// minutes per round spent proving what the previous round proved. The
		// objective probe has refused exactly this since it was written (see
		// probeObjective's fingerprint rule); the gate never learned it.
		before := o.changedFingerprint()
		o.qaDiagnoseAndFix(ctx, query, cmd, failText)
		if o.changedFingerprint() == before {
			o.emitWarn("test", fmt.Sprintf(
				"qa_gate: the fix pass wrote nothing in round %d/%d — stopping "+
					"rather than re-running %s against an unchanged tree", round, max, cmd), "")
			stalled = true
			break
		}
	}

	// One final verification of the last fix pass before declaring red — and
	// only when a fix pass actually changed something to verify.
	if ctx.Err() == nil && !stalled {
		final := o.runSmoke(ctx, cmd)
		o.noteProbeCost(final.Duration)
		if classifySmoke(cmd, final).Green {
			_ = o.store.Append(contextstore.DocScratch, "QA gate",
				"GREEN after final fix pass\n\n"+truncate(final.Output, 2000))
			o.emitFullL("test", stream.KindAgentEnd, "qa", "", "qa_gate green after final fix pass", "",
				truncate(final.Output, 800), stream.LevelSuccess)
			o.recordGate("qa_gate", true, cmd)
			o.runRegressionChecks(ctx, "post-gate")
			return false
		}
	}

	if ctx.Err() != nil {
		// The gate ran out of RUN, not out of rounds. Everything below is a
		// verdict — an event the operator reads, a recorded gate result, and a
		// board annotation — and none of it is true of a canceled run.
		return o.qaGateNoVerdict(cmd)
	}
	o.emitFullL("test", stream.KindAgentEnd, "qa", "",
		fmt.Sprintf("qa_gate still red after %d rounds", max), "", "", stream.LevelError)
	o.recordGate("qa_gate", false, cmd)
	if board != nil {
		for i := range board.Tasks {
			if board.Tasks[i].Column == plan.ColDone {
				board.Tasks[i].Notes = strings.TrimSpace(
					board.Tasks[i].Notes + "\nQA gate still failing: " + cmd)
				break
			}
		}
		o.persistBoard(board)
	}
	return true
}

// qaGateNoVerdict ends the gate because the RUN ended, and reports no verdict.
//
// It returns false — "the gate did not fail" — which is the only honest of the
// two answers a bool allows, and the consequential one. `true` flows straight
// into finalizeAfterExecute, which sets TesterRejected and feeds the board a
// SYNTHESIZED tester verdict (`{"passed":false,...,"qa_gate red"}`) through
// applyTesterFeedback — a planner call. So a user pressing Ctrl-C used to spend
// model calls on the way out and leave "QA gate still failing" annotated on a
// task, describing a gate that was never allowed to finish.
//
// Nothing is recorded: no recordGate, no board annotation. A canceled gate has
// not established that the project is green either, and the absence of a
// verdict is exactly what the ledger should show.
func (o *Orchestrator) qaGateNoVerdict(cmd string) bool {
	o.emitWarn("test", "qa_gate: run ended before the gate finished — no verdict recorded ("+cmd+")", "")
	return false
}

// changedFingerprint identifies the set of files the run has written so far.
// Two equal fingerprints mean no write happened in between, so any command that
// was already run against the tree would answer the same way again.
func (o *Orchestrator) changedFingerprint() string {
	return strings.Join(o.changedFilesSnapshot(), "\n")
}

// qaCommand resolves the project's test/smoke command.
func (o *Orchestrator) qaCommand() string {
	if o == nil || o.cfg == nil {
		return ""
	}
	cmd := strings.TrimSpace(o.cfg.QAGateCommand)
	if cmd != "" {
		return cmd
	}
	if cmd = blocks.ResolveQAGateCommand(o.cfg.Root, o.cfg.Root, o.cfg.ActivePack); cmd != "" {
		return cmd
	}
	return quality.DetectProjectCommandWithPack(o.cfg.Root, o.cfg.ActivePack)
}

// qaPreflight runs the cheap deterministic fixes before round 1.
func (o *Orchestrator) qaPreflight(ctx context.Context, round int, cmd string) {
	if round != 1 {
		return
	}
	o.formatWaveChanges(ctx)
	if !strings.Contains(cmd, "go test") {
		return
	}
	if _, err := os.Stat(filepath.Join(o.cfg.Root, "go.mod")); err != nil {
		return
	}
	br := o.runSmokeIn(ctx, "go build ./...", 30*time.Second)
	if !br.OK {
		o.emitWarn("test", "qa_gate: build failed — "+quality.FailureExcerpt(br.Output, 300), "")
		return
	}
	o.emit("test", "qa_gate: build OK, running full tests", "")
}

// formatWaveChanges formats the files THIS RUN changed, and nothing else.
//
// The pre-QA formatter used to run `gofmt -w .` / `goimports -w .` over the
// project root (quality.AutoFixFormatting, since deleted), so a repo that was not
// already gofmt-clean got an enormous unrelated diff attributed to the agent,
// with no checkpoint and no timeout. Its replacement is scoped to the changed
// set and snapshots every file first, so the pass stays undoable.
func (o *Orchestrator) formatWaveChanges(ctx context.Context) {
	if o == nil || o.cfg == nil {
		return
	}
	changed := o.changedFilesSnapshot()
	if len(changed) == 0 {
		return
	}
	var snapshot func(string)
	if o.workspace != nil && o.workspace.Checkpointer != nil {
		snapshot = o.workspace.Checkpointer.BackupIfNeeded
	}
	fixOut := quality.FormatChangedFiles(ctx, quality.FormatRequest{
		Root:  o.cfg.Root,
		Files: changed,
		// goimports stays opt-in: it rewrites the import block from the file it
		// can see and will delete an import only a build-tagged sibling needs.
		Goimports: false,
		Timeout:   quality.DefaultFormatTimeout,
		Snapshot:  snapshot,
	})
	if fixOut != "" {
		o.emit("test", "qa_gate: formatted changed files: "+truncate(fixOut, 200), "")
	}
}

// runQABootstrap applies the qa_bootstrap policy to the dependency install the
// QA command implies.
//
// BootstrapDeps proposes `pip install` / `npm install` / `go mod tidy` derived
// from an AGENT-AUTHORED manifest, which is arbitrary code execution from model
// output. quality.PlanBootstrap states the decision explicitly instead of
// assuming consent: off refuses and says so, ask routes the command through the
// ws_shell permission layer (shell mode, whitelist, approval flow — the same
// HITL every other command gets), auto runs it unattended.
func (o *Orchestrator) runQABootstrap(ctx context.Context, cmd string) {
	if o == nil || o.cfg == nil {
		return
	}
	policy := quality.NormalizeBootstrapPolicy(o.QABootstrapMode())
	bp := quality.PlanBootstrap(o.cfg.Root, cmd, policy)
	if bp.Command == "" {
		if bp.Reason != "" {
			// policy=off with a real candidate: say what was skipped, or the
			// run ends in "it just did not install anything" with no trace.
			o.emitWarn("test", "qa_gate bootstrap: "+truncate(bp.Reason, 200), "")
		}
		return
	}
	o.emit("test", "qa_gate bootstrap: "+truncate(bp.Command, 120)+
		" (policy="+string(bp.Policy)+")", "")

	var sr quality.SmokeResult
	if bp.NeedsApproval {
		o.emitWarn("test", truncate(bp.Reason, 240), "")
		sr = o.runGatedCommand(ctx, bp.Command, "qa bootstrap")
	} else {
		// policy=auto: the operator opted in explicitly, so it runs unattended,
		// exactly as quality.RunAcceptanceSmokeWithPolicy does for Run plans.
		sr = o.runSmoke(ctx, bp.Command)
	}
	_ = o.store.Append(contextstore.DocScratch, "QA bootstrap",
		fmt.Sprintf("cmd: %s\npolicy: %s\nran=%v ok=%v\n\n%s",
			bp.Command, bp.Policy, sr.Ran, sr.OK, quality.FailureExcerpt(sr.Output, 2000)))
	if !sr.OK {
		o.emitFullL("test", stream.KindOutput, "qa", "", "qa_gate bootstrap warning", "",
			quality.FailureExcerpt(sr.Output, 800), stream.LevelWarn)
	}
}

// qaDiagnoseAndFix runs the tester (diagnose) then the corrector (fix).
func (o *Orchestrator) qaDiagnoseAndFix(ctx context.Context, query, cmd, failText string) {
	o.emitAgent("test", plan.RoleTester, "", "qa_gate diagnose failures", "", "")
	testPack, _ := o.packBuild("tester", query, contextstore.DefaultDocsForRole("tester"), nil,
		o.skillPackFor("tester", query))
	diag, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
		"\n## QA gate failure\nCommand: "+cmd+"\n\n"+quality.FailureExcerpt(failText, 6000)+
		"\n\n"+o.langHint()+"\n\nDiagnose with ws_shell if helpful. List concrete file edits needed. "+
		"Return JSON with status and issues.")
	if strings.TrimSpace(diag) != "" {
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "qa diagnose", "", truncate(diag, 1000))
	}

	o.emitAgent("test", plan.RoleCorrector, "", "qa_gate fix iteration", "", "")
	fixPack, _ := o.packBuild("corrector", query, contextstore.DefaultDocsForRole("corrector"), nil,
		o.skillPackFor("corrector", query))
	fixPrompt := fixPack.Render() +
		"\n## Goal\nMake this command pass: `" + cmd + "`\n\n" +
		o.langHint() + "\n\n## Failure output\n" +
		quality.FailureExcerpt(failText, 5000) +
		"\n\n## Diagnosis\n" + truncate(diag, 3000) +
		"\n\nUse ws_edit / ws_patch / ws_write for SMALL fixes. Then return STRICT JSON status."
	fixOut, _ := o.runRoleTracked(ctx, plan.RoleCorrector, "", fixPrompt)
	if strings.TrimSpace(fixOut) != "" {
		o.emitFull("test", stream.KindOutput, plan.RoleCorrector, "", "qa fix output", "", truncate(fixOut, 1000))
	}
}

// runGatedCommand executes a command through the registered ws_shell tool, so
// it inherits the whole permission layer: shell mode (allow/ask/deny), the
// whitelist gate, the write guard and the approval flow. Falls back to a direct
// smoke run only when the tool is not registered (tests, bare orchestrators).
func (o *Orchestrator) runGatedCommand(ctx context.Context, cmd, label string) quality.SmokeResult {
	if strings.TrimSpace(cmd) == "" {
		return quality.SmokeResult{OK: true}
	}
	if o.tools != nil {
		if tool, ok := o.tools.GetTool("ws_shell"); ok {
			args, _ := json.Marshal(map[string]any{"command": cmd})
			out, err := tool.Execute(taskContext(ctx, "qa"), string(args))
			text := fmt.Sprintf("%v", out)
			if err != nil {
				return quality.SmokeResult{Ran: true, Command: cmd, Output: text, Summary: err.Error()}
			}
			// ws_shell answers refusals in prose rather than as an error.
			if refused := shellRefusal(text); refused != "" {
				o.emitWarn("test", label+" not permitted: "+truncate(refused, 200), "")
				return quality.SmokeResult{Ran: false, Command: cmd, Output: text, Summary: refused}
			}
			return quality.SmokeResult{OK: true, Ran: true, Command: cmd, Output: text}
		}
	}
	return o.runSmoke(ctx, cmd)
}

// shellRefusal detects the permission layer's prose refusals.
func shellRefusal(out string) string {
	lower := strings.ToLower(out)
	for _, marker := range []string{
		"shell denied by permission mode",
		"shell denied by user",
		"shell approval unavailable",
		"not in the allowed command list",
		"refused:",
	} {
		if strings.Contains(lower, marker) {
			return firstSentence(out)
		}
	}
	return ""
}

// runRegressionChecks replays what previous runs already fixed.
//
// This is what makes "fail once, then never again" real: evolve stores a cheap
// re-check for every failure it saw resolved, and a check that starts failing
// again means a regression, not a new bug. Command checks go through the
// permission layer (evolve deliberately never executes anything itself); the
// file assertions are safe and run offline.
func (o *Orchestrator) runRegressionChecks(ctx context.Context, when string) {
	if o == nil || o.evolve == nil || o.cfg == nil || !o.regressionChecksEnabled() {
		return
	}
	regs := o.evolve.Regressions()
	if regs == nil {
		return
	}

	offline := regs.RunOffline(o.cfg.Root)
	failed := 0
	for _, r := range offline {
		if !r.OK {
			failed++
		}
	}
	if n := len(offline); n > 0 {
		level := stream.LevelSuccess
		msg := fmt.Sprintf("regressions %s: %d/%d file checks pass", when, n-failed, n)
		if failed > 0 {
			level = stream.LevelProblem
		}
		o.emitFullL("test", stream.KindOutput, "regressions", "", msg, "", "", level)
		o.recordGate("regressions_offline", failed == 0, msg)
	}

	for _, chk := range regs.Runnable() {
		if err := ctx.Err(); err != nil {
			return
		}
		cmd := strings.TrimSpace(chk.Command)
		if cmd == "" {
			continue
		}
		sr := o.runGatedCommand(ctx, cmd, "regression check")
		if !sr.Ran {
			// Refused by the permission layer — not evidence either way.
			continue
		}
		regs.Record(chk.ID, sr.OK)
		if !sr.OK {
			o.emitFullL("test", stream.KindIntervention, "regressions", "",
				"regression returned: "+truncate(cmd, 120), quality.InterventionReview,
				quality.FailureExcerpt(sr.Output, 800), stream.LevelProblem)
			o.recordGate("regression:"+chk.ID, false, cmd)
		}
	}
}

// detectQACommand is kept for tests; delegates to quality.DetectProjectCommand.
func detectQACommand(root string) string {
	return quality.DetectProjectCommand(root)
}

// bootstrapQADeps reports the install command a QA command WOULD imply.
//
// It grants no permission and is not the production path — runQABootstrap is,
// and it goes through quality.PlanBootstrap so the qa_bootstrap policy decides.
// Kept for the detection tests, which assert what a project shape implies.
func bootstrapQADeps(root, cmd string) string {
	return quality.BootstrapDeps(root, cmd)
}

// shellNoticeProbe is one claim the operator-facing notice makes, paired with
// the command that PROVES it. The claim is only printed when the workspace
// package actually behaves that way for the sample.
type shellNoticeProbe struct {
	label  string
	sample string
}

// shellRefusedProbes name the classes ws_shell refuses under the whitelist.
//
// The notice used to be a hand-written sentence — "python, node, make, npx,
// `go run`, cp, mv and sed" — and it went stale the moment the shell guard
// grew the exec-flag and out-of-jail audits (`env <prog>`, `find -exec`,
// `go test -exec`, `go generate`, `cmake -P`, `mkdir` outside the root). An
// operator reading it concluded those were allowed. Each entry below is
// verified against workspace.GuardShellWhitelist at build time, so a refusal
// that changes shape drops out of the notice instead of lying in it.
var shellRefusedProbes = []shellNoticeProbe{
	{"interpreters (python, node, npx, make, `go run`)", "python script.py"},
	{"file movers (cp, mv, sed -i)", "cp a.go b.go"},
	{"`env <prog>` (runs a program the whitelist never sees)", "env python -c 'print(1)'"},
	{"`find -exec/-execdir/-ok/-delete/-fprintf`", "find . -name '*.go' -exec rm {} ;"},
	{"`go test -exec` / `-toolexec` / `-vettool` / `-ldflags` / `-gcflags`", "go test -exec ./runner ./..."},
	{"`go generate` (runs directives the repository chose)", "go generate ./..."},
	{"`cmake -P` / `-E` / `-C` / `--install`", "cmake -P script.cmake"},
	{"`mkdir` / `touch` outside the project root", "mkdir /tmp/outside"},
}

// shellAllowedProbes name what still runs untouched, so the notice cannot read
// as "verification is blocked".
var shellAllowedProbes = []shellNoticeProbe{
	{"go test", "go test ./... -short"},
	{"go build", "go build ./..."},
	{"pytest", "pytest -q"},
	{"npm test", "npm test"},
	{"cargo test", "cargo test"},
}

// ShellWhitelistNotice is the operator-facing summary of what `ws_shell` in
// whitelist mode refuses. It is DERIVED from pkg/workspace rather than restated:
// every clause is a claim the shell guard is asked to confirm.
//
// Test and build runners remain auto-allowed, so the ordinary verification path
// is unaffected. What changes is the unattended dependency bootstrap:
// pkg/quality's BootstrapDeps proposes `pip install` / `npm install` /
// `go mod tidy` derived from an AGENT-AUTHORED manifest, and that routes
// through the permission layer like any other command instead of running on its
// own authority.
var ShellWhitelistNotice = buildShellWhitelistNotice()

func buildShellWhitelistNotice() string {
	var refused, allowed []string
	for _, p := range shellRefusedProbes {
		if _, blocked := workspace.GuardShellWhitelist(p.sample, nil); blocked {
			refused = append(refused, p.label)
		}
	}
	for _, p := range shellAllowedProbes {
		if _, blocked := workspace.GuardShellWhitelist(p.sample, nil); !blocked {
			allowed = append(allowed, p.label)
		}
	}
	var b strings.Builder
	b.WriteString("ws_shell whitelist is ON. Refused unless listed in shell_allow " +
		"(or SLMCODE_BASH_ALLOW): ")
	if len(refused) == 0 {
		b.WriteString("nothing — the guard reported no refusals")
	} else {
		b.WriteString(strings.Join(refused, "; "))
	}
	b.WriteString(".")
	if len(allowed) > 0 {
		b.WriteString(" Still allowed: " + strings.Join(allowed, ", ") + ".")
	}
	b.WriteString(" Dependency bootstrap (pip install / npm install / go mod tidy) " +
		"asks for approval instead of running unattended.")
	return b.String()
}

// emitShellPolicyNotice tells the operator, once per run, what the shell policy
// will and will not let the agents do. Silent policy is how a run ends in
// "it just did not install anything" with nothing in the log to explain it.
func (o *Orchestrator) emitShellPolicyNotice() {
	if o == nil || o.cfg == nil || !o.cfg.ShellWhitelist {
		return
	}
	msg := ShellWhitelistNotice
	if len(o.cfg.ShellAllow) > 0 {
		msg += " Currently allowed: " + strings.Join(o.cfg.ShellAllow, ", ") + "."
	}
	o.emitWarn("init", msg, "")
}
