package loop

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// escLadderRunner returns a runner with a ladder of the given depth and every
// rung id registered, so escalation is free to fire.
func escLadderRunner(rungs int) *Runner {
	return &Runner{
		EscalationRungs: rungs,
		HasRole:         func(string) bool { return true },
	}
}

func failedTask(role string, attempts int) plan.Task {
	t := plan.Task{ID: "T1", Role: role}
	for i := 0; i < attempts; i++ {
		t.AttemptLog = append(t.AttemptLog, "attempt failed")
	}
	return t
}

func TestNoLadderMeansNoEscalation(t *testing.T) {
	// The opt-in property: with nothing configured, every id is the base id.
	r := &Runner{}
	task := failedTask(plan.RoleWorker, 9)
	if got := r.escalationRung(task); got != 0 {
		t.Errorf("rung = %d with no ladder", got)
	}
	if got := r.execAgentFor(task); got != plan.RoleWorker {
		t.Errorf("execAgentFor = %q, want the base worker", got)
	}
	if got := r.correctorIDFor(task); got != plan.RoleCorrector {
		t.Errorf("correctorIDFor = %q, want the base corrector", got)
	}
	if note := r.escalationNote(task); note != "" {
		t.Errorf("escalation note with no ladder: %q", note)
	}
}

func TestEscalationNeedsRepeatedFailures(t *testing.T) {
	// One failure is the common case for work that then succeeds; escalating
	// there would spend the big model on nearly every task in a run.
	r := escLadderRunner(2)
	if got := r.escalationRung(failedTask(plan.RoleWorker, 0)); got != 0 {
		t.Errorf("fresh task rung = %d", got)
	}
	if got := r.escalationRung(failedTask(plan.RoleWorker, 1)); got != 0 {
		t.Errorf("one failure rung = %d, want 0", got)
	}
	if got := r.escalationRung(failedTask(plan.RoleWorker, 2)); got != 1 {
		t.Errorf("two failures rung = %d, want 1", got)
	}
}

func TestEscalationIsMonotoneAndCapped(t *testing.T) {
	r := escLadderRunner(2)
	prev := 0
	for attempts := 0; attempts <= 12; attempts++ {
		got := r.escalationRung(failedTask(plan.RoleWorker, attempts))
		if got < prev {
			t.Fatalf("rung fell from %d to %d at %d attempts — the ladder oscillated",
				prev, got, attempts)
		}
		if got > 2 {
			t.Fatalf("rung %d exceeds the 2-rung ladder at %d attempts", got, attempts)
		}
		prev = got
	}
	if prev != 2 {
		t.Errorf("the ladder never reached its top rung: %d", prev)
	}
}

func TestGateRetriesAlsoDriveEscalation(t *testing.T) {
	// A task can rack up gate retries with an empty attempt log — the gate
	// retried before any attempt recorded a reason. Taking the larger of the
	// two counters is what keeps escalation from stalling on either.
	r := escLadderRunner(2)
	task := plan.Task{ID: "T1", Role: plan.RoleWorker, GateRetries: 3}
	if got := r.escalationRung(task); got == 0 {
		t.Fatal("gate retries alone did not escalate")
	}
}

func TestEscalateAfterIsConfigurable(t *testing.T) {
	r := escLadderRunner(2)
	r.EscalateAfter = 1
	if got := r.escalationRung(failedTask(plan.RoleWorker, 1)); got != 1 {
		t.Errorf("with EscalateAfter=1, one failure gave rung %d", got)
	}
	r.EscalateAfter = 5
	if got := r.escalationRung(failedTask(plan.RoleWorker, 4)); got != 0 {
		t.Errorf("with EscalateAfter=5, four failures gave rung %d", got)
	}
}

func TestEscalationNeverDispatchesToAnUnregisteredAgent(t *testing.T) {
	// An unregistered agent id is not a degraded run — it is a hard task
	// failure, landing on exactly the tasks that were already struggling.
	r := &Runner{EscalationRungs: 2, HasRole: func(string) bool { return false }}
	task := failedTask(plan.RoleWorker, 6)
	if got := r.execAgentFor(task); got != plan.RoleWorker {
		t.Errorf("execAgentFor = %q, want a fallback to the base worker", got)
	}
	if got := r.correctorIDFor(task); got != plan.RoleCorrector {
		t.Errorf("correctorIDFor = %q, want a fallback to the base corrector", got)
	}
	if note := r.escalationNote(task); note != "" {
		t.Errorf("note claimed an escalation that did not happen: %q", note)
	}

	// A nil HasRole is treated the same way: unknown means do not dispatch.
	nilCheck := &Runner{EscalationRungs: 2}
	if got := nilCheck.execAgentFor(task); got != plan.RoleWorker {
		t.Errorf("nil HasRole dispatched to %q", got)
	}
}

func TestEscalationOnlyForRegisteredRungs(t *testing.T) {
	// Rung 1 is registered, rung 2 is not: the task must stay on the base
	// rather than reaching for a rung nobody built.
	r := &Runner{EscalationRungs: 2, HasRole: func(id string) bool {
		_, rung := agents.BaseRoleID(id)
		return rung == 1
	}}
	one := r.execAgentFor(failedTask(plan.RoleWorker, 2))
	if _, rung := agents.BaseRoleID(one); rung != 1 {
		t.Errorf("rung-1 dispatch = %q", one)
	}
	two := r.execAgentFor(failedTask(plan.RoleWorker, 8))
	if two != plan.RoleWorker {
		t.Errorf("unregistered rung 2 dispatched to %q, want the base worker", two)
	}
}

func TestEscalatedDispatchTargetsTheRightAgent(t *testing.T) {
	r := escLadderRunner(2)
	task := failedTask(plan.RoleWorker, 2)
	want := agents.EscalatedRoleID(plan.RoleWorker, 1)
	if got := r.execAgentFor(task); got != want {
		t.Errorf("execAgentFor = %q, want %q", got, want)
	}
	wantCorr := agents.EscalatedRoleID(plan.RoleCorrector, 1)
	if got := r.correctorIDFor(task); got != wantCorr {
		t.Errorf("correctorIDFor = %q, want %q", got, wantCorr)
	}
	if note := r.escalationNote(task); note == "" {
		t.Error("an escalated task produced no operator-facing note")
	}
}

func TestEscalationRespectsTheConfiguredDefaultRole(t *testing.T) {
	// A language specialist is still the agent being escalated.
	r := escLadderRunner(1)
	r.DefaultRole = "go-worker"
	task := plan.Task{ID: "T1", Role: "", AttemptLog: []string{"a", "b"}}
	want := agents.EscalatedRoleID("go-worker", 1)
	if got := r.execAgentFor(task); got != want {
		t.Errorf("execAgentFor = %q, want %q", got, want)
	}
}

// ── An escalated agent must behave exactly like its base ──────────────────

func TestEscalatedRoleStillGetsTheAcceptanceGate(t *testing.T) {
	// The silent failure this guards is severe: the escalated corrector is the
	// agent holding a task that already failed twice, and a role-string switch
	// that stops matching would run it with the acceptance gate switched off.
	for _, role := range []string{plan.RoleWorker, plan.RoleCorrector, "deep"} {
		esc := agents.EscalatedRoleID(role, 1)
		if !acceptanceSmokeRole(esc) {
			t.Errorf("%q lost the acceptance gate", esc)
		}
		if acceptanceSmokeRole(role) != acceptanceSmokeRole(esc) {
			t.Errorf("%q and %q disagree about the acceptance gate", role, esc)
		}
	}
}

func TestEscalatedRoleKeepsItsIterationBudget(t *testing.T) {
	for _, role := range []string{plan.RoleWorker, plan.RoleCorrector, "deep", plan.RoleTester} {
		esc := agents.EscalatedRoleID(role, 2)
		if roleMaxIter(role) != roleMaxIter(esc) {
			t.Errorf("%q max iterations = %d, base %q = %d",
				esc, roleMaxIter(esc), role, roleMaxIter(role))
		}
	}
}

func TestEscalatedRoleKeepsSelfCritique(t *testing.T) {
	r := &Runner{WorkerCritique: true}
	for _, role := range []string{plan.RoleWorker, plan.RoleCorrector, "deep"} {
		esc := agents.EscalatedRoleID(role, 1)
		if r.wantCritique(role) != r.wantCritique(esc) {
			t.Errorf("%q and %q disagree about self-critique", role, esc)
		}
	}
}

func TestBaseRoleHelperStripsOnlyTheRung(t *testing.T) {
	if got := baseRole("go-worker"); got != "go-worker" {
		t.Errorf("baseRole mangled a hyphenated id: %q", got)
	}
	if got := baseRole(agents.EscalatedRoleID("go-worker", 3)); got != "go-worker" {
		t.Errorf("baseRole = %q", got)
	}
	if got := baseRole(""); got != "" {
		t.Errorf("baseRole(\"\") = %q", got)
	}
}

func TestItoaMatchesSmallIntegers(t *testing.T) {
	for n, want := range map[int]string{0: "0", 1: "1", 9: "9", 10: "10", 123: "123"} {
		if got := itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}
