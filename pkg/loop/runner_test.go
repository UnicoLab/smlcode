package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestStripScopedPack(t *testing.T) {
	in := "# Scoped context for role=worker\n\nbig pack\n\n## Task instructions\n\nDo the thing\n"
	got := StripScopedPack(in)
	if got != "Do the thing" {
		t.Fatalf("got %q", got)
	}
}

func TestHasToolWriteEvidence(t *testing.T) {
	if !hasToolWriteEvidence("Observation: ws_edit updated pkg/loop/runner.go") {
		t.Fatal("expected tool evidence")
	}
	// ws_edit tool returns "edited <path> (N replacement(s))"
	if !hasToolWriteEvidence("Observation: edited hello.go (1 replacement(s))") {
		t.Fatal("expected edited-path tool evidence")
	}
	if !hasToolWriteEvidence("## Disk evidence\n- modified: hello.go") {
		t.Fatal("expected disk evidence section")
	}
	if hasToolWriteEvidence(`{"status":"done","files_changed":["x.go"]}`) {
		t.Fatal("JSON-only claim must not count as tool evidence")
	}
}

func TestAlreadySatisfiedDocComment(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	task := plan.Task{
		ID: "T1", Title: "Add doc comment to Hello()", Files: []string{"hello.go"},
		Acceptance: "doc comment above Hello()",
	}
	if !alreadySatisfied(dir, task) {
		t.Fatal("expected alreadySatisfied for existing doc comment")
	}
}

func TestReviewBaselineUsesPreWaveSnapshot(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Add doc comment", Description: "add doc comment",
		Acceptance: "comment present", Files: []string{"hello.go"},
		Output: `{"status":"done","files_changed":["hello.go"],"summary":"added comment"}`,
	}
	baseline := r.snapshotTargets(task)
	_ = os.WriteFile(target, []byte("package main\n\n// Hello returns a greeting.\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	if !r.hasRealWriteEvidence(task, baseline) {
		t.Fatal("pre-wave baseline should detect disk modification")
	}
	// Re-baselining after the write (the old bug) hides the change.
	post := r.snapshotTargets(task)
	if r.hasRealWriteEvidence(task, post) {
		t.Fatal("post-write baseline should not show a content change")
	}
}

func TestEvidenceOKRequiresDiskOrTool(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"},
		Output: `{"status":"done","files_changed":["hello.go"],"summary":"claimed"}`,
	}
	baseline := map[string]string{"hello.go": fileFingerprint(target)}
	if ok, why := r.evidenceOK(task, baseline); ok {
		t.Fatalf("expected reject without real change, why=%s", why)
	}
	// mutate file → disk evidence passes
	_ = os.WriteFile(target, []byte("package main\n// hi\n"), 0o644)
	if ok, why := r.evidenceOK(task, baseline); !ok {
		t.Fatalf("expected pass after disk change: %s", why)
	}
}

func TestErrorHandlerWritesErrorsMD(t *testing.T) {
	dir := t.TempDir()
	fh := NewEnhancedFailureHandler(dir)
	board := &plan.Board{}
	task := plan.Task{ID: "T9", Title: "x", Role: "worker", Retries: 2, Review: "no changes"}
	if err := fh.ReportTaskFailure(board, task, errString("review rejected after max retries"), 2); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".slmcode", "errors", "errors.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "T9") {
		t.Fatalf("errors.md missing task: %s", data)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
