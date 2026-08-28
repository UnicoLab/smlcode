package orchestrator

import "fmt"

// ── The repair ledger ────────────────────────────────────────────────────
//
// A run that hit two defects and fixed both used to read exactly like a run
// where nothing went wrong: "Todo app — 3/3 tasks done, 0 failed". The work the
// harness did to get there — the ticket, the specialist, the reassignment, the
// re-verify — left no trace in the one line most people actually read.
//
// That is the wrong way round. The failures are already loud: a user watching
// the stream sees "tester found 1 failure" in warning colors and then a
// summary that mentions none of it. Either they conclude the failure was
// swallowed, or they conclude the summary is not to be trusted. Saying "2
// defects found and fixed" is both more honest and the only place the run's own
// resilience is visible.
//
// The ledger is folded from the same structured loop events the Studio's Fixes
// tab reads, with the same rule: a defect that comes back while its episode is
// open is one defect with another attempt, not two problems.

// repairLedger counts what a run found and what it repaired without a human.
type repairLedger struct {
	// found is distinct defects a gate raised.
	found int
	// resolved is defects the harness closed by itself.
	resolved int
	// restaffed is tickets a project manager moved to a different specialist.
	restaffed int
	// open tracks whether a defect is currently unclosed.
	open bool
	// blocked is set when the current defect ran out of moves.
	blocked bool
	// needsHuman counts defects that ended waiting on a person.
	needsHuman int
}

// note folds one loop action into the ledger.
//
// Mirrors web/src/components/Live/recovery.ts. Two implementations of one rule
// is a real cost; the alternative is the summary and the panel disagreeing
// about how many things went wrong, which is worse.
func (l *repairLedger) note(action string) {
	if l == nil {
		return
	}
	switch action {
	case "tester_reject", "placeholder_gaps":
		if l.open {
			// The same defect came back. Still one defect.
			return
		}
		l.open = true
		l.blocked = false
		l.found++
	case "restaffed_wave":
		if l.open {
			l.restaffed++
		}
	case "resolved", "objective_met", "escalate_resolved":
		if l.open {
			l.resolved++
			l.open = false
			l.blocked = false
		}
	case "unresolved", "continue_pending", "escalate_pending", "escalate_timeout", "aborted":
		if l.open && !l.blocked {
			l.blocked = true
			l.needsHuman++
		}
	}
}

// line is the human-readable tail for the run summary, "" when the run never
// had to repair anything.
func (l *repairLedger) line() string {
	if l == nil || l.found == 0 {
		return ""
	}
	switch {
	case l.resolved == l.found:
		return fmt.Sprintf("%s found and fixed", defects(l.found))
	case l.resolved > 0:
		return fmt.Sprintf("%d of %s fixed, %d still open",
			l.resolved, defects(l.found), l.found-l.resolved)
	default:
		return fmt.Sprintf("%s found, none resolved", defects(l.found))
	}
}

func defects(n int) string {
	if n == 1 {
		return "1 defect"
	}
	return fmt.Sprintf("%d defects", n)
}

// RunRepairs is the ledger as the API and the CLI report it.
type RunRepairs struct {
	// Found is distinct defects a gate raised during the run.
	Found int `json:"found"`
	// Resolved is how many the harness closed without a human.
	Resolved int `json:"resolved"`
	// Restaffed is tickets a project manager moved to another specialist.
	Restaffed int `json:"restaffed"`
	// NeedsHuman is defects that ended waiting on a person.
	NeedsHuman int `json:"needs_human"`
}

func (l *repairLedger) snapshot() *RunRepairs {
	if l == nil || l.found == 0 {
		return nil
	}
	return &RunRepairs{
		Found: l.found, Resolved: l.resolved,
		Restaffed: l.restaffed, NeedsHuman: l.needsHuman,
	}
}
