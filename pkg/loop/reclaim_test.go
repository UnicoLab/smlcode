package loop

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The exact board that deadlocked a live run: one task stranded in in_review by
// a wave that had already returned, and four dependents that can never become
// executable because only `done` satisfies a dependency.
//
// AgentWorkRemaining sees in_review and reports work in progress; the scheduler
// finds nothing executable because in_review is not a dispatchable column; the
// loop idles 31 rounds and gives up. Measured: ~9 minutes discarded and reported
// as a failure, with the actual edit already written and compiling.
func strandedBoard() *plan.Board {
	b := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Column: plan.ColInReview, Title: "install badge"},
		{ID: "T2", Role: plan.RoleWorker, Column: plan.ColReadyToDev, DependsOn: []string{"T1"}},
		{ID: "T3", Role: plan.RoleWorker, Column: plan.ColReadyToDev, DependsOn: []string{"T1"}},
		{ID: "T4", Role: plan.RoleTester, Column: plan.ColReadyToDev, DependsOn: []string{"T2"}},
	}}
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
	}
	return b
}

func TestStrandedBoardHasNoExecutableTaskButClaimsWorkRemains(t *testing.T) {
	b := strandedBoard()
	if len(b.ReadyTasks()) != 0 {
		t.Fatal("fixture is wrong: something is executable, so this is not the deadlock")
	}
	if !b.AgentWorkRemaining() {
		t.Fatal("fixture is wrong: the board must claim work remains, or the loop would just finish")
	}
}

func TestReclaimOrphanedUnblocksTheBoard(t *testing.T) {
	r := &Runner{}
	b := strandedBoard()

	moved := r.reclaimOrphaned(b)
	if moved != 1 {
		t.Fatalf("reclaimed %d task(s), want 1 (only T1 was mid-flight)", moved)
	}
	t1, ok := b.Get("T1")
	if !ok || t1.Column != plan.ColReadyToDev {
		t.Fatalf("T1 column = %q, want ready_to_dev", t1.Column)
	}
	if !strings.Contains(t1.Notes, "RECLAIMED") {
		t.Errorf("the reclaim is not recorded on the task: %q", t1.Notes)
	}
	// The point of the whole exercise: the board can now make progress.
	if len(b.ReadyTasks()) == 0 {
		t.Fatal("still nothing executable after reclaim — the deadlock survives")
	}
}

// Reclaim must touch ONLY the abandoned columns. A ready task is already
// dispatchable and a done task is finished; moving either would be a bug that
// re-runs completed work.
func TestReclaimLeavesSettledTasksAlone(t *testing.T) {
	r := &Runner{}
	b := &plan.Board{Tasks: []plan.Task{
		{ID: "A", Column: plan.ColDone},
		{ID: "B", Column: plan.ColReadyToDev},
		{ID: "C", Column: plan.ColBlocked},
		{ID: "D", Column: plan.ColToScope},
	}}
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
	}
	if moved := r.reclaimOrphaned(b); moved != 0 {
		t.Fatalf("reclaimed %d settled task(s), want 0", moved)
	}
	for _, want := range []struct{ id, col string }{
		{"A", plan.ColDone}, {"B", plan.ColReadyToDev},
		{"C", plan.ColBlocked}, {"D", plan.ColToScope},
	} {
		got, _ := b.Get(want.id)
		if got.Column != want.col {
			t.Errorf("%s moved to %q, want %q", want.id, got.Column, want.col)
		}
	}
}

// in_progress strands the same way in_review does — a wave that returns without
// settling its task leaves either column behind.
func TestReclaimHandlesInProgressToo(t *testing.T) {
	r := &Runner{}
	b := &plan.Board{Tasks: []plan.Task{{ID: "T1", Column: plan.ColInProgress}}}
	b.Tasks[0].Normalize()
	if moved := r.reclaimOrphaned(b); moved != 1 {
		t.Fatalf("reclaimed %d, want 1", moved)
	}
	if got, _ := b.Get("T1"); got.Column != plan.ColReadyToDev {
		t.Fatalf("T1 column = %q, want ready_to_dev", got.Column)
	}
}

func TestReclaimIsNilSafe(t *testing.T) {
	r := &Runner{}
	if moved := r.reclaimOrphaned(nil); moved != 0 {
		t.Fatalf("reclaimed %d from a nil board", moved)
	}
}
