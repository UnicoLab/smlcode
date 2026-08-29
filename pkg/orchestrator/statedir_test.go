package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// The half of the isolation contract that pkg/sandbox cannot test.
//
// pkg/sandbox/statedir_test.go proves everything derived from cfg.SlmDir()
// follows StateDir. The self-improvement engine does not go through SlmDir:
// pkg/memory joins <projectDir>/.slmcode/memory itself, so it follows whatever
// root the orchestrator hands it. Handed cfg.Root, an isolated run points the
// memory store, the repair-rule store and the bandit at the throwaway worktree.
//
// Measured on a live isolated run before this was fixed: a second memory store
// wrote into the worktree, its final WORKING.md landed AFTER cleanup and so
// re-created the directory git had just removed, and `git add -A` swept that
// .slmcode/ into the commit merged onto the operator's branch — nine harness
// state files in a commit that should have held one source change.
func TestEvolveStateFollowsStateDirNotRoot(t *testing.T) {
	origin := t.TempDir()
	worktree := t.TempDir()

	cfg := config.Default(worktree)
	cfg.StateDir = filepath.Join(origin, config.DirName)
	if !cfg.Evolve {
		t.Fatal("evolve is off by default — this test would prove nothing")
	}

	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	originMem := filepath.Join(origin, memory.SlmDirName, memory.DirName)
	if _, err := os.Stat(originMem); err != nil {
		t.Errorf("memory did not land in the pinned state directory %s: %v", originMem, err)
	}

	// The worktree is thrown away whole. Anything the engine writes there is
	// lost, and on a project that does not git-ignore .slmcode it is committed.
	worktreeState := filepath.Join(worktree, memory.SlmDirName)
	if _, err := os.Stat(worktreeState); !os.IsNotExist(err) {
		var found []string
		_ = filepath.Walk(worktreeState, func(p string, info os.FileInfo, _ error) error {
			if info != nil && !info.IsDir() {
				found = append(found, p)
			}
			return nil
		})
		t.Errorf("engine wrote harness state into the sandbox root %s (files: %v)", worktreeState, found)
	}
}

// With StateDir unset — every non-isolated run — the state root must still be
// Root, or this fix would relocate state for everybody.
func TestEvolveStateDefaultsToRoot(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)

	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	want := filepath.Join(root, memory.SlmDirName, memory.DirName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("memory did not land at the default location %s: %v", want, err)
	}
}
