package calibrate

import (
	"github.com/UnicoLab/slmcode/pkg/config"
)

// Deriving the harness's budgets from the window the model actually reports.
//
// THE PROBLEM. Calibration measures a great deal — the concurrency knee, solo
// p50/p95, decode throughput, and the context window straight from the server —
// and used to apply three of them: max_parallel, context_limit, task_timeout.
// Everything else stayed on a static profile sized for a 4-16K model. So a
// 262,144-token Qwen3-Coder-Next ran with a 380-token skill budget and a
// 260-token knowledge budget: 0.2% of its window, the same absolute allowance a
// 4K model gets. The better the model, the larger the fraction thrown away.
//
// THE RULE. Every budget below is a FRACTION OF THE MEASURED WINDOW, clamped
// into a range, and never allowed to shrink what the static profile already
// offered. Three properties make that safe:
//
//   - MONOTONIC. A bigger window never yields a smaller budget. Anything else
//     would make upgrading a model a downgrade in some dimension, silently.
//   - NEVER SHRINKS. max(derived, base) throughout. The static profiles encode
//     real knowledge about small models; derivation exists to lift ceilings for
//     big ones, not to second-guess a tuned floor.
//   - CLAMPED. Fractions of a very large window would otherwise produce budgets
//     that are technically affordable and practically absurd — a 32K "thinking"
//     allowance costs minutes of decode on a local model, and a 30k-token skill
//     block crowds out the actual task. The caps are where measurement stops
//     and judgement starts, and they are named rather than buried.
//
// WHY FRACTIONS AND NOT MEASUREMENTS. The window is measured; how to spend it
// is a policy, and it cannot be probed — no experiment tells you what share of
// context should go to skills. So these are stated ratios with reasons, not
// magic numbers: the denominators below say "skills get about 1.5% of context,
// knowledge about 1%", which is a claim a human can argue with.

// Budget shares of the model's context window.
//
// OUTPUT BUDGETS SCALE WEAKLY; INJECTION BUDGETS SCALE STRONGLY. The asymmetry
// is the whole design, and it is not arbitrary:
//
//   - How long a RESPONSE needs to be is a property of the TASK. A search/
//     replace edit needs the same few hundred tokens whether the window is 8K
//     or 262K. Raising max_tokens on a big model buys almost nothing real, and
//     costs something concrete: task_timeout is derived from the worst case
//     (tokens ÷ measured decode rate), so a 5x larger max_tokens is a 5x larger
//     timeout. Measured here: lifting max_tokens to window/8 turned a slow
//     model's recommended task_timeout into EIGHT HOURS. That is not adaptation,
//     it is a harness talking itself into waiting all day.
//
//   - How much REFERENCE MATERIAL fits is a property of the WINDOW, exactly.
//     Skills and knowledge are the budgets that were starving a big model:
//     260 and 180 tokens on a 262K window is 0.2%, the same absolute allowance
//     a 4K model gets. These are where the context actually goes to work.
const (
	maxTokensShare  = 24 // window / 24 — output is task-shaped, not window-shaped
	thinkingShare   = 16 // window / 16
	skillShare      = 64 // window / 64  ≈ 1.5%
	knowledgeShare  = 96 // window / 96  ≈ 1%
	maxTokensCap    = 8192
	thinkingCap     = 8192
	skillCap        = 4096
	knowledgeCap    = 3072
	minTurns        = 12
	maxTurnsCeiling = 48
	// turnsPerContextDoubling is how many extra ReAct turns each doubling of
	// the window buys. A bigger window does not make a model smarter, it makes
	// it able to keep more of its own trail in view — so this grows slowly and
	// stops well short of "loop forever".
	turnsPerContextDoubling = 4
	// turnsBaselineWindow is the window the static profiles were written
	// against; growth is counted in doublings from here.
	turnsBaselineWindow = 16384
)

// DeriveProfile scales a model profile to a measured context window.
//
// base is the statically resolved profile for this model — the floor. window is
// the measured context limit in tokens; a non-positive window returns base
// unchanged, because deriving from nothing is guessing.
func DeriveProfile(base config.ModelProfile, window int) config.ModelProfile {
	if window <= 0 {
		return base
	}
	out := base
	out.ContextLimit = maxInt(base.ContextLimit, window)
	out.MaxTokens = liftTo(base.MaxTokens, window/maxTokensShare, maxTokensCap)
	out.ThinkingBudgetTokens = liftTo(base.ThinkingBudgetTokens, window/thinkingShare, thinkingCap)
	out.SkillTokenBudget = liftTo(base.SkillTokenBudget, window/skillShare, skillCap)
	out.KnowledgeTokenBudget = liftTo(base.KnowledgeTokenBudget, window/knowledgeShare, knowledgeCap)
	out.MaxTurns = deriveTurns(base.MaxTurns, window)
	// Temperature is NOT derived. Nothing measured here says anything about the
	// right sampling temperature, and inventing one from context size would be
	// numerology.
	return out
}

// liftTo raises base toward want, without exceeding cap and without ever
// returning less than base.
func liftTo(base, want, cap int) int {
	if want > cap {
		want = cap
	}
	return maxInt(base, want)
}

// deriveTurns grows the ReAct turn ceiling with the window, in doublings.
//
// Turns are not free: each is a model call. The growth is deliberately slow and
// bounded — a wider window lets a model keep more of its own reasoning in view,
// which is worth a few more turns, and is not a license to iterate forever.
func deriveTurns(base, window int) int {
	turns := base
	if turns < minTurns {
		turns = minTurns
	}
	for w := turnsBaselineWindow; w > 0 && w <= window/2; w *= 2 {
		turns += turnsPerContextDoubling
		if turns >= maxTurnsCeiling {
			break
		}
	}
	if turns > maxTurnsCeiling {
		turns = maxTurnsCeiling
	}
	return maxInt(base, turns)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
