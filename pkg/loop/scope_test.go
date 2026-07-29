package loop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestScopeOKRejectsOutOfFocusMain(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pkg.go"), []byte("package p\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit pkg", Description: "implement tiny fix",
		Files: []string{"pkg.go"},
		Output: `{"status":"done","files_changed":["main.go"],"summary":"oops"}`,
	}
	if why := r.scopeOK(task); why == "" {
		t.Fatal("expected out-of-scope rejection for main.go claim")
	}
	task.Output = `{"status":"done","files_changed":["pkg.go"],"summary":"ok"}`
	if why := r.scopeOK(task); why != "" {
		t.Fatalf("unexpected: %s", why)
	}
}

func TestScheduleReadyOrdersFocusedFirst(t *testing.T) {
	ready := []plan.Task{
		{ID: "T2", Title: "long verification pass for suite", Role: "tester"},
		{ID: "T1", Title: "edit", Role: "worker", Files: []string{"a.go"}},
	}
	got := scheduleReady(ready)
	if got[0].ID != "T1" {
		t.Fatalf("expected focused worker first, got %s", got[0].ID)
	}
}

func TestEvidenceOKRejectsWanderClaim(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Files: []string{"hello.go"},
		Output: "edited hello.go (1 replacement(s))\n" +
			`{"status":"done","files_changed":["hello.go","main.go"],"summary":"bad"}`,
	}
	baseline := map[string]string{"hello.go": fileFingerprint(filepath.Join(dir, "hello.go"))}
	_ = os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n// x\n"), 0o644)
	if ok, why := r.evidenceOK(task, baseline); ok {
		t.Fatalf("should reject wander claim, why=%s", why)
	}
}
