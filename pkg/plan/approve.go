package plan

import (
	"encoding/json"
	"strings"
	"time"
)

// Plan approve modes (Claude Code Plan Mode gate).
const (
	PlanApproveModeOff  = "off"  // never pause
	PlanApproveModeAuto = "auto" // emit summary, continue
	PlanApproveModeAsk  = "ask"  // wait for user go/edit/replan
)

// NormalizePlanApprove maps aliases.
func NormalizePlanApprove(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", PlanApproveModeAuto, "skip", "continue":
		return PlanApproveModeAuto
	case PlanApproveModeAsk, "hitl", "approve", "gate":
		return PlanApproveModeAsk
	case PlanApproveModeOff, "none", "false":
		return PlanApproveModeOff
	default:
		return PlanApproveModeAuto
	}
}

// PlanApproveAsk is the SSE/file payload before execute.
type PlanApproveAsk struct {
	ID        string   `json:"id"`
	Query     string   `json:"query"`
	Summary   string   `json:"summary"`
	Goals     []string `json:"goals,omitempty"`
	Assumptions []string `json:"assumptions,omitempty"`
	TaskCount int      `json:"task_count"`
	Tasks     []string `json:"tasks,omitempty"` // "T1: title"
	CreatedAt string   `json:"created_at"`
}

// PlanApproveAnswer is the user decision.
type PlanApproveAnswer struct {
	Decision   string `json:"decision"` // approve | replan | edit
	Notes      string `json:"notes,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

// BuildPlanApproveAsk builds a compact approval card from the board.
func BuildPlanApproveAsk(query string, board *Board) PlanApproveAsk {
	ask := PlanApproveAsk{
		ID:        "plan-" + time.Now().UTC().Format("20060102T150405"),
		Query:     query,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if board == nil {
		return ask
	}
	ask.Summary = board.Plan.Summary
	ask.Goals = board.Plan.Goals
	ask.Assumptions = board.Plan.Assumptions
	ask.TaskCount = len(board.Tasks)
	for _, t := range board.Tasks {
		line := t.ID + ": " + t.Title
		if t.Acceptance != "" {
			line += " [" + firstLine(t.Acceptance) + "]"
		}
		ask.Tasks = append(ask.Tasks, line)
		if len(ask.Tasks) >= 12 {
			break
		}
	}
	return ask
}

// MarshalPlanAskJSON renders ask for SSE.
func MarshalPlanAskJSON(ask PlanApproveAsk) string {
	b, err := json.MarshalIndent(ask, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// IsPlanApproved reports whether the answer means continue execute.
func IsPlanApproved(ans PlanApproveAnswer) bool {
	d := strings.ToLower(strings.TrimSpace(ans.Decision))
	return d == "" || d == "approve" || d == "go" || d == "yes" || d == "ok"
}

// IsPlanReplan reports whether user wants a replan.
func IsPlanReplan(ans PlanApproveAnswer) bool {
	d := strings.ToLower(strings.TrimSpace(ans.Decision))
	return d == "replan" || d == "reject" || d == "no"
}
