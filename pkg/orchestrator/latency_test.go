package orchestrator

import (
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestThinkRefinePasses(t *testing.T) {
	if thinkRefinePasses(1) != 1 {
		t.Fatal("1")
	}
	if thinkRefinePasses(2) != 1 {
		t.Fatal("2→1 critique loop")
	}
	if thinkRefinePasses(3) != 2 {
		t.Fatal("3→2")
	}
}

func TestRoleTimeoutPlanningTighterThanWorkers(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.TaskTimeout = 12 * time.Minute
	o := &Orchestrator{cfg: cfg}
	planTO := o.roleTimeout(plan.RolePlanner)
	splitTO := o.roleTimeout("splitter")
	workerTO := o.roleTimeout(plan.RoleWorker)
	if planTO >= workerTO || splitTO >= workerTO {
		t.Fatalf("planning should be tighter: plan=%v split=%v worker=%v", planTO, splitTO, workerTO)
	}
	if planTO < 2*time.Minute {
		t.Fatalf("planner floor too aggressive: %v", planTO)
	}
	rev := o.roleTimeout(plan.RoleReviewer)
	if rev > planTO {
		t.Fatalf("reviewer should be ≤ planner: rev=%v plan=%v", rev, planTO)
	}
}
