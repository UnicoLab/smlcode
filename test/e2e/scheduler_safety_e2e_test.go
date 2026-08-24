package e2e_test

import (
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestFailedDependencyDoesNotCascadeThroughTheBoard proves, through the real
// persisted board rather than an in-memory fixture, that a failed upstream task
// stops its dependents instead of licensing them.
//
// The old behavior treated ColBlocked as satisfying a dependency, so a task
// whose prerequisite had FAILED was still handed to a worker — which then built
// on a foundation that was never laid. One failure became a wave of confidently
// wrong work.
func TestFailedDependencyDoesNotCascadeThroughTheBoard(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	ws, err := harness.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}

	err = ws.Board.Update(func(b *plan.Board) error {
		upstream := plan.Task{ID: "T1", Title: "Define the schema", Role: plan.RoleWorker}
		upstream.MoveTo(plan.ColBlocked) // it failed
		b.Tasks = append(b.Tasks, upstream)

		dependent := plan.Task{ID: "T2", Title: "Use the schema", Role: plan.RoleWorker, DependsOn: []string{"T1"}}
		dependent.MoveTo(plan.ColReadyToDev)
		b.Tasks = append(b.Tasks, dependent)

		// A task that depends on nothing must be unaffected — the fix must not
		// turn one failure into a stalled board.
		free := plan.Task{ID: "T3", Title: "Independent work", Role: plan.RoleWorker}
		free.MoveTo(plan.ColReadyToDev)
		b.Tasks = append(b.Tasks, free)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var ready []plan.Task
	if err := ws.Board.Update(func(b *plan.Board) error {
		ready = b.ExecutableTasks()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, task := range ready {
		if task.ID == "T2" {
			t.Fatal("T2 is executable although its dependency T1 failed — the cascade bug is back")
		}
	}
	if !containsTaskID(ready, "T3") {
		t.Error("T3 depends on nothing and must still be executable; the fix must not stall the board")
	}

	// The dependent must not merely be withheld — it must be visibly blocked,
	// with the reason recorded, so it reaches the human/escalation path instead
	// of silently never running.
	var t2 plan.Task
	if err := ws.Board.Update(func(b *plan.Board) error {
		got, ok := b.Get("T2")
		if !ok {
			t.Fatal("T2 vanished from the board")
		}
		t2 = got
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if t2.Column != plan.ColBlocked {
		t.Errorf("T2 column = %q, want %q — a task that can never run must say so", t2.Column, plan.ColBlocked)
	}
	if t2.Error == "" && t2.Notes == "" {
		t.Error("T2 was blocked with no explanation naming the failed upstream")
	}
}

// TestDependencyCycleDoesNotHangTheBoard guards the other half of the fix.
// Before it, pkg/plan had no cycle detection anywhere: a cycle meant those
// tasks were simply never executable, silently, and the stall detector spun
// until it gave up. The board must terminate and say what is wrong.
func TestDependencyCycleDoesNotHangTheBoard(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	ws, err := harness.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}

	err = ws.Board.Update(func(b *plan.Board) error {
		a := plan.Task{ID: "A", Title: "A", Role: plan.RoleWorker, DependsOn: []string{"B"}}
		a.MoveTo(plan.ColReadyToDev)
		bb := plan.Task{ID: "B", Title: "B", Role: plan.RoleWorker, DependsOn: []string{"A"}}
		bb.MoveTo(plan.ColReadyToDev)
		b.Tasks = append(b.Tasks, a, bb)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan []plan.Task, 1)
	go func() {
		var ready []plan.Task
		_ = ws.Board.Update(func(b *plan.Board) error {
			ready = b.ExecutableTasks()
			return nil
		})
		done <- ready
	}()

	select {
	case ready := <-done:
		if containsTaskID(ready, "A") || containsTaskID(ready, "B") {
			t.Error("a task inside a dependency cycle was reported executable; no execution order exists")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("readiness computation hung on a dependency cycle")
	}
}

func containsTaskID(tasks []plan.Task, id string) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}
