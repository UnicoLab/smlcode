package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Rename on disk + weak tool log must skip reviewer LLM and never escalate.
func TestRenameDiskWeakToolLogNoReviewerEscalate(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg", "new.go"), []byte("package pkg\n"), 0o644)

	r := &Runner{Root: root, Executor: nil} // nil executor must never be called
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	task := plan.Task{
		ID: "T1", Title: "rename pkg/old.go to pkg/new.go",
		Description: "use ws_mv", Acceptance: "RENAME_FILE pkg/old.go -> pkg/new.go",
		Files: []string{"pkg/old.go", "pkg/new.go"}, Role: plan.RoleWorker,
		Column: plan.ColInReview,
		// Weak tool log — no moved/ws_mv string, only vague JSON.
		Output: `{"status":"done","summary":"renamed","files_changed":["pkg/new.go","main.go"]}`,
	}
	board := &plan.Board{Tasks: []plan.Task{task}}
	baseline := map[string]string{"pkg/old.go": "1:1"} // old gone vs baseline

	err := r.reviewAndCorrect(context.Background(), board, task, baseline)
	if err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "skip reviewer LLM") {
		t.Fatalf("expected rename fast-path skip reviewer, got: %s", joined)
	}
	if strings.Contains(joined, "escalated") || strings.Contains(joined, "to_scope") {
		t.Fatalf("must not escalate: %s", joined)
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColDone {
		t.Fatalf("expected Success/done, col=%s review=%q", got.Column, got.Review)
	}
	if !strings.Contains(strings.ToLower(got.Review), "rename") &&
		!strings.Contains(strings.ToLower(got.Review), "auto-approved") {
		t.Fatalf("review=%q", got.Review)
	}
}

func TestTesterGateSkipsWhenRenameSatisfied(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "greet.go"), []byte("package greet\n\nfunc Greet() string { return \"hi\" }\n"), 0o644)
	r := &Runner{Root: root, Executor: nil}
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	task := plan.Task{
		ID: "T9", Title: "Rename Hello to Greet", Role: plan.RoleTester,
		Acceptance: "RENAME_SYMBOL Hello -> Greet", Files: []string{"greet.go"},
		Column: plan.ColInReview,
		Output: `{"passed":false,"summary":"does not work","failures":["unclear"]}`,
	}
	board := &plan.Board{Tasks: []plan.Task{task}}
	if err := r.reviewAndCorrect(context.Background(), board, task, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := board.Get("T9")
	if got.Column != plan.ColDone {
		t.Fatalf("tester must not reopen when rename OK: col=%s logs=%v", got.Column, logs)
	}
}
