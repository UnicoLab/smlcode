package loop

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestReviewVerdictLineNeverInventsAScore locks the operator-facing line.
//
// `review approved=false score=0` is a claim about what the reviewer decided.
// Printing it for a reply the reviewer never produced is what made the live
// log unreadable: four identical score-0 lines, none of which was a score.
func TestReviewVerdictLineNeverInventsAScore(t *testing.T) {
	noVerdict := resolveNoVerdict(plan.ParseReviewJSON(""), gateState{})
	line := reviewVerdictLine(noVerdict)
	if strings.Contains(line, "score=") {
		t.Errorf("no-verdict review reported as %q — a score the reviewer never gave", line)
	}
	if !strings.Contains(line, "no verdict") {
		t.Errorf("line=%q must say the reviewer produced no verdict", line)
	}

	for _, tc := range []plan.ReviewResult{
		{Approved: false, Score: 30, Summary: "stub"},
		{Approved: true, Score: 85, Summary: "ok"},
		// An approval the harness synthesized still reports its score: the
		// harness DID decide, and the operator should see what it decided.
		{Approved: true, Score: 80, NoVerdict: true, Summary: "auto-approved"},
	} {
		if line := reviewVerdictLine(tc); !strings.Contains(line, "score=") {
			t.Errorf("reviewVerdictLine(%+v) = %q, want a score", tc, line)
		}
	}
}

// TestResolveNoVerdictLeavesRealVerdictsAlone is the containment check: the
// normalization must only ever touch results the parser flagged.
func TestResolveNoVerdictLeavesRealVerdictsAlone(t *testing.T) {
	in := plan.ReviewResult{Approved: false, Score: 40,
		Summary: "acceptance not met", Issues: []string{"Median returns 0"}}
	if got := resolveNoVerdict(in, gateState{}); got.Summary != in.Summary ||
		len(got.Issues) != 1 || got.Issues[0] != in.Issues[0] {
		t.Fatalf("a considered rejection was rewritten: %+v", got)
	}
	approved := plan.ReviewResult{Approved: true, NoVerdict: true, Summary: "auto-approved"}
	if got := resolveNoVerdict(approved, gateState{}); !got.Approved || got.Summary != "auto-approved" {
		t.Fatalf("an approval must never be rewritten into a rejection: %+v", got)
	}
}
