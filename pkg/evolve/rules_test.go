package evolve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRules(t *testing.T) *Rules {
	t.Helper()
	r, err := OpenRules(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenRules: %v", err)
	}
	return r
}

func TestSeedRulesAreValidAndUnique(t *testing.T) {
	seeds := SeedRules()
	if len(seeds) < 15 {
		t.Fatalf("only %d seeded rules; the shipped set is too thin to be useful on day one", len(seeds))
	}
	ids := map[string]bool{}
	for _, r := range seeds {
		if err := r.Repair.Validate(); err != nil {
			t.Errorf("seed %s has an invalid repair: %v", r.ID, err)
		}
		if !r.Seeded || r.Scope != ScopeBuiltin {
			t.Errorf("seed %s is not marked builtin: %+v", r.ID, r)
		}
		if r.Trigger.Class == "" {
			t.Errorf("seed %s has no class in its trigger — it would match everything", r.ID)
		}
		if ids[r.ID] {
			t.Errorf("duplicate seed id %s", r.ID)
		}
		ids[r.ID] = true
		if c := r.Confidence(); c < 0.7 || c > 0.85 {
			t.Errorf("seed %s starts at confidence %.2f; seeds should start believed but not certain", r.ID, c)
		}
		if !r.Applicable() {
			t.Errorf("seed %s is not applicable out of the box", r.ID)
		}
	}
}

// The shipped rules must cover every failure mode the codebase already knows
// about, so a fresh install repairs them without ever consulting a model.
func TestSeededRulesCoverKnownFailureModes(t *testing.T) {
	r := newRules(t)
	cases := []struct {
		name     string
		sig      Signal
		wantKind RepairKind
		wantIn   string
	}{
		{
			name:     "ws_read line numbers leaked into old_str",
			sig:      Signal{Tool: "ws_edit", Message: "Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`)."},
			wantKind: RepairTransformArgs, wantIn: "gutter",
		},
		{
			name:     "old_str not found",
			sig:      Signal{Tool: "ws_edit", Message: "old_str not found in pkg/a/b.go."},
			wantKind: RepairAction, wantIn: "ws_read the file again",
		},
		{
			name:     "old_str found N times",
			sig:      Signal{Tool: "ws_edit", Message: "old_str found 4 times in pkg/a/b.go"},
			wantKind: RepairGuidance, wantIn: "surrounding context",
		},
		{
			name:     "json truncated by max_tokens",
			sig:      Signal{Message: "response stopped early: finish_reason: length (max_tokens=2048)"},
			wantKind: RepairAction, wantIn: "do NOT try to guess",
		},
		{
			name:     "multi-hunk diff failure",
			sig:      Signal{Tool: "ws_patch", Message: "hunk #3 FAILED at 210"},
			wantKind: RepairEditFormat, wantIn: "search/replace",
		},
		{
			name:     "context overflow",
			sig:      Signal{Message: "maximum context length is 32768 tokens, however you requested 40000"},
			wantKind: RepairAction, wantIn: "Compact",
		},
		{
			name:     "file must be read first",
			sig:      Signal{Tool: "ws_edit", Message: "File must be read first before edit — a.go has not been read in this session."},
			wantKind: RepairAction, wantIn: "must be ws_read",
		},
		{
			name:     "repeated identical tool call",
			sig:      Signal{Tool: "ws_read", Message: "Repeated identical tool call refused — you already called this tool with the same arguments."},
			wantKind: RepairAction, wantIn: "DIFFERENT action",
		},
		{
			name:     "shell command not permitted",
			sig:      Signal{Tool: "ws_shell", Message: "command is not allowed by the current permission mode"},
			wantKind: RepairGuidance, wantIn: "allowed equivalent",
		},
		{
			name:     "malformed json",
			sig:      Signal{Tool: "ws_edit", Message: "failed to parse tool arguments: unexpected end of JSON input"},
			wantKind: RepairTransformArgs, wantIn: "repaired mechanically",
		},
		{
			name:     "empty old_str",
			sig:      Signal{Tool: "ws_edit", Message: "Edit refused — old_str is empty (or only whitespace)."},
			wantKind: RepairGuidance, wantIn: "ws_write",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := r.Lookup(tc.sig)
			if !ok {
				t.Fatalf("no seeded rule matched a known failure mode:\n  %s", tc.sig.Message)
			}
			if s.Rule.Repair.Kind != tc.wantKind {
				t.Errorf("repair kind = %q, want %q (%s)", s.Rule.Repair.Kind, tc.wantKind, s.Rule.Repair.String())
			}
			if !strings.Contains(s.Rule.Repair.Guidance, tc.wantIn) {
				t.Errorf("guidance missing %q:\n  %s", tc.wantIn, s.Rule.Repair.Guidance)
			}
			if !s.Apply {
				t.Errorf("seeded rule was not confident enough to apply (%.2f)", s.Confidence)
			}
			if s.Reason() == "" {
				t.Error("suggestion has no reason")
			}
		})
	}
}

func TestLookupMissesUnknownFailures(t *testing.T) {
	r := newRules(t)
	if _, ok := r.Lookup(Signal{Message: "the flux capacitor is misaligned"}); ok {
		t.Error("an unclassifiable failure should not match a rule")
	}
	if _, ok := r.Lookup(Signal{}); ok {
		t.Error("an empty signal should not match a rule")
	}
}

func TestLookupPrefersExactFingerprintThenSpecificity(t *testing.T) {
	r, err := OpenRulesWith(t.TempDir(), t.TempDir(), RulesOptions{NoSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	sig := Signal{Tool: "ws_shell", Language: "go", Message: "./a.go:1:1: undefined: alpha"}
	fp := Analyze(sig)

	generic := Rule{
		Trigger: Trigger{Class: ClassCompileError},
		Repair:  Repair{Kind: RepairGuidance, Guidance: "generic"},
		Scope:   ScopeProject,
	}
	specific := Rule{
		Trigger: Trigger{Class: ClassCompileError, Tool: "ws_shell", Language: "go"},
		Repair:  Repair{Kind: RepairGuidance, Guidance: "specific"},
		Scope:   ScopeProject,
	}
	exact := Rule{
		Fingerprint: fp.ID,
		Trigger:     Trigger{Class: ClassTestFailure}, // deliberately non-matching trigger
		Repair:      Repair{Kind: RepairGuidance, Guidance: "exact"},
		Scope:       ScopeProject,
	}
	for _, rule := range []Rule{generic, specific} {
		rule.ID = RuleID(rule.Trigger, rule.Repair)
		r.insert(rule)
	}
	got, ok := r.Lookup(sig)
	if !ok || got.Rule.Repair.Guidance != "specific" {
		t.Fatalf("specificity tie-break failed: %+v", got.Rule.Repair)
	}
	exact.ID = RuleID(exact.Trigger, exact.Repair)
	r.insert(exact)
	got, ok = r.Lookup(sig)
	if !ok || got.Rule.Repair.Guidance != "exact" || !got.Exact {
		t.Fatalf("exact fingerprint did not win: %+v (exact=%v)", got.Rule.Repair, got.Exact)
	}
}

func TestRuleConfidenceEvolvesAndRetires(t *testing.T) {
	r, err := OpenRulesWith(t.TempDir(), t.TempDir(), RulesOptions{NoSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	sig := Signal{Tool: "ws_edit", Language: "go", Message: "old_str not found in a.go"}
	rule, ok := r.Learn(sig, Resolution{
		Repair:   Repair{Kind: RepairAction, Action: ActionRereadFile, Guidance: "re-read then retry"},
		Evidence: "ep_1",
	})
	if !ok {
		t.Fatal("Learn refused a valid resolution")
	}
	if c := rule.Confidence(); c > MinApplyConfidence {
		t.Errorf("a synthesized rule started at %.2f; it must start below the apply bar (%.2f)", c, MinApplyConfidence)
	}

	for i := 0; i < 3; i++ {
		r.Observe(rule.ID, true)
	}
	got, _ := r.Get(rule.ID)
	if got.Confidence() < MinApplyConfidence {
		t.Errorf("after three successes confidence is %.2f; it should have earned the apply bar", got.Confidence())
	}

	// Guardrail: one bad sample must not retire a rule.
	fresh, _ := OpenRulesWith(t.TempDir(), t.TempDir(), RulesOptions{NoSeed: true})
	r2, _ := fresh.Learn(sig, Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "g"}})
	fresh.Observe(r2.ID, false)
	if got, _ := fresh.Get(r2.ID); got.Retired || !got.Usable() {
		t.Errorf("a single failure retired the rule: %+v", got)
	}
	for i := 0; i < 8; i++ {
		fresh.Observe(r2.ID, false)
	}
	got2, _ := fresh.Get(r2.ID)
	if !got2.Retired {
		t.Errorf("a consistently failing rule was never retired: conf=%.2f samples=%d", got2.Confidence(), got2.Samples())
	}
	if _, ok := fresh.Lookup(sig); ok {
		t.Error("a retired rule is still being offered")
	}
}

func TestLearnIsIdempotent(t *testing.T) {
	r, _ := OpenRulesWith(t.TempDir(), t.TempDir(), RulesOptions{NoSeed: true})
	sig := Signal{Tool: "ws_edit", Language: "go", Message: "old_str not found"}
	res := Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "same"}}
	a, _ := r.Learn(sig, res)
	b, _ := r.Learn(sig, res)
	if a.ID != b.ID {
		t.Fatalf("re-learning made a duplicate: %s vs %s", a.ID, b.ID)
	}
	if r.Count() != 1 {
		t.Fatalf("store has %d rules, want 1", r.Count())
	}
	if b.Successes == 0 {
		t.Error("re-learning should credit the existing rule")
	}
	if _, ok := r.Learn(sig, Resolution{Repair: Repair{Kind: RepairGuidance}}); ok {
		t.Error("Learn accepted a repair with no guidance")
	}
}

func TestRulesPersistAcrossReopen(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	r, _ := OpenRules(proj, user)
	sig := Signal{Tool: "ws_shell", Language: "go", Message: "./a.go:1:1: undefined: alpha"}
	rule, _ := r.Learn(sig, Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "grep for the real name"}})
	r.Observe(rule.ID, true)
	r.Observe(rule.ID, true)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, ".slmcode", "evolve", "rules.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rules.json missing: %v", err)
	}
	// It must be readable, editable JSON.
	data, _ := os.ReadFile(path) //nolint:gosec
	var rf rulesFile
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatalf("rules.json is not valid JSON: %v", err)
	}

	r2, _ := OpenRules(proj, user)
	got, ok := r2.Get(rule.ID)
	if !ok || got.Successes != 2 {
		t.Fatalf("reloaded rule = %+v (ok=%v)", got, ok)
	}
	if r2.Count() < len(SeedRules()) {
		t.Errorf("seeded rules did not survive the reload: %d", r2.Count())
	}
}

func TestUserEditedRetirementSurvivesReload(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	r, _ := OpenRules(proj, user)
	seed := SeedRules()[0]
	if !r.Retire(seed.ID) {
		t.Fatal("Retire failed")
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, _ := OpenRules(proj, user)
	got, ok := r2.Get(seed.ID)
	if !ok || !got.Retired {
		t.Fatalf("a retired seed came back enabled: %+v", got)
	}
	if !got.Seeded {
		t.Error("the seeded flag was lost on reload, so the rule lost its prior")
	}
}

func TestRulesSurviveCorruptFile(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	dir := filepath.Join(proj, ".slmcode", "evolve")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte("{{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRules(proj, user)
	if err != nil {
		t.Fatalf("OpenRules must tolerate corruption: %v", err)
	}
	if len(r.Warnings()) == 0 {
		t.Error("corruption should be reported")
	}
	// Seeded rules still work.
	if _, ok := r.Lookup(Signal{Tool: "ws_edit", Message: "old_str not found in a.go"}); !ok {
		t.Error("a corrupt project store broke the built-in rules")
	}
}

func TestRulesPruneBoundsStoreAndKeepsSeeds(t *testing.T) {
	r, _ := OpenRules(t.TempDir(), t.TempDir())
	seedCount := r.Count()
	for i := 0; i < 500; i++ {
		r.Learn(
			Signal{Tool: "ws_shell", Language: "go", Message: "./a.go:1:1: undefined: sym" + itoa(i)},
			Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "fix " + itoa(i)}},
		)
	}
	r.Prune(RulePolicy{MaxRules: seedCount + 10, DropRetire: true})
	if r.Count() > seedCount+10 {
		t.Errorf("store has %d rules after pruning to %d", r.Count(), seedCount+10)
	}
	for _, seed := range SeedRules() {
		if _, ok := r.Get(seed.ID); !ok {
			t.Fatalf("prune removed the seeded rule %s", seed.ID)
		}
	}
}

func TestRulesForgetRestoresSeeds(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	r, _ := OpenRules(proj, user)
	r.Learn(Signal{Tool: "ws_edit", Language: "go", Message: "old_str not found"},
		Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "learned"}})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.Forget(); err != nil {
		t.Fatal(err)
	}
	if r.Count() != len(SeedRules()) {
		t.Errorf("after Forget the store has %d rules, want the %d seeds", r.Count(), len(SeedRules()))
	}
	if _, err := os.Stat(filepath.Join(proj, ".slmcode", "evolve", "rules.json")); !os.IsNotExist(err) {
		t.Errorf("rules.json survived Forget: %v", err)
	}
}

func TestSuggestRepairStringAPI(t *testing.T) {
	r := newRules(t)
	args := `{"path":"a.go","old_str":"   42| if err != nil {","new_str":"if err == nil {"}`
	guidance, newArgs, ok := r.SuggestRepair("ws_edit", "old_str still contains ws_read's line-number prefix", "go", "qwen2.5-coder", args)
	if !ok {
		t.Fatal("SuggestRepair found nothing for a seeded failure")
	}
	if guidance == "" {
		t.Error("no guidance returned")
	}
	if !strings.Contains(newArgs, `"if err != nil {"`) {
		t.Errorf("transform did not strip the gutter: %s", newArgs)
	}
	if _, _, ok := r.SuggestRepair("", "unrelated gibberish", "", "", ""); ok {
		t.Error("SuggestRepair matched nonsense")
	}
}

func TestRulesAgeOutUnusedLearnedRules(t *testing.T) {
	now := time.Now()
	r, _ := OpenRulesWith(t.TempDir(), t.TempDir(), RulesOptions{
		NoSeed: true, Now: func() time.Time { return now },
	})
	rule, _ := r.Learn(Signal{Tool: "ws_edit", Message: "old_str not found"},
		Resolution{Repair: Repair{Kind: RepairGuidance, Guidance: "g"}})
	// Nudge the clock two years forward.
	r.now = func() time.Time { return now.Add(2 * 365 * 24 * time.Hour) }
	r.Prune(RulePolicy{MaxAge: 365 * 24 * time.Hour})
	if _, ok := r.Get(rule.ID); ok {
		t.Error("an unused two-year-old learned rule was not pruned")
	}
}
