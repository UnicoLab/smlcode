package contextstore

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestChallengerRoleBudgetPercent comprehensively tests all casing, trimming,
// and edge cases for RoleBudgetPercent.
func TestChallengerRoleBudgetPercent(t *testing.T) {
	cases := []struct {
		role string
		want int
	}{
		// Specification / Interviewer roles (60%)
		{"interviewer", 60},
		{"INTERVIEWER", 60},
		{"InterViewer", 60},
		{" interviewer ", 60},
		{"\tinterviewer\n", 60},
		{"spec", 60},
		{"SPEC", 60},
		{" sPeC ", 60},
		{"\tspec\r\n", 60},
		{"specification", 60},
		{"SPECIFICATION", 60},
		{" Specification ", 60},
		{"\tspecification\n", 60},
		{"explorer", 60},
		{"context", 60},
		{"docs", 60},

		// Review & adversarial verification roles (85%)
		{"adversarial_reviewer", 85},
		{"ADVERSARIAL_REVIEWER", 85},
		{" Adversarial_Reviewer ", 85},
		{"\tadversarial_reviewer\t", 85},
		{"challenger", 85},
		{"CHALLENGER", 85},
		{" Challenger ", 85},
		{"\t\nchallenger\r\n", 85},
		{"reviewer", 85},
		{"tester", 85},

		// Implementation roles (100%)
		{"worker", 100},
		{"WORKER", 100},
		{"corrector", 100},
		{"deep", 100},
		{"placeholder", 100},

		// Planning roles (70%)
		{"architect", 70},
		{"planner", 70},
		{"splitter", 70},

		// Coordination & Memory roles (50%)
		{"coordinator", 50},
		{"memory", 50},

		// Fallback / Unknown roles (75%)
		{"", 75},
		{"   ", 75},
		{"\t\n", 75},
		{"unknown", 75},
		{"random_agent", 75},
		{"adversarial-reviewer", 75}, // hyphenated is not the registered underscore role
		{"interview", 75},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.role), func(t *testing.T) {
			got := RoleBudgetPercent(tc.role)
			if got != tc.want {
				t.Errorf("RoleBudgetPercent(%q) = %d; want %d", tc.role, got, tc.want)
			}
		})
	}
}

// TestChallengerIdentifierOnlyAndLeanRoles verifies DefaultIdentifierOnlyRoles
// and isLeanRole classifications.
func TestChallengerIdentifierOnlyAndLeanRoles(t *testing.T) {
	identRoles := DefaultIdentifierOnlyRoles()

	// Must include specification and interview roles
	expectedIdent := []string{
		"interviewer", "specification", "spec",
		"explorer", "docs", "context", "architect", "coordinator", "memory",
	}
	for _, exp := range expectedIdent {
		found := false
		for _, r := range identRoles {
			if strings.EqualFold(strings.TrimSpace(r), exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultIdentifierOnlyRoles() missing %q; got %v", exp, identRoles)
		}
	}

	// Verify isLeanRole
	leanCases := []struct {
		role string
		want bool
	}{
		{"adversarial_reviewer", true},
		{"ADVERSARIAL_REVIEWER", true},
		{" Adversarial_Reviewer ", true},
		{"challenger", true},
		{"CHALLENGER", true},
		{" Challenger ", true},
		{"worker", true},
		{"reviewer", true},
		{"tester", true},
		{"planner", true},
		{"interviewer", false},
		{"specification", false},
		{"spec", false},
		{"unknown", false},
	}

	for _, tc := range leanCases {
		t.Run("lean_"+tc.role, func(t *testing.T) {
			got := isLeanRole(tc.role)
			if got != tc.want {
				t.Errorf("isLeanRole(%q) = %v; want %v", tc.role, got, tc.want)
			}
		})
	}
}

// TestChallengerCeilingsAndPruningEdgeCases stress tests timestamp pruning,
// document ceilings, boundaries, and fallbacks.
func TestChallengerCeilingsAndPruningEdgeCases(t *testing.T) {
	head := "# Project Specification\n\n## Overview\nThis is the critical header that must survive pruning.\n\n"

	t.Run("chronological_order_pruning", func(t *testing.T) {
		// Construct 10 sections with distinct chronological dates
		var b strings.Builder
		b.WriteString(head)
		for i := 1; i <= 10; i++ {
			fmt.Fprintf(&b, "## Auto-learned (2026-01-%02dT12:00:00Z)\n\nSection content for item %d. %s\n\n",
				i, i, strings.Repeat("a", 500))
		}
		fullDoc := b.String()

		// Prune to a budget that fits approximately 3 sections plus head (~2200 bytes)
		capped := PruneTimestampedSections(fullDoc, 2500)
		if len(capped) > 2500 {
			t.Fatalf("pruned doc length %d exceeds cap 2500", len(capped))
		}
		if !strings.Contains(capped, "critical header that must survive") {
			t.Fatal("pruning destroyed document head")
		}
		// Oldest sections (1, 2, 3...) must be dropped
		if strings.Contains(capped, "Section content for item 1.") {
			t.Errorf("oldest section 1 was not pruned")
		}
		if strings.Contains(capped, "Section content for item 2.") {
			t.Errorf("oldest section 2 was not pruned")
		}
		// Newest section (10) must survive
		if !strings.Contains(capped, "Section content for item 10.") {
			t.Errorf("newest section 10 was unexpectedly dropped")
		}
		// Check valid UTF-8
		if !utf8.ValidString(capped) {
			t.Fatal("pruned content contains invalid UTF-8")
		}
	})

	t.Run("content_exactly_at_ceiling", func(t *testing.T) {
		exactDoc := head + strings.Repeat("x", 1000)
		ceiling := len(exactDoc)
		got := PruneTimestampedSections(exactDoc, ceiling)
		if got != exactDoc {
			t.Fatalf("expected exact content match when len == ceiling, got diff (len %d vs %d)", len(got), ceiling)
		}
	})

	t.Run("empty_content", func(t *testing.T) {
		got := PruneTimestampedSections("", 1000)
		if got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("no_timestamped_sections_fallback", func(t *testing.T) {
		unstructured := head + strings.Repeat("Plain unstructured paragraph text.\n", 200) // ~7 KB
		capBytes := 2000
		got := PruneTimestampedSections(unstructured, capBytes)
		if len(got) > capBytes {
			t.Fatalf("len(got) = %d > %d", len(got), capBytes)
		}
		if !strings.Contains(got, "critical header that must survive") {
			t.Fatal("document head lost in unstructured truncation")
		}
		if !strings.Contains(got, "trimmed to fit the context budget") {
			t.Fatal("expected fallback truncation notice")
		}
	})

	t.Run("head_exceeds_ceiling", func(t *testing.T) {
		hugeHead := "# Header\n\n" + strings.Repeat("A very long overview.\n", 500) // ~11 KB
		doc := hugeHead + "\n\n## Auto-learned (2026-01-01T10:00:00Z)\nSome section\n"
		capBytes := 2000
		got := PruneTimestampedSections(doc, capBytes)
		if len(got) > capBytes {
			t.Fatalf("len(got) = %d > %d", len(got), capBytes)
		}
		if !strings.Contains(got, "# Header") {
			t.Fatal("document start lost when head exceeds ceiling")
		}
	})

	t.Run("single_section_exceeds_ceiling", func(t *testing.T) {
		doc := head + "\n\n## Auto-learned (2026-01-01T10:00:00Z)\n" + strings.Repeat("huge section body\n", 400)
		capBytes := 1500
		got := PruneTimestampedSections(doc, capBytes)
		if len(got) > capBytes {
			t.Fatalf("len(got) = %d > %d", len(got), capBytes)
		}
		if !strings.Contains(got, "critical header") {
			t.Fatal("document head lost")
		}
	})

	t.Run("multibyte_utf8_truncation", func(t *testing.T) {
		multibyte := head + strings.Repeat("🚀 日本語 café résumé ", 100)
		capBytes := len(head) + 50 // cuts midway through multibyte rune sequence
		got := PruneTimestampedSections(multibyte, capBytes)
		if len(got) > capBytes {
			t.Fatalf("len(got) = %d > %d", len(got), capBytes)
		}
		if !utf8.ValidString(got) {
			t.Fatal("prune created invalid UTF-8 byte sequence")
		}
	})

	t.Run("zero_and_negative_cap", func(t *testing.T) {
		doc := head + "## Auto-learned (2026-01-01T10:00:00Z)\nBody\n"
		if got := PruneTimestampedSections(doc, 0); got != doc {
			t.Fatalf("cap 0 should be no-op, got len %d vs %d", len(got), len(doc))
		}
		if got := PruneTimestampedSections(doc, -100); got != doc {
			t.Fatalf("negative cap should be no-op, got len %d vs %d", len(got), len(doc))
		}
	})
}

// TestChallengerStoreBoundariesStress runs repeated appends and replacements
// across PRD.md, CONTRACT.md, and REVIEW.md to empirically stress-test bounds.
func TestChallengerStoreBoundariesStress(t *testing.T) {
	_, _, store := newWorkspace(t)

	// Document ceilings to test
	docs := []struct {
		name    string
		ceiling int
	}{
		{DocPRD, 24 * 1024},
		{DocContract, 16 * 1024},
		{DocReview, 24 * 1024},
	}

	for _, d := range docs {
		t.Run("stress_"+d.name, func(t *testing.T) {
			initContent := fmt.Sprintf("# %s\n\n## Overview\nInitial immutable overview for %s.\n", d.name, d.name)
			if err := store.Write(d.name, initContent); err != nil {
				t.Fatalf("initial write failed: %v", err)
			}

			// Perform 80 appends of 500 bytes each = ~40 KB (exceeds all ceilings)
			for i := 0; i < 80; i++ {
				body := fmt.Sprintf("Append run %d: %s", i, strings.Repeat("w", 500))
				if err := store.Append(d.name, "Run Log", body); err != nil {
					t.Fatalf("append %d failed: %v", i, err)
				}
				// Verify ceiling at intermediate steps
				if i%10 == 0 {
					content, err := store.Read(d.name)
					if err != nil {
						t.Fatalf("read failed at step %d: %v", i, err)
					}
					if len(content) > d.ceiling {
						t.Fatalf("%s exceeded ceiling at step %d: %d bytes > %d", d.name, i, len(content), d.ceiling)
					}
				}
			}

			// Final size check
			finalContent, err := store.Read(d.name)
			if err != nil {
				t.Fatalf("read final content failed: %v", err)
			}
			if len(finalContent) > d.ceiling {
				t.Fatalf("%s final size %d exceeds ceiling %d", d.name, len(finalContent), d.ceiling)
			}
			if !strings.Contains(finalContent, "Initial immutable overview") {
				t.Fatalf("%s lost its overview header after repeated appends", d.name)
			}

			// Now test WriteBounded with an oversized payload
			oversized := initContent
			for i := 0; i < 60; i++ {
				oversized += fmt.Sprintf("\n\n## Auto-learned (2026-02-%02dT10:00:00Z)\n\nEntry %d %s",
					(i%28)+1, i, strings.Repeat("z", 700))
			}
			if err := store.WriteBounded(d.name, oversized); err != nil {
				t.Fatalf("WriteBounded failed: %v", err)
			}
			boundedRead, err := store.Read(d.name)
			if err != nil {
				t.Fatalf("read bounded failed: %v", err)
			}
			if len(boundedRead) > d.ceiling {
				t.Fatalf("%s WriteBounded size %d exceeds ceiling %d", d.name, len(boundedRead), d.ceiling)
			}
			if !strings.Contains(boundedRead, "Initial immutable overview") {
				t.Fatalf("%s WriteBounded lost its overview header", d.name)
			}

			// Now test ReplaceSection with huge payload
			hugeSection := strings.Repeat("large replacement content\n", 500)
			if err := store.ReplaceSection(d.name, "Large Replaced Section", hugeSection); err != nil {
				t.Fatalf("ReplaceSection failed: %v", err)
			}
			replacedRead, err := store.Read(d.name)
			if err != nil {
				t.Fatalf("read replaced failed: %v", err)
			}
			if len(replacedRead) > d.ceiling {
				t.Fatalf("%s ReplaceSection size %d exceeds ceiling %d", d.name, len(replacedRead), d.ceiling)
			}
			if !strings.Contains(replacedRead, "Initial immutable overview") {
				t.Fatalf("%s ReplaceSection lost its overview header", d.name)
			}
			if !strings.Contains(replacedRead, "Large Replaced Section") {
				t.Fatalf("%s ReplaceSection lost the replaced section title", d.name)
			}
		})
	}
}

// TestChallengerPackerRolesIntegration tests NewPackerWithBudget and BuildPack
// integration for interviewer, specification, adversarial_reviewer, and challenger.
func TestChallengerPackerRolesIntegration(t *testing.T) {
	root, slm, store := newWorkspace(t)
	writeDoc(t, slm, DocPRD, "# PRD\n\nRequirements doc\n")
	writeDoc(t, slm, DocReview, "# REVIEW\n\nReview doc\n")
	writeFile(t, root, "pkg/sample/sample.go", "package sample\n\nfunc Calculate(x, y int) int {\n\treturn x + y\n}\n")

	windowTokens := 16384
	p := NewPackerWithBudget(store, root, windowTokens)

	// 1. Test Interviewer role:
	// - RoleBudgetPercent is 60%
	// - In DefaultIdentifierOnlyRoles -> identifiersOnly = true, file bodies not packed
	interviewerPack, err := p.BuildPack(BuildRequest{
		Role:      "interviewer",
		TaskTitle: "Gather requirements",
		Files:     []string{"pkg/sample/sample.go"},
	})
	if err != nil {
		t.Fatalf("interviewer BuildPack failed: %v", err)
	}
	expectedInterviewerBudget := DefaultBudget(windowTokens).Available("interviewer")
	if interviewerPack.BudgetTokens != expectedInterviewerBudget {
		t.Errorf("interviewer BudgetTokens = %d; want %d", interviewerPack.BudgetTokens, expectedInterviewerBudget)
	}
	// IdentifiersOnly should mean no full file excerpt in pack.Files
	if len(interviewerPack.Files) > 0 {
		t.Errorf("interviewer should not receive full file bodies; got files: %v", interviewerPack.Files)
	}
	// File should appear in Identifiers
	if len(interviewerPack.Identifiers) == 0 {
		t.Errorf("interviewer expected identifiers for file; got none")
	}

	// 2. Test Specification role:
	specPack, err := p.BuildPack(BuildRequest{
		Role:      "SPECIFICATION",
		TaskTitle: "Draft specification",
		Files:     []string{"pkg/sample/sample.go"},
	})
	if err != nil {
		t.Fatalf("specification BuildPack failed: %v", err)
	}
	expectedSpecBudget := DefaultBudget(windowTokens).Available("specification")
	if specPack.BudgetTokens != expectedSpecBudget {
		t.Errorf("specification BudgetTokens = %d; want %d", specPack.BudgetTokens, expectedSpecBudget)
	}
	if len(specPack.Files) > 0 {
		t.Errorf("specification should not receive full file bodies; got: %v", specPack.Files)
	}

	// 3. Test Adversarial Reviewer role:
	// - RoleBudgetPercent is 85%
	// - LeanFiles = true
	// - NOT in DefaultIdentifierOnlyRoles -> receives file bodies
	reviewerPack, err := p.BuildPack(BuildRequest{
		Role:      "adversarial_reviewer",
		TaskTitle: "Review implementation",
		Files:     []string{"pkg/sample/sample.go"},
	})
	if err != nil {
		t.Fatalf("adversarial_reviewer BuildPack failed: %v", err)
	}
	expectedReviewerBudget := DefaultBudget(windowTokens).Available("adversarial_reviewer")
	if reviewerPack.BudgetTokens != expectedReviewerBudget {
		t.Errorf("adversarial_reviewer BudgetTokens = %d; want %d", reviewerPack.BudgetTokens, expectedReviewerBudget)
	}
	if !reviewerPack.LeanFiles {
		t.Errorf("adversarial_reviewer expected LeanFiles = true")
	}
	if len(reviewerPack.Files) == 0 {
		t.Errorf("adversarial_reviewer expected file body; got none")
	}

	// 4. Test Challenger role:
	challengerPack, err := p.BuildPack(BuildRequest{
		Role:      "challenger",
		TaskTitle: "Challenge assumptions",
		Files:     []string{"pkg/sample/sample.go"},
	})
	if err != nil {
		t.Fatalf("challenger BuildPack failed: %v", err)
	}
	expectedChallengerBudget := DefaultBudget(windowTokens).Available("challenger")
	if challengerPack.BudgetTokens != expectedChallengerBudget {
		t.Errorf("challenger BudgetTokens = %d; want %d", challengerPack.BudgetTokens, expectedChallengerBudget)
	}
	if !challengerPack.LeanFiles {
		t.Errorf("challenger expected LeanFiles = true")
	}
	if len(challengerPack.Files) == 0 {
		t.Errorf("challenger expected file body; got none")
	}

	// Compare budgets: 85% vs 60%
	if reviewerPack.BudgetTokens <= interviewerPack.BudgetTokens {
		t.Errorf("adversarial_reviewer budget (%d) should be strictly greater than interviewer budget (%d)",
			reviewerPack.BudgetTokens, interviewerPack.BudgetTokens)
	}
}
