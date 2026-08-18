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

func TestBuildPlanApproveAskCapsTaskPreviews(t *testing.T) {
	b := &Board{}
	for i := 1; i <= 24; i++ {
		b.Tasks = append(b.Tasks, Task{ID: fmt.Sprintf("T%d", i), Title: "task"})
	}
	ask := BuildPlanApproveAsk("q", b)
	if len(ask.Tasks) != 12 {
		t.Fatalf("compact tasks=%d", len(ask.Tasks))
	}
	if len(ask.TaskDetails) != 20 {
		t.Fatalf("task details=%d", len(ask.TaskDetails))
	}
}
