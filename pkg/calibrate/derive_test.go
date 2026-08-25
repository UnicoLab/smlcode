package calibrate

import (
	"fmt"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// baseProfile is the shipped "default" profile: what every model got before
// anything was measured.
func baseProfile() config.ModelProfile {
	return config.ModelProfile{
		ContextLimit: 16384, MaxTokens: 3072, ThinkingBudgetTokens: 3072,
		SkillTokenBudget: 260, KnowledgeTokenBudget: 180,
		Temperature: 0.12, MaxTurns: 20,
	}
}

// realWindows are the context windows of models actually on this machine, plus
// the small ones the static profiles were written for.
var realWindows = []struct {
	name   string
	window int
}{
	{"tiny-4k", 4096},
	{"8k", 8192},
	{"profile-baseline-16k", 16384},
	{"qwen3.5-9b / coder-30b 32k", 32768},
	{"128k", 131072},
	{"qwen3-coder-next 262k", 262144},
}

// TestDeriveScalesBudgetsWithTheWindow is the headline: a bigger model must
// actually get to use itself.
//
// Before this, calibration wrote context_limit and left everything else on a
// profile sized for a 4-16K model, so a 262,144-token model ran with a
// 380-token skill budget — 0.2% of its window, the same ABSOLUTE allowance a 4K
// model gets. The better the model, the larger the fraction discarded.
func TestDeriveScalesBudgetsWithTheWindow(t *testing.T) {
	base := baseProfile()
	small := DeriveProfile(base, 32768)
	huge := DeriveProfile(base, 262144)

	if small.SkillTokenBudget <= base.SkillTokenBudget {
		t.Fatalf("32K model skill budget %d did not rise above the static %d",
			small.SkillTokenBudget, base.SkillTokenBudget)
	}
	if huge.SkillTokenBudget <= small.SkillTokenBudget {
		t.Fatalf("262K skill budget %d is not above the 32K one %d",
			huge.SkillTokenBudget, small.SkillTokenBudget)
	}
	if huge.KnowledgeTokenBudget <= small.KnowledgeTokenBudget {
		t.Fatalf("262K knowledge budget %d is not above the 32K one %d",
			huge.KnowledgeTokenBudget, small.KnowledgeTokenBudget)
	}
	if huge.MaxTokens <= base.MaxTokens {
		t.Fatalf("262K max_tokens %d did not rise above the static %d",
			huge.MaxTokens, base.MaxTokens)
	}
	// TURNS MUST NOT GROW WITH THE WINDOW. They used to, +4 per doubling, and
	// the measurement refuted the reasoning: on respects-scope the extra turns
	// took the run from 11 LLM calls to 26 and 130,255 prompt tokens to 435,296,
	// timed out a task, and finished no better. How many turns a task needs is a
	// property of the task; a turn ceiling is a safety bound, and raising a
	// safety bound because the model has more memory just lets a run that is not
	// converging go on longer. See deriveTurns.
	if huge.MaxTurns != base.MaxTurns {
		t.Fatalf("262K max_turns %d differs from the static %d — turns must not "+
			"scale with the window", huge.MaxTurns, base.MaxTurns)
	}
	if small.MaxTurns != base.MaxTurns {
		t.Fatalf("32K max_turns %d differs from the static %d", small.MaxTurns, base.MaxTurns)
	}
	t.Logf("skills %d→%d→%d, knowledge %d→%d→%d, max_tokens %d→%d, turns %d→%d",
		base.SkillTokenBudget, small.SkillTokenBudget, huge.SkillTokenBudget,
		base.KnowledgeTokenBudget, small.KnowledgeTokenBudget, huge.KnowledgeTokenBudget,
		base.MaxTokens, huge.MaxTokens, base.MaxTurns, huge.MaxTurns)
}

// TestDeriveIsMonotonic: a larger window must never produce a smaller budget in
// ANY dimension. Otherwise upgrading a model would silently be a downgrade
// somewhere, which is the kind of regression nobody goes looking for.
func TestDeriveIsMonotonic(t *testing.T) {
	base := baseProfile()
	var prev config.ModelProfile
	for i, w := range realWindows {
		got := DeriveProfile(base, w.window)
		if i > 0 {
			for _, f := range []struct {
				name       string
				prev, curr int
			}{
				{"context_limit", prev.ContextLimit, got.ContextLimit},
				{"max_tokens", prev.MaxTokens, got.MaxTokens},
				{"thinking_budget", prev.ThinkingBudgetTokens, got.ThinkingBudgetTokens},
				{"skill_budget", prev.SkillTokenBudget, got.SkillTokenBudget},
				{"knowledge_budget", prev.KnowledgeTokenBudget, got.KnowledgeTokenBudget},
				{"max_turns", prev.MaxTurns, got.MaxTurns},
			} {
				if f.curr < f.prev {
					t.Errorf("%s went DOWN from %d to %d when the window grew to %s",
						f.name, f.prev, f.curr, w.name)
				}
			}
		}
		prev = got
	}
}

// TestDeriveNeverShrinksTheStaticFloor. The static profiles encode real
// knowledge about small models; derivation exists to lift ceilings for big
// ones, never to second-guess a tuned floor.
func TestDeriveNeverShrinksTheStaticFloor(t *testing.T) {
	// A generous hand-tuned profile against a SMALL window — the case where a
	// naive fraction would cut every budget.
	rich := config.ModelProfile{
		ContextLimit: 8192, MaxTokens: 8000, ThinkingBudgetTokens: 8000,
		SkillTokenBudget: 4000, KnowledgeTokenBudget: 3000,
		Temperature: 0.3, MaxTurns: 40,
	}
	for _, w := range realWindows {
		got := DeriveProfile(rich, w.window)
		if got.MaxTokens < rich.MaxTokens {
			t.Errorf("%s: max_tokens cut from %d to %d", w.name, rich.MaxTokens, got.MaxTokens)
		}
		if got.SkillTokenBudget < rich.SkillTokenBudget {
			t.Errorf("%s: skill budget cut from %d to %d", w.name, rich.SkillTokenBudget, got.SkillTokenBudget)
		}
		if got.KnowledgeTokenBudget < rich.KnowledgeTokenBudget {
			t.Errorf("%s: knowledge budget cut from %d to %d", w.name, rich.KnowledgeTokenBudget, got.KnowledgeTokenBudget)
		}
		if got.MaxTurns < rich.MaxTurns {
			t.Errorf("%s: max_turns cut from %d to %d", w.name, rich.MaxTurns, got.MaxTurns)
		}
		if got.ContextLimit < rich.ContextLimit {
			t.Errorf("%s: context_limit cut from %d to %d", w.name, rich.ContextLimit, got.ContextLimit)
		}
	}
}

// TestDeriveIsClamped: fractions of a very large window would otherwise produce
// budgets that are technically affordable and practically absurd. A 32K
// "thinking" allowance is minutes of decode on a local model; a 30k-token skill
// block crowds out the task it was meant to support.
func TestDeriveIsClamped(t *testing.T) {
	base := baseProfile()
	// Far beyond anything real, to prove the caps bind rather than the window.
	got := DeriveProfile(base, 8_000_000)

	if got.MaxTokens > maxTokensCap {
		t.Errorf("max_tokens %d exceeds the cap %d", got.MaxTokens, maxTokensCap)
	}
	if got.ThinkingBudgetTokens > thinkingCap {
		t.Errorf("thinking %d exceeds the cap %d", got.ThinkingBudgetTokens, thinkingCap)
	}
	if got.SkillTokenBudget > skillCap {
		t.Errorf("skills %d exceeds the cap %d", got.SkillTokenBudget, skillCap)
	}
	if got.KnowledgeTokenBudget > knowledgeCap {
		t.Errorf("knowledge %d exceeds the cap %d", got.KnowledgeTokenBudget, knowledgeCap)
	}
	if got.MaxTurns > maxTurnsCeiling {
		t.Errorf("max_turns %d exceeds the ceiling %d", got.MaxTurns, maxTurnsCeiling)
	}
}

// TestDeriveRefusesToGuessFromNothing: an unmeasured window must leave the
// profile exactly as it was. Deriving from zero is inventing.
func TestDeriveRefusesToGuessFromNothing(t *testing.T) {
	base := baseProfile()
	for _, w := range []int{0, -1, -262144} {
		if got := DeriveProfile(base, w); got != base {
			t.Fatalf("window %d changed the profile: %+v", w, got)
		}
	}
}

// TestDeriveLeavesTemperatureAlone. Nothing calibration measures says anything
// about the right sampling temperature; deriving one from context size would be
// numerology dressed as adaptation.
func TestDeriveLeavesTemperatureAlone(t *testing.T) {
	base := baseProfile()
	for _, w := range realWindows {
		if got := DeriveProfile(base, w.window); got.Temperature != base.Temperature {
			t.Fatalf("%s: temperature moved %v → %v", w.name, base.Temperature, got.Temperature)
		}
	}
}

// TestDeriveBudgetsFitTheWindow is the safety property that matters most: the
// per-call budgets must not, together, exceed the window they are drawn from.
// A profile that cannot fit its own output plus its own injections is a prompt
// overflow waiting for a long task.
func TestDeriveBudgetsFitTheWindow(t *testing.T) {
	base := baseProfile()
	for _, w := range realWindows {
		got := DeriveProfile(base, w.window)
		// Output + thinking + injections, against the window. Two-thirds is a
		// deliberately loose bound — history and the task itself need the rest.
		spend := got.MaxTokens + got.ThinkingBudgetTokens +
			got.SkillTokenBudget + got.KnowledgeTokenBudget
		if limit := (w.window * 2) / 3; spend > limit && w.window >= turnsBaselineWindow {
			t.Errorf("%s: budgets total %d tokens of a %d window (>%d) — no room left "+
				"for history or the task", w.name, spend, w.window, limit)
		}
	}
}

// TestDeriveAcrossEveryStaticProfile: the shipped profiles are the real inputs,
// so every one of them must survive derivation at every real window.
func TestDeriveAcrossEveryStaticProfile(t *testing.T) {
	for key, prof := range config.DefaultModelProfiles() {
		for _, w := range realWindows {
			t.Run(fmt.Sprintf("%s@%s", key, w.name), func(t *testing.T) {
				got := DeriveProfile(prof, w.window)
				if got.MaxTokens <= 0 || got.MaxTurns <= 0 || got.ContextLimit <= 0 {
					t.Fatalf("derivation produced a non-positive budget: %+v", got)
				}
				if got.SkillTokenBudget < prof.SkillTokenBudget ||
					got.KnowledgeTokenBudget < prof.KnowledgeTokenBudget {
					t.Fatalf("derivation shrank an injection budget: %+v → %+v", prof, got)
				}
			})
		}
	}
}
