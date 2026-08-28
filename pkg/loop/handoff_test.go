package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

func registry(ids ...string) func(string) bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

func failedReview() plan.ReviewResult {
	return plan.ReviewResult{
		Approved: false,
		Summary:  "the handler returns a stub",
		Issues:   []string{"GetTodos returns nil", "no JSON encoding"},
	}
}

// The annoyance, at its source: an exhausted task used to go straight to
// to_scope with "needs human input or smaller scope" — the least actionable
// thing the harness can say, repeated on every long run.
func TestExhaustedTaskIsReassignedBeforeAskingAHuman(t *testing.T) {
	r := NewRunner(nil, nil)
	r.RoleExists = registry("go-worker", "go-corrector", "corrector", "worker")

	task := plan.Task{
		ID: "T1", Role: "go-worker", Squad: "backend",
		Title: "todo handler", Description: "Serve GET /api/todos.",
		Files: []string{"cmd/server/main.go"}, Retries: 4,
		Acceptance: "go test ./...",
	}
	got, ok := r.reassignFailedTask(context.Background(), task, failedReview())
	if !ok {
		t.Fatal("a task with an untried alternate specialist must be reassigned, not escalated")
	}

	// Different hands — never the role that just failed.
	if got.Role == "go-worker" {
		t.Error("reassigned to the specialist that just exhausted its retries")
	}
	if got.Role != "go-corrector" {
		t.Errorf("Role = %q, want the language corrector", got.Role)
	}
	// Workable immediately, with a clean retry budget.
	if got.Column != plan.ColReadyToDev || got.Status != plan.StatusReady {
		t.Errorf("task is not workable: %s/%s", got.Column, got.Status)
	}
	if got.Retries != 0 || got.Error != "" {
		t.Errorf("the new holder starts clean, got retries=%d err=%q", got.Retries, got.Error)
	}
	// Same work, not new work.
	if got.ID != "T1" || got.Acceptance != "go test ./..." ||
		len(got.Files) != 1 || got.Squad != "backend" {
		t.Errorf("identity/scope changed on handoff: %+v", got)
	}
	// And it is told what went wrong.
	for _, want := range []string{"GetTodos returns nil", "no JSON encoding", "Serve GET /api/todos"} {
		if !strings.Contains(got.Description, want) {
			t.Errorf("handoff context is missing %q:\n%s", want, got.Description)
		}
	}
	if !strings.Contains(got.Description, "do something different") {
		t.Error("the next specialist must be told not to repeat the last attempt")
	}
}

// One handoff. A second is a third agent guessing at work two others could not
// do — a scoping problem, which is exactly what the human is being asked about.
func TestReassignmentHappensAtMostOnce(t *testing.T) {
	r := NewRunner(nil, nil)
	r.RoleExists = registry("go-worker", "go-corrector", "corrector", "worker")

	task := plan.Task{ID: "T1", Role: "go-worker", Files: []string{"a.go"}, Retries: 4}
	first, ok := r.reassignFailedTask(context.Background(), task, failedReview())
	if !ok {
		t.Fatal("the first handoff should happen")
	}
	if _, ok := r.reassignFailedTask(context.Background(), first, failedReview()); ok {
		t.Fatal("a second handoff must fall through to the human")
	}
	if handoffCount(first) != 1 {
		t.Errorf("handoffCount = %d, want 1", handoffCount(first))
	}
}

// Naming an agent that cannot be dispatched is worse than escalating: the task
// would sit in ready_to_dev forever with nobody able to move it.
func TestReassignmentDeclinesWithNoAlternate(t *testing.T) {
	cases := []struct {
		name string
		reg  func(string) bool
		task plan.Task
	}{
		{"no registry at all", nil,
			plan.Task{ID: "T1", Role: "go-worker", Files: []string{"a.go"}}},
		{"only the failing role is registered", registry("go-worker"),
			plan.Task{ID: "T1", Role: "go-worker", Files: []string{"a.go"}}},
		{"nothing registered", registry(),
			plan.Task{ID: "T1", Role: plan.RoleWorker, Files: []string{"a.go"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRunner(nil, nil)
			r.RoleExists = tc.reg
			if _, ok := r.reassignFailedTask(context.Background(), tc.task, failedReview()); ok {
				t.Error("expected the human-escalation fallback")
			}
		})
	}
}

func TestAlternateSpecialistPrefersTheLanguageCorrector(t *testing.T) {
	r := NewRunner(nil, nil)
	r.CorrectorRole = "corrector"
	r.RoleExists = registry("go-corrector", "go-worker", "react-corrector", "corrector", "worker")

	cases := []struct {
		name string
		task plan.Task
		want string
	}{
		{"go task", plan.Task{Role: "go-worker", Files: []string{"main.go"}}, "go-corrector"},
		{"react task", plan.Task{Role: "react-worker", Files: []string{"App.tsx"}}, "react-corrector"},
		// No language signal → the configured corrector.
		{"no files", plan.Task{Role: plan.RoleWorker}, "corrector"},
		// Never the role that just failed.
		{"corrector failed", plan.Task{Role: "go-corrector", Files: []string{"main.go"}}, "go-worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.alternateSpecialist(tc.task); got != tc.want {
				t.Errorf("alternateSpecialist = %q, want %q", got, tc.want)
			}
		})
	}
}

// ── Manager triage ───────────────────────────────────────────────────────
//
// A ladder answers "who else could hold this". A manager answers "who should,
// and what do they need to know that the last attempt did not" — and the second
// half is what changes the outcome, because an agent handed identical context
// makes an identical attempt.

func triageRunner(t *testing.T, decide func(TriageRequest) (plan.TriageDecision, bool)) *Runner {
	t.Helper()
	r := NewRunner(nil, nil)
	r.RoleExists = registry("go-worker", "go-corrector", "react-worker", "corrector", "worker")
	r.RosterIDs = []string{"go-worker", "go-corrector", "react-worker", "corrector", "worker"}
	if decide != nil {
		r.Triage = func(_ context.Context, req TriageRequest) (plan.TriageDecision, bool) { return decide(req) }
	}
	return r
}

func exhaustedTask() plan.Task {
	return plan.Task{
		ID: "T1", Role: "go-worker", Squad: "backend",
		Title: "todo handler", Description: "Serve GET /api/todos.",
		Files: []string{"cmd/server/main.go"}, Retries: 4,
		AttemptLog: []string{"attempt 1: returned a bare slice"},
	}
}

func TestTheManagerDecidesWhoTakesItAndSaysWhatToDoDifferently(t *testing.T) {
	var seen TriageRequest
	r := triageRunner(t, func(req TriageRequest) (plan.TriageDecision, bool) {
		seen = req
		return plan.TriageDecision{
			Assignee: "go-corrector",
			Reason:   "compile error the worker could not resolve",
			Guidance: "Encode with json.NewEncoder(w).Encode(todos) and set Content-Type first.",
		}, true
	})

	got, ok := r.reassignFailedTask(context.Background(), exhaustedTask(), failedReview())
	if !ok {
		t.Fatal("the manager named a usable assignee, so the handoff must happen")
	}
	if got.Role != "go-corrector" {
		t.Errorf("Role = %q, want the manager's pick", got.Role)
	}
	// The direction goes ABOVE the failure dump: it is the one thing here the
	// previous attempt did not already have.
	if !strings.Contains(got.Description, "From the project manager") ||
		!strings.Contains(got.Description, "json.NewEncoder") {
		t.Errorf("the manager's guidance did not reach the next agent:\n%s", got.Description)
	}
	pmAt := strings.Index(got.Description, "From the project manager")
	failAt := strings.Index(got.Description, "## What failed")
	if pmAt < 0 || failAt < 0 || pmAt > failAt {
		t.Error("the guidance should come before the failure evidence, not after it")
	}

	// The manager is given what makes it better than a ladder.
	if seen.Language != "go" {
		t.Errorf("request language = %q", seen.Language)
	}
	if len(seen.Roster) == 0 {
		t.Error("the manager must be offered a roster")
	}
	if len(seen.Task.AttemptLog) == 0 {
		t.Error("the attempt ledger is the whole reason a manager beats a ladder")
	}
}

// Two manager answers are worse than no manager, and both must fall through to
// the ladder rather than being applied.
func TestAnUnusableVerdictFallsBackToTheLadder(t *testing.T) {
	cases := []struct {
		name string
		d    plan.TriageDecision
	}{
		// The task would sit in ready_to_dev with nobody able to move it.
		{"unregistered agent", plan.TriageDecision{Assignee: "cobol-worker", Reason: "why not"}},
		// The loop triage exists to end.
		{"re-picks the agent that just failed", plan.TriageDecision{Assignee: "go-worker", Reason: "try again"}},
		{"names nobody", plan.TriageDecision{Reason: "unsure"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := triageRunner(t, func(TriageRequest) (plan.TriageDecision, bool) { return tc.d, true })
			got, ok := r.reassignFailedTask(context.Background(), exhaustedTask(), failedReview())
			if !ok {
				t.Fatal("the ladder should still produce a handoff")
			}
			if got.Role != "go-corrector" {
				t.Errorf("Role = %q, want the ladder's pick", got.Role)
			}
			if strings.Contains(got.Description, "From the project manager") {
				t.Error("an ignored verdict must not leave its guidance behind")
			}
		})
	}
}

func TestNoManagerMeansTheLadderDecides(t *testing.T) {
	for _, decide := range []func(TriageRequest) (plan.TriageDecision, bool){
		nil,
		func(TriageRequest) (plan.TriageDecision, bool) { return plan.TriageDecision{}, false },
	} {
		r := triageRunner(t, decide)
		got, ok := r.reassignFailedTask(context.Background(), exhaustedTask(), failedReview())
		if !ok || got.Role != "go-corrector" {
			t.Errorf("ok=%v role=%q, want the deterministic ladder", ok, got.Role)
		}
	}
}

// The roster is built from the same predicate that gates the answer, so the
// manager can never be offered a choice that would then be refused.
func TestRosterOffersOnlyDispatchableAgents(t *testing.T) {
	r := NewRunner(nil, nil)
	r.RoleExists = registry("go-worker", "worker")
	r.RosterIDs = []string{"go-worker", "ghost-worker", "worker"}
	got := r.roster()
	if len(got) != 2 || got[0] != "go-worker" || got[1] != "worker" {
		t.Fatalf("roster = %v", got)
	}
	r.RoleExists = nil
	if got := r.roster(); got != nil {
		t.Errorf("with no registry the roster is empty, got %v", got)
	}
}

// ── The team's own manager ───────────────────────────────────────────────
//
// A run-wide manager answering for a specific team picks from a roster it has
// no reason to understand: the agents who can fix a failing Go handler are not
// the ones staffing the React half. The request has to say which team this is.

func staffedRunner(t *testing.T, decide func(TriageRequest) (plan.TriageDecision, bool)) *Runner {
	t.Helper()
	r := triageRunner(t, decide)
	r.Squads = &squads.Plan{
		Squads: []squads.Squad{
			{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./...",
				Worker: "go-corrector", Manager: "backend-pm"},
			{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build",
				Worker: "react-worker"},
		},
	}
	r.Squads.Normalize()
	return r
}

func TestTheManagerIsToldWhichTeamItAnswersFor(t *testing.T) {
	var seen TriageRequest
	r := staffedRunner(t, func(req TriageRequest) (plan.TriageDecision, bool) {
		seen = req
		return plan.TriageDecision{Assignee: "go-corrector", Reason: "compile error"}, true
	})

	if _, ok := r.reassignFailedTask(context.Background(), exhaustedTask(), failedReview()); !ok {
		t.Fatal("the manager named a usable assignee, so the handoff must happen")
	}
	if seen.Staffing.Squad != "backend" {
		t.Errorf("Staffing.Squad = %q, want the failing task's team", seen.Staffing.Squad)
	}
	if seen.Staffing.Manager != "backend-pm" {
		t.Errorf("Staffing.Manager = %q, want the team's own manager", seen.Staffing.Manager)
	}
	// Its own people come first, so the model reads them before the other
	// half's staff.
	if len(seen.Roster) == 0 || seen.Roster[0] != "go-corrector" {
		t.Errorf("roster = %v, want the backend's own worker first", seen.Roster)
	}
}

// Ordering, not filtering: the reason a delivery was rejected may be that the
// team lacks the skill the fix needs.
func TestTheManagerCanStillReachOutsideItsOwnTeam(t *testing.T) {
	var seen TriageRequest
	r := staffedRunner(t, func(req TriageRequest) (plan.TriageDecision, bool) {
		seen = req
		return plan.TriageDecision{Assignee: "react-worker", Reason: "the seam is on the web side"}, true
	})

	got, ok := r.reassignFailedTask(context.Background(), exhaustedTask(), failedReview())
	if !ok {
		t.Fatal("an out-of-team pick is still a usable answer")
	}
	if got.Role != "react-worker" {
		t.Errorf("Role = %q, want the manager's pick honored", got.Role)
	}
	if len(seen.Roster) != len(r.RosterIDs) {
		t.Errorf("roster = %v, want all %d dispatchable agents", seen.Roster, len(r.RosterIDs))
	}
}

// A single-stream run has no teams, and asking must not require checking first.
func TestAnUnassignedTaskCarriesNoTeam(t *testing.T) {
	var seen TriageRequest
	r := staffedRunner(t, func(req TriageRequest) (plan.TriageDecision, bool) {
		seen = req
		return plan.TriageDecision{Assignee: "go-corrector", Reason: "compile error"}, true
	})
	task := exhaustedTask()
	task.Squad = ""

	if _, ok := r.reassignFailedTask(context.Background(), task, failedReview()); !ok {
		t.Fatal("no team is not a reason to skip triage")
	}
	if seen.Staffing.Squad != "" || seen.Staffing.Manager != "" {
		t.Errorf("Staffing = %+v, want the zero value for an unassigned task", seen.Staffing)
	}
	if len(seen.Roster) != len(r.RosterIDs) {
		t.Errorf("roster = %v, want the full roster", seen.Roster)
	}
}
