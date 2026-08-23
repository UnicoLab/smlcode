package compact

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestMarginallyOverBudgetSummaryIsNotDemoted pins the first half of defect 6.
//
// The candidate was truncated to maxBytes BEFORE the gates ran, so a good
// summary a few hundred bytes over target had its tail cut off — and the tail
// is where the last `path/like.go` mentions live — and GateLostPaths then
// rejected the model's work for damage the harness had just done to it.
func TestMarginallyOverBudgetSummaryIsNotDemoted(t *testing.T) {
	// A realistic CONTEXT.md: a lot of prose, then a "files touched" list at
	// the BOTTOM. That layout is why truncate-then-gate was so destructive —
	// the paths the gate measures all live in the part a byte truncation eats.
	var b strings.Builder
	b.WriteString("## Locked PRD\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "Wave %d chatter: the agent re-read the module and restated the plan.\n", i)
	}
	b.WriteString("\n## Files\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "- `pkg/mod%02d/file%02d.go` still relevant\n", i, i)
	}
	body := b.String()

	var s strings.Builder
	s.WriteString("## Locked PRD\nBuild the thing; the wave chatter is dropped.\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&s, "Kept note %d about the module layout and its constraints.\n", i)
	}
	s.WriteString("\n## Files\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&s, "- `pkg/mod%02d/file%02d.go`\n", i, i)
	}
	summary := s.String()
	if len(summary) >= len(body) {
		t.Fatalf("fixture must be smaller than the input (%d vs %d)", len(summary), len(body))
	}
	// Marginally over: ~15% of the summary, all of it in the file list.
	maxBytes := len(summary) - 640
	if maxBytes <= 0 {
		t.Fatal("fixture too small")
	}

	// Sanity: the summary the MODEL produced keeps every path.
	if got := AcceptCompaction(body, summary); got != GateOK {
		t.Fatalf("fixture summary must be acceptable as written, got %q", got)
	}
	// …and the truncated one does not. That is the damage the harness used to
	// do to the candidate before judging it.
	if got := AcceptCompaction(body, truncateBytes(summary, maxBytes)); got != GateLostPaths {
		t.Fatalf("fixture must lose paths when truncated first, got %q", got)
	}

	res := Summarize(context.Background(), "llm", body, maxBytes,
		func(context.Context, string, int) (string, error) { return summary, nil })
	if res.Rejected != GateOK {
		t.Fatalf("a good summary was demoted to the heuristic: %q", res.Rejected)
	}
	if res.Engine != "llm" {
		t.Fatalf("engine = %q, want llm", res.Engine)
	}
	if res.AfterBytes > maxBytes {
		t.Fatalf("accepted summary is %d bytes, over the %d budget", res.AfterBytes, maxBytes)
	}
}

// TestHeadingGateOnlyAppliesToSectionedInput pins the second half of defect 6:
// the `## ` gate is a structure-PRESERVED check, not a house style, so a
// document that never had a heading cannot be required to grow one.
func TestHeadingGateOnlyAppliesToSectionedInput(t *testing.T) {
	flat := strings.Repeat("The project is a small Go module with `cmd/app/main.go`.\n", 60)
	summary := "A small Go module. Entry point `cmd/app/main.go`. " +
		strings.Repeat("Notes about the module and its layout. ", 20)

	if got := AcceptCompaction(flat, summary); got != GateOK {
		t.Fatalf("a correct summary of a heading-less document was rejected: %q", got)
	}
	// A sectioned input still has to keep its sections.
	sectioned := "## Locked PRD\n" + flat
	if got := AcceptCompaction(sectioned, summary); got != GateNoHeading {
		t.Fatalf("a sectioned document must still require a heading, got %q", got)
	}
}
