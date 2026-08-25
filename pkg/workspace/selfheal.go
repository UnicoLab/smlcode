package workspace

import (
	"os"
	"path/filepath"
)

// Undoing a shell write to a PROTECTED file, rather than only reporting it.
//
// MEASURED, 2026-08-25: on the honest-failure scenario — whose query says in
// plain words "You may not edit, add, delete or skip any _test.go file" — a
// Qwen3-Coder-30B run made 142 tool calls and modified mathx/add_test.go. The
// harness detected it, raised the violation, and left the edited file on disk.
// The task was impossible, and the model made it possible by changing the test.
//
// WHY THIS IS NARROWER THAN "REVERT SHELL WRITES". reportShellScope
// deliberately does NOT revert in general, and that reasoning still holds: a
// command's own build output is indistinguishable after the fact from a stray
// write, so undoing everything a shell touched would delete legitimate work.
//
// Two things make a PROTECTED path different, and both must hold:
//
//   1. There is no ambiguity about intent. The task was told not to write here.
//      No write to this path is legitimate, so there is no good change to lose.
//   2. We have the exact prior bytes. The checkpointer snapshots protected
//      files before the wave that protects them, so restoring is returning the
//      file to a state we recorded, not reconstructing a guess.
//
// Without (2) this would be deletion, not repair — which is why a path with no
// backup is reported and left alone rather than truncated or removed.

// healProtectedWrites restores protected files a shell command modified.
//
// Returns the paths actually restored, so the caller can tell the model what
// was undone. A path is skipped — reported, never guessed at — when no
// pre-violation snapshot exists.
func (w *Workspace) healProtectedWrites(events []ShellScopeEvent) []string {
	if w == nil || w.Checkpointer == nil {
		return nil
	}
	var healed []string
	for _, e := range events {
		if !e.Protected {
			continue
		}
		// A harness-state path is protected too, but .slmcode/ is the harness's
		// own bookkeeping and is not restored from a task checkpoint: its
		// writers are the harness itself, and rolling one back mid-run would
		// corrupt state the run is still using.
		if IsHarnessStatePath(e.Path) {
			continue
		}
		if err := w.Checkpointer.Restore(e.Path); err != nil {
			continue
		}
		healed = append(healed, e.Path)
	}
	return healed
}

// SnapshotProtected backs up every existing file matching the guard's
// protections, so a later violation can be undone.
//
// Called when a wave installs its protections — before any agent in that wave
// runs, which is the only moment the file is known to be untouched. Bounded by
// maxProtectedSnapshots: a protection pattern can in principle match a large
// tree, and a backup sweep is not worth turning into its own cost.
func (w *Workspace) SnapshotProtected(patterns []string) int {
	if w == nil || w.Checkpointer == nil || len(patterns) == 0 {
		return 0
	}
	n := 0
	root := w.Root
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || n >= maxProtectedSnapshots {
			if n >= maxProtectedSnapshots {
				return filepath.SkipAll
			}
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = normalizeRel(rel)
		if IsHarnessStatePath(rel) || !IsProtectedPath(w.Focus, rel) {
			return nil
		}
		w.Checkpointer.BackupIfNeeded(rel)
		n++
		return nil
	})
	return n
}

// maxProtectedSnapshots bounds the pre-wave backup sweep.
//
// A pattern like "*_test.go" matches every test file in a repository, and this
// runs per wave. Two hundred covers any realistic protected set — the patterns
// come from a task's own prohibitions, which name specific files or one
// extension — while keeping a pathological pattern from turning a guard into a
// full-tree copy.
const maxProtectedSnapshots = 200
