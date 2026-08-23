package quality

import (
	"strings"
	"testing"
)

func TestIncompleteFinalizeReason(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "empty_response"},
		{"   ", "empty_response"},
		{`{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`, "ended_on_tool_call"},
		{`{"status":"blocked","summary":"empty finalize","notes":"retry with clearer finish instruction"}`, "ended_on_tool_call"},
		{"<tool_call>ws_edit</tool_call>", "ended_on_tool_call"},
		{`{"status":"done","summary":"ok","files_changed":["a.go"]}`, ""},
		{`{"status":"blocked","summary":"missing API key","notes":"need human"}`, ""},
		{"hello\n\n## Disk evidence\n- modified: a.go", ""},
	}
	for _, tc := range cases {
		got := IncompleteFinalizeReason(tc.in)
		if got != tc.want {
			t.Fatalf("in=%q got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestAssessResponseCatchesToolEndBlock(t *testing.T) {
	a := AssessResponse(`{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`, nil, nil, nil)
	if a.OK || a.Reason != "ended_on_tool_call" {
		t.Fatalf("%+v", a)
	}
}

func TestFinishSteerMessageEvidence(t *testing.T) {
	msg := FinishSteerMessage("ended_on_tool_call", true)
	if !strings.Contains(msg, "STRICT JSON") || !strings.Contains(msg, "do NOT start new tool") {
		t.Fatalf("unexpected steer: %s", msg)
	}
}

func TestProvisionalDoneFromEvidence(t *testing.T) {
	out := ProvisionalDoneFromEvidence([]string{"main.py"}, "ended_on_tool_call")
	if IncompleteFinalizeReason(out) != "" {
		t.Fatalf("provisional should be complete: %s", out)
	}
	if !strings.Contains(out, `"status":"done"`) || !strings.Contains(out, "main.py") {
		t.Fatalf("unexpected provisional: %s", out)
	}
}

func TestPhraseForUserEndedOnTool(t *testing.T) {
	if got := PhraseForUser("ended_on_tool_call"); !strings.Contains(got, "tool call") {
		t.Fatalf("%q", got)
	}
}

// TestHarnessSectionHeadersMatchTheEmittedMarkdown is the drift guard: every
// exported header must be the one the formatter actually writes. Two of the
// strip literals disagreed with the formatters for as long as they existed.
func TestHarnessSectionHeadersMatchTheEmittedMarkdown(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		emitted string
	}{
		{"static gate", StaticSectionHeader,
			FormatStaticSection([]StaticIssue{{Path: "a.go", Reason: "stub"}})},
		{"claims gate", ClaimsSectionHeader,
			FormatClaimsSection([]ClaimIssue{{Path: "b.go", Reason: "missing"}})},
		{"deterministic smoke", SmokeSectionHeader,
			FormatSmokeSection(SmokeResult{Ran: true, Command: "go build ./...", Output: "boom"})},
		{"acceptance smoke", AcceptanceSectionHeader,
			FormatAcceptanceSection(SmokeResult{Ran: true, Command: "pytest", Output: "boom"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.emitted, "\n"+tc.header+"\n") {
				t.Fatalf("%s: emitted section %q does not contain header %q", tc.name, tc.emitted, tc.header)
			}
			if !containsHeader(HarnessSectionHeaders, tc.header) {
				t.Fatalf("%s: %q is missing from HarnessSectionHeaders", tc.name, tc.header)
			}
		})
	}
}

func containsHeader(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestGateSectionsAreStrippedBeforeCompletenessIsJudged is the behavioral
// consequence: an empty model finalize carrying only a FAILED gate section must
// still be judged empty, and a FAILED static/claims gate must never leak into
// the text the completeness check reads.
func TestGateSectionsAreStrippedBeforeCompletenessIsJudged(t *testing.T) {
	static := FormatStaticSection([]StaticIssue{{Path: "a.go", Reason: "stub"}})
	claims := FormatClaimsSection([]ClaimIssue{{Path: "b.go", Reason: "missing"}})
	smoke := FormatSmokeSection(SmokeResult{Ran: true, Command: "go test ./...", Output: "FAIL"})

	cases := []struct {
		name       string
		output     string
		wantReason string
	}{
		{"static gate alone is not an answer", "\n" + static, "empty_response"},
		{"claims gate alone is not an answer", "\n" + claims, "empty_response"},
		{"smoke section alone is not an answer", "\n" + smoke, "empty_response"},
		{"every gate at once is still not an answer", "\n" + smoke + static + claims, "empty_response"},
		{"a real answer with gates appended is fine",
			`{"status":"done","summary":"did it","files_changed":["a.go"]}` + "\n" + smoke + static + claims, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IncompleteFinalizeReason(tc.output); got != tc.wantReason {
				t.Fatalf("IncompleteFinalizeReason = %q, want %q (core=%q)",
					got, tc.wantReason, StripHarnessSections(tc.output))
			}
			if core := StripHarnessSections(tc.output); strings.Contains(core, SmokeFailedMarker) {
				t.Fatalf("harness-authored %q leaked into the judged text: %q", SmokeFailedMarker, core)
			}
		})
	}
}
