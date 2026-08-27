package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func phaseIDs(c composer.Composition) map[string]bool {
	out := map[string]bool{}
	for _, p := range c.Phases {
		if p.Enabled && p.When != pipeline.WhenNever {
			out[p.ID] = true
		}
	}
	return out
}

func TestHeuristicCompositionRecordsTheBudgetClass(t *testing.T) {
	c := heuristicComposition("fix the typo in README.md", nil, "go", "", "")
	if c.Complexity != composer.ComplexityTrivial {
		t.Errorf("Complexity = %q, want trivial", c.Complexity)
	}
	if c.Kind != composer.KindTask {
		t.Errorf("Kind = %q, want task", c.Kind)
	}
}

func TestConfidentTrivialClassNarrowsTheHeuristicPipeline(t *testing.T) {
	trivial := heuristicComposition("fix the typo in README.md", nil, "go", "", "")
	standard := heuristicComposition("add rate limiting to the API", nil, "go", "", "")
	if len(trivial.Phases) >= len(standard.Phases) {
		t.Fatalf("trivial kept %d phases vs standard's %d — the class bought nothing",
			len(trivial.Phases), len(standard.Phases))
	}
	// The expensive breadth is what goes.
	got := phaseIDs(trivial)
	for _, gone := range []string{"architect", "docs", "coord", "polish"} {
		if got[gone] {
			t.Errorf("trivial pipeline kept the %q phase", gone)
		}
	}
}

func TestBudgetClassNeverDropsTheFourCriticalPhases(t *testing.T) {
	// Narrowing must never reach plan/split/execute/test: without planning a
	// run falls through to fallbackTasks, whose acceptance is the unrunnable
	// string "Query goals met" — which would silently gut the criteria gate.
	for _, q := range []string{
		"fix the typo in README.md",
		"what does the retry ladder do?",
		"rotate the production API key",
		"add rate limiting to the API",
	} {
		got := phaseIDs(heuristicComposition(q, nil, "go", "", ""))
		for _, need := range []string{"plan", "split", "execute", "test"} {
			if !got[need] {
				t.Errorf("query %q dropped the critical phase %q", q, need)
			}
		}
	}
}

func TestUnconfidentClassLeavesTheHeuristicShapeAlone(t *testing.T) {
	// The classifier admitting it cannot tell a one-file addition from a
	// subsystem is not a license to trim that subsystem's pipeline.
	q := "add rate limiting to the API"
	if cls := composer.Classify(q); cls.Confident {
		t.Skipf("query %q became confidently classified — pick another fixture", q)
	}
	withClass := heuristicComposition(q, nil, "go", "", "")
	if withClass.Complexity != composer.ComplexityStandard {
		t.Errorf("Complexity = %q, want standard", withClass.Complexity)
	}
	// The standard profile is a superset of what the heuristics build for a
	// plain feature request, so nothing should have been trimmed away.
	got := phaseIDs(withClass)
	for _, need := range []string{"context", "explore", "plan", "split", "execute", "test"} {
		if !got[need] {
			t.Errorf("unconfident class trimmed %q", need)
		}
	}
}

func TestCriticalClassWidensThePipeline(t *testing.T) {
	c := heuristicComposition("rotate the production API key", nil, "go", "", "")
	if c.Complexity != composer.ComplexityCritical {
		t.Fatalf("Complexity = %q, want critical", c.Complexity)
	}
	got := phaseIDs(c)
	for _, need := range []string{"architect", "docs", "polish", "coord", "memory"} {
		if !got[need] {
			t.Errorf("critical class did not enable %q", need)
		}
	}
	if c.Execute.MaxWaves < 4 {
		t.Errorf("critical max_waves = %d, want at least 4", c.Execute.MaxWaves)
	}
}

func TestBudgetClassIsExplainedInTheStrategy(t *testing.T) {
	c := heuristicComposition("fix the typo in README.md", nil, "go", "", "")
	if !strings.Contains(c.Strategy, "budget class") {
		t.Errorf("strategy does not explain the class: %q", c.Strategy)
	}
}

// ── applyBudgetProfile: the config-ceiling asymmetry ──────────────────────

func TestApplyBudgetProfileNeverEnablesADisabledGate(t *testing.T) {
	// A gate the operator switched off is a decision the harness has no
	// business reversing, however dangerous the class.
	cfg := &config.Config{RequireSmoke: false, StaticQuality: false}
	r := &loop.Runner{RequireSmoke: false, StaticQuality: false}
	applyBudgetProfile(r, cfg, composer.ProfileFor(composer.ComplexityCritical, composer.KindTask))
	if r.RequireSmoke || r.StaticQuality {
		t.Fatalf("a budget class re-enabled operator-disabled gates: smoke=%v static=%v",
			r.RequireSmoke, r.StaticQuality)
	}
}

func TestApplyBudgetProfileMayDisableSmokeForAnInquiry(t *testing.T) {
	// The one deliberate downgrade: an inquiry writes nothing, so demanding a
	// smoke PASS is a gate it cannot satisfy — a deadlock, not a safeguard.
	cfg := &config.Config{RequireSmoke: true, StaticQuality: true}
	r := &loop.Runner{RequireSmoke: true, StaticQuality: true}
	applyBudgetProfile(r, cfg, composer.ProfileFor(composer.ComplexitySimple, composer.KindInquiry))
	if r.RequireSmoke {
		t.Error("an inquiry kept a smoke requirement it can never satisfy")
	}
	if !r.StaticQuality {
		t.Error("the free static gate was switched off")
	}
}

func TestApplyBudgetProfileDeepensButNeverShallowsThinkPasses(t *testing.T) {
	cfg := &config.Config{}
	// Critical deepens a shallow config.
	deep := &loop.Runner{ThinkPasses: 1}
	applyBudgetProfile(deep, cfg, composer.ProfileFor(composer.ComplexityCritical, composer.KindTask))
	if deep.ThinkPasses != 3 {
		t.Errorf("critical think passes = %d, want 3", deep.ThinkPasses)
	}
	// A trivial class must NOT undercut an operator who asked for more.
	shallow := &loop.Runner{ThinkPasses: 4}
	applyBudgetProfile(shallow, cfg, composer.ProfileFor(composer.ComplexityTrivial, composer.KindTask))
	if shallow.ThinkPasses != 4 {
		t.Errorf("trivial class shallowed think passes to %d", shallow.ThinkPasses)
	}
}

func TestApplyBudgetProfileOnlyFillsAnUnsetWaveBudget(t *testing.T) {
	cfg := &config.Config{}
	set := &loop.Runner{MaxWaves: 7}
	applyBudgetProfile(set, cfg, composer.ProfileFor(composer.ComplexityTrivial, composer.KindTask))
	if set.MaxWaves != 7 {
		t.Errorf("an explicit wave budget was overridden: %d", set.MaxWaves)
	}
	unset := &loop.Runner{}
	applyBudgetProfile(unset, cfg, composer.ProfileFor(composer.ComplexityCritical, composer.KindTask))
	if unset.MaxWaves != 4 {
		t.Errorf("unset wave budget = %d, want the class default 4", unset.MaxWaves)
	}
}

func TestApplyBudgetProfileIsNilSafe(t *testing.T) {
	p := composer.ProfileFor(composer.ComplexityStandard, composer.KindTask)
	applyBudgetProfile(nil, &config.Config{}, p)
	applyBudgetProfile(&loop.Runner{}, nil, p)
}

func TestBudgetProfileAbsentWithoutADynamicComposition(t *testing.T) {
	// A static pipeline is the operator saying "run what I configured".
	o := &Orchestrator{}
	if _, ok := o.budgetProfile(); ok {
		t.Error("a class was reported with no dynamic composition active")
	}
	var nilOrch *Orchestrator
	if _, ok := nilOrch.budgetProfile(); ok {
		t.Error("a nil orchestrator reported a class")
	}
}

func TestBudgetProfileReadsTheActiveComposition(t *testing.T) {
	o := &Orchestrator{}
	comp := composer.Composition{Complexity: composer.ComplexityCritical, Kind: composer.KindDebug}
	o.dynamicComposition = &comp
	got, ok := o.budgetProfile()
	if !ok {
		t.Fatal("no class reported for an active composition")
	}
	if got.Complexity != composer.ComplexityCritical || got.Kind != composer.KindDebug {
		t.Errorf("budgetProfile = %s", got)
	}
}
