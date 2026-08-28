package orchestrator

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// A run that hit two defects and fixed both used to read exactly like a run
// where nothing went wrong. The failures are already loud in the stream, so a
// summary that mentions none of them reads either as a swallowed failure or as
// a summary not worth trusting.

func fold(actions ...string) *repairLedger {
	l := &repairLedger{}
	for _, a := range actions {
		l.note(a)
	}
	return l
}

func TestACleanRunSaysNothingAboutRepairs(t *testing.T) {
	l := fold()
	if got := l.line(); got != "" {
		t.Errorf("line = %q, want empty on a run that never had to repair anything", got)
	}
	if l.snapshot() != nil {
		t.Error("a clean run reports no repair record")
	}
	var nilLedger *repairLedger
	if got := nilLedger.line(); got != "" {
		t.Errorf("nil ledger line = %q", got)
	}
}

func TestOneDefectFoundAndFixed(t *testing.T) {
	l := fold("tester_reject", "rewrite", "corrective_wave", "reverify", "resolved")
	if got, want := l.line(), "1 defect found and fixed"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	snap := l.snapshot()
	if snap == nil || snap.Found != 1 || snap.Resolved != 1 || snap.NeedsHuman != 0 {
		t.Errorf("snapshot = %+v", snap)
	}
}

// A defect that comes back is one defect with another attempt. Counting it
// twice would make a run look twice as broken as it is — and would disagree
// with the Fixes tab, which folds the same events by the same rule.
func TestARepeatIsStillOneDefect(t *testing.T) {
	l := fold(
		"tester_reject", "rewrite", "corrective_wave", "reverify",
		"tester_reject", "restaffed_wave", "resolved",
	)
	if got, want := l.line(), "1 defect found and fixed"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if got := l.snapshot().Restaffed; got != 1 {
		t.Errorf("Restaffed = %d, want the manager's handoff counted", got)
	}
}

func TestTwoSeparateDefectsAreCountedSeparately(t *testing.T) {
	l := fold("tester_reject", "resolved", "tester_reject", "resolved")
	if got, want := l.line(), "2 defects found and fixed"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestAPartialRepairSaysWhatIsStillOpen(t *testing.T) {
	l := fold("tester_reject", "resolved", "tester_reject", "unresolved")
	if got, want := l.line(), "1 of 2 defects fixed, 1 still open"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if got := l.snapshot().NeedsHuman; got != 1 {
		t.Errorf("NeedsHuman = %d, want 1", got)
	}
}

func TestNothingResolvedIsSaidPlainly(t *testing.T) {
	l := fold("tester_reject", "rewrite", "unresolved")
	if got, want := l.line(), "1 defect found, none resolved"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// A defect that stalls twice is one defect waiting on one person.
func TestABlockedDefectIsCountedOnce(t *testing.T) {
	l := fold("tester_reject", "unresolved", "continue_pending", "escalate_pending")
	if got := l.snapshot().NeedsHuman; got != 1 {
		t.Errorf("NeedsHuman = %d, want 1", got)
	}
}

// A resolution with nothing open is somebody else's green light — the objective
// gate firing on a clean run — and must not invent a defect to close.
func TestAResolutionWithNothingOpenIsIgnored(t *testing.T) {
	l := fold("objective_met", "resolved")
	if l.found != 0 || l.resolved != 0 {
		t.Errorf("ledger = %+v, want an untouched ledger", l)
	}
	if got := l.line(); got != "" {
		t.Errorf("line = %q, want empty", got)
	}
}

func TestTheSummaryCarriesTheRepairLine(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColDone}, {ID: "T2", Column: plan.ColDone},
	}}
	pl := plan.Plan{Summary: "Todo app: Go API + React SPA"}

	clean := summarizeWithRepairs(board, pl, fold())
	if want := "Todo app: Go API + React SPA — 2/2 tasks done, 0 failed"; clean != want {
		t.Errorf("clean summary = %q, want %q", clean, want)
	}

	repaired := summarizeWithRepairs(board, pl, fold("tester_reject", "restaffed_wave", "resolved"))
	if want := clean + " · 1 defect found and fixed"; repaired != want {
		t.Errorf("repaired summary = %q, want %q", repaired, want)
	}
}

// Every squad green and the app broken is a defect like any other. Without it
// on the ledger the summary reads "0 failed" over a broken application.
func TestABrokenSeamIsADefect(t *testing.T) {
	l := fold("integration_failed")
	if got, want := l.line(), "1 defect found, none resolved"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	fixed := fold("integration_failed", "restaffed_wave", "resolved")
	if got, want := fixed.line(), "1 defect found and fixed"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}
