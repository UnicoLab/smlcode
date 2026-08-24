package orchestrator

import (
	"testing"
	"time"

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

// Planning used to be given a hardcoded fraction of task_timeout so a stuck
// planner could not stall the whole budget. That fraction is gone — it starved
// slow models (see TestSlowModelIsNotStarvedAtColdStart) — but the property it
// bought must still hold once the harness has measured the roles: a planner
// that answers in a minute gets a minute-ish budget, not the whole task.
func TestPlanningTightensOnceMeasured(t *testing.T) {
	o := newTimeoutFixture(t, "qwen2.5-coder:14b", 12*time.Minute)

	// Untouched, everything starts from the full budget.
	if got := o.roleTimeout(plan.RolePlanner); got != 12*time.Minute {
		t.Fatalf("cold planner budget = %v, want the full 12m", got)
	}

	o.seedLatency(t, plan.RolePlanner, 5, 70*time.Second)
	o.seedLatency(t, "splitter", 5, 70*time.Second)
	o.seedLatency(t, plan.RoleReviewer, 5, 25*time.Second)
	o.seedLatency(t, plan.RoleWorker, 5, 6*time.Minute)

	planTO := o.roleTimeout(plan.RolePlanner)
	splitTO := o.roleTimeout("splitter")
	workerTO := o.roleTimeout(plan.RoleWorker)
	rev := o.roleTimeout(plan.RoleReviewer)

	if planTO >= workerTO || splitTO >= workerTO {
		t.Fatalf("measured planning should be tighter: plan=%v split=%v worker=%v", planTO, splitTO, workerTO)
	}
	if planTO >= 12*time.Minute {
		t.Fatalf("a measured planner must not keep the whole budget: %v", planTO)
	}
	if planTO < roleFloorPlanning {
		t.Fatalf("planner floor breached: %v < %v", planTO, roleFloorPlanning)
	}
	if rev > planTO {
		t.Fatalf("reviewer should be ≤ planner: rev=%v plan=%v", rev, planTO)
	}
}
