package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Consecutive edits to one file used to spawn two checkers per edit (the
// "before" of edit N+1 is the "after" of edit N, re-parsed from scratch). The
// verdict is now memoized by content hash per path.
func TestSyntaxGuardMemoizesBeforeVerdict(t *testing.T) {
	var calls atomic.Int32
	orig := checkSyntaxFn
	checkSyntaxFn = func(_ context.Context, abs string, _ time.Duration) SyntaxResult {
		calls.Add(1)
		data, _ := os.ReadFile(abs) //nolint:gosec // test-owned temp path
		if strings.Contains(string(data), "BROKEN") {
			return SyntaxResult{Status: SyntaxBroken, Errors: "fake: broken", Tool: "fake"}
		}
		return SyntaxResult{Status: SyntaxOK, Tool: "fake"}
	}
	t.Cleanup(func() { checkSyntaxFn = orig })

	w, root := newTestWS(t)
	w.SyntaxCheck = true
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nvar A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.markRead("a.go")

	edit := func(oldS, newS string) string {
		return strOut(w.editFile(ctx, map[string]interface{}{"path": "a.go", "old_str": oldS, "new_str": newS}))
	}
	if out := edit("var A = 1", "var A = 2"); !strings.HasPrefix(out, "edited a.go") {
		t.Fatalf("edit 1: %q", out)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("first edit spawned %d checkers, want 2 (before + after)", got)
	}
	if out := edit("var A = 2", "var A = 3"); !strings.HasPrefix(out, "edited a.go") {
		t.Fatalf("edit 2: %q", out)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("two consecutive edits spawned %d checkers, want 3 (the before-verdict was memoized)", got)
	}

	// A revert leaves the file at prev, whose verdict is still memoized: the
	// next good edit costs one checker again.
	if out := edit("var A = 3", "BROKEN"); !strings.Contains(out, "EDIT REVERTED") {
		t.Fatalf("broken edit not reverted: %q", out)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("reverted edit spawned %d checkers total, want 4", got)
	}
	if out := edit("var A = 3", "var A = 4"); !strings.HasPrefix(out, "edited a.go") {
		t.Fatalf("edit after revert: %q", out)
	}
	if got := calls.Load(); got != 5 {
		t.Fatalf("edit after revert spawned %d checkers total, want 5", got)
	}

	// Content changed behind the memo (a shell command, another worker): the
	// hash misses and the before-check runs again rather than trusting a
	// stale verdict.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nvar A = 40\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := edit("var A = 40", "var A = 41"); !strings.HasPrefix(out, "edited a.go") {
		t.Fatalf("edit after external change: %q", out)
	}
	if got := calls.Load(); got != 7 {
		t.Fatalf("edit after external change spawned %d checkers total, want 7", got)
	}
}
