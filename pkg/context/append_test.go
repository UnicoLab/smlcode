package contextstore

import (
	"fmt"
	"strings"
	"testing"
)

func timestampedDoc(head string, sections int, bodyBytes int) string {
	var b strings.Builder
	b.WriteString(head)
	for i := 0; i < sections; i++ {
		fmt.Fprintf(&b, "\n\n## Auto-learned (2024-01-%02dT10:00:00Z)\n\n", (i%28)+1)
		fmt.Fprintf(&b, "section-%d %s\n", i, strings.Repeat("x", bodyBytes))
	}
	return b.String()
}

func TestPruneTimestampedSections(t *testing.T) {
	head := "# Project: demo\n\n## Overview\n\nHand written and must survive.\n"
	tests := []struct {
		name     string
		doc      string
		maxBytes int
		wantHead bool
		wantCap  bool
	}{
		{"under cap", timestampedDoc(head, 3, 50), 100000, true, false},
		{"drops oldest", timestampedDoc(head, 40, 200), 4000, true, true},
		{"aggressive cap", timestampedDoc(head, 40, 200), 800, true, true},
		{"no timestamps", head + strings.Repeat("plain body\n", 500), 1000, true, true},
		{"zero cap is a no-op", timestampedDoc(head, 10, 100), 0, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PruneTimestampedSections(tc.doc, tc.maxBytes)
			if tc.wantCap && len(got) > tc.maxBytes {
				t.Fatalf("pruned to %d bytes, cap was %d", len(got), tc.maxBytes)
			}
			if !tc.wantCap && got != tc.doc {
				t.Fatal("document should have passed through unchanged")
			}
			if tc.wantHead && !strings.Contains(got, "Hand written and must survive") {
				t.Fatalf("prune destroyed the document head:\n%s", got[:min(300, len(got))])
			}
			if tc.name == "drops oldest" {
				if strings.Contains(got, "section-0 ") {
					t.Fatal("oldest section should have been dropped first")
				}
				if !strings.Contains(got, "section-39 ") {
					t.Fatalf("newest section must survive:\n%s", got)
				}
			}
		})
	}
}

func TestStoreAppendIsBounded(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		cap  int
	}{
		{"project", DocProject, ProjectAppendMaxBytes},
		{"context", DocContext, ContextAppendMaxBytes},
		{"memory", DocMemory, MemoryAppendMaxBytes},
		{"prd", DocPRD, PRDAppendMaxBytes},
		{"contract", DocContract, ContractAppendMaxBytes},
		{"review", DocReview, ReviewAppendMaxBytes},
		{"other", DocPlan, DefaultAppendMaxBytes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, store := newWorkspace(t)
			if err := store.Write(tc.doc, "# "+tc.doc+"\n\n## Overview\n\nkeep me\n"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 400; i++ {
				if err := store.Append(tc.doc, "Auto-learned", strings.Repeat("y", 400)); err != nil {
					t.Fatal(err)
				}
			}
			body, err := store.Read(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) > tc.cap {
				t.Fatalf("%s grew to %d bytes, cap %d", tc.doc, len(body), tc.cap)
			}
			if !strings.Contains(body, "keep me") {
				t.Fatalf("append destroyed the document head:\n%s", body[:min(300, len(body))])
			}
			if !strings.Contains(body, "Auto-learned") {
				t.Fatal("append lost the appended content entirely")
			}
		})
	}
}

func TestStoreSetAppendPolicy(t *testing.T) {
	_, _, store := newWorkspace(t)
	store.SetAppendPolicy(DocScratch, 2000)
	for i := 0; i < 50; i++ {
		if err := store.Append(DocScratch, "Note", strings.Repeat("z", 300)); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := store.Read(DocScratch)
	if len(body) > 2000 {
		t.Fatalf("custom policy ignored: %d bytes", len(body))
	}

	// 0 disables capping.
	store.SetAppendPolicy(DocScratch, 0)
	for i := 0; i < 50; i++ {
		_ = store.Append(DocScratch, "Note", strings.Repeat("z", 300))
	}
	body, _ = store.Read(DocScratch)
	if len(body) <= 2000 {
		t.Fatalf("policy 0 should disable capping, got %d bytes", len(body))
	}
}

func TestStoreReplaceSection(t *testing.T) {
	_, _, store := newWorkspace(t)
	if err := store.Write(DocProject, "# Project\n\n## Overview\n\nold\n\n## Key paths\n\n| a | b |\n"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := store.ReplaceSection(DocProject, "Auto-learned", fmt.Sprintf("- run %d\n", i)); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := store.Read(DocProject)
	if strings.Count(body, "## Auto-learned") != 1 {
		t.Fatalf("ReplaceSection must not accumulate sections:\n%s", body)
	}
	if !strings.Contains(body, "- run 19") || strings.Contains(body, "- run 0\n") {
		t.Fatalf("latest content should win:\n%s", body)
	}
	if !strings.Contains(body, "## Key paths") {
		t.Fatalf("other sections destroyed:\n%s", body)
	}
}

func TestTeamworkDocumentCeilingsAndPolicies(t *testing.T) {
	if DocReview != "REVIEW.md" {
		t.Fatalf("DocReview = %q, want REVIEW.md", DocReview)
	}
	if PRDAppendMaxBytes != 24*1024 {
		t.Fatalf("PRDAppendMaxBytes = %d, want %d", PRDAppendMaxBytes, 24*1024)
	}
	if ContractAppendMaxBytes != 16*1024 {
		t.Fatalf("ContractAppendMaxBytes = %d, want %d", ContractAppendMaxBytes, 16*1024)
	}
	if ReviewAppendMaxBytes != 24*1024 {
		t.Fatalf("ReviewAppendMaxBytes = %d, want %d", ReviewAppendMaxBytes, 24*1024)
	}

	tests := []struct {
		doc  string
		want int
	}{
		{DocPRD, PRDAppendMaxBytes},
		{DocContract, ContractAppendMaxBytes},
		{DocReview, ReviewAppendMaxBytes},
		{DocProject, ProjectAppendMaxBytes},
		{DocContext, ContextAppendMaxBytes},
		{DocMemory, MemoryAppendMaxBytes},
		{DocScratch, ScratchAppendMaxBytes},
		{"CUSTOM.md", DefaultAppendMaxBytes},
	}
	for _, tc := range tests {
		got := DefaultAppendPolicy(tc.doc).MaxBytes
		if got != tc.want {
			t.Errorf("DefaultAppendPolicy(%q).MaxBytes = %d, want %d", tc.doc, got, tc.want)
		}
	}
}

func TestStoreWriteBounded(t *testing.T) {
	_, _, store := newWorkspace(t)

	// Writing under ceiling preserves content exactly.
	short := "# PRD\n\n## Overview\n\nInitial short requirements.\n"
	if err := store.WriteBounded(DocPRD, short); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(DocPRD)
	if err != nil {
		t.Fatal(err)
	}
	if got != short {
		t.Fatalf("expected exact match for short write, got %q", got)
	}

	// Writing content that exceeds ceiling with timestamped sections prunes oldest.
	head := "# Review Report\n\n## Summary\n\nExecutive summary must survive.\n"
	longDoc := timestampedDoc(head, 50, 800) // ~40KB, exceeds ReviewAppendMaxBytes (24KB)
	if err := store.WriteBounded(DocReview, longDoc); err != nil {
		t.Fatal(err)
	}
	pruned, err := store.Read(DocReview)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) > ReviewAppendMaxBytes {
		t.Fatalf("WriteBounded did not enforce ceiling: %d > %d", len(pruned), ReviewAppendMaxBytes)
	}
	if !strings.Contains(pruned, "Executive summary must survive") {
		t.Fatalf("document head was lost:\n%s", pruned[:min(300, len(pruned))])
	}
	if !strings.Contains(pruned, "auto-appended sections pruned") {
		t.Fatalf("expected prune notification marker, got:\n%s", pruned[:min(500, len(pruned))])
	}
}

func TestStoreReplaceSectionWithCeilingPruning(t *testing.T) {
	_, _, store := newWorkspace(t)
	store.SetAppendPolicy(DocContract, 2048)

	head := "# Contract\n\n## Overview\n\nCore contract terms.\n"
	initialDoc := timestampedDoc(head, 10, 300) // ~3KB, will be capped when ReplaceSection runs
	if err := store.Write(DocContract, initialDoc); err != nil {
		t.Fatal(err)
	}

	if err := store.ReplaceSection(DocContract, "New Section", "Replaced content here.\n"); err != nil {
		t.Fatal(err)
	}

	body, err := store.Read(DocContract)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 2048 {
		t.Fatalf("ReplaceSection failed to prune over-limit doc: %d bytes > 2048", len(body))
	}
	if !strings.Contains(body, "Core contract terms") {
		t.Fatal("ReplaceSection prune destroyed the head")
	}
	if !strings.Contains(body, "Replaced content here") {
		t.Fatal("ReplaceSection did not include the new section")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
