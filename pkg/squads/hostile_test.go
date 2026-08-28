package squads

import (
	"strings"
	"testing"
)

// ── The org chart comes from a model too ─────────────────────────────────
//
// Parse takes the manager specialist's raw output. Everything downstream — the
// ownership deny list, the frozen contract, the integration gate — is built
// from whatever comes out, so a panic here takes the run down before any of
// those safety properties get a chance to hold.

func hostileSquadOutput() []string {
	return []string{
		"", " ", "\x00", "\xff\xfe", "no json",
		"{", "}", "{}", "[]", "null", `{"squads":`,
		`{"squads":"not-a-list"}`, `{"squads":[1,2]}`, `{"squads":[{}]}`,
		`{"squads":[{"id":""}]}`, `{"squads":[{"id":null}]}`,
		`{"squads":[{"id":"a","owns":"not-a-list"}]}`,
		`{"contract":{"interfaces":"nope"}}`,
		`{"squads":[{"id":"a","owns":["**"]},{"id":"a","owns":["**"]}]}`,
		// A charter containing braces must not truncate the object.
		`{"summary":"we own {web} and {api}","squads":[{"id":"a","owns":["x/**"]}]}`,
		// Depth and size.
		strings.Repeat("{", 500),
		`{"summary":"` + strings.Repeat("x", 100000) + `"}`,
		"Here you go:\n```json\n{\"squads\":[{\"id\":\"a\",\"owns\":[\"a/**\"]}]}\n```\n",
	}
}

func TestParseNeverPanics(t *testing.T) {
	for i, in := range hostileSquadOutput() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Parse panicked on input %d (%.50q): %v", i, in, r)
				}
			}()
			p, err := Parse(in)
			if err != nil {
				return
			}
			// Anything that parses must survive the operations the run performs
			// on it — that is where a half-built plan would actually bite.
			_ = p.Validate()
			_ = p.Summarize()
			_ = RenderContract(p)
			_ = LaneOf(&p, []string{"a/b.go"})
			_, _ = SeamOwner(&p, "a/b.go:1: boom")
			for _, s := range p.Squads {
				_ = p.Brief(s.ID)
				_ = StaffingFor(&p, s.ID)
			}
			_, _ = p.Owner("a/b.go")
		}()
	}
}

// A plan that parses must never be activated while invalid: an overlap is what
// silently loses one team's edits.
func TestAnythingThatParsesIsEitherValidOrSaysWhy(t *testing.T) {
	for _, in := range hostileSquadOutput() {
		p, err := Parse(in)
		if err != nil {
			continue
		}
		probs := p.Validate()
		if probs.Errors() && len(probs.Strings()) == 0 {
			t.Errorf("Parse(%.40q) produced an invalid plan with no explanation", in)
		}
	}
}
