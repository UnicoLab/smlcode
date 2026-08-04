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

func TestAlreadySatisfiedCreateNotImplement(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644)
	// Existing file + "implement" must NOT count as satisfied (needs write evidence).
	edit := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Files: []string{"hello.go"}, Acceptance: "file updated",
	}
	if alreadySatisfied(dir, edit) {
		t.Fatal("implement/edit must not be alreadySatisfied merely because file exists")
	}
	create := plan.Task{
		ID: "T2", Title: "Create agent module", Description: "scaffold src/lg_agent/graph.py",
		Files: []string{"src/lg_agent/graph.py"}, Acceptance: "file created",
	}
	_ = os.MkdirAll(filepath.Join(dir, "src", "lg_agent"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "lg_agent", "graph.py"),
		[]byte("def create_graph():\n    return {\"nodes\": []}\n"), 0o644)
	if !alreadySatisfied(dir, create) {
		t.Fatal("expected alreadySatisfied for scaffold create when file exists")
	}
	// Placeholder stubs must never count as already satisfied.
	_ = os.WriteFile(filepath.Join(dir, "src", "lg_agent", "graph.py"),
		[]byte("def create_graph():\n    # Placeholder implementation\n    return {\"output\": \"run_result\"}\n"), 0o644)
	if alreadySatisfied(dir, create) {
		t.Fatal("placeholder scaffold must not be alreadySatisfied")
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

func TestHasRealWriteEvidenceTrustsDiskSectionWhenBaselineAmbiguous(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n// done\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"},
		Output: "worker mumbled\n\n## Disk evidence\n- modified: hello.go\n",
	}
	// Ambiguous: nil baseline (late snapshot / lost fingerprints)
	if !r.hasRealWriteEvidence(task, nil) {
		t.Fatal("disk evidence section must count even with nil baseline")
	}
	// Ambiguous: empty map
	if !r.hasRealWriteEvidence(task, map[string]string{}) {
		t.Fatal("disk evidence section must count with empty baseline")
	}
	// Ambiguous: baseline fingerprints missing for focus files
	if !r.hasRealWriteEvidence(task, map[string]string{"other.go": "1:2"}) {
		t.Fatal("disk evidence section must count when focus missing from baseline")
	}
	// Auto-approve path via evidenceOK
	if ok, why := r.evidenceOK(task, nil); !ok {
		t.Fatalf("evidenceOK should pass with disk section: %s", why)
	}
}

func TestEvidenceOKCreateSatisfiedWithoutDelta(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname=\"lg-agent\"\n"), 0o644)
	r := &Runner{Root: dir}
	task := plan.Task{
		ID: "T1", Title: "Create pyproject.toml", Description: "scaffold deps",
		Acceptance: "pyproject.toml exists", Files: []string{"pyproject.toml"},
		Output: "I can see there's already a pyproject.toml file",
	}
	baseline := map[string]string{"pyproject.toml": fileFingerprint(filepath.Join(dir, "pyproject.toml"))}
	if ok, why := r.evidenceOK(task, baseline); !ok {
		t.Fatalf("create file already on disk should pass: %s", why)
	}
}

func TestReviewFastPathSkipsExecutor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.go")
	_ = os.WriteFile(target, []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	r := &Runner{Root: dir, Executor: nil} // nil executor must never be called
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	task := plan.Task{
		ID: "T1", Title: "Edit hello", Description: "implement comment",
		Acceptance: "file updated", Files: []string{"hello.go"}, Role: plan.RoleWorker,
		Column: plan.ColInReview,
		Output: "done\n\n## Disk evidence\n- modified: hello.go\n",
	}
	baseline := map[string]string{"hello.go": "1:1"} // stale; disk section still counts
	board := &plan.Board{Tasks: []plan.Task{task}}
	// Mutate so hasRealWriteEvidence also sees a delta vs fingerprint-less baseline path
	_ = os.WriteFile(target, []byte("package main\n\n// Hello greets.\nfunc Hello() string { return \"hi\" }\n"), 0o644)

	err := r.reviewAndCorrect(context.Background(), board, task, baseline)
	if err != nil {
		t.Fatalf("reviewAndCorrect: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "skip reviewer LLM") {
		t.Fatalf("expected review fast-path log, got: %s", joined)
	}
	got, ok := board.Get("T1")
	if !ok {
		t.Fatal("missing task")
	}
	if got.Column != plan.ColDone && !strings.Contains(strings.ToLower(got.Review), "auto-approved") {
		// Done column is ideal; at minimum review summary must show auto-approve.
		if !strings.Contains(strings.ToLower(got.Review), "auto-approved") &&
			!strings.Contains(strings.ToLower(got.Review), "disk") {
			t.Fatalf("expected auto-approve review, col=%s review=%q", got.Column, got.Review)
		}
	}
}

func TestIncompleteFinalizeNudgeDetectsToolEndBlock(t *testing.T) {
	r := &Runner{QualityMonitor: true}
	reason, issue, need := r.incompleteFinalizeNudge(plan.Task{
		Output: `{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`,
	})
	if !need || reason != "ended_on_tool_call" {
		t.Fatalf("need=%v reason=%q", need, reason)
	}
	if !strings.Contains(issue, "STRICT") && !strings.Contains(issue, "status JSON") {
		t.Fatalf("issue=%q", issue)
	}
	_, _, needOK := r.incompleteFinalizeNudge(plan.Task{
		Output: `{"status":"done","summary":"ok","files_changed":["a.go"]}`,
	})
	if needOK {
		t.Fatal("done JSON must not need finish-steer")
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
