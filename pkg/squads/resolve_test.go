package squads

import "testing"

// The reference a model writes and the id the library uses are not the same
// string, and never will be. A clause whose provider does not resolve is owed
// by nobody: Validate rejects it, and the run loses the frozen seam it was
// supposed to be protected by — over a suffix.
func TestResolveRefForgivesTheSuffixAModelDrops(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "backend-go", Owns: []string{"cmd/**"}},
		{ID: "frontend-react", Owns: []string{"web/**"}},
	}}

	for ref, want := range map[string]string{
		"backend-go":     "backend-go",
		"backend":        "backend-go",
		"Backend":        "backend-go",
		" backend ":      "backend-go",
		"frontend":       "frontend-react",
		"react":          "frontend-react",
		"frontend-react": "frontend-react",
		// The other direction: the model wrote a longer id than the plan holds.
		"go": "backend-go",
	} {
		got, ok := p.ResolveRef(ref)
		if !ok || got != want {
			t.Errorf("ResolveRef(%q) = %q,%v — want %q", ref, got, ok, want)
		}
	}
}

// Ambiguity is a real question only the plan's author can answer. Guessing puts
// a contract clause on the wrong team, which is worse than dropping it: the
// wrong half then owns an interface it was never told to build.
func TestResolveRefRefusesToGuessBetweenTwoTeams(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "backend-go", Owns: []string{"cmd/**"}},
		{ID: "backend-node", Owns: []string{"server/**"}},
	}}

	if id, ok := p.ResolveRef("backend"); ok {
		t.Fatalf("ResolveRef(\"backend\") = %q — two teams claim that prefix", id)
	}
	// Each full id still resolves exactly.
	for _, id := range []string{"backend-go", "backend-node"} {
		if got, ok := p.ResolveRef(id); !ok || got != id {
			t.Errorf("ResolveRef(%q) = %q,%v", id, got, ok)
		}
	}
}

func TestResolveRefOnNothing(t *testing.T) {
	p := &Plan{Squads: []Squad{{ID: "backend", Owns: []string{"cmd/**"}}}}
	for _, ref := range []string{"", "   ", "docs", "!!!"} {
		if id, ok := p.ResolveRef(ref); ok {
			t.Errorf("ResolveRef(%q) = %q — nothing should match", ref, id)
		}
	}
	var nilPlan *Plan
	if _, ok := nilPlan.ResolveRef("backend"); ok {
		t.Error("a nil plan resolves nothing")
	}
}

// The exact match wins even when a looser one would also fit, or a plan holding
// both `api` and `api-gateway` would route `api` to the wrong one.
func TestResolveRefPrefersAnExactMatch(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "api-gateway", Owns: []string{"gw/**"}},
		{ID: "api", Owns: []string{"api/**"}},
	}}
	if got, ok := p.ResolveRef("api"); !ok || got != "api" {
		t.Fatalf("ResolveRef(\"api\") = %q,%v — want the exact team", got, ok)
	}
}
