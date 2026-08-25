package autoresearch

// Scaling the SEARCH RANGE to the model, not just the starting value.
//
// THE SAME DEFECT, ONE LAYER UP. pkg/calibrate now derives a model's budgets
// from its measured context window, because a 262,144-token model running with
// a 260-token knowledge budget was discarding 99.8% of what it could hold. The
// search surface had the identical problem in its DOMAINS: `memory_tokens`
// searched 100..800 and `repo_map_tokens` 0..2000 whether the model held 4K or
// 262K. Calibration could set a good starting value and the optimiser would
// immediately propose values from a range written for a small model — so the
// search actively pulled a large model back toward small-model settings.
//
// A range is a claim about where the optimum might be. On a bigger window that
// claim has to move, or the search cannot find what is now reachable.
//
// WHAT DOES NOT SCALE, and why it matters that these are separate:
//
//   - Counts and rounds (max_retries, max_task_calls, think_passes) are about
//     how many TIMES to do something. A wider window does not make a third
//     retry more likely to work.
//   - Percentages (context_slack_percent, react_compact_at_percent) are already
//     relative; scaling them would be scaling twice.
//   - temperature, structured_decoding, worker_critique are not sizes at all.
//
// Only budgets denominated in TOKENS move, because only they are spending the
// window.

// scalableTokenKnobs are the whitelist keys whose domain is a token budget
// drawn from the model's context window.
var scalableTokenKnobs = map[string]bool{
	"max_tokens":      true,
	"memory_tokens":   true,
	"repo_map_tokens": true,
}

// domainBaselineWindow is the context window the whitelist ranges were written
// against. Scaling is measured in multiples of this.
const domainBaselineWindow = 16384

// maxDomainScale caps how far a range may stretch.
//
// Without it a 262K window would multiply every token range 16x, and a search
// that can propose a 32,000-token memory block will eventually propose one —
// spending most of a context on recall before the task is even stated. Four is
// deliberately conservative: it opens real headroom above the small-model
// ranges while keeping every proposal something a human would recognize as
// plausible. The optimiser explores; it does not get to redefine the harness.
const maxDomainScale = 4

// scaleDomain widens a token-budget domain to suit the model's window.
//
// window <= 0 (nothing measured) returns the domain untouched: an unmeasured
// model gets the conservative small-model range, which is the safe direction.
// The MINIMUM never moves — the bottom of the range is still a legitimate
// answer, and on a big model "spend less" remains a hypothesis worth testing.
func scaleDomain(key string, d Domain, window int) Domain {
	if window <= 0 || d.Kind != KnobInt || !scalableTokenKnobs[key] {
		return d
	}
	mult := window / domainBaselineWindow
	if mult <= 1 {
		return d
	}
	if mult > maxDomainScale {
		mult = maxDomainScale
	}
	out := d
	out.Max = d.Max * float64(mult)
	// Keep roughly the same number of steps across the range, so a wider search
	// space does not become a slower one: the ratchet spends one experiment per
	// value it tries, and a 16x longer range at the old step size would be a
	// budget the run does not have.
	if d.Step > 0 {
		out.Step = d.Step * float64(mult)
	}
	return out
}
