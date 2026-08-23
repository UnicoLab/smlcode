package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// gitFixture creates a repository with one committed file and returns its root.
func gitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write(t, filepath.Join(root, "a.txt"), "one\n")
	run("add", "-A")
	run("commit", "-qm", "init")
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunChangesReportsWhatTheRunTouched(t *testing.T) {
	root := gitFixture(t)
	before := fingerprintDirty(root)

	write(t, filepath.Join(root, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(root, "new.txt"), "fresh\n")

	got := runChanges(root, before)
	paths := diffPaths(got)
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "new.txt" {
		t.Fatalf("runChanges = %v, want [a.txt new.txt]", paths)
	}
	files, added, removed := changeTotals(got)
	if files != 2 || added != 2 || removed != 0 {
		t.Errorf("totals = %d files +%d -%d, want 2 files +2 -0", files, added, removed)
	}
}

// A tree that was already dirty when the run started must not be reported as
// this run's work — the whole point of the "nothing changed" statement is that
// it is trustworthy.
func TestRunChangesIgnoresPreExistingDirt(t *testing.T) {
	root := gitFixture(t)
	write(t, filepath.Join(root, "a.txt"), "one\nuser edit\n")
	write(t, filepath.Join(root, "untracked.txt"), "mine\n")

	before := fingerprintDirty(root)
	if got := runChanges(root, before); len(got) != 0 {
		t.Fatalf("runChanges = %v, want none (the run touched nothing)", diffPaths(got))
	}

	// Now the run edits one of them: that IS attributable.
	write(t, filepath.Join(root, "untracked.txt"), "mine\nand the agent's\n")
	got := runChanges(root, before)
	if p := diffPaths(got); len(p) != 1 || p[0] != "untracked.txt" {
		t.Fatalf("runChanges = %v, want [untracked.txt]", p)
	}
}

// .slmcode/ is rewritten on every run; counting it would mean a run that
// touched nothing still reported changes.
func TestRunChangesExcludesHarnessState(t *testing.T) {
	root := gitFixture(t)
	before := fingerprintDirty(root)
	write(t, filepath.Join(root, ".slmcode", "board.json"), "{}\n")
	write(t, filepath.Join(root, ".slmcode", "CONTEXT.md"), "# ctx\n")

	if got := runChanges(root, before); len(got) != 0 {
		t.Fatalf("runChanges = %v, want none", diffPaths(got))
	}
}

func TestIsSlmState(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{".slmcode", true},
		{".slmcode/board.json", true},
		{".slmcode/agents/go-worker.yaml", true},
		{"pkg/.slmcode-notes.md", false},
		{"slmcode/board.json", false},
		{"cmd/slmcode/root.go", false},
	} {
		if got := isSlmState(c.path); got != c.want {
			t.Errorf("isSlmState(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestTallyBoardSeparatesForcedDoneFromVerifiedDone(t *testing.T) {
	board := plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColDone, Review: `{"approved":true,"score":90}`},
		{ID: "T2", Column: plan.ColDone, Review: "human mark_done after escalate"},
		{ID: "T3", Column: plan.ColToScope, Error: "rejected by evidence gate"},
		{ID: "T4", Column: plan.ColBlocked, Error: "aborted"},
		{ID: "T5", Column: plan.ColReadyToDev},
	}}
	got := tallyBoard(board)
	if got.total != 5 || got.done != 2 {
		t.Fatalf("total=%d done=%d, want 5/2", got.total, got.done)
	}
	if got.forced != 1 {
		t.Errorf("forced = %d, want 1 — a human override must not read as a verified pass", got.forced)
	}
	if got.escalated != 1 || got.blocked != 1 || got.untouched != 1 {
		t.Errorf("escalated=%d blocked=%d untouched=%d, want 1/1/1",
			got.escalated, got.blocked, got.untouched)
	}
	if got.stuck != "T3" {
		t.Errorf("stuck = %q, want T3 (the first task a human should look at)", got.stuck)
	}
}

// A task the run never reached has no verdict to read, so pointing the user at
// `task show` for it would be a dead end of its own.
func TestTallyBoardDoesNotCallUnattemptedTasksStuck(t *testing.T) {
	board := plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColReadyToDev},
		{ID: "T2", Column: plan.ColToScope},
	}}
	got := tallyBoard(board)
	if got.untouched != 2 || got.escalated != 0 {
		t.Fatalf("untouched=%d escalated=%d, want 2/0", got.untouched, got.escalated)
	}
	if got.stuck != "" {
		t.Errorf("stuck = %q, want empty", got.stuck)
	}
}

func TestTaskWasAttempted(t *testing.T) {
	for _, c := range []struct {
		name string
		task plan.Task
		want bool
	}{
		{"fresh", plan.Task{ID: "T1"}, false},
		{"retried", plan.Task{ID: "T1", Retries: 1}, true},
		{"has output", plan.Task{ID: "T1", Output: `{"status":"done"}`}, true},
		{"has review", plan.Task{ID: "T1", Review: "rejected"}, true},
		{"has error", plan.Task{ID: "T1", Error: "boom"}, true},
	} {
		if got := taskWasAttempted(c.task); got != c.want {
			t.Errorf("%s: taskWasAttempted = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestErrorsLogPathOnlyWhenItHoldsSomething(t *testing.T) {
	slm := t.TempDir()
	if got := errorsLogPath(slm); got != "" {
		t.Errorf("errorsLogPath with no file = %q, want empty", got)
	}
	path := filepath.Join(slm, "errors", "errors.md")
	write(t, path, "")
	if got := errorsLogPath(slm); got != "" {
		t.Errorf("errorsLogPath with an EMPTY file = %q, want empty", got)
	}
	write(t, path, "## failure\n")
	if got := errorsLogPath(slm); got != path {
		t.Errorf("errorsLogPath = %q, want %q", got, path)
	}
}

func TestFreshWorkspaceFilesAndPhantomBackups(t *testing.T) {
	slm := t.TempDir()
	// config.yaml pre-exists; pipeline.yaml does not.
	write(t, filepath.Join(slm, "config.yaml"), "provider: openai\n")
	fresh := freshWorkspaceFiles(slm)
	if len(fresh) != 1 || filepath.Base(fresh[0]) != "pipeline.yaml" {
		t.Fatalf("freshWorkspaceFiles = %v, want [pipeline.yaml]", fresh)
	}

	// Both grow a .bak; only the one for the file this init created is dropped.
	write(t, filepath.Join(slm, "config.yaml.bak"), "old\n")
	write(t, filepath.Join(slm, "pipeline.yaml.bak"), "phantom\n")
	dropPhantomBackups(fresh)

	if _, err := os.Stat(filepath.Join(slm, "config.yaml.bak")); err != nil {
		t.Errorf("a REAL backup of a pre-existing file was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(slm, "pipeline.yaml.bak")); !os.IsNotExist(err) {
		t.Errorf("phantom pipeline.yaml.bak survived: %v", err)
	}
}

func TestChangeHeadlineSingularAndPlural(t *testing.T) {
	if got := changeHeadline(1, 7, 0); !strings.Contains(got, "1 file ") {
		t.Errorf("changeHeadline(1,…) = %q, want a singular noun", got)
	}
	if got := changeHeadline(3, 47, 12); !strings.Contains(got, "3 files") {
		t.Errorf("changeHeadline(3,…) = %q, want a plural noun", got)
	}
	if got := changeHeadline(3, 47, 12); !strings.Contains(got, "+47") || !strings.Contains(got, "12") {
		t.Errorf("changeHeadline lost its counters: %q", got)
	}
}

// diffPaths lists the paths of a diff slice, for readable assertions.
func diffPaths(diffs []cli.FileDiff) []string {
	out := make([]string, 0, len(diffs))
	for _, fd := range diffs {
		out = append(out, fd.Path)
	}
	return out
}
