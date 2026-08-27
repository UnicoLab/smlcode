package composer

import (
	"strings"
	"testing"
)

// ── Classification ────────────────────────────────────────────────────────

func TestClassifyCriticalSubjects(t *testing.T) {
	for _, q := range []string{
		"fix the password comparison in auth.go",
		"add a new payment method to checkout",
		"rotate the production API key",
		"write a migration that will drop table sessions",
		"change how we store the private key",
		"refactor the authz middleware",
	} {
		got := Classify(q)
		if got.Complexity != ComplexityCritical {
			t.Errorf("Classify(%q).Complexity = %q, want critical (%s)", q, got.Complexity, got.Why)
		}
		if !got.Confident {
			t.Errorf("Classify(%q) not confident about a critical subject", q)
		}
	}
}

func TestClassifyCriticalOutranksTrivial(t *testing.T) {
	// A rename IS a mechanical edit. A rename inside auth code is still a
	// change to auth code, and the harness cannot tell from the query which
	// one merely moves text.
	got := Classify("rename the password hashing helper in auth.go")
	if got.Complexity != ComplexityCritical {
		t.Fatalf("Complexity = %q, want critical", got.Complexity)
	}
}

func TestClassifyDoesNotMatchSubjectsMidWord(t *testing.T) {
	// "auth" inside "author", "pii" inside "piint". A one-word coincidence
	// must not re-route a whole run onto the most expensive budget.
	for _, q := range []string{
		"add the author field to the post model",
		"update the authoritative list of mirrors in docs.md",
	} {
		if got := Classify(q); got.Complexity == ComplexityCritical {
			t.Errorf("Classify(%q) = critical on a mid-word match (%s)", q, got.Why)
		}
	}
}

func TestClassifyMatchesInflectedSignals(t *testing.T) {
	// The list cannot spell out every form a person types, and the form that
	// got missed decided a whole run's budget. An inflection may only EXTEND a
	// word — never let a different one through.
	for _, q := range []string{
		"the parser panics on empty input",
		"the worker panicked mid-wave",
		"the run crashes when the board is empty",
	} {
		if got := Classify(q); got.Kind != KindDebug {
			t.Errorf("Classify(%q).Kind = %q, want debug", q, got.Kind)
		}
	}
	for _, q := range []string{
		"add payments to the invoice model",
		"rotate the signing keys",
	} {
		if got := Classify(q); got.Complexity != ComplexityCritical {
			t.Errorf("Classify(%q).Complexity = %q, want critical", q, got.Complexity)
		}
	}
	// …and the boundary still holds against a longer, unrelated word.
	for _, q := range []string{
		"add the author field to the post model",
		"rename the secretary role in roles.go",
	} {
		if got := Classify(q); got.Complexity == ComplexityCritical {
			t.Errorf("Classify(%q) = critical: an inflection matched a different word (%s)", q, got.Why)
		}
	}
}

func TestClassifyInquiry(t *testing.T) {
	for _, q := range []string{
		"what does the retry ladder do?",
		"explain how the context packer budgets tokens",
		"where is the board persisted",
		"show me the phases in the default pipeline",
	} {
		got := Classify(q)
		if got.Kind != KindInquiry {
			t.Errorf("Classify(%q).Kind = %q, want inquiry (%s)", q, got.Kind, got.Why)
		}
	}
}

func TestClassifyWriteVerbOverridesInterrogativeOpener(t *testing.T) {
	// "How do I add X" reads like a question and is a request to write code.
	for _, q := range []string{
		"how do I add rate limiting — add it to the server",
		"what if we implement a retry in client.go",
	} {
		got := Classify(q)
		if got.Kind == KindInquiry {
			t.Errorf("Classify(%q).Kind = inquiry despite a write verb (%s)", q, got.Why)
		}
	}
}

func TestClassifyDebug(t *testing.T) {
	for _, q := range []string{
		"the parser panics on empty input",
		"tests are failing after the merge",
		"fix the bug in the retry ladder",
		"why does the run hangs at the review phase",
	} {
		got := Classify(q)
		if got.Kind != KindDebug {
			t.Errorf("Classify(%q).Kind = %q, want debug (%s)", q, got.Kind, got.Why)
		}
	}
}

func TestClassifyTrivialNeedsBothVerbAndNarrowTarget(t *testing.T) {
	got := Classify("fix the typo in README.md")
	if got.Complexity != ComplexityTrivial || !got.Confident {
		t.Fatalf("Classify = %+v, want confident trivial", got)
	}
	// A mechanical verb across many files is not a trivial change.
	wide := Classify("rename the Sum symbol in calc.go, math.go, util.go and api.go")
	if wide.Complexity == ComplexityTrivial {
		t.Errorf("a four-file rename classified trivial: %+v", wide)
	}
}

func TestClassifyDefaultsToUnconfidentStandard(t *testing.T) {
	// The honest answer for the ambiguous middle: these heuristics cannot tell
	// a one-file addition from a subsystem, and pretending otherwise is how a
	// budget class becomes a correctness regression.
	for _, q := range []string{
		"add rate limiting to the API",
		"implement a cache for the repo map",
		"port the CLI to cobra",
	} {
		got := Classify(q)
		if got.Complexity != ComplexityStandard {
			t.Errorf("Classify(%q).Complexity = %q, want standard", q, got.Complexity)
		}
		if got.Confident {
			t.Errorf("Classify(%q) claimed confidence with no decisive signal (%s)", q, got.Why)
		}
	}
}

func TestClassifyEmptyQuery(t *testing.T) {
	got := Classify("")
	if got.Complexity != ComplexityStandard || got.Kind != KindTask || got.Confident {
		t.Fatalf("Classify(\"\") = %+v", got)
	}
}

func TestClassifyAlwaysExplainsItself(t *testing.T) {
	for _, q := range []string{"", "fix the typo in a.go", "what is this", "add rate limiting"} {
		if strings.TrimSpace(Classify(q).Why) == "" {
			t.Errorf("Classify(%q) gave no reason", q)
		}
	}
}

// ── Profiles ──────────────────────────────────────────────────────────────

func TestProfileForNormalizesUnknownInput(t *testing.T) {
	// An unrecognized class is a classifier bug, and the right response to a
	// classifier bug is the middle of the range — not a crash, not a free pass.
	p := ProfileFor("wildly-unknown", "also-unknown")
	if p.Complexity != ComplexityStandard || p.Kind != KindTask {
		t.Fatalf("ProfileFor(unknown) = %s", p)
	}
	if len(p.Phases) == 0 {
		t.Error("unknown class produced an empty pipeline")
	}
}

func TestProfileBudgetsIncreaseWithComplexity(t *testing.T) {
	triv := ProfileFor(ComplexityTrivial, KindTask)
	simp := ProfileFor(ComplexitySimple, KindTask)
	std := ProfileFor(ComplexityStandard, KindTask)
	crit := ProfileFor(ComplexityCritical, KindTask)

	if len(triv.Phases) > len(simp.Phases) ||
		len(simp.Phases) > len(std.Phases) ||
		len(std.Phases) >= len(crit.Phases) {
		t.Errorf("phase counts not monotonic: %d %d %d %d",
			len(triv.Phases), len(simp.Phases), len(std.Phases), len(crit.Phases))
	}
	if triv.MaxWaves > simp.MaxWaves || simp.MaxWaves > std.MaxWaves || std.MaxWaves >= crit.MaxWaves {
		t.Errorf("wave budgets not monotonic: %d %d %d %d",
			triv.MaxWaves, simp.MaxWaves, std.MaxWaves, crit.MaxWaves)
	}
	if triv.QAGateRounds > std.QAGateRounds || std.QAGateRounds >= crit.QAGateRounds {
		t.Errorf("QA rounds not monotonic: %d %d %d", triv.QAGateRounds, std.QAGateRounds, crit.QAGateRounds)
	}
}

func TestEveryProfileKeepsTheFourCriticalPhases(t *testing.T) {
	// plan/split/execute/test are re-enabled unconditionally by the
	// orchestrator. A profile that omits them is not narrowing the pipeline —
	// it is describing a pipeline that cannot happen, and the mismatch would
	// show up as phases appearing that the class never asked for.
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard, ComplexityCritical} {
		for _, k := range []string{KindInquiry, KindTask, KindDebug} {
			set := ProfileFor(cx, k).PhaseSet()
			for _, need := range []string{"plan", "split", "execute", "test"} {
				if !set[need] {
					t.Errorf("%s:%s omits the critical phase %q", cx, k, need)
				}
			}
		}
	}
}

func TestNoProfileDisablesStaticQuality(t *testing.T) {
	// The static gate costs no LLM call and catches a stub. There is no budget
	// at which switching it off is a saving worth having.
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard, ComplexityCritical} {
		for _, k := range []string{KindInquiry, KindTask, KindDebug} {
			if !ProfileFor(cx, k).StaticQuality {
				t.Errorf("%s:%s disabled the static quality gate", cx, k)
			}
		}
	}
}

func TestOnlyInquiryDropsTheSmokeRequirement(t *testing.T) {
	// The single deliberate gate downgrade, and the reason it is safe: an
	// inquiry writes nothing, so RequireSmoke is a gate it cannot satisfy.
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard, ComplexityCritical} {
		for _, k := range []string{KindTask, KindDebug} {
			if !ProfileFor(cx, k).RequireSmoke {
				t.Errorf("%s:%s dropped RequireSmoke on work that writes code", cx, k)
			}
		}
		if ProfileFor(cx, KindInquiry).RequireSmoke {
			t.Errorf("%s:inquiry demands a smoke pass it can never produce", cx)
		}
	}
}

func TestInquiryIgnoresComplexityForBreadth(t *testing.T) {
	// "Explain the auth flow" is critical by SUBJECT and read-only in fact.
	// Its cost is bounded by what it reads, not by how dangerous the topic is.
	crit := ProfileFor(ComplexityCritical, KindInquiry)
	simple := ProfileFor(ComplexitySimple, KindInquiry)
	if len(crit.Phases) != len(simple.Phases) {
		t.Errorf("inquiry breadth varied with complexity: %d vs %d", len(crit.Phases), len(simple.Phases))
	}
	if crit.QAGateRounds != 0 {
		t.Errorf("a read-only inquiry was given %d QA rounds", crit.QAGateRounds)
	}
	// …but the strict reviewer still engages on a dangerous subject.
	if !crit.StrictReview {
		t.Error("a critical-subject inquiry lost the strict reviewer")
	}
}

func TestOnlyCriticalEngagesStrictReview(t *testing.T) {
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard} {
		if ProfileFor(cx, KindTask).StrictReview {
			t.Errorf("%s spent a second reviewer call", cx)
		}
	}
	if !ProfileFor(ComplexityCritical, KindTask).StrictReview {
		t.Error("critical work did not engage the strict reviewer")
	}
}

func TestDebugAlwaysGetsAtLeastTwoWaves(t *testing.T) {
	// One wave to reproduce, one to fix. A debug run capped at a single wave
	// can only ever guess.
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard, ComplexityCritical} {
		if got := ProfileFor(cx, KindDebug).MaxWaves; got < 2 {
			t.Errorf("%s:debug capped at %d wave(s)", cx, got)
		}
	}
}

func TestProfileStringNamesTheClass(t *testing.T) {
	s := ProfileFor(ComplexityCritical, KindDebug).String()
	if !strings.Contains(s, ComplexityCritical) || !strings.Contains(s, KindDebug) {
		t.Errorf("Profile.String() = %q", s)
	}
}

func TestEveryProfileExplainsItself(t *testing.T) {
	for _, cx := range []string{ComplexityTrivial, ComplexitySimple, ComplexityStandard, ComplexityCritical} {
		for _, k := range []string{KindInquiry, KindTask, KindDebug} {
			if strings.TrimSpace(ProfileFor(cx, k).Why) == "" {
				t.Errorf("%s:%s has no operator-facing rationale", cx, k)
			}
		}
	}
}

// ── Composition integration ───────────────────────────────────────────────

func TestNormalizeFillsTheBudgetClass(t *testing.T) {
	c := Composition{Summary: "s"}
	c.Normalize()
	if c.Complexity != ComplexityStandard || c.Kind != KindTask {
		t.Fatalf("Normalize left the class as %q/%q", c.Complexity, c.Kind)
	}
	// …and a model-authored class survives.
	c2 := Composition{Complexity: "CRITICAL", Kind: "Debug"}
	c2.Normalize()
	if c2.Complexity != ComplexityCritical || c2.Kind != KindDebug {
		t.Fatalf("Normalize mangled a supplied class: %q/%q", c2.Complexity, c2.Kind)
	}
}

func TestApplyProfileOnlyFillsGaps(t *testing.T) {
	// Precedence is one-directional: a composer that named its phases has
	// reasoned about THIS request, which beats a class derived from a string.
	c := Composition{
		Phases:  []PhaseChoice{{ID: "execute", Enabled: true}},
		Execute: ExecuteChoice{MaxWaves: 5},
	}
	c.ApplyProfile(ProfileFor(ComplexityCritical, KindTask))
	if len(c.Phases) != 1 || c.Phases[0].ID != "execute" {
		t.Errorf("explicit phases were overridden: %+v", c.Phases)
	}
	if c.Execute.MaxWaves != 5 {
		t.Errorf("explicit max_waves was overridden: %d", c.Execute.MaxWaves)
	}
	if c.Complexity != ComplexityCritical {
		t.Errorf("class was not recorded: %q", c.Complexity)
	}
}

func TestApplyProfileSeedsAnEmptyComposition(t *testing.T) {
	c := Composition{}
	p := ProfileFor(ComplexitySimple, KindTask)
	c.ApplyProfile(p)
	if len(c.Phases) != len(p.Phases) {
		t.Fatalf("phases = %d, want %d", len(c.Phases), len(p.Phases))
	}
	if c.Execute.MaxWaves != p.MaxWaves {
		t.Errorf("max_waves = %d, want %d", c.Execute.MaxWaves, p.MaxWaves)
	}
	for _, ph := range c.Phases {
		if !ph.Enabled {
			t.Errorf("seeded phase %q is disabled", ph.ID)
		}
	}
}

func TestCompositionProfileIsSafeOnZeroValue(t *testing.T) {
	p := Composition{}.Profile()
	if p.Complexity != ComplexityStandard || len(p.Phases) == 0 {
		t.Fatalf("zero-value Composition.Profile() = %s", p)
	}
}

func TestApplyProfileIsNilSafe(t *testing.T) {
	var c *Composition
	c.ApplyProfile(ProfileFor(ComplexityStandard, KindTask)) // must not panic
}
