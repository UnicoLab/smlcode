package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// eventLog collects every event an orchestrator emits so a test can assert on
// what the operator was actually told.
//
// It keeps the whole Event, not just its Message, so a test can single out one
// kind of announcement (see scoped) instead of grepping the entire stream.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) sink() EventHandler {
	return func(e Event) {
		l.mu.Lock()
		l.events = append(l.events, e)
		l.mu.Unlock()
	}
}

// attach registers the log as the orchestrator's event sink, through the same
// lock-guarded setter Studio uses.
func (l *eventLog) attach(o *Orchestrator) { o.OnEvent(l.sink()) }

func (l *eventLog) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	msgs := make([]string, 0, len(l.events))
	for _, e := range l.events {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "\n")
}

func (l *eventLog) contains(sub string) bool {
	return strings.Contains(l.all(), sub)
}

// scoped returns the messages of every event carrying the given scope.
func (l *eventLog) scoped(scope string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, e := range l.events {
		if e.Scope == scope {
			out = append(out, e.Message)
		}
	}
	return out
}

// headlessOrch builds an orchestrator with a real query turn on disk, so the
// retained-work hint has a run id and a directory to name.
func headlessOrch(t *testing.T, mutate func(*config.Config)) (*Orchestrator, *eventLog) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = plan.PlanApproveModeAsk
	cfg.PlanApproveTimeout = 50 * time.Millisecond
	if mutate != nil {
		mutate(cfg)
	}
	log := &eventLog{}
	o := &Orchestrator{cfg: cfg, onEvent: log.sink()}
	turn, err := session.BeginTurn(cfg.SlmDir(), "run-headless-test", "q")
	if err != nil {
		t.Fatalf("begin turn: %v", err)
	}
	o.currentTurn = turn
	return o, log
}

func plannedBoard() *plan.Board {
	return &plan.Board{
		QueryID: "run-headless-test",
		Query:   "q",
		Plan:    plan.Plan{Summary: "implement the median function"},
		Tasks: []plan.Task{
			{ID: "T1", Title: "add Median"},
			{ID: "T2", Title: "test Median"},
		},
	}
}

// TestHeadlessRunDoesNotDiscardCompletedPlanningWork is THE regression.
//
// Reproduced live: `slmcode run -v "…"` with stdout piped spent 9m17s on
// explore → compose → plan → split → scope-judge, produced a green scope judge
// and a valid two-task board, hit the plan gate, resolved it to "not approved"
// instantly (no TTY, plan_approve=ask, --on-gate-timeout=stop), and wrote zero
// lines of code. Nine minutes of compute, discarded, with one terse line of
// explanation.
//
// A headless run on the DEFAULT gate policy must never reach that state: it
// either refuses at the door (before a single model call) or it approves and
// executes. "Plan everything, then stop with nothing" is not one of the
// outcomes any more.
func TestHeadlessRunDoesNotDiscardCompletedPlanningWork(t *testing.T) {
	o, log := headlessOrch(t, nil)
	// No TTY, and the operator did not choose a policy: the exact live repro.
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: false})

	// 1. The run is allowed to start (nothing to refuse), and says what it
	//    decided BEFORE any model call.
	decisions, err := o.GatePreflight()
	if err != nil {
		t.Fatalf("a default headless run must be allowed to start: %v", err)
	}
	if !hasGateDecision(decisions, "plan", "auto-approve") {
		t.Fatalf("no plan-gate decision announced at run start: %+v", decisions)
	}

	// 2. The gate that used to throw the work away now approves it.
	board := plannedBoard()
	ok, gerr := o.runPlanApprovalGate(context.Background(), "q", board)
	if gerr != nil {
		t.Fatalf("headless plan gate returned an error instead of executing: %v", gerr)
	}
	if !ok {
		t.Fatal("headless plan gate did not approve — the run would plan for minutes and then discard it")
	}
	if !log.contains("auto-approving plan gate") {
		t.Fatalf("the decision was not logged:\n%s", log.all())
	}
	if log.contains("plan not approved") {
		t.Fatalf("the discard path was reached:\n%s", log.all())
	}
}

// A headless run with no explicit flag approves the plan gate AND says so.
func TestHeadlessPlanGateAutoApprovesAndLogsTheDecision(t *testing.T) {
	o, log := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: true})
	// The handler a CLI would register must never be consulted: the engine
	// decides first, so nothing can answer "no" on the operator's behalf.
	o.OnPlanApprove(func(context.Context, plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		t.Fatal("the plan handler was consulted on a headless run")
		return plan.PlanApproveAnswer{}, nil
	})
	ok, err := o.runPlanApprovalGate(context.Background(), "q", plannedBoard())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !log.contains("no TTY: auto-approving plan gate (override with --on-gate-timeout=stop)") {
		t.Fatalf("the decision must name the override flag:\n%s", log.all())
	}
}

// An EXPLICIT --on-gate-timeout=stop still stops, even headless — and it stops
// at the DOOR, before a single model call, not after the whole budget is spent.
func TestExplicitStopRefusesHeadlessRunUpFront(t *testing.T) {
	o, _ := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: true})

	if o.headlessAutoApproves() {
		t.Fatal("an explicit stop must never be overridden by the headless default")
	}
	_, err := o.GatePreflight()
	if err == nil {
		t.Fatal("an explicit stop on a headless run must refuse before planning")
	}
	var blocked *GateBlockedError
	if !asGateBlocked(err, &blocked) {
		t.Fatalf("want a GateBlockedError, got %T: %v", err, err)
	}
	if blocked.Gate != "plan" {
		t.Fatalf("gate=%q want plan", blocked.Gate)
	}
	msg := blocked.Error()
	// Actionable: it names the condition, the config key and the flag.
	for _, want := range []string{
		"plan_approve=ask",
		"--on-gate-timeout=stop",
		"--on-gate-timeout=approve",
		"slmcode config set plan_approve auto",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal is missing %q:\n%s", want, msg)
		}
	}
	// And it refuses BEFORE the work, which is the whole point.
	if !strings.Contains(msg, "Refusing now instead of spending the budget first") {
		t.Errorf("the refusal must say it is failing fast:\n%s", msg)
	}
}

// --on-gate-timeout=reject behaves the same way: an explicit choice.
func TestExplicitRejectRefusesHeadlessRunUpFront(t *testing.T) {
	o, _ := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessReject, Explicit: true})
	if _, err := o.GatePreflight(); err == nil {
		t.Fatal("an explicit reject on a headless run must refuse before planning")
	}
}

// A TTY-attached run is untouched: no preflight decisions, no auto-approval,
// the gate still asks its handler.
func TestAttachedRunIsUnchanged(t *testing.T) {
	o, log := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: false, Policy: HeadlessStop})

	decisions, err := o.GatePreflight()
	if err != nil || len(decisions) != 0 {
		t.Fatalf("an attached run must have nothing to decide: %+v / %v", decisions, err)
	}
	if o.headlessAutoApproves() {
		t.Fatal("an attached run must not auto-approve anything")
	}
	asked := false
	o.OnPlanApprove(func(context.Context, plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		asked = true
		return plan.PlanApproveAnswer{Decision: "approve"}, nil
	})
	ok, gerr := o.runPlanApprovalGate(context.Background(), "q", plannedBoard())
	if gerr != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, gerr)
	}
	if !asked {
		t.Fatal("the plan gate must still ask the human when one is attached")
	}
	if log.contains("no TTY") {
		t.Fatalf("an attached run must not claim to be headless:\n%s", log.all())
	}
}

// An attached run that answers "no" still stops — headless defaults never leak
// into the interactive path.
func TestAttachedRejectionStillStops(t *testing.T) {
	o, log := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: false})
	o.OnPlanApprove(func(context.Context, plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		return plan.PlanApproveAnswer{Decision: "stop"}, nil
	})
	_, err := o.runPlanApprovalGate(context.Background(), "q", plannedBoard())
	if err == nil {
		t.Fatal("an explicit human no must stop the run")
	}
	if !log.contains("plan not approved — stopping before execute") {
		t.Fatalf("the stop was not reported:\n%s", log.all())
	}
}

// The shell-permission gate is a SAFETY gate, not a convenience gate: it must
// never auto-approve headless. It fails fast with an actionable message.
func TestShellPermissionGateNeverAutoApprovesHeadless(t *testing.T) {
	o, _ := headlessOrch(t, func(c *config.Config) {
		c.ShellPermission = permissions.ShellAsk
		// Convenience gates all off, so the only thing left to refuse is shell.
		c.PlanApprove = plan.PlanApproveModeOff
		c.ClarifyMode = plan.ClarifyOff
		c.ContinueAsk = plan.ContinueAskOff
		c.EscalateAsk = plan.EscalateAskOff
	})
	// The friendliest possible headless posture: the operator asked for
	// everything to be approved. The safety gate still refuses.
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessApprove, Explicit: true})

	decisions, err := o.GatePreflight()
	if err == nil {
		t.Fatalf("shell_permission=ask must not auto-approve headless (decisions=%+v)", decisions)
	}
	var blocked *GateBlockedError
	if !asGateBlocked(err, &blocked) {
		t.Fatalf("want a GateBlockedError, got %T: %v", err, err)
	}
	if blocked.Gate != "shell" {
		t.Fatalf("gate=%q want shell", blocked.Gate)
	}
	msg := blocked.Error()
	for _, want := range []string{
		"shell_permission=ask",
		"never",
		"slmcode config set shell_permission allow",
		"slmcode config set shell_permission deny",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("shell refusal is missing %q:\n%s", want, msg)
		}
	}
	for _, forbidden := range []string{"auto-approv"} {
		if strings.Contains(strings.ToLower(msg), forbidden+"ing") {
			t.Errorf("the shell gate must not offer to auto-approve:\n%s", msg)
		}
	}
}

// The shell gate outranks the plan gate: a run with both misconfigured is
// refused for the SAFETY reason, not the convenience one.
func TestShellGateRefusalOutranksPlanGate(t *testing.T) {
	o, _ := headlessOrch(t, func(c *config.Config) { c.ShellPermission = permissions.ShellAsk })
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: true})
	_, err := o.GatePreflight()
	var blocked *GateBlockedError
	if !asGateBlocked(err, &blocked) || blocked.Gate != "shell" {
		t.Fatalf("want the shell refusal first, got %v", err)
	}
}

// A gate that DOES stop the run must report the plan/board/tasks as retained
// and resumable, with the exact command — never a bare "stopping".
func TestStoppedGateReportsRetainedWorkAndResumeCommand(t *testing.T) {
	o, log := headlessOrch(t, nil)
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: true})
	o.OnPlanApprove(func(context.Context, plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		return plan.PlanApproveAnswer{Decision: "stop"}, nil
	})
	board := plannedBoard()
	o.persistBoard(board)

	_, err := o.runPlanApprovalGate(context.Background(), "q", board)
	if err == nil {
		t.Fatal("an explicit stop must stop")
	}
	// The ERROR the CLI prints carries it…
	for _, want := range []string{
		"plan not approved",
		"board.json, PLAN.md, TASKS.md",
		"slmcode session resume run-headless-test",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("stop error is missing %q:\n%s", want, err.Error())
		}
	}
	// …and so does the transcript, which is what a piped run keeps.
	if !log.contains("are kept, not discarded") {
		t.Errorf("the transcript must say the work was kept:\n%s", log.all())
	}
	if !log.contains("slmcode session resume run-headless-test") {
		t.Errorf("the transcript must name the resume command:\n%s", log.all())
	}
	// The directory it names really holds the artifacts.
	dir := session.TurnDir(o.cfg.SlmDir(), "run-headless-test")
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("stop error must name the turn directory %q:\n%s", dir, err.Error())
	}
	for _, f := range []string{"board.json", "PLAN.md", "TASKS.md"} {
		if !fileExists(t, dir, f) {
			t.Errorf("%s is missing from %s — the hint points at nothing", f, dir)
		}
	}
}

// Without an explicit choice, the engine falls back on its OWN long-standing
// notion of headless (no event subscriber attached) rather than a second one.
func TestHeadlessFallsBackOnSubscribed(t *testing.T) {
	cfg := config.Default(t.TempDir())
	bare := &Orchestrator{cfg: cfg}
	if !bare.HeadlessGates().Headless {
		t.Fatal("an orchestrator with no subscriber at all is headless")
	}
	attached := &Orchestrator{cfg: cfg}
	attached.OnEvent(func(Event) {})
	if attached.HeadlessGates().Headless {
		t.Fatal("an orchestrator with an attached listener is not headless")
	}
	// And an explicit answer always wins over the fallback.
	attached.SetHeadlessGates(HeadlessGates{Headless: true})
	if !attached.HeadlessGates().Headless {
		t.Fatal("SetHeadlessGates must override the Subscribed() fallback")
	}
}

// auto_approve short-circuits everything, safety gate included: the operator
// asked for it by name.
func TestAutoApproveSkipsPreflight(t *testing.T) {
	o, _ := headlessOrch(t, func(c *config.Config) {
		c.AutoApprove = true
		c.ShellPermission = permissions.ShellAsk
	})
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: true})
	if _, err := o.GatePreflight(); err != nil {
		t.Fatalf("auto_approve must not be refused: %v", err)
	}
}

// The convenience gates that can strand a headless run are all downgraded, and
// each downgrade is announced.
func TestPreflightCoversEveryConvenienceGate(t *testing.T) {
	o, _ := headlessOrch(t, func(c *config.Config) {
		c.ClarifyMode = plan.ClarifyAsk
		c.ContinueAsk = plan.ContinueAskAsk
		c.EscalateAsk = plan.EscalateAskAsk
	})
	o.SetHeadlessGates(HeadlessGates{Headless: true})
	decisions, err := o.GatePreflight()
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	for _, gate := range []string{"plan", "clarify", "continue", "escalate"} {
		if !hasGate(decisions, gate) {
			t.Errorf("no decision announced for the %s gate: %+v", gate, decisions)
		}
	}
	// Each one downgrades to a mode the engine can resolve on its own.
	if got := o.headlessGateMode(plan.ClarifyAsk, plan.ClarifyAsk, plan.ClarifyAuto); got != plan.ClarifyAuto {
		t.Errorf("clarify headless mode = %q", got)
	}
	if got := o.headlessGateMode(plan.ContinueAskAsk, plan.ContinueAskAsk, plan.ContinueAskAuto); got != plan.ContinueAskAuto {
		t.Errorf("continue headless mode = %q", got)
	}
	if got := o.headlessGateMode(plan.EscalateAskAsk, plan.EscalateAskAsk, plan.EscalateAskAuto); got != plan.EscalateAskAuto {
		t.Errorf("escalate headless mode = %q", got)
	}
	// An explicit stop leaves every mode exactly as configured.
	o.SetHeadlessGates(HeadlessGates{Headless: true, Policy: HeadlessStop, Explicit: true})
	if got := o.headlessGateMode(plan.ContinueAskAsk, plan.ContinueAskAsk, plan.ContinueAskAuto); got != plan.ContinueAskAsk {
		t.Errorf("an explicit stop must not downgrade the continue gate, got %q", got)
	}
	// …and an attached run is never downgraded either.
	o.SetHeadlessGates(HeadlessGates{Headless: false})
	if got := o.headlessGateMode(plan.ClarifyAsk, plan.ClarifyAsk, plan.ClarifyAuto); got != plan.ClarifyAsk {
		t.Errorf("an attached run must keep clarify_mode=ask, got %q", got)
	}
}

func TestNormalizeHeadlessPolicy(t *testing.T) {
	for in, want := range map[string]HeadlessPolicy{
		"":        HeadlessStop,
		"stop":    HeadlessStop,
		"approve": HeadlessApprove,
		"yes":     HeadlessApprove,
		"auto":    HeadlessApprove,
		"reject":  HeadlessReject,
		"no":      HeadlessReject,
		"garbage": HeadlessStop,
	} {
		if got := NormalizeHeadlessPolicy(in); got != want {
			t.Errorf("NormalizeHeadlessPolicy(%q)=%q want %q", in, got, want)
		}
	}
}

// GateBlockedError carries the documented "gate could not be answered" code.
func TestGateBlockedExitCode(t *testing.T) {
	e := &GateBlockedError{Gate: "plan", Reason: "r", Remedies: []string{"do x"}}
	if e.ExitCode() != 6 {
		t.Fatalf("exit code = %d want 6", e.ExitCode())
	}
	if !strings.Contains(e.Error(), "do x") {
		t.Fatalf("the remedy must be part of the message: %s", e.Error())
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func hasGate(ds []GateDecision, gate string) bool {
	for _, d := range ds {
		if d.Gate == gate {
			return true
		}
	}
	return false
}

func hasGateDecision(ds []GateDecision, gate, behavior string) bool {
	for _, d := range ds {
		if d.Gate == gate && d.Behavior == behavior {
			return true
		}
	}
	return false
}

func asGateBlocked(err error, out **GateBlockedError) bool {
	return errors.As(err, out)
}

func fileExists(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
