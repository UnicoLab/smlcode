package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Escalate-ask modes (HITL when a task hits max review retries).
const (
	EscalateAskAsk = "ask" // pause pipeline for this task until user acts or timeout
	// EscalateAskAuto reopens without prompting, up to the gate retry cap.
	// It said "once" and meant "every time" — which, with no cap, is what an
	// unattended run needed to loop forever.
	EscalateAskAuto = "auto"
	EscalateAskOff  = "off" // leave in to_scope, no pause
)

// Escalate actions applied to the escalated task.
const (
	EscalateActionReScope  = "re_scope"  // keep in to_scope for human edit (timeout default)
	EscalateActionRetry    = "retry"     // reopen to ready_to_dev for another wave
	EscalateActionMarkDone = "mark_done" // force done (human override)
	EscalateActionAbort    = "abort"     // block task
)

// DefaultMaxGateRetries is how many times ONE task may be reopened by the
// escalate gate before "retry" is refused and downgraded to re_scope.
//
// Without a cap, "retry" is not a decision — it is a loop. A task that
// escalates, is answered `retry`, and re-enters an identical failing ladder
// will escalate again on identical evidence, and the timeout arbitrator's own
// heuristic defaults to retry, so an unattended run answered retry forever:
// measured at 200 gate retries and ~2,000 model calls on a ONE-task board
// before RunBoard's round guard tripped.
//
// Two is deliberate. Each gate retry costs a full ladder (up to
// max_task_calls), so the ceiling a task can spend is
// (1 + DefaultMaxGateRetries) × max_task_calls; at the shipped defaults that is
// 3 × 10 = 30 calls, which is a number an operator can wait for. The gate card
// shows the count and the cap so the person answering knows how many are left.
const DefaultMaxGateRetries = 2

// AttemptLogLimit bounds Task.AttemptLog so a long-lived board cannot grow it
// without limit.
const AttemptLogLimit = 6

// EscalateAsk is emitted when a single task needs human review mid-execute.
type EscalateAsk struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"` // "escalate"
	TaskID    string   `json:"task_id"`
	Title     string   `json:"title,omitempty"`
	Role      string   `json:"role,omitempty"`
	Files     []string `json:"files,omitempty"`
	Detail    string   `json:"detail,omitempty"` // review / error context
	Summary   string   `json:"summary"`
	Options   []string `json:"options"` // re_scope | retry | mark_done | abort
	TimeoutS  int      `json:"timeout_sec,omitempty"`
	OnTimeout string   `json:"on_timeout,omitempty"` // "slm" — @escalate decides
	// GateRetries / MaxGateRetries make the retry budget visible to whoever is
	// answering. "retry" with none left is refused and downgraded to re_scope,
	// and a gate that does not say so is asking for a decision it will ignore.
	GateRetries    int `json:"gate_retries"`
	MaxGateRetries int `json:"max_gate_retries"`
	// AttemptLog is the "attempt N failed because X" ledger so far.
	AttemptLog []string `json:"attempt_log,omitempty"`
	CreatedAt  string   `json:"created_at"`
}

// RetriesLeft reports how many more times this task may be reopened by the gate.
func (a EscalateAsk) RetriesLeft() int {
	if n := a.MaxGateRetries - a.GateRetries; n > 0 {
		return n
	}
	return 0
}

// EscalateAnswer is the user (or auto/timeout) decision.
type EscalateAnswer struct {
	AskID      string `json:"ask_id,omitempty"`
	Action     string `json:"action"` // re_scope | retry | mark_done | abort
	Notes      string `json:"notes,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

// NormalizeEscalateAsk maps config aliases.
func NormalizeEscalateAsk(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", EscalateAskAsk, "hitl", "prompt", "pause":
		return EscalateAskAsk
	case EscalateAskAuto, "once", "retry":
		return EscalateAskAuto
	case EscalateAskOff, "skip", "none", "false":
		return EscalateAskOff
	default:
		return EscalateAskAsk
	}
}

// BuildEscalateAsk constructs an escalate prompt for one task using the default
// gate-retry cap.
func BuildEscalateAsk(t Task, detail string, timeoutSec int) EscalateAsk {
	return BuildEscalateAskWithCap(t, detail, timeoutSec, DefaultMaxGateRetries)
}

// BuildEscalateAskWithCap is BuildEscalateAsk with an explicit retry cap, so
// the card an operator sees matches the cap the harness will actually enforce.
func BuildEscalateAskWithCap(t Task, detail string, timeoutSec, maxGateRetries int) EscalateAsk {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if maxGateRetries < 0 {
		maxGateRetries = 0
	}
	sum := t.ID + " needs human review after max retries"
	if strings.TrimSpace(t.Title) != "" {
		sum = t.ID + " — " + strings.TrimSpace(t.Title) + " needs human review"
	}
	left := maxGateRetries - t.GateRetries
	if left > 0 {
		sum += fmt.Sprintf(" (retry %d of %d)", t.GateRetries+1, maxGateRetries)
	} else {
		sum += fmt.Sprintf(" (retry budget spent: %d of %d used — retry will be refused)",
			t.GateRetries, maxGateRetries)
	}
	opts := []string{EscalateActionReScope, EscalateActionRetry, EscalateActionMarkDone, EscalateActionAbort}
	if left <= 0 {
		// Do not offer a choice that will be silently downgraded.
		opts = []string{EscalateActionReScope, EscalateActionMarkDone, EscalateActionAbort}
	}
	return EscalateAsk{
		ID:             "escalate-" + t.ID + "-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Kind:           "escalate",
		TaskID:         t.ID,
		Title:          t.Title,
		Role:           t.Role,
		Files:          append([]string{}, t.Files...),
		Detail:         strings.TrimSpace(detail),
		Summary:        sum,
		Options:        opts,
		TimeoutS:       timeoutSec,
		OnTimeout:      "slm",
		GateRetries:    t.GateRetries,
		MaxGateRetries: maxGateRetries,
		AttemptLog:     append([]string{}, t.AttemptLog...),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}

// MarshalEscalateAskJSON serializes for SSE KindAsk output.
func MarshalEscalateAskJSON(ask EscalateAsk) string {
	data, err := json.Marshal(ask)
	if err != nil {
		return `{"kind":"escalate","options":["re_scope","retry","mark_done","abort"]}`
	}
	return string(data)
}

// NormalizeEscalateAction maps answer aliases.
func NormalizeEscalateAction(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case EscalateActionRetry, "continue", "yes", "y", "go", "reopen":
		return EscalateActionRetry
	case EscalateActionMarkDone, "done", "force_done", "approve":
		return EscalateActionMarkDone
	case EscalateActionAbort, "block", "fail", "stop":
		return EscalateActionAbort
	default:
		return EscalateActionReScope
	}
}

// ApplyEscalateAction mutates the board task for the chosen action, using the
// default gate-retry cap. It returns nothing for compatibility; callers that
// need to know which action was actually APPLIED (retry can be downgraded to
// re_scope once the cap is spent) should call ApplyEscalateActionCapped.
func ApplyEscalateAction(board *Board, taskID, action, notes string) {
	_ = ApplyEscalateActionCapped(board, taskID, action, notes, DefaultMaxGateRetries)
}

// ApplyEscalateActionCapped mutates the board task for the chosen action and
// returns the action that was actually applied.
//
// `retry` is the interesting case. Answering retry has to CHANGE something or
// the task re-enters a byte-identical failing loop, so a granted retry:
//
//   - appends "attempt N failed because X" to the task's AttemptLog, taken from
//     the review/error that caused the escalation, so the next worker and
//     corrector prompt can carry an explicit do-not-repeat ledger;
//   - clears Output and Review, so the reviewer is not re-judging stale text
//     that the next attempt did not produce;
//   - re-derives the file scope from the evidence when the review named a
//     subset of the task's files, so the retry is narrower than the attempt
//     that failed;
//   - increments GateRetries, which is the bound.
//
// maxGateRetries <= 0 means "no retries at all" — every retry is downgraded.
// Pass a negative number only from tests that want the old unbounded behavior.
func ApplyEscalateActionCapped(board *Board, taskID, action, notes string, maxGateRetries int) string {
	if board == nil || taskID == "" {
		return ""
	}
	action = NormalizeEscalateAction(action)
	applied := action
	for i := range board.Tasks {
		if board.Tasks[i].ID != taskID {
			continue
		}
		t := &board.Tasks[i]
		t.Normalize()
		note := strings.TrimSpace(notes)
		if action == EscalateActionRetry && maxGateRetries >= 0 && t.GateRetries >= maxGateRetries {
			// Downgrade rather than loop. The operator (or the timeout
			// arbitrator, whose heuristic default IS retry) asked for something
			// the harness has already tried its budget of times.
			action = EscalateActionReScope
			applied = EscalateActionReScope
			t.Notes = strings.TrimSpace(t.Notes + "\n" +
				fmt.Sprintf("RETRY REFUSED: escalate retry cap reached (%d of %d used) — re-scoping instead. "+
					"Narrow the acceptance or fix the blocking evidence, then promote back to ready_to_dev.",
					t.GateRetries, maxGateRetries))
		}
		switch action {
		case EscalateActionRetry:
			t.GateRetries++
			t.AttemptLog = appendAttemptLog(t.AttemptLog, t.GateRetries, escalateFailureReason(*t))
			// A retry re-judges nothing: the stale output and the review that
			// rejected it belong to the attempt that just failed.
			t.Output = ""
			t.Review = ""
			t.Retries = 0
			t.Files = narrowScopeFromEvidence(*t)
			t.Error = ""
			t.Notes = strings.TrimSpace(t.Notes + "\n" +
				fmt.Sprintf("REOPENED: escalate → retry (%d of %d)", t.GateRetries, maxGateRetries))
			if note != "" {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
			}
			t.MoveTo(ColReadyToDev)
		case EscalateActionMarkDone:
			t.Error = ""
			t.Review = "human mark_done after escalate"
			if note != "" {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
			}
			if strings.TrimSpace(t.Output) == "" {
				t.Output = `{"status":"done","summary":"marked done by human after escalate"}`
			}
			t.MoveTo(ColDone)
		case EscalateActionAbort:
			t.Error = "aborted by human after escalate"
			if note != "" {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
			}
			t.MoveTo(ColBlocked)
		default: // re_scope
			t.MoveTo(ColToScope)
			t.Notes = strings.TrimSpace(t.Notes + "\nAWAITING: re-scope / precise fix")
			if note != "" {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
			}
		}
		board.Tasks[i] = *t
		return applied
	}
	return applied
}

// appendAttemptLog records one "attempt N failed because X" line, deduplicated
// and bounded.
func appendAttemptLog(log []string, attempt int, reason string) []string {
	reason = strings.TrimSpace(collapseWS(reason))
	if reason == "" {
		reason = "review rejected the attempt without naming an issue"
	}
	if len(reason) > 300 {
		reason = reason[:300] + "…"
	}
	line := fmt.Sprintf("attempt %d failed: %s — do not repeat this approach", attempt, reason)
	for _, existing := range log {
		if existing == line {
			return log
		}
	}
	log = append(log, line)
	if n := len(log); n > AttemptLogLimit {
		log = log[n-AttemptLogLimit:]
	}
	return log
}

// escalateFailureReason is the one-line "because X" for the attempt that just
// failed, drawn from whichever evidence the gate actually has.
func escalateFailureReason(t Task) string {
	for _, s := range []string{t.Review, t.Error} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	return ""
}

// collapseWS folds newlines and runs of spaces into single spaces so a
// multi-line review summary reads as one ledger line.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// narrowScopeFromEvidence re-derives a retry's file scope from the review that
// rejected the previous attempt.
//
// When the review names a strict, non-empty subset of the task's files, that
// subset is the scope for the retry: a task that failed on one of five files
// should not send the worker back at all five. When the review names all of
// them, none of them, or the task has a single file, the scope is unchanged —
// narrowing to nothing would be worse than not narrowing at all.
func narrowScopeFromEvidence(t Task) []string {
	if len(t.Files) < 2 {
		return t.Files
	}
	blob := strings.ToLower(t.Review + "\n" + t.Error + "\n" + strings.Join(t.AttemptLog, "\n"))
	if strings.TrimSpace(blob) == "" {
		return t.Files
	}
	var named []string
	for _, f := range t.Files {
		if strings.Contains(blob, strings.ToLower(strings.TrimSpace(f))) {
			named = append(named, f)
		}
	}
	if len(named) == 0 || len(named) == len(t.Files) {
		return t.Files
	}
	return named
}
