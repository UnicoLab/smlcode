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

// Once EVERY named deliverable exists, the objective being green is a
// meaningful answer again.
func TestBuiltDeliverableDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "cmd/server/main.go")
	touch(t, root, "pkg/tasks/store.go")
	touch(t, root, "pkg/tasks/store_test.go")
	board := &plan.Board{Query: buildRequest, Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColReadyToDev, Files: []string{"cmd/server/main.go"}},
	}}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("blocked with every named file present: %q", got)
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

// A DOCUMENT the request refers to is context, not a deliverable — the board
// never undertook to build it and it must not block.
func TestUnplannedDocumentMentionDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	board := &plan.Board{
		Query: "the behavior is described in docs/design.md — implement it in api.go",
		Tasks: []plan.Task{{ID: "T1", Column: plan.ColDone, Files: []string{"api.go"}}},
	}
	touch(t, root, "api.go")
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("a referenced document blocked the early stop: %q", got)
	}
}

// The measured failure this widening exists for.
//
// A full-stack request named a Go server, a Go store, Go tests AND
// web/src/TaskList.tsx. The board planned the Go half, the frontend task was
// dropped, and because NO task owned the .tsx the guard found nothing owed:
//
//	✔ … — 2/2 tasks done, 0 failed (objective met between waves —
//	  1 task(s) not executed)
//
// go build and go test were genuinely green and half the request did not exist.
func TestNamedSourceTargetNothingPlannedStillBlocks(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "cmd/server/main.go")
	touch(t, root, "pkg/tasks/store.go")
	// web/src/TaskList.tsx deliberately absent AND owned by no task.

	board := &plan.Board{
		Query: "a Go HTTP server in cmd/server/main.go with an in-memory store in " +
			"pkg/tasks/store.go, plus a React task list component in web/src/TaskList.tsx",
		Tasks: []plan.Task{
			{ID: "T1", Column: plan.ColDone, Files: []string{"cmd/server/main.go"}},
			{ID: "T2", Column: plan.ColDone, Files: []string{"pkg/tasks/store.go"}},
		},
	}
	got := unbuiltDeliverable(root, board.Query, board)
	if got == "" {
		t.Fatal("a named source deliverable that nothing planned did not block the early stop")
	}
	if !strings.Contains(got, "web/src/TaskList.tsx") {
		t.Errorf("the blocker does not name the missing file: %q", got)
	}
}

// Once the board DID build it, an unplanned-but-present path is not debt.
func TestNamedSourceTargetOnDiskDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "web/src/TaskList.tsx")
	board := &plan.Board{
		Query: "plus a React task list component in web/src/TaskList.tsx",
		Tasks: []plan.Task{{ID: "T1", Column: plan.ColDone, Files: []string{"other.go"}}},
	}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("a file that exists blocked the early stop: %q", got)
	}
}

// A finished task's own file is not owed — this guard is about work still
// outstanding, and a done task is the board saying it is not.
//
// (Whether that task TOLD THE TRUTH about writing the file is a different
// question, and the claims and disk-evidence gates are the ones that ask it.)
func TestDoneTaskIsNotOwed(t *testing.T) {
	root := t.TempDir()
	board := &plan.Board{
		Query: "Build a Go REST backend in cmd/server/main.go.",
		Tasks: []plan.Task{{ID: "T1", Column: plan.ColDone, Files: []string{"cmd/server/main.go"}}},
	}
	if got := unbuiltDeliverable(root, board.Query, board); got != "" {
		t.Fatalf("a done task blocked the early stop: %q", got)
	}
}

// The narrowing that keeps the widened rule honest: a DOCUMENT the request
// refers to is input, and its absence says nothing about whether the run built
// what it was asked to build.
func TestNamedDocumentIsNotADeliverable(t *testing.T) {
	for _, f := range []string{"docs/design.md", "notes.txt", "config.yaml", "data.json"} {
		if namedCodeDeliverable(f) {
			t.Errorf("%q counted as a code deliverable", f)
		}
	}
	for _, f := range []string{"web/src/TaskList.tsx", "pkg/tasks/store.go", "app/main.py"} {
		if !namedCodeDeliverable(f) {
			t.Errorf("%q is not counted as a code deliverable", f)
		}
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
