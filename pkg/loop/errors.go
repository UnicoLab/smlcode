package loop

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors let the orchestrator tell "the board finished" apart from
// "the loop gave up". RunBoard used to return a bare nil after ~15s of idle
// spinning with work still in progress, and a bare fmt.Errorf after the safety
// guard tripped — both indistinguishable from a clean finish.
var (
	// ErrGaveUp is the class every give-up path wraps.
	ErrGaveUp = errors.New("loop: gave up before the board was finished")
	// ErrIdleTimeout means agent work was still in progress but no task became
	// executable within the idle budget.
	ErrIdleTimeout = fmt.Errorf("%w: idle while agent work still in progress", ErrGaveUp)
	// ErrHumanBacklog means only human-owned columns (to_scope/scoped) remain.
	ErrHumanBacklog = fmt.Errorf("%w: waiting on human backlog", ErrGaveUp)
	// ErrSafetyGuard means the wave loop hit its hard iteration cap. It is a
	// BACKSTOP, not the bound: ErrNoProgress and the per-task attempt ceiling
	// are what actually stop a board that is not advancing, and they fire in a
	// handful of rounds rather than a couple of hundred.
	ErrSafetyGuard = fmt.Errorf("%w: wave loop exceeded safety guard", ErrGaveUp)
	// ErrNoProgress means consecutive full rounds completed with nothing
	// changing: no task moved column, no file changed on disk, no new output.
	ErrNoProgress = fmt.Errorf("%w: the board stopped advancing", ErrGaveUp)
	// ErrTaskCallBudget means a task exhausted its per-task LLM call budget.
	ErrTaskCallBudget = errors.New("loop: per-task LLM call budget exhausted")
	// ErrTaskAttempts means one task was dispatched to a wave more times than
	// the board-level attempt ceiling allows.
	ErrTaskAttempts = errors.New("loop: task exceeded its board-level attempt ceiling")
)

// GaveUpError carries the reason plus how much work was left behind.
type GaveUpError struct {
	Reason    error
	Rounds    int
	Remaining int
	// Stalled names the tasks that were not advancing, with their column and
	// attempt counts. An operator cannot act on "tasks still open=1".
	Stalled []string
	// Remedy is the one concrete thing to do about it.
	Remedy string
}

func (e *GaveUpError) Error() string {
	msg := fmt.Sprintf("%v (rounds=%d, tasks still open=%d)", e.Reason, e.Rounds, e.Remaining)
	if len(e.Stalled) > 0 {
		msg += "; stalled: " + strings.Join(e.Stalled, "; ")
	}
	if strings.TrimSpace(e.Remedy) != "" {
		msg += " — " + e.Remedy
	}
	return msg
}

func (e *GaveUpError) Unwrap() error { return e.Reason }
