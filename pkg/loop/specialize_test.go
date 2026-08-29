package loop

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func roleRegistry(ids ...string) func(string) bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) bool { return set[id] }
}

// The whole point of the change: ONE board, TWO languages, two specialists.
//
// The composition can only name one execute.default_role, and the splitter's
// contract only lets it emit the generic role "worker", so before this a
// request for "a Go backend and a React frontend" ran every task on whichever
// single specialist the query hint happened to match first.
func TestMixedLanguageBoardRoutesToBothSpecialists(t *testing.T) {
	r := &Runner{HasRole: roleRegistry("worker", "go-worker", "react-worker", "tester")}

	backend := plan.Task{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/server/main.go"}}
	frontend := plan.Task{ID: "T2", Role: plan.RoleWorker, Files: []string{"web/src/App.jsx"}}

	if got := r.execAgentFor(backend); got != "go-worker" {
		t.Errorf("backend task went to %q, want go-worker", got)
	}
	if got := r.execAgentFor(frontend); got != "react-worker" {
		t.Errorf("frontend task went to %q, want react-worker", got)
	}
}

// Narrowness is the safety property: only the generic worker is re-routed.
func TestSpecializeLeavesNonGenericRolesAlone(t *testing.T) {
	r := &Runner{HasRole: roleRegistry("worker", "go-worker", "python-worker", "tester", "explorer")}
	for _, tc := range []struct{ role, want string }{
		// A role someone deliberately pinned keeps it, even though the files
		// say Go — an explicit choice outranks an inferred one.
		{"python-worker", "python-worker"},
		// Non-implementing roles are never re-routed.
		{plan.RoleTester, plan.RoleTester},
		{"explorer", "explorer"},
	} {
		got := r.specializeExecRole(tc.role, plan.Task{Files: []string{"main.go"}})
		if got != tc.want {
			t.Errorf("specializeExecRole(%q) = %q, want %q", tc.role, got, tc.want)
		}
	}
}

// An unregistered specialist must leave the task on the generic worker rather
// than dispatching to an agent that does not exist.
func TestSpecializeFallsBackWhenSpecialistIsAbsent(t *testing.T) {
	r := &Runner{HasRole: roleRegistry("worker", "tester")}
	got := r.execAgentFor(plan.Task{Role: plan.RoleWorker, Files: []string{"main.go"}})
	if got != plan.RoleWorker {
		t.Errorf("got %q, want the generic worker when go-worker is not registered", got)
	}
}

// A task with no files, or files in no known language, keeps its role.
func TestSpecializeNeedsFileEvidence(t *testing.T) {
	r := &Runner{HasRole: roleRegistry("worker", "go-worker")}
	for _, files := range [][]string{nil, {"README"}, {"notes.txt"}} {
		got := r.execAgentFor(plan.Task{Role: plan.RoleWorker, Files: files})
		if got != plan.RoleWorker {
			t.Errorf("files=%v routed to %q, want the generic worker", files, got)
		}
	}
}

// Routing composes with the model ladder rather than replacing it: a task that
// has earned a rung must still escalate, from its SPECIALIZED base.
func TestSpecializedRoleStillEscalates(t *testing.T) {
	r := &Runner{
		HasRole:         roleRegistry("worker", "go-worker", "go-worker@esc1"),
		EscalationRungs: 1,
		EscalateAfter:   1,
	}
	t1 := plan.Task{
		ID: "T1", Role: plan.RoleWorker, Files: []string{"main.go"},
		AttemptLog: []string{"attempt 1 failed"},
	}
	if got := r.execAgentFor(t1); got != "go-worker@esc1" {
		t.Errorf("got %q, want the escalated specialist go-worker@esc1", got)
	}
}
