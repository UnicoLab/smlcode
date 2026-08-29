package orchestrator

import (
	"strings"
	"testing"
)

func TestQueryLanguagesNamed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			"full stack names both",
			"Build a Go REST backend with net/http and a React frontend in web/",
			[]string{"go-worker", "react-worker"},
		},
		{"single language names one", "add a pytest for the parser", []string{"python-worker"}},
		{"no language named", "make the button bigger", nil},
		{"golang spelling", "a golang service", []string{"go-worker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := queryLanguagesNamed(tc.query)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// " go " is padded so it cannot match inside another word. Without that,
// "django" and "logo" would each add a Go specialist to the roster.
func TestBareGoKeywordDoesNotMatchInsideWords(t *testing.T) {
	for _, q := range []string{"a django app", "update the logo", "the cargo manifest"} {
		for _, id := range queryLanguagesNamed(q) {
			if id == "go-worker" {
				t.Errorf("%q matched go-worker", q)
			}
		}
	}
}

// The contradiction that motivated the change: one contract must not name a
// single owner for a request whose halves are in different languages.
func TestMultiLanguageHandoffNamesTheRuleNotOneOwner(t *testing.T) {
	line := specialistHandoffLine(
		"Go REST backend plus a React frontend", "react-worker", "react-tester")
	if strings.Contains(line, "Use react-worker for implementation") {
		t.Errorf("full-stack handoff still names one owner: %q", line)
	}
	for _, want := range []string{"go-worker", "react-worker", "owns its files"} {
		if !strings.Contains(line, want) {
			t.Errorf("handoff %q is missing %q", line, want)
		}
	}
}

// The single-language wording is unchanged — this fix adds a case, it does not
// rewrite the common one.
func TestSingleLanguageHandoffIsUnchanged(t *testing.T) {
	line := specialistHandoffLine("add a pytest for the parser", "python-worker", "python-tester")
	want := "Use python-worker for implementation and python-tester for verification"
	if line != want {
		t.Errorf("got %q, want %q", line, want)
	}
}

// A composition for a full-stack request must not carry a verification command
// that only one half of the board could run alongside a single-owner claim.
// This is TestHeuristicCompositionKeepsTheTeamAndVerificationConsistent's
// invariant, extended to the greenfield multi-language case it did not cover.
func TestFullStackCompositionHasNoContradictoryHandoff(t *testing.T) {
	comp := heuristicComposition(
		"Build a Go REST backend and a React frontend in web/ with tests", nil, "", "", "")
	var owner, verify string
	for _, h := range comp.Handoff {
		if strings.Contains(h, "for implementation and") {
			owner = h
		}
		if strings.HasPrefix(h, "Verify with ") || strings.HasPrefix(h, "verify with ") {
			verify = h
		}
	}
	if owner != "" && verify != "" {
		t.Errorf("handoff names a single owner %q while prescribing %q", owner, verify)
	}
}
