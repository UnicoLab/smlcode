package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestChallenger1_ContextCeilingsAndBudgetStress tests document ceilings under extreme
// overflow, concurrent document appends, role budget normalization, and identifier-only packing.
func TestChallenger1_ContextCeilingsAndBudgetStress(t *testing.T) {
	// 1. Extreme Document Ceiling Overflow (100 KB on 24 KB ceiling)
	t.Run("doc_prd_extreme_ceiling_overflow", func(t *testing.T) {
		dir := t.TempDir()
		store := New(dir)

		// Append 100 timestamped sections of ~1000 bytes each (total > 100 KB)
		for i := 0; i < 100; i++ {
			title := fmt.Sprintf("Section %03d", i)
			body := fmt.Sprintf("Requirements specification payload %d: %s\n", i, strings.Repeat("A", 900))
			if err := store.Append(DocPRD, title, body); err != nil {
				t.Fatalf("Append failed at iteration %d: %v", i, err)
			}
		}

		// Read back PRD.md
		content, err := store.Read(DocPRD)
		if err != nil {
			t.Fatalf("Failed to read DocPRD: %v", err)
		}

		// Content length must not exceed PRDAppendMaxBytes (24 KB)
		if len(content) > PRDAppendMaxBytes {
			t.Fatalf("CEILING VIOLATION: DocPRD length %d exceeds PRDAppendMaxBytes (%d)", len(content), PRDAppendMaxBytes)
		}

		// Verify newest section (Section 099) is present
		if !strings.Contains(content, "Section 099") {
			t.Errorf("PRUNING BUG: newest section (Section 099) was unexpectedly dropped!")
		}

		// Verify oldest sections were pruned and pruning notice exists
		if !strings.Contains(content, "older auto-appended sections pruned") {
			t.Errorf("PRUNING BUG: missing pruning disclosure banner")
		}
		if strings.Contains(content, "Section 000") {
			t.Errorf("PRUNING BUG: oldest section (Section 000) was NOT pruned despite 100KB overflow!")
		}
	})

	// 2. Concurrent Document Appends under Race Contention
	t.Run("concurrent_appends_race_safety", func(t *testing.T) {
		dir := t.TempDir()
		store := New(dir)
		var wg sync.WaitGroup
		numWorkers := 20

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					title := fmt.Sprintf("Reviewer %d Run %d", workerID, i)
					body := fmt.Sprintf("Adversarial finding summary from worker %d iteration %d: %s\n",
						workerID, i, strings.Repeat("R", 300))
					_ = store.Append(DocReview, title, body)
				}
			}(w)
		}

		wg.Wait()

		// Read back DocReview
		content, err := store.Read(DocReview)
		if err != nil {
			t.Fatalf("Failed to read DocReview: %v", err)
		}

		// Verify length bounded by ReviewAppendMaxBytes (24 KB)
		if len(content) > ReviewAppendMaxBytes {
			t.Fatalf("CEILING VIOLATION: DocReview length %d exceeds ReviewAppendMaxBytes (%d)", len(content), ReviewAppendMaxBytes)
		}
	})

	// 3. Role Budget Normalization, Case Insensitivity, and Fallback
	t.Run("role_budget_normalization_and_fallback", func(t *testing.T) {
		cases := []struct {
			role     string
			expected int
		}{
			// Worker: 100%
			{"teamwork-worker", 100},
			{"teamwork_worker", 100},
			{"TEAMWORK-WORKER", 100},
			{"  teamwork-worker  ", 100},
			// Review: 85%
			{"teamwork-review", 85},
			{"teamwork_review", 85},
			{"TEAMWORK-REVIEW", 85},
			{"  teamwork-review  ", 85},
			{"adversarial_reviewer", 85},
			{"ADVERSARIAL_REVIEWER", 85},
			{"challenger", 85},
			// Plan: 70%
			{"teamwork-plan", 70},
			{"teamwork_plan", 70},
			{"TEAMWORK-PLAN", 70},
			{"  teamwork-plan  ", 70},
			// Spec: 60%
			{"teamwork-spec", 60},
			{"teamwork_spec", 60},
			{"TEAMWORK-SPEC", 60},
			{"  teamwork-spec  ", 60},
			{"interviewer", 60},
			{"specification", 60},
			// Default Fallback: 75%
			{"unknown_arbitrary_agent", 75},
			{"", 75},
			{"   ", 75},
		}

		for _, tc := range cases {
			got := RoleBudgetPercent(tc.role)
			if got != tc.expected {
				t.Errorf("RoleBudgetPercent(%q) = %d; want %d", tc.role, got, tc.expected)
			}
		}
	})

	// 4. Identifier-Only and Lean Roles Registration
	t.Run("identifier_only_and_lean_roles_registration", func(t *testing.T) {
		idRoles := DefaultIdentifierOnlyRoles()
		foundHyphen := false
		foundUnderscore := false
		for _, r := range idRoles {
			if strings.EqualFold(r, "teamwork-spec") {
				foundHyphen = true
			}
			if strings.EqualFold(r, "teamwork_spec") {
				foundUnderscore = true
			}
		}
		if !foundHyphen && !foundUnderscore {
			t.Errorf("DefaultIdentifierOnlyRoles() missing teamwork spec roles: %v", idRoles)
		}

		// isLeanRole assertions
		leanRoles := []string{
			"teamwork-worker", "teamwork_worker",
			"teamwork-review", "teamwork_review",
			"teamwork-plan", "teamwork_plan",
		}
		for _, r := range leanRoles {
			if !isLeanRole(r) {
				t.Errorf("isLeanRole(%q) = false; want true", r)
			}
		}
	})

	// 5. Direct File Mutation Defense on Store.WriteBounded
	t.Run("write_bounded_file_mutation_defense", func(t *testing.T) {
		dir := t.TempDir()
		store := New(dir)
		oversized := strings.Repeat("Z", 50*1024) // 50 KB

		err := store.WriteBounded(DocContract, oversized)
		if err != nil {
			t.Fatalf("WriteBounded returned error: %v", err)
		}

		fi, err := os.Stat(filepath.Join(dir, DocContract))
		if err != nil {
			t.Fatalf("Stat failed on DocContract: %v", err)
		}
		if fi.Size() > int64(ContractAppendMaxBytes) {
			t.Fatalf("WriteBounded allowed oversized file: %d bytes (ceiling %d)", fi.Size(), ContractAppendMaxBytes)
		}
	})
}
