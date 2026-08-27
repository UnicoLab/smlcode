package orchestrator

import "testing"

func TestQueryHintNeverOverridesAnUncorroboratedProjectLanguage(t *testing.T) {
	// The reported failure, exactly: a Go repository whose query mentioned
	// Python assembled python-worker + python-tester while the same handoff
	// contract said "Detected project language: Go" and "verify with
	// go test ./... -count=1". Briefing the workers on one language and the
	// verification on another is not a suboptimal team, it is a broken one.
	inv := []string{"main.go", "pkg/loop/runner.go", "go.mod"}
	worker, tester := pickSpecialists("port the python script logic into the parser", "go", inv)
	if worker != "go-worker" || tester != "go-tester" {
		t.Fatalf("pickSpecialists = (%q, %q), want the Go pair", worker, tester)
	}
}

func TestQueryHintWinsWhenTheInventoryCorroboratesIt(t *testing.T) {
	// A polyglot repository: the top-level detector says Go, the request is
	// about the web tree, and the web tree exists. Here the query knows
	// something the detector does not.
	inv := []string{"main.go", "go.mod", "web/index.html", "web/app.css"}
	worker, tester := pickSpecialists("fix the css on the html landing page", "go", inv)
	if worker != "web-worker" || tester != "web-tester" {
		t.Fatalf("pickSpecialists = (%q, %q), want the web pair", worker, tester)
	}
}

func TestQueryHintAppliesWhenTheProjectLanguageIsUnknown(t *testing.T) {
	// Greenfield: an empty directory has nothing else to go on, and falling
	// back to a generic worker would throw away the only signal there is.
	worker, tester := pickSpecialists("write a python CLI that reverses a string", "", nil)
	if worker != "python-worker" || tester != "python-tester" {
		t.Fatalf("pickSpecialists = (%q, %q), want the Python pair", worker, tester)
	}
}

func TestProjectLanguageIsUsedWithNoQueryHint(t *testing.T) {
	worker, tester := pickSpecialists("make the retry ladder converge faster", "rust",
		[]string{"src/main.rs", "Cargo.toml"})
	if worker != "rust-worker" || tester != "rust-tester" {
		t.Fatalf("pickSpecialists = (%q, %q), want the Rust pair", worker, tester)
	}
}

func TestGenericPairWhenNothingIsKnown(t *testing.T) {
	worker, tester := pickSpecialists("do the thing", "", nil)
	if worker != "worker" || tester != "tester" {
		t.Fatalf("pickSpecialists = (%q, %q), want the generic pair", worker, tester)
	}
}

func TestInventoryHasLanguage(t *testing.T) {
	inv := []string{"main.go", "web/index.html", "scripts/build.sh", "README.md", "no-extension"}
	cases := map[string]bool{
		"go-worker":     true,
		"web-worker":    true,
		"shell-worker":  true,
		"python-worker": false,
		"rust-worker":   false,
		// A worker with no extension mapping can never be corroborated, which
		// is the safe direction: it defers to the detected project language.
		"worker": false,
		"":       false,
	}
	for worker, want := range cases {
		if got := inventoryHasLanguage(worker, inv); got != want {
			t.Errorf("inventoryHasLanguage(%q) = %v, want %v", worker, got, want)
		}
	}
}

func TestInventoryHasLanguageIgnoresCaseAndSpace(t *testing.T) {
	if !inventoryHasLanguage("  GO-Worker  ", []string{"MAIN.GO"}) {
		t.Error("case or surrounding space defeated corroboration")
	}
}

func TestHeuristicCompositionKeepsTheTeamAndVerificationConsistent(t *testing.T) {
	// End to end through the composition: whatever pair is chosen, the handoff
	// contract must not tell the workers to verify in a different language.
	inv := []string{"main.go", "go.mod"}
	comp := heuristicComposition("add python-style docstrings to the exported funcs", inv, "go", "", "")
	if comp.Execute.DefaultRole != "go-worker" {
		t.Fatalf("worker = %q, want go-worker", comp.Execute.DefaultRole)
	}
	for _, h := range comp.Handoff {
		if containsAny(h, "python-worker", "python-tester", "pytest") {
			t.Errorf("handoff contradicts the Go team: %q", h)
		}
	}
}
