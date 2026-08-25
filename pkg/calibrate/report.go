package calibrate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// The calibration report: what was measured, what it changed, and how to
// disagree with it.
//
// WHY A REPORT AND NOT A LOG LINE. Calibration silently rewrites the numbers a
// run is governed by — concurrency, timeouts, and now every token budget
// derived from the model's window. Numbers that arrive without their evidence
// are numbers nobody can argue with, so the first time something feels wrong
// the reflex is to turn the whole feature off. The report exists so each value
// can be traced to the measurement that produced it and overridden
// individually.
//
// NOTHING HERE ASKS FOR PERMISSION. Calibration applies what it measured and
// then shows its work; a modal that blocks a run on a keystroke would make the
// harness unusable in CI and in the TUI. The override path is config, which
// already outranks calibration everywhere (see Explicit), so "adjust it" means
// one documented line — not a dialog the user has to be present for.

// Report is a completed calibration, its evidence, and its effect.
type Report struct {
	// Profile is the raw measurement.
	Profile Profile
	// Applied lists the config keys calibration changed, with reasons.
	Applied []Applied
	// Before and After are the model profile's budgets either side of
	// derivation, so the token budgets can be shown as a diff.
	Before, After config.ModelProfile
	// Adjustable names the config keys a user can pin to override any of this.
	Adjustable []string
	// Blocked lists measurements an explicit setting refused to accept.
	Blocked []Blocked
}

// budgetRow is one derived budget, for rendering.
type budgetRow struct {
	Key           string
	Before, After int
	Note          string
}

// budgetRows returns the derived budgets that actually moved.
func (r Report) budgetRows() []budgetRow {
	all := []budgetRow{
		{"context_limit", r.Before.ContextLimit, r.After.ContextLimit, r.Profile.ContextSource},
		{"max_tokens", r.Before.MaxTokens, r.After.MaxTokens, "per-response output budget"},
		{"thinking_budget_tokens", r.Before.ThinkingBudgetTokens, r.After.ThinkingBudgetTokens, "multipass reasoning budget"},
		{"skill_token_budget", r.Before.SkillTokenBudget, r.After.SkillTokenBudget, "skills injected per call"},
		{"knowledge_token_budget", r.Before.KnowledgeTokenBudget, r.After.KnowledgeTokenBudget, "knowledge injected per call"},
		{"max_turns", r.Before.MaxTurns, r.After.MaxTurns, "ReAct steps per agent call"},
	}
	out := make([]budgetRow, 0, len(all))
	for _, row := range all {
		if row.After != row.Before {
			out = append(out, row)
		}
	}
	return out
}

// ChangedBudgets reports how many derived budgets actually moved. Callers use
// it to decide whether the compact line is worth printing at all — a run that
// changed nothing should say nothing.
func (r Report) ChangedBudgets() int { return len(r.budgetRows()) }

// Render produces the operator-facing report.
//
// Three sections, in the order a reader needs them: what was MEASURED (the
// evidence), what CHANGED because of it (the effect), and how to OVERRIDE
// (the escape hatch). A reader who trusts the tool stops after section two.
func (r Report) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Calibration — %s\n", r.Profile.Key.String())
	b.WriteString(strings.Repeat("─", 60) + "\n")

	// ── measured ──
	b.WriteString("\nMeasured\n")
	if r.Profile.ContextLimit > 0 {
		src := r.Profile.ContextSource
		if src == "" {
			src = "server metadata"
		}
		fmt.Fprintf(&b, "  context window       %d tokens (%s)\n", r.Profile.ContextLimit, src)
	}
	if r.Profile.P50Ms > 0 || r.Profile.P95Ms > 0 {
		fmt.Fprintf(&b, "  solo latency         p50 %s · p95 %s (%d samples)\n",
			ms(r.Profile.P50Ms), ms(r.Profile.P95Ms), r.Profile.SoloSamples)
	}
	if r.Profile.TokensPerSec > 0 {
		fmt.Fprintf(&b, "  decode rate          %.1f tok/s\n", r.Profile.TokensPerSec)
	}
	fmt.Fprintf(&b, "  concurrency knee     %d", r.Profile.MaxParallel)
	if l, ok := r.Profile.levelAbove(r.Profile.MaxParallel); ok {
		fmt.Fprintf(&b, "  (%d-way ran at %.0f%% efficiency — below the %.0f%% floor)",
			l.Concurrency, l.Efficiency*100, r.Profile.FloorUsed*100)
	}
	b.WriteString("\n")
	if r.Profile.QueueInflation > 1 {
		fmt.Fprintf(&b, "  queueing at the knee %.2fx solo latency\n", r.Profile.QueueInflation)
	}
	for _, l := range r.Profile.Levels {
		fmt.Fprintf(&b, "    level %-2d           %.0f%% efficiency · %.2fx solo throughput\n",
			l.Concurrency, l.Efficiency*100, l.Throughput)
	}
	if r.Profile.Partial {
		b.WriteString("  ⚠ PARTIAL — the probe ran out of budget; the knee may be an underestimate,\n" +
			"    and this profile expires in an hour rather than the usual month.\n")
	}

	// ── applied ──
	b.WriteString("\nApplied\n")
	if len(r.Applied) == 0 && len(r.budgetRows()) == 0 && len(r.Blocked) == 0 {
		b.WriteString("  nothing — the measurement agreed with the current configuration\n")
	}
	if len(r.Applied) == 0 && len(r.budgetRows()) == 0 && len(r.Blocked) > 0 {
		b.WriteString("  nothing — see \"Not applied\" below; this is NOT agreement\n")
	}
	for _, a := range r.Applied {
		fmt.Fprintf(&b, "  %-22s %s → %s\n", a.Key, a.From, a.To)
		if a.Why != "" {
			fmt.Fprintf(&b, "  %-22s   because %s\n", "", a.Why)
		}
	}
	// Applied already names context_limit — it is the measurement, not a budget
	// derived from one — so listing it again below reads as two separate
	// changes to the same key.
	seen := map[string]bool{}
	for _, a := range r.Applied {
		seen[a.Key] = true
	}
	for _, row := range r.budgetRows() {
		if seen[row.Key] {
			continue
		}
		fmt.Fprintf(&b, "  %-22s %d → %d\n", row.Key, row.Before, row.After)
		if row.Note != "" {
			fmt.Fprintf(&b, "  %-22s   %s\n", "", row.Note)
		}
	}

	// ── blocked ──
	//
	// Deliberately its own section, above the override advice: a user who
	// pinned a value months ago and is now wondering why the harness ignores a
	// 262K window needs this to be impossible to miss.
	if len(r.Blocked) > 0 {
		b.WriteString("\nNot applied — your configuration takes precedence\n")
		for _, bl := range r.Blocked {
			fmt.Fprintf(&b, "  %-22s measured %s, using %s\n", bl.Key, bl.Measured, bl.Current)
			if bl.How != "" {
				fmt.Fprintf(&b, "  %-22s   %s\n", "", bl.How)
			}
		}
	}

	// ── override ──
	if len(r.Adjustable) > 0 {
		b.WriteString("\nPrefer your own value?\n")
		b.WriteString("  Anything you set explicitly outranks calibration and is never overwritten:\n")
		keys := append([]string(nil), r.Adjustable...)
		sort.Strings(keys)
		fmt.Fprintf(&b, "    slmcode config set %s <value>\n", keys[0])
		fmt.Fprintf(&b, "  Adjustable here: %s\n", strings.Join(keys, ", "))
		fmt.Fprintf(&b, "  Stored per model+endpoint; re-measure at any time with "+
			"`slmcode calibrate --force`.\n")
	}
	return b.String()
}

// OneLine is the compact form, for a run that should not stop to be read.
func (r Report) OneLine() string {
	parts := []string{r.Profile.Summary()}
	if n := len(r.budgetRows()); n > 0 {
		parts = append(parts, fmt.Sprintf("%d budget(s) scaled to the window", n))
	}
	return strings.Join(parts, "; ")
}

// NewReport assembles a report from a measurement and the config it touched.
//
// before must be captured BEFORE Apply, or the diff is empty and the report
// claims nothing happened.
func NewReport(p Profile, applied []Applied, before, after config.ModelProfile) Report {
	return Report{
		Profile: p, Applied: applied, Before: before, After: after,
		Adjustable: []string{
			"max_parallel", "task_timeout", "max_tokens",
			"model_profiles", "context_slack_percent",
		},
	}
}

func ms(v int64) string {
	if v <= 0 {
		return "—"
	}
	return (time.Duration(v) * time.Millisecond).Round(10 * time.Millisecond).String()
}
