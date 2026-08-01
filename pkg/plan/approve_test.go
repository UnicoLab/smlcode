package plan

import "testing"

func TestBuildPlanApproveAsk(t *testing.T) {
	b := &Board{
		Plan: Plan{Summary: "hello CLI", Goals: []string{"print hello"}},
		Tasks: []Task{
			{ID: "T1", Title: "Create main.py", Acceptance: "prints hello"},
		},
	}
	ask := BuildPlanApproveAsk("build cli", b)
	if ask.TaskCount != 1 || ask.Summary == "" || len(ask.Tasks) != 1 {
		t.Fatalf("%+v", ask)
	}
	if !IsPlanApproved(PlanApproveAnswer{Decision: "approve"}) {
		t.Fatal("approve")
	}
	if !IsPlanReplan(PlanApproveAnswer{Decision: "replan"}) {
		t.Fatal("replan")
	}
}
