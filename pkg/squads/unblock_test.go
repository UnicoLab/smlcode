package squads

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func frozenSeamPlan() *Plan {
	return &Plan{
		Contract: Contract{Interfaces: []Interface{{
			ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"},
			Spec: "200 → {todos: [{id, title, done}]}",
		}}},
		Squads: []Squad{
			{ID: "backend", Owns: []string{"cmd/**", "internal/**"}},
			{ID: "frontend", Owns: []string{"web/**"}},
		},
	}
}

// The measured shape: disjoint files, different teams, and a planner-written
// wait that the frozen seam already answered. The halves ran in consecutive
// single-task waves — every mechanism working, twice the wall clock.
func TestUnblockLetsTheConsumerStartAtOnce(t *testing.T) {
	p := frozenSeamPlan()
	tasks := []plan.Task{
		{ID: "T1", Squad: "backend", Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Squad: "frontend", Files: []string{"web/src/App.tsx"}, DependsOn: []string{"T1"}},
	}

	dropped := UnblockAcrossTeams(p, tasks)

	if len(tasks[1].DependsOn) != 0 {
		t.Fatalf("T2 still waits on %v", tasks[1].DependsOn)
	}
	if len(dropped) != 1 {
		t.Fatalf("dropped = %v, want one line — changing the wave graph must be reported", dropped)
	}
	// The line has to name the interface that justified it, or it reads as the
	// harness quietly deciding a dependency did not matter.
	if !strings.Contains(dropped[0], "GET /api/todos") {
		t.Errorf("the report does not name the interface: %q", dropped[0])
	}
}

// Four waits that are real. Dropping any of them would start work against
// something that genuinely is not there yet.
func TestUnblockKeepsTheWaitsThatAreReal(t *testing.T) {
	for name, c := range map[string]struct {
		plan  *Plan
		tasks []plan.Task
	}{
		// Ordinary ordering inside one team, nothing to do with the seam.
		"inside one team": {frozenSeamPlan(), []plan.Task{
			{ID: "T1", Squad: "backend"},
			{ID: "T2", Squad: "backend", DependsOn: []string{"T1"}},
		}},
		// The seam IS integration — it comes after the halves it joins.
		"the seam waiting on both halves": {frozenSeamPlan(), []plan.Task{
			{ID: "T1", Squad: "backend"},
			{ID: "T2", Squad: "frontend"},
			{ID: "T3", Squad: "", DependsOn: []string{"T1", "T2"}},
		}},
		// Nothing was agreed between these two, so the consumer would be guessing.
		"two teams with no interface between them": {
			&Plan{
				Contract: Contract{Interfaces: []Interface{{
					ID: "x", Provider: "backend", Consumers: []string{"docs"},
				}}},
				Squads: []Squad{{ID: "backend", Owns: []string{"cmd/**"}},
					{ID: "frontend", Owns: []string{"web/**"}}},
			},
			[]plan.Task{
				{ID: "T1", Squad: "backend"},
				{ID: "T2", Squad: "frontend", DependsOn: []string{"T1"}},
			}},
		// Backwards: the PROVIDER waiting on its consumer is not something the
		// contract says anything about.
		"the provider waiting on the consumer": {frozenSeamPlan(), []plan.Task{
			{ID: "T1", Squad: "frontend"},
			{ID: "T2", Squad: "backend", DependsOn: []string{"T1"}},
		}},
	} {
		before := map[string]int{}
		for _, task := range c.tasks {
			before[task.ID] = len(task.DependsOn)
		}
		dropped := UnblockAcrossTeams(c.plan, c.tasks)
		if len(dropped) != 0 {
			t.Errorf("%s: dropped %v — that wait is real", name, dropped)
		}
		for _, task := range c.tasks {
			if len(task.DependsOn) != before[task.ID] {
				t.Errorf("%s: %s lost a dependency", name, task.ID)
			}
		}
	}
}

// With no frozen seam there is nothing standing in for the code, so every wait
// stands. This is also the single-stream case.
func TestUnblockNeedsAFrozenContract(t *testing.T) {
	tasks := []plan.Task{
		{ID: "T1", Squad: "backend"},
		{ID: "T2", Squad: "frontend", DependsOn: []string{"T1"}},
	}
	bare := &Plan{Squads: []Squad{
		{ID: "backend", Owns: []string{"cmd/**"}}, {ID: "frontend", Owns: []string{"web/**"}}}}

	for name, p := range map[string]*Plan{"no contract": bare, "no plan": nil} {
		if dropped := UnblockAcrossTeams(p, tasks); len(dropped) != 0 {
			t.Errorf("%s: dropped %v", name, dropped)
		}
		if len(tasks[1].DependsOn) != 1 {
			t.Fatalf("%s: T2's dependency was removed", name)
		}
	}
}

// A task can wait on several things at once, and only the answered wait goes.
func TestUnblockKeepsTheOtherDependencies(t *testing.T) {
	p := frozenSeamPlan()
	tasks := []plan.Task{
		{ID: "T1", Squad: "backend"},
		{ID: "T2", Squad: "frontend"},
		{ID: "T3", Squad: "frontend", DependsOn: []string{"T1", "T2"}},
	}

	UnblockAcrossTeams(p, tasks)

	if got := tasks[2].DependsOn; len(got) != 1 || got[0] != "T2" {
		t.Fatalf("T3 waits on %v, want only its own team's T2", got)
	}
}
