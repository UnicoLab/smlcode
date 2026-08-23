package workspace

import (
	"strings"
	"testing"
)

// Each rung of the fallback ladder is unit-tested individually, plus the
// "never apply a non-unique match" invariant.
func TestFindEditMatchLadder(t *testing.T) {
	body := "package demo\n\nfunc Hello() {\n\treturn\n}\n\nfunc Bye() {\n\treturn\n}\n"
	cases := []struct {
		name     string
		content  string
		search   string
		want     string // expected strategy
		found    bool
		matchTxt string // text expected at the matched span
	}{
		{
			name: "exact", content: body,
			search: "func Hello() {\n\treturn\n}",
			want:   MatchExact, found: true, matchTxt: "func Hello() {\n\treturn\n}",
		},
		{
			name: "trailing whitespace drift", content: body,
			search: "func Hello() {   \n\treturn\t\n}",
			want:   MatchTrailingWS, found: true, matchTxt: "func Hello() {\n\treturn\n}",
		},
		{
			name:    "indentation normalized",
			content: "class A:\n    def go(self):\n        return 1\n",
			// same relative indent, shifted left by four columns
			search: "def go(self):\n    return 1",
			want:   MatchIndent, found: true, matchTxt: "    def go(self):\n        return 1",
		},
		{
			name:    "blank line insensitive",
			content: "a := 1\n\n\nb := 2\nc := 3\n",
			search:  "a := 1\nb := 2\nc := 3",
			want:    MatchBlankLine, found: true, matchTxt: "a := 1\n\n\nb := 2\nc := 3",
		},
		{
			name:    "anchored first and last line",
			content: "func F() int {\n\tx := 1\n\ty := 2\n\treturn x + y\n}\n",
			search:  "func F() int {\n\tSOMETHING ELSE\n\tENTIRELY\n\tDIFFERENT\n}",
			want:    MatchAnchored, found: true,
			matchTxt: "func F() int {\n\tx := 1\n\ty := 2\n\treturn x + y\n}",
		},
		{
			name: "no match at all", content: body,
			search: "func Nothing() {}", found: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := FindEditMatch(tc.content, tc.search)
			if res.Found != tc.found {
				t.Fatalf("found=%v want %v (ambiguous=%v/%d %s)",
					res.Found, tc.found, res.Ambiguous, res.AmbigN, res.AmbigWhat)
			}
			if !tc.found {
				return
			}
			if res.Match.Strategy != tc.want {
				t.Fatalf("strategy=%s want %s", res.Match.Strategy, tc.want)
			}
			got := tc.content[res.Match.Start:res.Match.End]
			if got != tc.matchTxt {
				t.Fatalf("span=%q want %q", got, tc.matchTxt)
			}
		})
	}
}

func TestFindEditMatchNeverAppliesAmbiguous(t *testing.T) {
	content := "func a() {\n\treturn\n}\n\nfunc b() {\n\treturn\n}\n"
	res := FindEditMatch(content, "\treturn")
	if res.Found {
		t.Fatalf("ambiguous search must not resolve, got span %q",
			content[res.Match.Start:res.Match.End])
	}
	if !res.Ambiguous || res.AmbigN < 2 {
		t.Fatalf("expected ambiguity report, got %+v", res)
	}
}

func TestFindEditMatchInWindow(t *testing.T) {
	content := "x\ny\nTARGET\nz\nTARGET\nw\n"
	// Whole file: two matches → ambiguous.
	if res := FindEditMatch(content, "TARGET"); res.Found {
		t.Fatal("expected ambiguity over the whole file")
	}
	// Window covering only the first occurrence → unique.
	lo, hi := 0, strings.Index(content, "z")
	res := FindEditMatchIn(content, "TARGET", lo, hi)
	if !res.Found || res.Match.Start != strings.Index(content, "TARGET") {
		t.Fatalf("windowed match failed: %+v", res)
	}
}

func TestApplyEditReplacementNewlines(t *testing.T) {
	cases := []struct {
		name             string
		content, old, nw string
		want             string
	}{
		{
			name:    "exact keeps replacement verbatim",
			content: "alpha\nbeta\n", old: "alpha\n", nw: "ALPHA\n",
			want: "ALPHA\nbeta\n",
		},
		{
			name: "line strategy does not double the newline",
			// trailing spaces force the trailing-whitespace strategy
			content: "alpha  \nbeta\n", old: "alpha\n", nw: "ALPHA\n",
			want: "ALPHA\nbeta\n",
		},
		{
			name:    "line strategy keeps an intentionally added blank line",
			content: "alpha\t\nbeta\n", old: "alpha\n", nw: "ALPHA\n\n",
			want: "ALPHA\n\nbeta\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := FindEditMatch(tc.content, tc.old)
			if !res.Found {
				t.Fatalf("no match for %q in %q", tc.old, tc.content)
			}
			got := ApplyEditReplacement(tc.content, res.Match, tc.old, tc.nw)
			if got != tc.want {
				t.Fatalf("got %q want %q (strategy %s)", got, tc.want, res.Match.Strategy)
			}
		})
	}
}

func TestApplyEditMatchReindents(t *testing.T) {
	content := "class A:\n    def go(self):\n        return 1\n"
	res := FindEditMatch(content, "def go(self):\n    return 1")
	if !res.Found || res.Match.Strategy != MatchIndent {
		t.Fatalf("expected indent strategy, got %+v", res)
	}
	got := ApplyEditReplacement(content, res.Match, "def go(self):\n    return 1", "def go(self):\n    return 2")
	if got != "class A:\n    def go(self):\n        return 2\n" {
		t.Fatalf("reindent failed: %q", got)
	}
}

func TestStrategyNoteMentionsDrift(t *testing.T) {
	if StrategyNote(MatchExact) != "" {
		t.Fatal("exact matches need no note")
	}
	for _, s := range []string{MatchTrailingWS, MatchIndent, MatchBlankLine, MatchAnchored} {
		if note := StrategyNote(s); note == "" || !strings.Contains(note, "matched") {
			t.Fatalf("strategy %s note=%q", s, note)
		}
	}
}

func TestRelativeIndents(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []int
	}{
		{"flat", []string{"a", "b"}, []int{0, 0}},
		{"nested", []string{"a", "  b", "    c"}, []int{0, 2, 4}},
		{"shifted keeps relative", []string{"    a", "      b"}, []int{0, 2}},
		{"blank is -1", []string{"a", "", "  b"}, []int{0, -1, 2}},
		{"tab counts as four", []string{"a", "\tb"}, []int{0, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeIndents(tc.lines)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}
