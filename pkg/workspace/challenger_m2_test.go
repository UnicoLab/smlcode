package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmpiricalReadOnlyRoleRefusalAllTools tests all read-only roles against all workspace write tools.
func TestEmpiricalReadOnlyRoleRefusalAllTools(t *testing.T) {
	roles := []string{
		"interviewer",
		"specification",
		"spec",
		"interview",
		"adversarial_reviewer",
		"challenger",
		"teamwork_spec",
		"teamwork_review",
		// prefixed roles
		"go-interviewer",
		"py-challenger",
		"go-adversarial_reviewer",
		"ts-spec",
		"py-specification",
		// case & whitespace
		" INTERVIEWER ",
		"Challenger",
		"Spec",
		"ADVERSARIAL_REVIEWER",
	}

	for _, role := range roles {
		t.Run("role_"+strings.TrimSpace(role), func(t *testing.T) {
			root := t.TempDir()
			existingFile := filepath.Join(root, "target.go")
			if err := os.WriteFile(existingFile, []byte("package target\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Test 1: Workspace with nil Focus
			wNilFocus := &Workspace{Root: root, Focus: nil}
			ctx := WithRole(context.Background(), role)

			// 1.1 ws_write
			_, err := wNilFocus.writeFile(ctx, map[string]interface{}{
				"path": "new.go", "content": "package new\n",
			})
			assertReadOnlyRefusal(t, err, "writeFile (nil Focus)", role, "new.go")

			// 1.2 ws_edit
			wNilFocus.markRead("target.go")
			_, err = wNilFocus.editFile(ctx, map[string]interface{}{
				"path": "target.go", "old_str": "package target", "new_str": "package target // edit",
			})
			assertReadOnlyRefusal(t, err, "editFile (nil Focus)", role, "target.go")

			// 1.3 ws_patch
			_, err = wNilFocus.patchFile(ctx, map[string]interface{}{
				"path": "target.go", "diff": "--- target.go\n+++ target.go\n@@ -1,1 +1,2 @@\n package target\n+// comment\n",
			})
			assertReadOnlyRefusal(t, err, "patchFile (nil Focus)", role, "target.go")

			// 1.4 ws_mv
			_, err = wNilFocus.moveFile(ctx, map[string]interface{}{
				"from": "target.go", "to": "target_mv.go",
			})
			assertReadOnlyRefusal(t, err, "moveFile (nil Focus)", role, "target.go")

			// 1.5 ws_delete
			_, err = wNilFocus.deleteFile(ctx, map[string]interface{}{
				"path": "target.go",
			})
			assertReadOnlyRefusal(t, err, "deleteFile (nil Focus)", role, "target.go")

			// Test 1.6: Review and DryRun permission modes must also be refused
			wReview := &Workspace{Root: root, Permission: "review"}
			_, err = wReview.writeFile(ctx, map[string]interface{}{
				"path": "review_test.go", "content": "package review\n",
			})
			assertReadOnlyRefusal(t, err, "writeFile in review mode", role, "review_test.go")

			wDryRun := &Workspace{Root: root, Permission: "dry-run"}
			_, err = wDryRun.writeFile(ctx, map[string]interface{}{
				"path": "dryrun_test.go", "content": "package dryrun\n",
			})
			assertReadOnlyRefusal(t, err, "writeFile in dryrun mode", role, "dryrun_test.go")

			// Test 2: FocusGuard disabled (!g.Enabled())
			gDisabled := NewFocusGuard()
			if gDisabled.Enabled() {
				t.Fatal("expected guard to be disabled")
			}
			wDisabled := &Workspace{Root: root, Focus: gDisabled}

			_, err = wDisabled.writeFile(ctx, map[string]interface{}{
				"path": "new2.go", "content": "package new2\n",
			})
			assertReadOnlyRefusal(t, err, "writeFile (disabled guard)", role, "new2.go")

			// Test 3: Target path is IN focus list (g.files)
			gFocus := NewFocusGuard()
			gFocus.SetWave([][]string{{"target.go", "new3.go"}})
			if !gFocus.Allow("target.go") {
				t.Fatal("target.go should be allowed for regular worker")
			}
			wFocus := &Workspace{Root: root, Focus: gFocus}

			_, err = wFocus.writeFile(ctx, map[string]interface{}{
				"path": "new3.go", "content": "package new3\n",
			})
			assertReadOnlyRefusal(t, err, "writeFile (path in focus list)", role, "new3.go")

			// Test 4: FocusGuard.Check directly
			err = gDisabled.Check(ctx, "target.go")
			assertReadOnlyRefusal(t, err, "FocusGuard.Check (disabled)", role, "target.go")

			err = gFocus.Check(ctx, "target.go")
			assertReadOnlyRefusal(t, err, "FocusGuard.Check (in focus list)", role, "target.go")
		})
	}
}

func assertReadOnlyRefusal(t *testing.T, err error, op, role, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected read-only role refusal for role %q, got nil error", op, role)
	}
	errStr := err.Error()

	// Verify exact terminating instructions match readOnlyRoleRefusal
	expectedPrefix := "out-of-scope write blocked: " + path
	if !strings.HasPrefix(errStr, expectedPrefix) {
		t.Errorf("%s: error should start with %q, got: %q", op, expectedPrefix, errStr)
	}
	expectedTerminating := "Next action: stop calling edit/write tools and finish now — put the file, the symbol and the change that is needed in your answer"
	if !strings.Contains(errStr, expectedTerminating) {
		t.Errorf("%s: error missing terminating guidance: %q", op, errStr)
	}
	expectedContract := "role does not edit files at all"
	if !strings.Contains(errStr, expectedContract) {
		t.Errorf("%s: error missing contract explanation: %q", op, errStr)
	}
}

// TestEmpiricalAdaptiveToolOutputCappingBoundaries thoroughly stress-tests boundary limits for adaptive capping.
func TestEmpiricalAdaptiveToolOutputCappingBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		tokens      int
		legacyKB    int
		explicitCap int
		expectedCap int
	}{
		// Boundary <= 8192
		{"1 token", 1, 0, 0, MaxToolChars8K},
		{"4096 tokens", 4096, 0, 0, MaxToolChars8K},
		{"8192 boundary exact", 8192, 0, 0, MaxToolChars8K},

		// Boundary <= 16384
		{"8193 boundary +1", 8193, 0, 0, MaxToolChars16K},
		{"12000 tokens", 12000, 0, 0, MaxToolChars16K},
		{"16384 boundary exact", 16384, 0, 0, MaxToolChars16K},

		// Larger context (> 16384)
		{"16385 boundary +1", 16385, 0, 0, DefaultMaxToolChars},
		{"32768 tokens", 32768, 0, 0, DefaultMaxToolChars},
		{"131072 tokens", 131072, 0, 0, DefaultMaxToolChars},

		// Unconfigured / invalid
		{"0 tokens", 0, 0, 0, DefaultMaxToolChars},
		{"-5 tokens", -5, 0, 0, DefaultMaxToolChars},

		// Explicit override
		{"Explicit override overrides 8K", 8192, 0, 2500, 2500},
		{"Explicit override overrides 16K", 16384, 0, 6000, 6000},
		{"Explicit override overrides 32K", 32768, 0, 1500, 1500},
		{"Explicit override overrides zero", 0, 0, 999, 999},

		// Legacy KB fallback (tokens = 0)
		{"Legacy 16KB -> 4096 tokens <= 8K", 0, 16, 0, MaxToolChars8K},
		{"Legacy 32KB -> 8192 tokens <= 8K", 0, 32, 0, MaxToolChars8K},
		{"Legacy 48KB -> 12288 tokens <= 16K", 0, 48, 0, MaxToolChars16K},
		{"Legacy 64KB -> 16384 tokens <= 16K", 0, 64, 0, MaxToolChars16K},
		{"Legacy 128KB -> 32768 tokens > 16K", 0, 128, 0, DefaultMaxToolChars},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &Workspace{
				ContextLimitTokens: tc.tokens,
				MaxContextKB:       tc.legacyKB,
				MaxToolChars:       tc.explicitCap,
			}
			gotCap := w.maxToolChars()
			if gotCap != tc.expectedCap {
				t.Fatalf("w.maxToolChars() = %d, want %d", gotCap, tc.expectedCap)
			}
		})
	}
}

// TestEmpiricalConcurrencyStress verifies concurrent access across different roles and tools.
func TestEmpiricalConcurrencyStress(t *testing.T) {
	root := t.TempDir()
	g := NewFocusGuard()
	g.SetWave([][]string{{"allowed.go"}})
	w := &Workspace{Root: root, Focus: g}

	ctxWritable := WithRole(context.Background(), "worker")
	ctxReadOnly := WithRole(context.Background(), "interviewer")

	const goroutines = 50
	errCh := make(chan error, goroutines*2)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			path := fmt.Sprintf("file_%d.go", id)
			// Read-only role must fail
			err := w.checkFocus(ctxReadOnly, path)
			if err == nil || !strings.Contains(err.Error(), "the interviewer role does not edit files at all") {
				errCh <- fmt.Errorf("read-only goroutine %d did not get expected refusal: %v", id, err)
				return
			}
			errCh <- nil
		}(i)

		go func(id int) {
			// Writable role should pass for allowed.go
			err := w.checkFocus(ctxWritable, "allowed.go")
			if err != nil {
				errCh <- fmt.Errorf("writable goroutine %d failed for allowed file: %v", id, err)
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < goroutines*2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

// TestEmpiricalToolOutputTruncationNotice verifies truncation notice behavior on short vs long tool outputs.
func TestEmpiricalToolOutputTruncationNotice(t *testing.T) {
	w8K := &Workspace{ContextLimitTokens: 8192}
	cap8K := w8K.maxToolChars() // 3000

	// 1. Output below cap: should NOT be truncated and no notice added
	shortOutput := strings.Repeat("x", cap8K-10)
	execShort := w8K.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return shortOutput, nil
	})
	resShort, err := execShort(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sShort, ok := resShort.(string)
	if !ok || sShort != shortOutput {
		t.Fatalf("short output was altered: length %d vs %d", len(sShort), len(shortOutput))
	}
	if strings.Contains(sShort, "result truncated") {
		t.Fatalf("short output contains unexpected truncation notice")
	}

	// 2. Output exactly at cap: should NOT be truncated
	exactOutput := strings.Repeat("y", cap8K)
	execExact := w8K.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return exactOutput, nil
	})
	resExact, err := execExact(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sExact, ok := resExact.(string)
	if !ok || sExact != exactOutput {
		t.Fatalf("exact output was altered")
	}
	if strings.Contains(sExact, "result truncated") {
		t.Fatalf("exact output contains unexpected truncation notice")
	}

	// 3. Output above cap: truncated with notice
	longOutput := strings.Repeat("z", 10000)
	execLong := w8K.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return longOutput, nil
	})
	resLong, err := execLong(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sLong, ok := resLong.(string)
	if !ok {
		t.Fatalf("expected string result")
	}

	expectedNotice := fmt.Sprintf("[result truncated: %d chars total, %d shown]", len(longOutput), cap8K)
	if !strings.Contains(sLong, expectedNotice) {
		t.Fatalf("missing expected notice %q in:\n%s", expectedNotice, sLong[len(sLong)-500:])
	}
	if !strings.Contains(sLong, "NARROW THE QUERY — do not re-run this call unchanged") {
		t.Fatalf("missing narrowing instructions in truncation footer")
	}
}
