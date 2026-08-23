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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
