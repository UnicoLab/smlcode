package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestChallenger1_WorkspaceBoundaryAndBypassAttacks challenges the workspace isolation,
// FocusGuard, path traversal protection, symlink escape defenses, and role normalization.
func TestChallenger1_WorkspaceBoundaryAndBypassAttacks(t *testing.T) {
	ctx := context.Background()

	// 1. Role Normalization Adversarial Mutation Suite
	t.Run("role_normalization_adversarial_mutations", func(t *testing.T) {
		readOnlyMutations := []string{
			// Whitespace variations
			"  teamwork-review  ",
			"\t\nteamwork-spec\r\n",
			"   teamwork_plan   ",
			" \t interviewer \t ",
			"   adversarial_reviewer   ",
			"  challenger  ",
			// Case variations
			"TEAMWORK-REVIEW",
			"Teamwork-Spec",
			"TEAMWORK_PLAN",
			"INTERVIEWER",
			"Adversarial_Reviewer",
			"CHALLENGER",
			// Language prefixes with hyphens
			"go-teamwork-review",
			"py-teamwork-spec",
			"ts-teamwork-plan",
			"rust-interviewer",
			"cpp-challenger",
			"custom-lang-teamwork-review",
			// Underscored variants
			"go-teamwork_review",
			"py-teamwork_spec",
			"ts-teamwork_plan",
		}

		for _, role := range readOnlyMutations {
			if !IsReadOnlyRole(role) {
				t.Errorf("BYPASS DEFECT: IsReadOnlyRole(%q) returned false; expected true", role)
			}
			// Verify FocusGuard.Check blocks write even if file is in focus
			g := NewFocusGuard()
			g.SetWave([][]string{{"service.go"}})
			err := g.Check(WithRole(ctx, role), "service.go")
			if err == nil {
				t.Errorf("BYPASS DEFECT: FocusGuard.Check allowed write for read-only role %q", role)
			}
			if !strings.Contains(err.Error(), "does not edit files at all") {
				t.Errorf("Refusal message missing read-only contract for %q: %v", role, err)
			}
		}
	})

	// 2. Directory Traversal and Escape Bypass Attempts
	t.Run("directory_traversal_and_escape_bypasses", func(t *testing.T) {
		root := t.TempDir()
		g := NewFocusGuard()
		g.SetWave([][]string{{"pkg/auth/token.go"}})
		w := &Workspace{Root: root, Focus: g}

		// Traversal paths that attempt to escape outside workspace or access sensitive areas
		traversals := []string{
			"../outside.go",
			"../../etc/passwd",
			"pkg/auth/../../outside.go",
			"./../evil.sh",
			"..",
			"pkg/auth/../../../secret.txt",
			".slmcode/hooks.json",
			".slmcode/config.yaml",
			"./.slmcode/secret",
		}

		for _, p := range traversals {
			// Test via FocusGuard.Check
			err := g.Check(WithRole(ctx, "worker"), p)
			if err == nil {
				t.Errorf("SECURITY ESCAPE: FocusGuard.Check allowed traversal path: %q", p)
			}

			// Test via Workspace.writeFile
			_, err = w.writeFile(WithRole(ctx, "worker"), map[string]interface{}{
				"path":    p,
				"content": "malicious payload",
			})
			if err == nil {
				t.Errorf("SECURITY ESCAPE: Workspace.writeFile allowed traversal path: %q", p)
			}
		}
	})

	// 3. Symlink Escape Attempt Defense
	t.Run("symlink_escape_attempt_defense", func(t *testing.T) {
		root := t.TempDir()
		outsideDir := t.TempDir()
		secretFile := filepath.Join(outsideDir, "secret.key")
		if err := os.WriteFile(secretFile, []byte("super_secret_value"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Create a symlink inside the workspace pointing outside
		symlinkPath := filepath.Join(root, "sym_escape")
		if err := os.Symlink(outsideDir, symlinkPath); err != nil {
			t.Skipf("Symlink creation not supported: %v", err)
		}

		w := &Workspace{Root: root}

		// Attempt to write through symlink to overwrite outside secret
		_, err := w.writeFile(WithRole(ctx, "worker"), map[string]interface{}{
			"path":    "sym_escape/secret.key",
			"content": "overwritten",
		})
		if err == nil {
			t.Errorf("CRITICAL SECURITY DEFECT: Workspace allowed writing through symlink pointing outside root!")
		}

		// Verify outside file was NOT modified
		content, _ := os.ReadFile(secretFile)
		if string(content) != "super_secret_value" {
			t.Errorf("CRITICAL SECURITY DEFECT: Outside file was modified via symlink escape: %q", string(content))
		}
	})

	// 4. Adaptive Tool Cap Precision and UTF-8 Boundary Stress
	t.Run("tool_output_cap_precision_and_utf8", func(t *testing.T) {
		// Verify AdaptiveMaxToolChars at various window sizes
		caps := map[int]int{
			4096:   MaxToolChars8K,
			8192:   MaxToolChars8K,
			16384:  MaxToolChars16K,
			32768:  DefaultMaxToolChars,
			65536:  DefaultMaxToolChars,
			131072: DefaultMaxToolChars,
		}
		for win, expected := range caps {
			if got := AdaptiveMaxToolChars(win); got != expected {
				t.Errorf("AdaptiveMaxToolChars(%d) = %d; want %d", win, got, expected)
			}
		}

		// Zero and negative window sizes must fallback safely
		if got := AdaptiveMaxToolChars(0); got != DefaultMaxToolChars {
			t.Errorf("AdaptiveMaxToolChars(0) = %d; want default %d", got, DefaultMaxToolChars)
		}
		if got := AdaptiveMaxToolChars(-100); got != DefaultMaxToolChars {
			t.Errorf("AdaptiveMaxToolChars(-100) = %d; want default %d", got, DefaultMaxToolChars)
		}

		// Capped output with large content
		w := &Workspace{ContextLimitTokens: 8192}
		longOutput := strings.Repeat("x", 10000)
		wrapped := w.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			return longOutput, nil
		})
		res, err := wrapped(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		strRes := res.(string)
		if len(strRes) >= len(longOutput) {
			t.Errorf("w.capped did not truncate output: len = %d", len(strRes))
		}
		if !strings.Contains(strRes, "result truncated") {
			t.Errorf("w.capped output missing truncation indicator")
		}
	})

	// 5. Concurrency Race Stress on FocusGuard
	t.Run("focus_guard_concurrent_waves_and_checks", func(t *testing.T) {
		g := NewFocusGuard()
		var wg sync.WaitGroup
		iterations := 100

		// Goroutine continuously updating waves
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				f1 := fmt.Sprintf("pkg/file_%d.go", i%5)
				f2 := fmt.Sprintf("pkg/file_%d.go", (i+1)%5)
				g.SetWave([][]string{{f1, f2}})
			}
		}()

		// Multiple goroutines concurrently performing checks with various roles
		for workerID := 0; workerID < 10; workerID++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					target := fmt.Sprintf("pkg/file_%d.go", i%5)
					role := "worker"
					if (i+id)%2 == 0 {
						role = "teamwork-review"
					}
					_ = g.Check(WithRole(ctx, role), target)
					_ = g.Allow(target)
					_ = g.Enabled()
				}
			}(workerID)
		}

		wg.Wait()
	})
}
