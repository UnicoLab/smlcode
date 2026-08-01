package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestPlanApprovalAuto(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = plan.PlanApproveModeAuto
	o := &Orchestrator{cfg: cfg, onEvent: func(Event) {}}
	ok, err := o.runPlanApprovalGate(context.Background(), "q", &plan.Board{
		Plan:  plan.Plan{Summary: "x"},
		Tasks: []plan.Task{{ID: "T1", Title: "a"}},
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestPlanApprovalAskHandler(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = plan.PlanApproveModeAsk
	cfg.PlanApproveTimeout = time.Second
	o := &Orchestrator{cfg: cfg, onEvent: func(Event) {}}
	o.OnPlanApprove(func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		return plan.PlanApproveAnswer{Decision: "approve"}, nil
	})
	ok, err := o.runPlanApprovalGate(context.Background(), "q", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "a"}},
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestAutoApproveSkipsPlanAsk(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = plan.PlanApproveModeAsk
	cfg.AutoApprove = true
	o := &Orchestrator{cfg: cfg, onEvent: func(Event) {}}
	ok, err := o.runPlanApprovalGate(context.Background(), "q", &plan.Board{})
	if err != nil || !ok {
		t.Fatal(err)
	}
}
