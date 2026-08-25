package loop

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// THE SNAPSHOT HOOK, WHICH WAS DEAD.
//
// Workspace.SnapshotProtected records the bytes a protected file is restored
// from when a shell command rewrites it, and the orchestrator wires it to
// Runner.OnProtect. Nothing ever CALLED OnProtect.
//
// Every unit test for the heal passed anyway, because they all call
// SnapshotProtected directly. In production the checkpointer therefore held no
// prior bytes, healProtectedWrites hit its "no backup, do not guess" branch on
// every violation, and the feature reported without ever restoring — which is
// exactly the behavior it was written to replace.
//
// The snapshot has one correct moment: after the deny list is installed and
// before any agent in the wave runs. This pins the call at that moment, with
// the patterns the guard actually enforces.
func TestProtectionsAreSnapshottedWhenTheWaveInstallsThem(t *testing.T) {
	fx := newProtectFixture(t, []string{"pkg/app/main.go"})
	task := plan.Task{
		ID:    "T1",
		Role:  plan.RoleWorker,
		Title: "Implement Run",
		Description: "Implement Run in pkg/app/main.go so it returns the queue depth. " +
			"Do not edit, add or delete any _test.go file.",
		Files:      []string{"pkg/app/main.go"},
		Acceptance: "go test ./pkg/app/...",
	}

	r := fx.runner(t)
	var got []string
	calls := 0
	r.OnProtect = func(pats []string) {
		calls++
		got = append([]string(nil), pats...)
	}

	undo := r.applyWaveProtections([]plan.Task{task})
	defer undo()

	if calls != 1 {
		t.Fatalf("OnProtect fired %d times, want exactly 1 — without it no protected "+
			"file is ever backed up and the self-heal can only report", calls)
	}
	// The snapshot must cover the SAME patterns the guard denies. Any drift and
	// the harness backs up one set of files while protecting another.
	want := waveProtections([]plan.Task{task})
	if len(got) == 0 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OnProtect got %v, guard enforces %v", got, want)
	}
}

// TestNoProtectionsMeansNoSnapshotSweep. SnapshotProtected walks the worktree,
// and a wave that protects nothing must not pay for a walk that can back up
// nothing.
func TestNoProtectionsMeansNoSnapshotSweep(t *testing.T) {
	fx := newProtectFixture(t, []string{"pkg/app/main.go"})
	r := fx.runner(t)
	fired := false
	r.OnProtect = func([]string) { fired = true }

	undo := r.applyWaveProtections([]plan.Task{{
		ID:          "T1",
		Role:        plan.RoleWorker,
		Title:       "Add a helper",
		Description: "Add a small helper to pkg/app/main.go.",
		Files:       []string{"pkg/app/main.go"},
	}})
	defer undo()

	if fired {
		t.Fatal("OnProtect fired for a wave with no protections")
	}
}

// TestSnapshotHookIsOptional: the hook is nil in every caller that does not
// wire a workspace, and applyWaveProtections is on the hot path of every wave.
func TestSnapshotHookIsOptional(t *testing.T) {
	fx := newProtectFixture(t, []string{"pkg/app/main.go"})
	r := fx.runner(t)
	r.OnProtect = nil
	undo := r.applyWaveProtections([]plan.Task{{
		ID: "T1", Role: plan.RoleWorker, Title: "Implement Run",
		Description: "Implement Run. Do not edit any _test.go file.",
		Files:       []string{"pkg/app/main.go"},
	}})
	undo()
}
