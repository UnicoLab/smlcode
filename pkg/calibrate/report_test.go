package calibrate

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func reportFixture() Report {
	p := measured()
	p.ContextLimit = 262144
	p.ContextSource = "max_model_len"
	p.TokensPerSec = 42.5
	p.P50Ms, p.P95Ms, p.SoloSamples = 900, 1400, 3

	before := config.ModelProfile{
		ContextLimit: 16384, MaxTokens: 3072, ThinkingBudgetTokens: 3072,
		SkillTokenBudget: 260, KnowledgeTokenBudget: 180, MaxTurns: 20, Temperature: 0.12,
	}
	after := DeriveProfile(before, p.ContextLimit)
	applied := []Applied{
		{Key: "max_parallel", From: "4", To: "2", Why: "4-way ran at 38% efficiency"},
	}
	return NewReport(p, applied, before, after)
}

// TestReportShowsEvidenceNotJustNumbers is the point of the whole file.
//
// Calibration silently rewrites the values a run is governed by. A number that
// arrives without its evidence is a number nobody can argue with, so the first
// time one feels wrong the reflex is to switch the feature off entirely. Every
// value must be traceable to the measurement behind it.
func TestReportShowsEvidenceNotJustNumbers(t *testing.T) {
	out := reportFixture().Render()

	for _, want := range []string{
		"262144",        // the measured window
		"max_model_len", // and where it came from
		"42.5",          // decode rate
		"p95",           // latency evidence
		"efficiency",    // why this concurrency and not a higher one
		"max_parallel",  // what changed
		"because",       // and the reason it changed
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report never mentions %q:\n%s", want, out)
		}
	}
}

// TestReportShowsTheDerivedBudgetsAsADiff: the budgets are the part a user is
// most likely to disagree with, so "260 → 1024" has to be visible rather than
// implied by a context number.
//
// max_turns is deliberately NOT in this list any more. The report shows budgets
// that CHANGED, and turns no longer scale with the window — see deriveTurns for
// the measurement that removed the growth. Asserting it here would re-require
// the behavior that measurement removed.
func TestReportShowsTheDerivedBudgetsAsADiff(t *testing.T) {
	r := reportFixture()
	out := r.Render()

	if r.ChangedBudgets() == 0 {
		t.Fatal("a 16K→262K derivation moved no budgets")
	}
	if strings.Contains(out, "max_turns") {
		t.Errorf("the report lists max_turns as changed, but turns no longer "+
			"scale with the window:\n%s", out)
	}
	for _, want := range []string{
		"skill_token_budget", "knowledge_token_budget", "→",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the budget diff omits %q:\n%s", want, out)
		}
	}
}

// TestReportTellsTheUserHowToDisagree. A report that explains a decision but
// not how to change it is a lecture.
func TestReportTellsTheUserHowToDisagree(t *testing.T) {
	out := reportFixture().Render()
	if !strings.Contains(out, "slmcode config set") {
		t.Errorf("no override instruction:\n%s", out)
	}
	if !strings.Contains(out, "outranks calibration") {
		t.Errorf("the report does not say an explicit value wins:\n%s", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("no way to re-measure:\n%s", out)
	}
}

// TestReportSaysWhenNothingHappened. Silence and "nothing changed" look
// identical in a log, and only one of them means the tool ran.
func TestReportSaysWhenNothingHappened(t *testing.T) {
	base := config.ModelProfile{ContextLimit: 32768, MaxTokens: 4096, MaxTurns: 24}
	r := NewReport(measured(), nil, base, base)
	out := r.Render()
	if !strings.Contains(out, "nothing") {
		t.Errorf("a no-op calibration did not say so:\n%s", out)
	}
	if r.ChangedBudgets() != 0 {
		t.Errorf("ChangedBudgets()=%d for an unchanged profile", r.ChangedBudgets())
	}
}

// TestReportFlagsAPartialMeasurement. A partial profile is the one most likely
// to be wrong, and it expires in an hour rather than a month — a reader has to
// know which kind they are looking at.
func TestReportFlagsAPartialMeasurement(t *testing.T) {
	r := reportFixture()
	r.Profile.Partial = true
	out := r.Render()
	if !strings.Contains(out, "PARTIAL") {
		t.Errorf("a partial measurement was not flagged:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "underestimate") {
		t.Errorf("the report does not say what partial MEANS:\n%s", out)
	}
}

// TestOneLineIsShortEnoughForARun: the compact form prints before every run, so
// it must not be a paragraph.
func TestOneLineIsShortEnoughForARun(t *testing.T) {
	line := reportFixture().OneLine()
	if strings.Contains(line, "\n") {
		t.Errorf("OneLine spans lines: %q", line)
	}
	if len(line) > 160 {
		t.Errorf("OneLine is %d chars, too long to print before every run: %q", len(line), line)
	}
	if line == "" {
		t.Error("OneLine is empty")
	}
}
