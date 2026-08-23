package evolve

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutcomeReward(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		wantMin float64
		wantMax float64
	}{
		{"perfect", Outcome{Applied: true, GateRan: true, GatePassed: true}, 0.85, 1.0},
		{"applied but gate failed", Outcome{Applied: true, GateRan: true}, 0.55, 0.75},
		{"not applied", Outcome{}, 0.0, 0.35},
		{"applied with retries", Outcome{Applied: true, GateRan: true, GatePassed: true, Retries: 3}, 0.70, 0.90},
		{"hard failure caps low", Outcome{Applied: true, GateRan: true, GatePassed: true, Failed: true}, 0.0, 0.10},
		{"unknown gate is neutral", Outcome{Applied: true}, 0.60, 0.85},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.outcome.Reward()
			if r < 0 || r > 1 {
				t.Fatalf("reward %f is outside [0,1]", r)
			}
			if r < tc.wantMin || r > tc.wantMax {
				t.Errorf("reward = %.3f, want in [%.2f, %.2f]", r, tc.wantMin, tc.wantMax)
			}
		})
	}
}

// Correctness must dominate cost: a cheap broken result must never outscore an
// expensive working one.
func TestRewardNeverTradesCorrectnessForCost(t *testing.T) {
	cheapBroken := Outcome{Applied: false, GateRan: true, GatePassed: false, TokensUsed: 10, TokenBudget: 10000, WallMS: 10, WallBudgetMS: 60000}
	expensiveWorking := Outcome{Applied: true, GateRan: true, GatePassed: true, TokensUsed: 9999, TokenBudget: 10000, WallMS: 59000, WallBudgetMS: 60000}
	if cheapBroken.Reward() >= expensiveWorking.Reward() {
		t.Fatalf("a cheap broken outcome (%.3f) outscored an expensive working one (%.3f)",
			cheapBroken.Reward(), expensiveWorking.Reward())
	}
}

func TestRewardBreaksTiesOnCost(t *testing.T) {
	cheap := Outcome{Applied: true, GateRan: true, GatePassed: true, TokensUsed: 100, TokenBudget: 10000, WallMS: 100, WallBudgetMS: 10000}
	pricey := Outcome{Applied: true, GateRan: true, GatePassed: true, TokensUsed: 9500, TokenBudget: 10000, WallMS: 9500, WallBudgetMS: 10000}
	if cheap.Reward() <= pricey.Reward() {
		t.Errorf("cost did not break the tie: cheap %.3f vs pricey %.3f", cheap.Reward(), pricey.Reward())
	}
}

func newBandit(t *testing.T, opt BanditOptions) *Bandit {
	t.Helper()
	b, err := OpenBanditWith(t.TempDir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The load-bearing bandit claim: given an arm that really is better, repeated
// play must converge on it.
func TestBanditConvergesOnTheBetterArm(t *testing.T) {
	b := newBandit(t, BanditOptions{Seed: 42})
	key := Key{Decision: DecEditFormat, ModelFamily: "qwen2.5-coder", Language: "go"}
	arms := []string{"search_replace", "unified_diff", "whole_file"}

	// Ground truth: unified_diff is the best format for this imaginary model,
	// which is the OPPOSITE of the shipped prior — so the test proves the
	// bandit learns from evidence rather than merely obeying its warm start.
	truth := map[string]float64{"search_replace": 0.35, "unified_diff": 0.90, "whole_file": 0.50}

	picks := map[string]int{}
	for i := 0; i < 400; i++ {
		arm := b.Choose(key, arms)
		picks[arm]++
		applied := pseudoRandom(i) < truth[arm]
		b.Update(key, arm, Outcome{Applied: applied, GateRan: true, GatePassed: applied})
	}
	if picks["unified_diff"] < 200 {
		t.Fatalf("bandit did not converge on the better arm: %v", picks)
	}

	// And late play should be dominated by it.
	late := map[string]int{}
	for i := 0; i < 100; i++ {
		arm := b.Choose(key, arms)
		late[arm]++
		applied := pseudoRandom(i+1000) < truth[arm]
		b.Update(key, arm, Outcome{Applied: applied, GateRan: true, GatePassed: applied})
	}
	if late["unified_diff"] < 70 {
		t.Errorf("late play still exploring too much: %v", late)
	}
}

// pseudoRandom is a deterministic [0,1) sequence so the convergence test is
// reproducible without depending on the bandit's own RNG.
func pseudoRandom(i int) float64 {
	x := math.Sin(float64(i)*12.9898) * 43758.5453
	return x - math.Floor(x)
}

func TestBanditWarmStartFavoursSensibleDefaults(t *testing.T) {
	b := newBandit(t, BanditOptions{Deterministic: true})
	key := Key{Decision: DecEditFormat, ModelFamily: "any", Language: "go"}
	got := b.Choose(key, []string{"search_replace", "unified_diff", "whole_file"})
	if got != "search_replace" {
		t.Fatalf("a fresh install chose %q; the shipped prior should favor search/replace for small models", got)
	}
}

func TestBanditDeterministicMode(t *testing.T) {
	key := Key{Decision: DecEditFormat, ModelFamily: "m", Language: "go"}
	arms := []string{"search_replace", "unified_diff", "whole_file"}

	var runs [][]string
	for r := 0; r < 3; r++ {
		b := newBandit(t, BanditOptions{Deterministic: true})
		var seq []string
		for i := 0; i < 25; i++ {
			arm := b.Choose(key, arms)
			seq = append(seq, arm)
			b.Update(key, arm, Outcome{Applied: i%2 == 0, GateRan: true, GatePassed: i%2 == 0})
		}
		runs = append(runs, seq)
	}
	for i := 1; i < len(runs); i++ {
		if strings.Join(runs[i], ",") != strings.Join(runs[0], ",") {
			t.Fatalf("deterministic mode is not reproducible:\n  %v\n  %v", runs[0], runs[i])
		}
	}
	// And it must never report exploring.
	b := newBandit(t, BanditOptions{Deterministic: true})
	for i := 0; i < 20; i++ {
		if c := b.ChooseWithReason(key, arms); c.Explore {
			t.Fatalf("deterministic mode explored: %s", c.Reason)
		}
	}
}

func TestBanditSeededExplorationIsReproducible(t *testing.T) {
	key := Key{Decision: DecThinkPasses}
	arms := []string{"1", "2", "3"}
	var seqs [][]string
	for r := 0; r < 2; r++ {
		b := newBandit(t, BanditOptions{Seed: 7})
		var seq []string
		for i := 0; i < 30; i++ {
			arm := b.Choose(key, arms)
			seq = append(seq, arm)
			b.UpdateReward(key, arm, 0.5)
		}
		seqs = append(seqs, seq)
	}
	if strings.Join(seqs[0], ",") != strings.Join(seqs[1], ",") {
		t.Error("a seeded bandit is not reproducible")
	}
}

func TestBanditExplorationDecays(t *testing.T) {
	b := newBandit(t, BanditOptions{Seed: 1})
	key := Key{Decision: DecExplorePhase}
	early := b.exploreRate(0)
	mid := b.exploreRate(ExploreHalfLife)
	late := b.exploreRate(10000)
	if !(early > mid && mid > late) {
		t.Fatalf("ε did not decay: %.3f → %.3f → %.3f", early, mid, late)
	}
	if late < MinExploreRate-1e-9 {
		t.Errorf("ε decayed below its floor: %.4f < %.4f", late, MinExploreRate)
	}
	_ = key
}

// A bad early sample must not be able to lock in a poor arm.
func TestBanditGuardrailAgainstEarlyLockIn(t *testing.T) {
	b := newBandit(t, BanditOptions{Seed: 3})
	key := Key{Decision: DecRetryLadder, ModelFamily: "m", Language: "go"}
	arms := []string{"reread_then_shrink", "escalate_model"}

	// The genuinely good arm has one unlucky failure right at the start.
	b.Update(key, "escalate_model", Outcome{Applied: false, Failed: true})

	seen := map[string]int{}
	for i := 0; i < 40; i++ {
		arm := b.Choose(key, arms)
		seen[arm]++
		// From now on escalate_model always works.
		ok := arm == "escalate_model"
		b.Update(key, arm, Outcome{Applied: ok, GateRan: true, GatePassed: ok})
	}
	if seen["escalate_model"] < 10 {
		t.Fatalf("one bad early sample locked out the better arm: %v", seen)
	}
}

func TestBanditMinPullsWarmUp(t *testing.T) {
	b := newBandit(t, BanditOptions{Seed: 5})
	key := Key{Decision: DecReviewStrictness}
	arms := []string{"lenient", "normal", "strict"}
	counts := map[string]int{}
	for i := 0; i < len(arms)*MinPulls; i++ {
		c := b.ChooseWithReason(key, arms)
		counts[c.Arm]++
		b.Update(key, c.Arm, Outcome{Applied: true})
	}
	for _, a := range arms {
		if counts[a] < MinPulls {
			t.Errorf("arm %q got %d pulls during warm-up, want at least %d (%v)", a, counts[a], MinPulls, counts)
		}
	}
}

func TestBanditPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	key := Key{Decision: DecEditFormat, ModelFamily: "m", Language: "go"}
	b, err := OpenBanditWith(dir, BanditOptions{Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		b.Update(key, "search_replace", Outcome{Applied: true, GateRan: true, GatePassed: true})
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".slmcode", "evolve", "policy.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("policy.json missing: %v", err)
	}

	b2, err := OpenBanditWith(dir, BanditOptions{Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	snap := b2.Snapshot()
	if len(snap) != 1 || snap[0].Pulls != 10 {
		t.Fatalf("reloaded snapshot = %+v", snap)
	}
	if !strings.Contains(b2.Why(key), "search_replace") {
		t.Errorf("Why lost the reloaded arm:\n%s", b2.Why(key))
	}
}

func TestBanditSurvivesCorruptPolicy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".slmcode", "evolve")
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "policy.json"), []byte("<<<not json>>>"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := OpenBanditWith(dir, BanditOptions{Deterministic: true})
	if err != nil {
		t.Fatalf("OpenBandit must tolerate corruption: %v", err)
	}
	if len(b.Warnings()) == 0 {
		t.Error("corruption should be reported")
	}
	if got := b.Choose(Key{Decision: DecEditFormat}, []string{"search_replace", "unified_diff"}); got != "search_replace" {
		t.Errorf("after corruption the bandit chose %q; it should fall back to priors", got)
	}
}

func TestBanditWhyIsHumanReadable(t *testing.T) {
	b := newBandit(t, BanditOptions{Deterministic: true})
	key := Key{Decision: DecEditFormat, ModelFamily: "qwen2.5-coder", Language: "go"}
	if got := b.Why(key); !strings.Contains(got, "no evidence yet") {
		t.Errorf("Why on a fresh key = %q", got)
	}
	for i := 0; i < 6; i++ {
		b.Update(key, "search_replace", Outcome{Applied: true, GateRan: true, GatePassed: true})
		b.Update(key, "unified_diff", Outcome{Applied: false})
	}
	got := b.Why(key)
	for _, want := range []string{"edit_format", "qwen2.5-coder", "go", "search_replace", "unified_diff", "%"} {
		if !strings.Contains(got, want) {
			t.Errorf("Why missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "→ search_replace") {
		t.Errorf("Why does not mark the leading arm:\n%s", got)
	}
}

func TestBanditDegenerateInputs(t *testing.T) {
	b := newBandit(t, BanditOptions{Deterministic: true})
	key := Key{Decision: DecEditFormat}
	if got := b.Choose(key, nil); got != "" {
		t.Errorf("no arms should yield an empty choice, got %q", got)
	}
	if got := b.Choose(key, []string{"only"}); got != "only" {
		t.Errorf("single arm = %q", got)
	}
	if got := b.Choose(key, []string{"", "  ", "x"}); got != "x" {
		t.Errorf("blank arms should be dropped, got %q", got)
	}
	b.UpdateReward(key, "", 1) // must not panic
	b.UpdateReward(key, "x", 5)
	b.UpdateReward(key, "x", -3)
	for _, ks := range b.Snapshot() {
		for _, a := range ks.Arms {
			if a.Mean() < 0 || a.Mean() > 1 {
				t.Errorf("arm %s mean %f escaped [0,1]", a.Name, a.Mean())
			}
		}
	}
}

func TestBanditPosteriorDecayKeepsNumbersBounded(t *testing.T) {
	b := newBandit(t, BanditOptions{Deterministic: true})
	key := Key{Decision: DecEditFormat, ModelFamily: "m", Language: "go"}
	for i := 0; i < DecayAfter*4; i++ {
		b.UpdateReward(key, "search_replace", 1)
	}
	snap := b.Snapshot()
	if len(snap) == 0 {
		t.Fatal("no state")
	}
	for _, a := range snap[0].Arms {
		if a.Alpha > float64(DecayAfter)*2 {
			t.Errorf("posterior grew unbounded: α=%.1f after %d pulls", a.Alpha, DecayAfter*4)
		}
	}
}

func TestBanditPruneAndForget(t *testing.T) {
	dir := t.TempDir()
	b, _ := OpenBanditWith(dir, BanditOptions{Deterministic: true})
	for i := 0; i < 50; i++ {
		b.UpdateReward(Key{Decision: DecEditFormat, Language: "lang" + itoa(i)}, "a", 1)
	}
	if removed := b.Prune(10); removed != 40 {
		t.Errorf("Prune removed %d, want 40", removed)
	}
	if len(b.Snapshot()) != 10 {
		t.Errorf("after prune %d keys remain", len(b.Snapshot()))
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.Forget(); err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshot()) != 0 {
		t.Error("Forget left state behind")
	}
	if _, err := os.Stat(filepath.Join(dir, ".slmcode", "evolve", "policy.json")); !os.IsNotExist(err) {
		t.Errorf("policy.json survived Forget: %v", err)
	}
}

func TestKeyNormalizeAndString(t *testing.T) {
	k := Key{Decision: " EDIT_FORMAT ", ModelFamily: " Qwen ", Language: "Golang"}.Normalize()
	if k.Decision != DecEditFormat || k.ModelFamily != "qwen" || k.Language != "go" {
		t.Fatalf("normalize = %+v", k)
	}
	blank := Key{Decision: DecEditFormat}
	if !strings.Contains(blank.String(), "*") {
		t.Errorf("blank namespace should render as *, got %q", blank.String())
	}
}

func TestSampleBetaStaysInRange(t *testing.T) {
	b := newBandit(t, BanditOptions{Seed: 11})
	for _, pair := range [][2]float64{{1, 1}, {0.5, 0.5}, {100, 1}, {1, 100}, {0, 0}, {-1, 3}} {
		for i := 0; i < 200; i++ {
			v := sampleBeta(b.rng, pair[0], pair[1])
			if v < 0 || v > 1 || math.IsNaN(v) {
				t.Fatalf("sampleBeta(%v) = %f", pair, v)
			}
		}
	}
}
