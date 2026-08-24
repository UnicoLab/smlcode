package loop

import (
	"strings"
	"testing"
)

// TestAdaptiveGuidanceNoLongerGatesOnKeywords is the pkg/loop half of removing
// the keyword allowlist.
//
// recentLessonLines used to keep only lines containing one of eight hardcoded
// substrings (timeout, deadline, smoke, qa_gate, acceptance, placeholder, stub,
// max retries) — the tail of the eleven-word gate in pkg/learning. Between the
// two, a lesson about anything else was written to disk and then never seen by
// a model again. Every lesson below deliberately avoids all eleven words and
// must now reach the prompt.
func TestAdaptiveGuidanceNoLongerGatesOnKeywords(t *testing.T) {
	gateWords := []string{
		"timeout", "timed out", "deadline", "max_parallel", "contention",
		"smoke", "qa_gate", "acceptance", "placeholder", "stub", "max retries",
	}
	lessons := []string{
		"Import cycle: pkg/graph must not import pkg/orchestrator",
		"Exported identifiers in this repo never abbreviate",
		"Run migrations before seeding or the foreign keys blow up",
	}
	raw := ""
	for _, l := range lessons {
		for _, w := range gateWords {
			if strings.Contains(strings.ToLower(l), w) {
				t.Fatalf("test premise broken: %q contains the old gate word %q", l, w)
			}
		}
		raw += "- ⚠ " + l + "\n"
	}

	got := adaptiveGuidance(raw, 900)
	for _, l := range lessons {
		if !strings.Contains(got, l) {
			t.Errorf("keyword gate still filtering — lost %q:\n%s", l, got)
		}
	}
	if !strings.Contains(got, "- Learned: ") {
		t.Errorf("lessons were not rendered as guidance lines:\n%s", got)
	}
}

// TestAdaptiveGuidanceKeepsCannedAdviceAsEnrichment: the hardcoded advice for
// the classes the harness understands natively is still worth emitting — it
// says something concrete the raw lesson text does not. It just no longer
// decides which lessons survive.
func TestAdaptiveGuidanceKeepsCannedAdviceAsEnrichment(t *testing.T) {
	got := adaptiveGuidance("- ⚠ Wave aborted: context deadline exceeded on task T3\n", 900)
	if !strings.Contains(got, "Timeout adaptation") {
		t.Errorf("canned timeout enrichment disappeared:\n%s", got)
	}
	if !strings.Contains(got, "context deadline exceeded on task T3") {
		t.Errorf("the lesson itself was replaced by the canned advice:\n%s", got)
	}
}

// TestAdaptiveGuidanceStripsProvenanceAndRespectsLimit: MEMORY.md bullets now
// carry a machine-readable provenance comment so they can be parsed back into
// typed facts. A model must never see it, and the byte budget is unchanged.
func TestAdaptiveGuidanceStripsProvenanceAndRespectsLimit(t *testing.T) {
	raw := "- ⚠ Import cycle between pkg/a and pkg/b <!-- slm task=T7 kind=failure at=2026-08-24T10:11:12Z -->\n" +
		"- ⚙ Fixtures live under testdata/ <!-- slm task=T8 kind=convention at=2026-08-24T10:11:13Z -->\n"
	got := adaptiveGuidance(raw, 900)
	if strings.Contains(got, "<!--") || strings.Contains(got, "task=T7") {
		t.Errorf("provenance bookkeeping leaked into the prompt:\n%s", got)
	}
	if !strings.Contains(got, "Import cycle between pkg/a and pkg/b") {
		t.Errorf("stripping provenance ate the lesson:\n%s", got)
	}
	if strings.Contains(got, "⚠") || strings.Contains(got, "⚙") {
		t.Errorf("kind glyphs leaked into the prompt:\n%s", got)
	}

	var big strings.Builder
	for i := 0; i < 40; i++ {
		big.WriteString("- ⚠ A fairly long lesson about the widget subsystem and all of its many moving parts\n")
	}
	for _, limit := range []int{0, 50, 300, 900} {
		if n := len(adaptiveGuidance(big.String(), limit)); n > limit {
			t.Errorf("limit %d: emitted %d bytes", limit, n)
		}
	}
}
