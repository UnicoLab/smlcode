package loop

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func assemblerRunner(assembler string, registered ...string) *Runner {
	has := map[string]bool{}
	for _, r := range registered {
		has[r] = true
	}
	return &Runner{
		FrontendAssembler: assembler,
		HasRole:           func(id string) bool { return has[id] },
	}
}

// The measured failure. Per-task routing staffs a .tsx task from its own file
// extensions BEFORE this runs, so the task arrives as `react-worker` — and the
// guard accepted only the generic `worker`. On a live shadcn run the harness
// printed
//
//	· init frontend: shadcn-worker — the request named shadcn/ui
//
// and then executed the task as @react-worker. The dedicated assembler, whose
// whole purpose is to REUSE published components instead of writing them, was
// chosen and never used.
func TestAssemblerSupersedesTheInferredFrontendWorker(t *testing.T) {
	r := assemblerRunner("shadcn-worker", "shadcn-worker")
	task := plan.Task{Files: []string{"src/App.tsx"}}

	for _, role := range []string{"react-worker", "ts-worker", plan.RoleWorker} {
		if got := r.specializeExecRole(role, task); got != "shadcn-worker" {
			t.Errorf("role %q specialized to %q, want shadcn-worker", role, got)
		}
	}
}

// A role from another stack is not a frontend role the assembler refines.
func TestAssemblerLeavesOtherRolesAlone(t *testing.T) {
	r := assemblerRunner("shadcn-worker", "shadcn-worker")
	task := plan.Task{Files: []string{"src/App.tsx"}}

	for _, role := range []string{"go-worker", "python-worker", plan.RoleTester, plan.RoleReviewer} {
		if got := r.specializeExecRole(role, task); got != role {
			t.Errorf("role %q was replaced by %q", role, got)
		}
	}
}

// Choosing the hand-written option leaves FrontendAssembler empty, and then
// routing's own answer must stand untouched.
func TestNoAssemblerKeepsTheRoutedRole(t *testing.T) {
	r := assemblerRunner("", "shadcn-worker")
	task := plan.Task{Files: []string{"src/App.tsx"}}

	if got := r.specializeExecRole("react-worker", task); got != "react-worker" {
		t.Errorf("got %q, want the routed react-worker", got)
	}
}

// An assembler that is not registered cannot be routed to.
func TestUnregisteredAssemblerIsNotUsed(t *testing.T) {
	r := assemblerRunner("untitledui-worker") // registered: nothing
	task := plan.Task{Files: []string{"src/App.tsx"}}

	if got := r.specializeExecRole("react-worker", task); got != "react-worker" {
		t.Errorf("got %q, want react-worker", got)
	}
}

// Non-React files are not this assembler's business, whatever was chosen.
func TestAssemblerOnlyAppliesToReactFiles(t *testing.T) {
	r := assemblerRunner("shadcn-worker", "shadcn-worker")

	if got := r.specializeExecRole("react-worker",
		plan.Task{Files: []string{"cmd/server/main.go"}}); got != "react-worker" {
		t.Errorf("got %q for a Go file, want react-worker", got)
	}
}

// A task already staffed with the assembler must not churn.
func TestAssemblerIsStableOnItsOwnRole(t *testing.T) {
	r := assemblerRunner("shadcn-worker", "shadcn-worker")
	if got := r.specializeExecRole("shadcn-worker",
		plan.Task{Files: []string{"src/App.tsx"}}); got != "shadcn-worker" {
		t.Errorf("got %q", got)
	}
}
