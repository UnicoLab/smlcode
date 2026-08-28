package loop

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
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
	got, ok := r.reassignFailedTask(task, failedReview())
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
	first, ok := r.reassignFailedTask(task, failedReview())
	if !ok {
		t.Fatal("the first handoff should happen")
	}
	if _, ok := r.reassignFailedTask(first, failedReview()); ok {
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
			if _, ok := r.reassignFailedTask(tc.task, failedReview()); ok {
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
