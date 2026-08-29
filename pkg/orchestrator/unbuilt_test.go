package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const buildRequest = "Build a Go REST backend in cmd/server/main.go with an " +
	"in-memory task store in pkg/tasks/store.go and Go unit tests in pkg/tasks/store_test.go."

// The measured failure: `go test ./...` went green over the one package that
// existed, the wave that would have written cmd/server/main.go never ran, and
// the run finished ✔ with the server absent — reported only as "not executed".
func TestRequestNamedDeliverableBlocksTheEarlyStop(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "pkg/tasks/store.go")
	touch(t, root, "pkg/tasks/store_test.go")
	// cmd/server/main.go deliberately absent.

	board := &plan.Board{Query: buildRequest, Tasks: []plan.Task{
		{ID: "T2", Column: plan.ColDone, Files: []string{"pkg/tasks/store.go"}},
		{ID: "T1", Column: plan.ColReadyToDev, Files: []string{"cmd/server/main.go"}},
	}}
	got := unbuiltDeliverable(root, board.Query, board)
	if got == "" {
		t.Fatal("the early stop was allowed with a request-named deliverable missing")
	}
	if !strings.Contains(got, "cmd/server/main.go") {
		t.Errorf("the blocker does not name the missing file: %q", got)
	}
}

// Once it exists, the objective being green is a meaningful answer again.
func TestBuiltDeliverableDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "cmd/server/main.go")
	touch(t, root, "pkg/tasks/store.go")
	board := &plan.Board{Query: buildRequest, Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColReadyToDev, Files: []string{"cmd/server/main.go"}},
	}}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("blocked with the file present: %q", got)
	}
}

// The early stop exists to skip work a green objective made redundant. A
// planner's own intermediate target is a guess, so it must NOT block — keying
// on every task's files would switch the optimization off entirely.
func TestPlannerGuessesDoNotBlock(t *testing.T) {
	root := t.TempDir()
	board := &plan.Board{
		Query: "make the tests pass",
		Tasks: []plan.Task{{ID: "T1", Column: plan.ColReadyToDev,
			Files: []string{"internal/helper/thing.go"}}},
	}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("a path the request never named blocked the early stop: %q", got)
	}
}

// A file the request mentions that nothing was planned for is not this run's
// debt — the board never undertook to build it.
func TestUnplannedMentionDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	board := &plan.Board{
		Query: "the behavior is described in docs/design.md — implement it in api.go",
		Tasks: []plan.Task{{ID: "T1", Column: plan.ColDone, Files: []string{"api.go"}}},
	}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("an unplanned mention blocked the early stop: %q", got)
	}
}

// A finished task's file is not owed, whatever the request said.
func TestDoneTaskIsNotOwed(t *testing.T) {
	root := t.TempDir()
	board := &plan.Board{Query: buildRequest, Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColDone, Files: []string{"cmd/server/main.go"}},
	}}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("a done task blocked the early stop: %q", got)
	}
}

func TestUnbuiltDeliverableIsSafeOnEmptyInput(t *testing.T) {
	if got := unbuiltDeliverable("", "q", &plan.Board{}); got != "" {
		t.Errorf("no root: %q", got)
	}
	if got := unbuiltDeliverable(t.TempDir(), "q", nil); got != "" {
		t.Errorf("nil board: %q", got)
	}
	if got := unbuiltDeliverable(t.TempDir(), "", &plan.Board{}); got != "" {
		t.Errorf("no query: %q", got)
	}
}
