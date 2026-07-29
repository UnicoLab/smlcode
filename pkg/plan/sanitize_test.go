package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeTasksCollapsesTiny(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Locate Hello", Role: RoleExplorer, Column: ColReadyToDev},
		{ID: "T2", Title: "Verify", Role: RoleExplorer, DependsOn: []string{"T1"}, Files: []string{"path/to/file/containing/Hello"}, Column: ColReadyToDev},
		{ID: "T3", Title: "Add doc", Role: RoleWorker, DependsOn: []string{"T2"}, Column: ColReadyToDev},
		{ID: "T4", Title: "Validate", Role: RoleTester, DependsOn: []string{"T3"}, Column: ColReadyToDev},
	}
	out := SanitizeTasks(tasks, "Found hello.go with func Hello()", "Add a Doc comment to Hello(). Keep the change tiny.")
	if len(out) != 1 {
		t.Fatalf("expected collapse to 1 task for tiny query, got %d: %+v", len(out), out)
	}
	if out[0].Role != RoleWorker {
		t.Fatalf("role=%s", out[0].Role)
	}
	if len(out[0].Files) == 0 || out[0].Files[0] != "hello.go" {
		t.Fatalf("files=%v", out[0].Files)
	}
}

func TestSanitizeTasksDropsHallucinatedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "math.go"), []byte("package main\nfunc Add(a,b int) int { return a+b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := []Task{{
		ID: "T1", Title: "Add doc", Role: RoleWorker, Column: ColReadyToDev,
		Files: []string{"internal/calculator/calculator.go"},
	}}
	out := SanitizeTasksIn(tasks, "math.go", "Add a doc comment to Add(). Keep tiny.", root)
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	if len(out[0].Files) != 1 || out[0].Files[0] != "math.go" {
		t.Fatalf("files=%v want [math.go]", out[0].Files)
	}
}

func TestReconcileFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	got := ReconcileFiles(root, []string{"missing/x.go"}, []string{"a.go"})
	if len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("%v", got)
	}
}

func TestExecutableSoftSkipBlockedDep(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColBlocked, Role: RoleExplorer},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
	}}
	ready := b.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "T2" {
		t.Fatalf("%+v", ready)
	}
}
