package plan

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ── Parsers meet whatever the model said ─────────────────────────────────
//
// Every one of these takes raw model output. A 30B model under a constrained
// grammar mostly produces the right shape, but "mostly" is the operative word:
// it truncates at the token limit, wraps JSON in prose, emits a bare array
// where an object was asked for, and occasionally answers in the wrong
// contract entirely. None of that is exotic — it is what the repair layer
// exists for.
//
// A parser that panics on any of it takes the run down, and the input is the
// one thing the harness does not control. So: none of them may panic, whatever
// they are handed.

// hostileInputs are the shapes model output actually takes when it goes wrong.
func hostileInputs() []string {
	return []string{
		"", " ", "\n\n", "\x00", "\xff\xfe", "not json at all",
		"{", "}", "[", "]", "{}", "[]", "null", "true", "0", `""`,
		`{"a":`, `{"a":1,`, `[{"id":`, `{"tasks":[{`,
		// Truncation at the token limit, which is the common one.
		`{"summary":"did the thing","steps":["a","b`,
		`{"passed":false,"failures":["boom"`,
		// The right shape with the wrong types.
		`{"tasks":"not-a-list"}`, `{"tasks":[1,2,3]}`, `{"passed":"yes"}`,
		`{"steps":{"a":1}}`, `{"assignee":[]}`, `{"issues":"one"}`,
		// A different contract's answer.
		`{"approved":true,"score":90}`, `{"assignee":"go-worker"}`,
		// Prose around JSON, and JSON inside a fence.
		"Here you go:\n```json\n{\"passed\":true}\n```\nHope that helps.",
		"I think the answer is {\"passed\":true} but I am not sure.",
		// Nesting and size.
		strings.Repeat("[", 200) + strings.Repeat("]", 200),
		`{"a":` + strings.Repeat(`{"b":`, 100) + "1" + strings.Repeat("}", 100) + "}",
		strings.Repeat("a", 100000),
		`{"summary":"` + strings.Repeat("x", 50000) + `"}`,
		// Control characters and invalid UTF-8 inside strings.
		"{\"summary\":\"\x07\x1b bell and escape\"}",
		"{\"summary\":\"emoji \U0001F680 and \\\" quotes\"}",
		"{\"summary\":\"\xc3\x28\"}",
	}
}

func TestNoParserPanicsOnHostileModelOutput(t *testing.T) {
	parsers := map[string]func(string){
		"ParseClarifyJSON":    func(s string) { _ = ParseClarifyJSON(s) },
		"ParseEscalateDecide": func(s string) { _, _ = ParseEscalateDecide(s) },
		"ParseScopeInterview": func(s string) { _ = ParseScopeInterview(s) },
		"ParseScopeJudgeJSON": func(s string) { _ = ParseScopeJudgeJSON(s) },
		"ParsePlanJSON":       func(s string) { _, _ = ParsePlanJSON(s) },
		"ParseTasksJSON":      func(s string) { _, _ = ParseTasksJSON(s) },
		"ParseTesterJSON":     func(s string) { _ = ParseTesterJSON(s) },
		"ParseReviewJSON":     func(s string) { _ = ParseReviewJSON(s) },
		"ParseTriage":         func(s string) { _, _ = ParseTriage(s) },
		"SpecialistFor":       func(s string) { _ = SpecialistFor([]string{s}, nil) },
		"LanguageOf":          func(s string) { _ = LanguageOf([]string{s}) },
		"RankRoster":          func(s string) { _ = RankRoster([]string{s}, []string{s}) },
		"CorrectionKeyOf":     func(s string) { _ = CorrectionKeyOf(Task{Notes: s}) },
		"CorrectionAttemptOf": func(s string) { _ = CorrectionAttemptOf(Task{Notes: s}) },
		"NewCorrectionTicket": func(s string) {
			_ = NewCorrectionTicket(CorrectionInput{Summary: s, Output: s, Failures: []string{s}}, nil)
		},
	}
	for name, parse := range parsers {
		for i, in := range hostileInputs() {
			t.Run(name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s panicked on input %d (%.60q): %v", name, i, in, r)
					}
				}()
				parse(in)
			})
		}
	}
}

// Whatever a parser returns is fed into a prompt, so it must be valid UTF-8.
//
// textutil's own doc says why: invalid UTF-8 tokenizes into replacement-character
// byte fallbacks, which wastes tokens and measurably degrades small-model
// comprehension. Providers reject it outright, which turns one stray byte in a
// model answer into a failed request the user cannot explain.
//
// The parsers echo raw input into their result whenever JSON parsing fails —
// that is the fallback that keeps a malformed answer useful — so the raw bytes
// reach a prompt by design and have to be clean on the way through.
func TestParsedTextStaysValidUTF8(t *testing.T) {
	check := func(t *testing.T, parser, in string, out ...string) {
		t.Helper()
		for _, s := range out {
			if !utf8.ValidString(s) {
				t.Errorf("%s(%.30q) produced invalid UTF-8: %q", parser, in, s)
			}
		}
	}
	for _, in := range hostileInputs() {
		if utf8.ValidString(in) {
			continue // only the invalid inputs can produce invalid output
		}
		tr := ParseTesterJSON(in)
		check(t, "ParseTesterJSON", in, append([]string{tr.Summary}, tr.Failures...)...)

		rv := ParseReviewJSON(in)
		check(t, "ParseReviewJSON", in, append([]string{rv.Summary}, rv.Issues...)...)

		cl := ParseClarifyJSON(in)
		check(t, "ParseClarifyJSON", in, append(append(cl.Questions, cl.Assumptions...), cl.Acceptance...)...)

		sj := ParseScopeJudgeJSON(in)
		check(t, "ParseScopeJudgeJSON", in, append(sj.Issues, sj.Hints...)...)

		if d, err := ParseTriage(in); err == nil {
			check(t, "ParseTriage", in, d.Assignee, d.Reason, d.Guidance)
		}
		if pl, err := ParsePlanJSON(in); err == nil {
			check(t, "ParsePlanJSON", in, append([]string{pl.Summary}, pl.Steps...)...)
		}
	}
}
