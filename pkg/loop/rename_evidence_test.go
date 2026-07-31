package loop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestRenameOKSymbolSatisfied(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "greet.go"), []byte("package greet\n\nfunc Greet() string { return \"hello\" }\n"), 0o644)
	task := plan.Task{
		ID: "T1", Title: "Rename Hello to Greet", Acceptance: "RENAME_SYMBOL Hello -> Greet",
		Files: []string{"greet.go"}, Role: plan.RoleWorker,
	}
	if !renameOK(root, task) {
		t.Fatal("expected renameOK")
	}
	if !alreadySatisfied(root, task) {
		t.Fatal("expected alreadySatisfied via rename")
	}
	r := &Runner{Root: root}
	ok, why := r.evidenceOK(task, map[string]string{"greet.go": "old"})
	if !ok {
		t.Fatalf("evidenceOK: %s", why)
	}
}

func TestRenameOKFileSatisfied(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg", "new.go"), []byte("package pkg\n"), 0o644)
	task := plan.Task{
		ID: "T1", Title: "rename pkg/old.go to pkg/new.go",
		Files: []string{"pkg/old.go", "pkg/new.go"}, Role: plan.RoleWorker,
	}
	if !renameOK(root, task) {
		t.Fatal("expected file rename OK")
	}
	r := &Runner{Root: root}
	ok, why := r.evidenceOK(task, nil)
	if !ok {
		t.Fatalf("evidenceOK: %s", why)
	}
}

func TestHasToolWriteEvidenceMove(t *testing.T) {
	if !hasToolWriteEvidence("Observation: moved pkg/old.go → pkg/new.go") {
		t.Fatal("expected mv evidence")
	}
}
