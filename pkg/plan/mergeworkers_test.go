package plan

import (
	"strings"
	"testing"
)

// The board a live 30B produced for one UI request: two workers on src/App.tsx
// doing the same job, plus a third worker elsewhere so the all-or-nothing
// shouldCollapseSameFile rule declined and both duplicates survived.
func TestMergeSameFileWorkersFoldsDuplicates(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"src/lib/api.ts"}, Title: "add api client"},
		{ID: "T2", Role: RoleWorker, Files: []string{"src/App.tsx"},
			Title: "Render task list with Card, Badge and Delete Button",
			Criteria: []Criterion{
				{Text: "each task renders in a Card", Priority: "must", Verify: "npx tsc --noEmit"},
			}},
		{ID: "T3", Role: RoleWorker, Files: []string{"src/App.tsx"},
			Title: "Import and implement shadcn components in App.tsx",
			Criteria: []Criterion{
				{Text: "components are imported from @/components/ui", Priority: "must", Verify: "npx tsc --noEmit"},
			}},
	}
	out := mergeSameFileWorkers(tasks)
	if len(out) != 2 {
		t.Fatalf("got %d tasks, want 2 (T3 folds into T2): %+v", len(out), out)
	}
	var merged *Task
	for i := range out {
		if out[i].ID == "T2" {
			merged = &out[i]
		}
		if out[i].ID == "T3" {
			t.Fatal("T3 survived — the duplicate was not folded")
		}
	}
	if merged == nil {
		t.Fatal("T2 disappeared")
	}
	// The absorbed task's instructions have to survive, or the merge silently
	// drops half the requested work.
	if !strings.Contains(merged.Description, "Import and implement shadcn components") {
		t.Errorf("absorbed work is missing from the survivor: %q", merged.Description)
	}
	if len(merged.Criteria) != 2 {
		t.Errorf("criteria = %d, want both tasks' conditions carried: %+v", len(merged.Criteria), merged.Criteria)
	}
}

// A dependency on an absorbed task must follow it, or the dependent waits
// forever on an id that no longer exists — the deadlock this whole area keeps
// producing.
func TestMergeRewritesDependenciesOntoTheSurvivor(t *testing.T) {
	tasks := []Task{
		{ID: "T2", Role: RoleWorker, Files: []string{"src/App.tsx"}, Title: "render list"},
		{ID: "T3", Role: RoleWorker, Files: []string{"src/App.tsx"}, Title: "wire components"},
		{ID: "T4", Role: RoleTester, Files: []string{"src/App.test.tsx"}, DependsOn: []string{"T3"}},
	}
	out := mergeSameFileWorkers(tasks)
	var tester *Task
	for i := range out {
		if out[i].ID == "T4" {
			tester = &out[i]
		}
	}
	if tester == nil {
		t.Fatal("the tester task disappeared")
	}
	if len(tester.DependsOn) != 1 || tester.DependsOn[0] != "T2" {
		t.Fatalf("T4 depends on %v, want [T2] — the absorbed id must be rewritten", tester.DependsOn)
	}
}

// Merging must never create a self-edge, which would make a task depend on
// itself and never become executable.
func TestMergeDropsSelfDependency(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"a.tsx"}, Title: "first"},
		{ID: "T2", Role: RoleWorker, Files: []string{"a.tsx"}, Title: "second", DependsOn: []string{"T1"}},
	}
	out := mergeSameFileWorkers(tasks)
	if len(out) != 1 {
		t.Fatalf("got %d tasks, want 1", len(out))
	}
	for _, d := range out[0].DependsOn {
		if d == out[0].ID {
			t.Fatalf("%s depends on itself — it can never become executable", out[0].ID)
		}
	}
}

// Only worker roles merge. A tester on the same file is a different job, and
// folding it away would delete the verification step.
func TestMergeLeavesNonWorkerRolesAlone(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"src/App.tsx"}, Title: "implement"},
		{ID: "T2", Role: RoleTester, Files: []string{"src/App.tsx"}, Title: "verify"},
		{ID: "T3", Role: RoleReviewer, Files: []string{"src/App.tsx"}, Title: "review"},
	}
	if out := mergeSameFileWorkers(tasks); len(out) != 3 {
		t.Fatalf("got %d tasks, want 3 — tester and reviewer are not duplicates", len(out))
	}
}

// Work on genuinely different files stays separate — those tasks can share a
// wave and do not overwrite each other.
func TestMergeKeepsDisjointWorkSeparate(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"a.tsx"}},
		{ID: "T2", Role: RoleWorker, Files: []string{"b.tsx"}},
		{ID: "T3", Role: RoleWorker, Files: []string{"c.tsx"}},
	}
	if out := mergeSameFileWorkers(tasks); len(out) != 3 {
		t.Fatalf("got %d tasks, want 3", len(out))
	}
}

// The live shape this rule exists for: every worker rewrites the same primary
// file, each listing a different tail of components it happened to mention. As
// exact sets those are five distinct jobs; as work they are one file rewritten
// five times, in five separate waves, each on a stale copy.
func TestMergeFoldsWorkersSharingAPrimaryFile(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"src/App.tsx", "src/components/ui/card.tsx"}},
		{ID: "T2", Role: RoleWorker, Files: []string{"src/App.tsx", "src/components/ui/button.tsx", "src/components/ui/dialog.tsx"}},
		{ID: "T3", Role: RoleWorker, Files: []string{"src/App.tsx"}},
		{ID: "T4", Role: RoleWorker, Files: []string{"src/App.tsx", "src/components/ui/badge.tsx"}},
		{ID: "T5", Role: RoleWorker, Files: []string{"src/lib/api.ts"}},
	}
	out := mergeSameFileWorkers(tasks)
	if len(out) != 2 {
		t.Fatalf("got %d tasks, want 2 (four App.tsx workers fold; the api.ts worker stands): %+v",
			len(out), out)
	}
}

// File ORDER must not decide identity, or the same pair of files in a different
// order reads as two different jobs.
func TestMergeIgnoresFileOrder(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"a.tsx", "b.tsx"}},
		{ID: "T2", Role: RoleWorker, Files: []string{"b.tsx", "a.tsx"}},
	}
	if out := mergeSameFileWorkers(tasks); len(out) != 1 {
		t.Fatalf("got %d tasks, want 1", len(out))
	}
}

// A task with no files has unknown scope; merging on "no files" would fold
// unrelated work together.
func TestMergeSkipsTasksWithoutFiles(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Title: "something"},
		{ID: "T2", Role: RoleWorker, Title: "something else"},
	}
	if out := mergeSameFileWorkers(tasks); len(out) != 2 {
		t.Fatalf("got %d tasks, want 2 — unknown scope is not a shared scope", len(out))
	}
}

// The board a live 30B produced for one small full-stack change: FOUR tester
// tasks over the same two files. Four full model rounds for one answer, on a
// budget that then ran out with seven of eight tasks never dispatched.
// Verification is idempotent — running it three times buys nothing.
func TestMergeFoldsDuplicateTesters(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"cmd/server/main.go"}, Title: "serve the API"},
		{ID: "T3", Role: RoleTester, Files: []string{"cmd/server/main.go"}, Title: "verify the API"},
		{ID: "T6", Role: "go-tester", Files: []string{"cmd/server/main.go", "internal/todo/todo.go"},
			Title: "verify the handler and the store"},
		{ID: "T7", Role: RoleTester, Files: []string{"cmd/server/main.go", "internal/todo/todo.go"},
			Title: "run the tests"},
	}

	out := mergeSameFileWorkers(tasks)

	if len(out) != 2 {
		t.Fatalf("got %d tasks, want 2 (three testers fold into one, the worker stands): %+v", len(out), out)
	}
	var tester *Task
	for i := range out {
		if IsTesterRole(out[i].Role) {
			tester = &out[i]
		}
	}
	if tester == nil {
		t.Fatal("the tester disappeared — verification must survive the merge")
	}
	// Scope is UNIONED, not narrowed: folding a tester over two files into one
	// over one file silently stops verifying the second.
	if len(tester.Files) != 2 {
		t.Fatalf("merged tester files = %v, want both files still covered", tester.Files)
	}
}

// An implementer WRITES a file and a tester PROVES it. They answer different
// contracts, and a survivor carrying both would be handed a shape it cannot
// produce.
func TestMergeNeverFoldsAWorkerIntoATester(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"cmd/main.go"}, Title: "write it"},
		{ID: "T2", Role: RoleTester, Files: []string{"cmd/main.go"}, Title: "prove it"},
	}
	out := mergeSameFileWorkers(tasks)
	if len(out) != 2 {
		t.Fatalf("got %d tasks, want both — writing and proving are different jobs: %+v", len(out), out)
	}
}

// A merged implementer must be ALLOWED to write every file its own merged
// description now tells it to change, or the wave denies it at the tool layer.
func TestMergeUnionsTheImplementerScope(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"src/App.tsx"}, Title: "render the list"},
		{ID: "T2", Role: RoleWorker, Files: []string{"src/App.tsx", "src/Todos.tsx"}, Title: "extract a component"},
	}
	out := mergeSameFileWorkers(tasks)
	if len(out) != 1 {
		t.Fatalf("got %d tasks, want 1", len(out))
	}
	if len(out[0].Files) != 2 {
		t.Fatalf("files = %v — the survivor must keep the absorbed task's scope", out[0].Files)
	}
}
