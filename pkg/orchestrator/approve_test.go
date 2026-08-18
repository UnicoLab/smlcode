package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
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
		if ask.TimeoutS != 1 || ask.OnTimeout != "approve" {
			t.Fatalf("bad ask metadata: %+v", ask)
		}
		if ask.Validation == nil || !ask.Validation.OK {
			t.Fatalf("missing validation metadata: %+v", ask.Validation)
		}
		return plan.PlanApproveAnswer{Decision: "approve", Notes: "use table tests"}, nil
	})
	board := &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "a"}},
	}
	ok, err := o.runPlanApprovalGate(context.Background(), "q", board, &plan.ScopeJudgeResult{OK: true})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(board.Plan.Assumptions) != 1 || board.Plan.Assumptions[0] != "User plan note: use table tests" {
		t.Fatalf("assumptions=%v", board.Plan.Assumptions)
	}
}

func TestPlanApprovalAskIncludesDynamicComposition(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = plan.PlanApproveModeAsk
	cfg.PlanApproveTimeout = time.Second
	o := &Orchestrator{
		cfg: cfg,
		dynamicComposition: &composer.Composition{
			Summary:  "targeted go fix",
			Strategy: "skip docs; execute and test only",
			Handoff:  []string{"Touch pkg/a.go only", "Verify with go test ./pkg/..."},
			Phases: []composer.PhaseChoice{
				{ID: "execute", Agent: "go-worker", Enabled: true, When: pipeline.WhenAlways},
				{ID: "test", Agent: "go-tester", Enabled: true, When: pipeline.WhenAlways},
			},
			Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 1},
			Team:    []composer.TeamMember{{Role: "go-worker", Skills: []string{"atomic-coding"}}},
			Slots: []pipeline.Slot{{
				ID: "preflight", Agent: "go-tester", Before: "execute", When: pipeline.WhenAlways,
				PersistTo: pipeline.PersistScratch, FailMode: pipeline.FailContinue,
			}},
		},
		onEvent: func(Event) {},
	}
	o.OnPlanApprove(func(ctx context.Context, ask plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		if ask.Composition == nil {
			t.Fatal("missing composition")
		}
		if ask.Composition.Execute.DefaultRole != "go-worker" || ask.Composition.Execute.MaxWaves != 1 {
			t.Fatalf("execute=%+v", ask.Composition.Execute)
		}
		if len(ask.Composition.Phases) != 2 || ask.Composition.Phases[0].Agent != "go-worker" {
			t.Fatalf("phases=%+v", ask.Composition.Phases)
		}
		if len(ask.Composition.Team) != 1 || ask.Composition.Team[0].Skills[0] != "atomic-coding" {
			t.Fatalf("team=%+v", ask.Composition.Team)
		}
		if len(ask.Composition.Slots) != 1 || ask.Composition.Slots[0].Before != "execute" {
			t.Fatalf("slots=%+v", ask.Composition.Slots)
		}
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
