package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Under `--isolate worktree` the work happens in a throwaway checkout and the
// harness's state stays in the operator's directory. scratchDir was derived
// from Root, so the checklist landed in the sandbox — where `git add -A` swept
// it into the commit that gets merged back.
//
// Measured, on a run whose commit should have held one source file:
//
//	.slmcode/scratch/TODO.md | 9 +++++++++
//	strutil.go               | 9 +++++++++
func TestTodoIsWrittenToTheStateDirNotTheSandbox(t *testing.T) {
	sandbox, state := t.TempDir(), t.TempDir()
	w := &Workspace{Root: sandbox, SlmDir: filepath.Join(state, ".slmcode")}

	w.SetTodos([]TodoItem{{Text: "write strutil.go"}})

	got := filepath.Join(state, ".slmcode", "scratch", "TODO.md")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("TODO.md is not in the state dir: %v", err)
	}
	if body, err := os.ReadFile(got); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(body), "write strutil.go") {
		t.Errorf("wrong content: %q", body)
	}
	// Nothing at all may appear under the sandbox — that is the whole point.
	if _, err := os.Stat(filepath.Join(sandbox, ".slmcode")); !os.IsNotExist(err) {
		t.Errorf(".slmcode leaked into the worktree the commit is taken from (err=%v)", err)
	}
}

// Without isolation the two are the same place, and the old layout must hold.
func TestTodoFallsBackToTheRootLayout(t *testing.T) {
	root := t.TempDir()
	(&Workspace{Root: root}).SetTodos([]TodoItem{{Text: "ship it"}})

	if _, err := os.Stat(filepath.Join(root, ".slmcode", "scratch", "TODO.md")); err != nil {
		t.Fatalf("the un-isolated layout changed: %v", err)
	}
}

// A Workspace with neither is a no-op rather than a write to the process cwd.
func TestTodoWritesNothingWithoutADirectory(t *testing.T) {
	(&Workspace{}).SetTodos([]TodoItem{{Text: "x"}})
	if _, err := os.Stat(filepath.Join(".slmcode", "scratch", "TODO.md")); !os.IsNotExist(err) {
		t.Errorf("wrote into the working directory (err=%v)", err)
	}
}
