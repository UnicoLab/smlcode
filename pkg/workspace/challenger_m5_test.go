package workspace

import (
	"context"
	"strings"
	"testing"
)

// TestEmpiricalTeamworkWorkspaceDefects isolates the confirmed security and anti-wander defects.
func TestEmpiricalTeamworkWorkspaceDefects(t *testing.T) {
	// 1. IsReadOnlyRole must recognize teamwork-review (hyphenated built-in agent id)
	t.Run("IsReadOnlyRole for teamwork-review and language variants", func(t *testing.T) {
		roles := []string{
			"teamwork-review",
			"teamwork_review",
			"go-teamwork-review",
			"py-teamwork-review",
			"ts-teamwork-review",
		}
		for _, r := range roles {
			if !IsReadOnlyRole(r) {
				t.Errorf("SECURITY BUG: IsReadOnlyRole(%q) = false; expected true! The built-in agent is %q and must be blocked from modifying files", r, r)
			}
		}
	})

	// 2. IsReadOnlyRole for planning roles
	t.Run("IsReadOnlyRole for teamwork-plan", func(t *testing.T) {
		roles := []string{
			"teamwork-plan",
			"teamwork_plan",
		}
		for _, r := range roles {
			if !IsReadOnlyRole(r) {
				t.Errorf("GAP: IsReadOnlyRole(%q) = false; planning/architect roles must be read-only", r)
			}
		}
	})

	// 3. Workspace checkFocus with context carrying teamwork-review
	t.Run("checkFocus blocks writes for teamwork-review", func(t *testing.T) {
		root := t.TempDir()
		w := &Workspace{Root: root}
		ctx := WithRole(context.Background(), "teamwork-review")

		err := w.checkFocus(ctx, "cmd/app/main.go")
		if err == nil {
			t.Fatalf("SECURITY VIOLATION: checkFocus allowed write to main.go for role teamwork-review!")
		}
		if !strings.Contains(err.Error(), "the teamwork-review role does not edit files at all") &&
			!strings.Contains(err.Error(), "role does not edit files at all") {
			t.Errorf("checkFocus error missing read-only refusal guidance: %v", err)
		}
	})

	// 4. FocusGuard.Check directly with context carrying teamwork-review
	t.Run("FocusGuard.Check blocks writes for teamwork-review", func(t *testing.T) {
		g := NewFocusGuard()
		// Even if main.go is in the focus list, read-only role must be blocked!
		g.SetWave([][]string{{"main.go"}})
		ctx := WithRole(context.Background(), "teamwork-review")

		err := g.Check(ctx, "main.go")
		if err == nil {
			t.Fatalf("SECURITY VIOLATION: FocusGuard.Check allowed write to focus file main.go for role teamwork-review!")
		}
		if !strings.Contains(err.Error(), "does not edit files at all") {
			t.Errorf("FocusGuard.Check error missing read-only refusal: %v", err)
		}
	})

	// 5. OutOfScopeReason for teamwork-review
	t.Run("OutOfScopeReason for teamwork-review", func(t *testing.T) {
		reason := OutOfScopeReason("teamwork-review", []string{"pkg/auth/token.go"})
		if !strings.Contains(reason, "does not edit files at all") {
			t.Errorf("OutOfScopeReason(%q) = %q; expected to state that role does not edit files at all", "teamwork-review", reason)
		}
	})
}
