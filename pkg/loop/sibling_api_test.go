package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func writeSrc(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The exact failure this exists for. A worker scoped to cmd/server/main.go used
// task.Title / CreatedAt / UpdatedAt on a Task a sibling task had defined as
// {ID, Description, Status}. pkg/tasks compiled and its tests passed; the
// server did not build, and the task retried to its ceiling re-deriving the
// same invented API from the same absent information.
func TestSiblingAPIShowsWhatAnotherTaskDefined(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "pkg/tasks/types.go", `package tasks

// Task represents a task in the system
type Task struct {
	ID          string
	Description string
	Status      string
}

func NewTask(id string) Task { return Task{ID: id} }
`)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"pkg/tasks/types.go"}},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	r := &Runner{Root: root}

	got := r.siblingAPISection(board, board.Tasks[0])
	if got == "" {
		t.Fatal("no section — the worker is left to guess the sibling's API")
	}
	if !strings.Contains(got, "pkg/tasks/types.go") {
		t.Errorf("the file is not named:\n%s", got)
	}
	for _, want := range []string{"Task", "NewTask"} {
		if !strings.Contains(got, want) {
			t.Errorf("exported %q is missing:\n%s", want, got)
		}
	}
	// And it must say not to edit files outside the focus list, or the fix for
	// a mismatch becomes "rewrite the other task's work".
	if !strings.Contains(got, "do not edit these files") {
		t.Errorf("the section does not warn against editing them:\n%s", got)
	}
}

// A task's own files are ones it will read anyway; repeating them spends a
// small model's window on nothing.
func TestSiblingAPISkipsTheTasksOwnFiles(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "a.go", "package a\n\nfunc Exported() {}\n")
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"a.go"}},
		{ID: "T2", Role: plan.RoleWorker, Files: []string{"a.go"}},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	r := &Runner{Root: root}
	if got := r.siblingAPISection(board, board.Tasks[0]); got != "" {
		t.Errorf("got a section for the task's own file:\n%s", got)
	}
}

// Files that do not exist yet say nothing, and must not produce an empty
// heading that costs window and carries no information.
func TestSiblingAPIIsSilentWithoutFilesOnDisk(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Role: plan.RoleWorker, Files: []string{"pkg/tasks/types.go"}},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	r := &Runner{Root: t.TempDir()}
	if got := r.siblingAPISection(board, board.Tasks[0]); got != "" {
		t.Errorf("got a section for files that do not exist:\n%s", got)
	}
}

// A tester runs a command and an explorer is already reading the tree; neither
// needs somebody else's signatures.
func TestSiblingAPIIsForImplementersOnly(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "pkg/x/x.go", "package x\n\nfunc Exported() {}\n")
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleTester, Files: []string{"x_test.go"}},
		{ID: "T2", Role: plan.RoleWorker, Files: []string{"pkg/x/x.go"}},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	r := &Runner{Root: root}
	if got := r.siblingAPISection(board, board.Tasks[0]); got != "" {
		t.Errorf("a tester got an API section:\n%s", got)
	}
}

// The section is part of a prompt whose prefix should stay byte-identical
// across calls, or the local KV cache stops hitting.
func TestSiblingAPIIsDeterministic(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"pkg/a/a.go", "pkg/b/b.go", "pkg/c/c.go"} {
		writeSrc(t, root, f, "package p\n\nfunc Exported() {}\n")
	}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"main.go"}},
		{ID: "T2", Role: plan.RoleWorker, Files: []string{"pkg/a/a.go", "pkg/b/b.go", "pkg/c/c.go"}},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	r := &Runner{Root: root}
	first := r.siblingAPISection(board, board.Tasks[0])
	for i := 0; i < 20; i++ {
		if got := r.siblingAPISection(board, board.Tasks[0]); got != first {
			t.Fatalf("unstable output:\n%q\nthen\n%q", first, got)
		}
	}
}

// It competes with the code for the window, so it stays bounded.
func TestSiblingAPIIsBounded(t *testing.T) {
	root := t.TempDir()
	var big strings.Builder
	big.WriteString("package p\n")
	for i := 0; i < 200; i++ {
		big.WriteString("func Exported")
		big.WriteString(strings.Repeat("X", i%20+1))
		big.WriteString("() {}\n")
	}
	sibling := plan.Task{ID: "T2", Role: plan.RoleWorker}
	for i := 0; i < 20; i++ {
		rel := filepath.ToSlash(filepath.Join("pkg", "p", "f"+strings.Repeat("a", i+1)+".go"))
		writeSrc(t, root, rel, big.String())
		sibling.Files = append(sibling.Files, rel)
	}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"main.go"}}, sibling,
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	got := (&Runner{Root: root}).siblingAPISection(board, board.Tasks[0])
	if n := strings.Count(got, "\n- "); n > siblingAPIMaxFiles {
		t.Errorf("summarized %d files, want at most %d", n, siblingAPIMaxFiles)
	}
	if len(got) > siblingAPIMaxBytes*3 {
		t.Errorf("section is %d bytes — unbounded growth", len(got))
	}
}
