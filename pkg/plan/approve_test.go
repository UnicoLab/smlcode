package plan

import (
	"fmt"
	"testing"
)

func TestBuildPlanApproveAsk(t *testing.T) {
	b := &Board{
		Plan: Plan{Summary: "hello CLI", Goals: []string{"print hello"}},
		Tasks: []Task{
			{ID: "T1", Title: "Create main.py", Description: "Add command entrypoint", Role: RoleWorker, Files: []string{"main.py"}, Acceptance: "prints hello", DependsOn: []string{"T0"}, Priority: 2},
		},
	}
	ask := BuildPlanApproveAsk("build cli", b)
	if ask.TaskCount != 1 || ask.Summary == "" || len(ask.Tasks) != 1 || len(ask.TaskDetails) != 1 {
		t.Fatalf("%+v", ask)
	}
	if ask.Kind != "plan" || ask.OnTimeout != "approve" || len(ask.Options) != 2 {
		t.Fatalf("missing approval metadata: %+v", ask)
	}
	detail := ask.TaskDetails[0]
	if detail.ID != "T1" || detail.Role != RoleWorker || detail.Files[0] != "main.py" || detail.DependsOn[0] != "T0" || detail.Acceptance == "" {
		t.Fatalf("bad task detail: %+v", detail)
	}
	if !IsPlanApproved(PlanApproveAnswer{Decision: "approve"}) {
		t.Fatal("approve")
	}
	if !IsPlanReplan(PlanApproveAnswer{Decision: "replan"}) {
		t.Fatal("replan")
	}
}

// The two lists are capped differently on purpose. `tasks` is the human-readable
// summary a terminal prints, and twelve lines is as much as anyone reads;
// `task_details` is what the approval editor edits, and a task past that cap is
// one the user cannot correct without throwing the whole board away — so it is
// far more generous, and TaskCount stays honest about the difference.
func TestBuildPlanApproveAskCapsTaskPreviews(t *testing.T) {
	b := &Board{}
	for i := 1; i <= 80; i++ {
		b.Tasks = append(b.Tasks, Task{ID: fmt.Sprintf("T%d", i), Title: "task"})
	}
	ask := BuildPlanApproveAsk("q", b)
	if len(ask.Tasks) != 12 {
		t.Fatalf("compact tasks=%d", len(ask.Tasks))
	}
	if len(ask.TaskDetails) != maxEditableTasks {
		t.Fatalf("task details=%d want %d", len(ask.TaskDetails), maxEditableTasks)
	}
	if ask.TaskCount != 80 {
		t.Fatalf("task_count=%d — the card must report the real board size, "+
			"or the UI cannot say how many tasks it is hiding", ask.TaskCount)
	}
}

// A board smaller than the cap is fully editable — the common case, and the one
// that would silently regress if the cap were ever applied unconditionally.
func TestBuildPlanApproveAskKeepsEveryTaskOnASmallBoard(t *testing.T) {
	b := &Board{}
	for i := 1; i <= 24; i++ {
		b.Tasks = append(b.Tasks, Task{ID: fmt.Sprintf("T%d", i), Title: "task"})
	}
	ask := BuildPlanApproveAsk("q", b)
	if len(ask.TaskDetails) != 24 {
		t.Fatalf("task details=%d, want all 24 editable", len(ask.TaskDetails))
	}
}
