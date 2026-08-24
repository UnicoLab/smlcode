package learning

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestExtractAndRender(t *testing.T) {
	done := plan.Task{
		ID: "T1", Title: "Add helper", Column: plan.ColDone,
		Output:     "Added Greeting(). Tests pass.",
		Acceptance: "compiles",
	}
	done.Normalize()
	lessons := Extract(done)
	if len(lessons) == 0 {
		t.Fatal("expected lessons")
	}
	md := RenderMarkdown(lessons)
	if !strings.Contains(md, "T1") {
		t.Fatalf("md=%s", md)
	}

	blocked := plan.Task{ID: "T2", Column: plan.ColBlocked, Error: "missing import"}
	blocked.Normalize()
	fail := Extract(blocked)
	if len(fail) == 0 || fail[0].Kind != "failure" {
		t.Fatalf("%+v", fail)
	}

	delta := ContextDelta([]plan.Task{done, blocked})
	if !strings.Contains(delta, "Completed") || !strings.Contains(delta, "Blocked") {
		t.Fatalf("delta=%s", delta)
	}

	md2 := JSONLessonsToMarkdown(`{"lessons":[{"kind":"convention","text":"Prefer small edits"}]}`)
	if !strings.Contains(md2, "Prefer small edits") {
		t.Fatalf("json md=%s", md2)
	}
}

// TestRenderMarkdownRoundTripsProvenance is the regression for the defect that
// RenderMarkdown accepted a Lesson{TaskID, Kind, Text, At} and emitted only
// "- <glyph> <Text>", destroying TaskID and At at WRITE time — before storage,
// so no later reader could ever recover them.
func TestRenderMarkdownRoundTripsProvenance(t *testing.T) {
	in := []Lesson{
		{TaskID: "T7", Kind: "failure", Text: "Import cycle between pkg/a and pkg/b", At: "2026-08-24T10:11:12Z"},
		{TaskID: "T8", Kind: "convention", Text: "Fixtures live under testdata/", At: "2026-08-24T10:11:13+02:00"},
		{TaskID: "T9", Kind: "success", Text: "Renamed the exported symbol in one pass", At: "2026-08-24T10:11:14Z"},
		{Kind: "note", Text: "A kind the glyph table does not know", At: "2026-08-24T10:11:15Z"},
	}
	md := RenderMarkdown(in)
	got := ParseMarkdown(md)
	if len(got) != len(in) {
		t.Fatalf("parsed %d lessons, want %d:\n%s", len(got), len(in), md)
	}
	for i, want := range in {
		if got[i] != want {
			t.Errorf("lesson %d round-trip mismatch:\n got %+v\nwant %+v\n md: %s", i, got[i], want, md)
		}
	}

	// The mirror must stay Markdown a human can read: bullets, and provenance
	// hidden in a comment rather than shouted in the prose.
	for _, line := range strings.Split(strings.TrimSpace(md), "\n") {
		if !strings.HasPrefix(line, "- ") {
			t.Errorf("not a Markdown bullet: %q", line)
		}
	}
	if !strings.Contains(md, "<!-- slm task=T7 kind=failure at=2026-08-24T10:11:12Z -->") {
		t.Errorf("provenance suffix missing or reshaped:\n%s", md)
	}

	// And it must be strippable, because a model has no use for it.
	clean := StripProvenance(md)
	if strings.Contains(clean, "<!--") || strings.Contains(clean, "task=T7") {
		t.Errorf("StripProvenance left bookkeeping behind:\n%s", clean)
	}
	if !strings.Contains(clean, "Import cycle between pkg/a and pkg/b") {
		t.Errorf("StripProvenance ate the lesson:\n%s", clean)
	}
}

// TestParseMarkdownAcceptsLegacyBullets: MEMORY.md files written before
// provenance existed must keep parsing, with the kind recovered from the glyph.
func TestParseMarkdownAcceptsLegacyBullets(t *testing.T) {
	legacy := "# Long-term Memory\n\n## Wave lessons (2026-08-05T17:29:28+02:00)\n\n" +
		"- ✓ T1 (Implement reversal): smoke passed\n" +
		"- ⚙ Use go test . -short for deterministic smoke testing\n" +
		"- ⚠ No test files found in package\n" +
		"- • Remember to include test files\n"
	got := ParseMarkdown(legacy)
	if len(got) != 4 {
		t.Fatalf("parsed %d lessons, want 4: %+v", len(got), got)
	}
	wantKinds := []string{"success", "convention", "failure", ""}
	for i, k := range wantKinds {
		if got[i].Kind != k {
			t.Errorf("lesson %d kind = %q, want %q", i, got[i].Kind, k)
		}
		if strings.ContainsAny(got[i].Text, "✓⚙⚠•") {
			t.Errorf("lesson %d kept its glyph: %q", i, got[i].Text)
		}
	}
}
