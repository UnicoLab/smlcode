package plan

import (
	"encoding/json"
	"strings"
	"time"
)

// Escalate-ask modes (HITL when a task hits max review retries).
const (
	EscalateAskAsk  = "ask"  // pause pipeline for this task until user acts or timeout
	EscalateAskAuto = "auto" // reopen once without prompting
	EscalateAskOff  = "off"  // leave in to_scope, no pause
)

// Escalate actions applied to the escalated task.
const (
	EscalateActionReScope  = "re_scope"  // keep in to_scope for human edit (timeout default)
	EscalateActionRetry    = "retry"     // reopen to ready_to_dev for another wave
	EscalateActionMarkDone = "mark_done" // force done (human override)
	EscalateActionAbort    = "abort"     // block task
)

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
	CreatedAt string   `json:"created_at"`
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

// BuildEscalateAsk constructs a Studio/TUI escalate prompt for one task.
func BuildEscalateAsk(t Task, detail string, timeoutSec int) EscalateAsk {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	sum := t.ID + " needs human review after max retries"
	if strings.TrimSpace(t.Title) != "" {
		sum = t.ID + " — " + strings.TrimSpace(t.Title) + " needs human review"
	}
	return EscalateAsk{
		ID:        "escalate-" + t.ID + "-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Kind:      "escalate",
		TaskID:    t.ID,
		Title:     t.Title,
		Role:      t.Role,
		Files:     append([]string{}, t.Files...),
		Detail:    strings.TrimSpace(detail),
		Summary:   sum,
		Options:   []string{EscalateActionReScope, EscalateActionRetry, EscalateActionMarkDone, EscalateActionAbort},
		TimeoutS:  timeoutSec,
		OnTimeout: "slm",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
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

// ApplyEscalateAction mutates the board task for the chosen action.
func ApplyEscalateAction(board *Board, taskID, action, notes string) {
	if board == nil || taskID == "" {
		return
	}
	action = NormalizeEscalateAction(action)
	for i := range board.Tasks {
		if board.Tasks[i].ID != taskID {
			continue
		}
		t := &board.Tasks[i]
		t.Normalize()
		note := strings.TrimSpace(notes)
		switch action {
		case EscalateActionRetry:
			t.Error = ""
			t.Notes = strings.TrimSpace(t.Notes + "\nREOPENED: escalate → retry")
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
			t.Notes = strings.TrimSpace(t.Notes + "\nAWAITING: re-scope / precise fix in Studio")
			if note != "" {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
			}
		}
		board.Tasks[i] = *t
		return
	}
}
