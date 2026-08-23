package loop

import (
	"errors"
	"fmt"
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
	// ErrSafetyGuard means the wave loop hit its hard iteration cap.
	ErrSafetyGuard = fmt.Errorf("%w: wave loop exceeded safety guard", ErrGaveUp)
	// ErrTaskCallBudget means a task exhausted its per-task LLM call budget.
	ErrTaskCallBudget = errors.New("loop: per-task LLM call budget exhausted")
)

// GaveUpError carries the reason plus how much work was left behind.
type GaveUpError struct {
	Reason    error
	Rounds    int
	Remaining int
}

func (e *GaveUpError) Error() string {
	return fmt.Sprintf("%v (rounds=%d, tasks still open=%d)", e.Reason, e.Rounds, e.Remaining)
}

func (e *GaveUpError) Unwrap() error { return e.Reason }
