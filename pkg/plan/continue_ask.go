package plan

import (
	"encoding/json"
	"strings"
	"time"
)

// Continue-ask modes (HITL when retries / QA exhausted).
const (
	ContinueAskAuto = "auto" // one automatic corrective wave, then stop
	ContinueAskAsk  = "ask"  // pause for user continue/stop
	ContinueAskOff  = "off"  // never ask; finish with failure
)

// ContinueAsk is emitted when work remains after retries/QA are exhausted.
type ContinueAsk struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"` // "continue"
	Query     string   `json:"query,omitempty"`
	Reason    string   `json:"reason"`
	Summary   string   `json:"summary"`
	Gaps      []string `json:"gaps,omitempty"`      // precise path:line — reason
	Escalated []string `json:"escalated,omitempty"` // task IDs
	Options   []string `json:"options"`             // continue | stop | flag_only
	TimeoutS  int      `json:"timeout_sec,omitempty"`
	OnTimeout string   `json:"on_timeout,omitempty"` // "stop"
	CreatedAt string   `json:"created_at"`
}

// ContinueAnswer is the user (or auto) decision.
type ContinueAnswer struct {
	AskID      string `json:"ask_id,omitempty"`
	Action     string `json:"action"` // continue | stop | flag_only
	Notes      string `json:"notes,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

// NormalizeContinueAsk maps config aliases.
func NormalizeContinueAsk(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", ContinueAskAsk, "hitl", "prompt":
		return ContinueAskAsk
	case ContinueAskAuto, "once", "retry":
		return ContinueAskAuto
	case ContinueAskOff, "skip", "none", "false":
		return ContinueAskOff
	default:
		return ContinueAskAsk
	}
}

// BuildContinueAsk constructs a Studio/TUI continue prompt.
func BuildContinueAsk(query, reason string, escalated []string, gaps []string) ContinueAsk {
	return ContinueAsk{
		ID:        "continue-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Kind:      "continue",
		Query:     query,
		Reason:    reason,
		Summary:   "Retries/QA exhausted but work remains. Continue another wave, stop, or keep precise flags?",
		Gaps:      gaps,
		Escalated: escalated,
		Options:   []string{"continue", "stop", "flag_only"},
		OnTimeout: "stop",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// MarshalContinueAskJSON serializes for SSE KindAsk output.
func MarshalContinueAskJSON(ask ContinueAsk) string {
	data, err := json.Marshal(ask)
	if err != nil {
		return `{"kind":"continue","options":["continue","stop","flag_only"]}`
	}
	return string(data)
}

// NormalizeContinueAction maps answer aliases.
func NormalizeContinueAction(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "continue", "yes", "y", "retry", "go":
		return "continue"
	case "flag_only", "flag", "precise", "keep_flags":
		return "flag_only"
	default:
		return "stop"
	}
}
