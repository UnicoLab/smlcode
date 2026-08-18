package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestResolveAskUsesHandler(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ClarifyMode = plan.ClarifyAsk
	cfg.ClarifyTimeout = time.Second
	o := &Orchestrator{cfg: cfg, onEvent: func(Event) {}}
	interview := plan.ScopeInterview{
		NeedsUser: true,
		Questions: []plan.ScopeQuestion{{
			ID: "q1", Header: "Language", Question: "Which?",
			Options: []plan.ScopeOption{
				{Label: "Python", Recommended: true},
				{Label: "Go"},
			},
			Recommended: "Python",
		}},
		Language: "python", Entrypoint: "main.py",
		PRD: plan.ScopePRD{Summary: "cli", Acceptance: []string{"prints hello"}},
	}
	o.OnAsk(func(ctx context.Context, ask plan.ScopeAsk) (plan.ScopeAnswers, error) {
		if ask.Kind != "clarify" || ask.TimeoutS != 1 || ask.OnTimeout != "use_recommended" {
			t.Fatalf("bad ask metadata: %+v", ask)
		}
		return plan.ScopeAnswers{
			Answers: []plan.ScopeAnswer{{
				QuestionID: "q1", Selected: []string{"Go"},
			}},
		}, nil
	})
	got := o.resolveAsk(context.Background(), "build a cli", interview)
	joined := strings.Join(got.Assumptions, "\n")
	if !strings.Contains(joined, "Go") {
		t.Fatalf("expected Go decision, got %v", got.Assumptions)
	}
}

func TestRunScopeJudgeGateEnriches(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ScopeJudge = true
	cfg.ThinkPasses = 1
	o := &Orchestrator{cfg: cfg, onEvent: func(Event) {}}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "fix", Role: plan.RoleWorker, Description: "x", Acceptance: "done"},
	}}
	prd := plan.ScopePRD{
		Language: "python", Entrypoint: "main.py",
		Acceptance: []string{"python main.py prints hello"},
	}
	o.runScopeJudgeGate(context.Background(), "Create a Python CLI", board, prd)
	if board.Tasks[0].Acceptance == "done" || board.Tasks[0].Acceptance == "" {
		t.Fatalf("expected enriched acceptance, got %q", board.Tasks[0].Acceptance)
	}
}
