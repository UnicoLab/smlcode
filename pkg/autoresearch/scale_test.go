package autoresearch

import "testing"

// TestSearchRangeFollowsTheModel is the second half of adapting to a model, and
// the half that is easy to miss.
//
// pkg/calibrate derives a good STARTING VALUE from the measured window. If the
// search DOMAIN stays fixed, the optimiser immediately proposes values from a
// range written for a small model — so a well-calibrated large model gets
// pulled straight back toward small-model settings, one experiment at a time.
// A range is a claim about where the optimum might be; on a bigger window that
// claim has to move too.
func TestSearchRangeFollowsTheModel(t *testing.T) {
	base := Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50}

	small := scaleDomain("memory_tokens", base, 16384) // the baseline window
	mid := scaleDomain("memory_tokens", base, 32768)   // 9B / Coder-30B
	huge := scaleDomain("memory_tokens", base, 262144) // Coder-Next

	if small.Max != base.Max {
		t.Fatalf("the baseline window changed the range: %v → %v", base.Max, small.Max)
	}
	if mid.Max <= small.Max {
		t.Fatalf("a 32K model searches up to %v, no higher than the 16K baseline %v",
			mid.Max, small.Max)
	}
	if huge.Max <= mid.Max {
		t.Fatalf("a 262K model searches up to %v, no higher than a 32K model %v",
			huge.Max, mid.Max)
	}
	// The floor never moves: "spend less" stays a hypothesis worth testing on
	// any model.
	for _, d := range []Domain{small, mid, huge} {
		if d.Min != base.Min {
			t.Fatalf("the minimum moved to %v; the bottom of the range is still a "+
				"legitimate answer", d.Min)
		}
	}
	t.Logf("memory_tokens search range — 16K: %v, 32K: %v, 262K: %v",
		small.Max, mid.Max, huge.Max)
}

// TestScalingIsCapped: without a ceiling a 262K window multiplies every token
// range 16x, and a search that CAN propose a 32,000-token memory block
// eventually will — spending most of a context on recall before the task is
// even stated. The optimiser explores; it does not redefine the harness.
func TestScalingIsCapped(t *testing.T) {
	base := Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50}
	got := scaleDomain("memory_tokens", base, 100*domainBaselineWindow)
	if want := base.Max * maxDomainScale; got.Max != want {
		t.Fatalf("a 100x window produced Max=%v, want the %dx cap (%v)",
			got.Max, maxDomainScale, want)
	}
}

// TestOnlyTokenBudgetsScale pins the distinction the whole design rests on.
//
// Counts and rounds are about how many TIMES to do something — a wider window
// does not make a third retry more likely to work. Percentages are already
// relative, so scaling them would be scaling twice. Neither may move.
func TestOnlyTokenBudgetsScale(t *testing.T) {
	const huge = 262144
	for _, key := range []string{
		"max_retries", "max_task_calls", "think_passes",
		"context_slack_percent", "react_compact_at_percent",
		"skill_disclosure", "skill_max_expanded", "worker_critique",
		"structured_decoding", "temperature", "excerpt_window_lines",
	} {
		base := Domain{Kind: KnobInt, Min: 1, Max: 10, Step: 1}
		got := scaleDomain(key, base, huge)
		if got.Min != base.Min || got.Max != base.Max || got.Step != base.Step {
			t.Errorf("%s is not a token budget but its range moved: %+v → %+v",
				key, base, got)
		}
	}
	for _, key := range []string{"max_tokens", "memory_tokens", "repo_map_tokens"} {
		base := Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50}
		if got := scaleDomain(key, base, huge); got.Max <= base.Max {
			t.Errorf("%s is a token budget but its range did not move: %+v", key, got)
		}
	}
}

// TestUnmeasuredWindowKeepsTheSmallModelRange: not knowing must be safe, and
// safe here means conservative — the small range, not a guessed large one.
func TestUnmeasuredWindowKeepsTheSmallModelRange(t *testing.T) {
	base := Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50}
	for _, w := range []int{0, -1, 512} {
		got := scaleDomain("memory_tokens", base, w)
		if got.Min != base.Min || got.Max != base.Max || got.Step != base.Step {
			t.Fatalf("window %d changed the range: %+v", w, got)
		}
	}
}

// TestScalingKeepsTheSearchAffordable. The ratchet spends one experiment per
// value it tries, so a 4x longer range at the original step would be a 4x
// longer search — a budget a run does not have. Steps grow with the range.
func TestScalingKeepsTheSearchAffordable(t *testing.T) {
	base := Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50}
	baseSteps := int((base.Max - base.Min) / base.Step)
	for _, w := range []int{32768, 131072, 262144} {
		got := scaleDomain("memory_tokens", base, w)
		steps := int((got.Max - got.Min) / got.Step)
		if steps > baseSteps*2 {
			t.Errorf("window %d yields %d candidate values against the baseline's %d — "+
				"the search got dramatically more expensive", w, steps, baseSteps)
		}
	}
}
