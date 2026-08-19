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
	ID          string            `json:"id"`
	Kind        string            `json:"kind,omitempty"`
	Query       string            `json:"query"`
	Summary     string            `json:"summary"`
	Goals       []string          `json:"goals,omitempty"`
	Assumptions []string          `json:"assumptions,omitempty"`
	TaskCount   int               `json:"task_count"`
	Tasks       []string          `json:"tasks,omitempty"` // "T1: title"
	TaskDetails []PlanApproveTask `json:"task_details,omitempty"`
	Composition *PlanComposition  `json:"composition,omitempty"`
	Validation  *ScopeJudgeResult `json:"validation,omitempty"`
	Options     []string          `json:"options,omitempty"`
	TimeoutS    int               `json:"timeout_sec,omitempty"`
	OnTimeout   string            `json:"on_timeout,omitempty"` // "approve"
	CreatedAt   string            `json:"created_at"`
}

// PlanApproveTask is the structured, UI-friendly task preview for validation.
type PlanApproveTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Role        string   `json:"role,omitempty"`
	Column      string   `json:"column,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Files       []string `json:"files,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
}

// PlanComposition is a compact, UI-friendly dynamic pipeline preview attached
// to the plan approval ask.
type PlanComposition struct {
	Summary  string                 `json:"summary,omitempty"`
	Strategy string                 `json:"strategy,omitempty"`
	Handoff  []string               `json:"handoff,omitempty"`
	SLMFit   []string               `json:"slm_fit,omitempty"`
	Phases   []PlanCompositionPhase `json:"phases,omitempty"`
	Execute  PlanCompositionExecute `json:"execute,omitempty"`
	Team     []PlanCompositionTeam  `json:"team,omitempty"`
	Slots    []PlanCompositionSlot  `json:"slots,omitempty"`
}

type PlanCompositionPhase struct {
	ID      string `json:"id"`
	Agent   string `json:"agent,omitempty"`
	Enabled bool   `json:"enabled"`
	When    string `json:"when,omitempty"`
}

type PlanCompositionExecute struct {
	DefaultRole string `json:"default_role,omitempty"`
	Reviewer    string `json:"reviewer,omitempty"`
	Corrector   string `json:"corrector,omitempty"`
	MaxWaves    int    `json:"max_waves,omitempty"`
}

type PlanCompositionTeam struct {
	Role   string   `json:"role"`
	Skills []string `json:"skills,omitempty"`
}

type PlanCompositionSlot struct {
	ID        string `json:"id"`
	Agent     string `json:"agent,omitempty"`
	Title     string `json:"title,omitempty"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Replace   string `json:"replace,omitempty"`
	When      string `json:"when,omitempty"`
	PersistTo string `json:"persist_to,omitempty"`
	FailMode  string `json:"fail_mode,omitempty"`
}

// PlanApproveAnswer is the user decision.
type PlanApproveAnswer struct {
	AskID      string `json:"ask_id,omitempty"`
	Decision   string `json:"decision"` // approve | replan
	Notes      string `json:"notes,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

// BuildPlanApproveAsk builds a compact approval card from the board.
func BuildPlanApproveAsk(query string, board *Board) PlanApproveAsk {
	ask := PlanApproveAsk{
		ID:        "plan-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Kind:      "plan",
		Query:     query,
		Options:   []string{"approve", "replan"},
		OnTimeout: "approve",
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
	for _, t := range board.Tasks {
		if len(ask.TaskDetails) >= 20 {
			break
		}
		ask.TaskDetails = append(ask.TaskDetails, PlanApproveTask{
			ID:          t.ID,
			Title:       t.Title,
			Description: compactPlanText(t.Description, 360),
			Role:        t.Role,
			Column:      t.Column,
			Priority:    t.Priority,
			DependsOn:   append([]string{}, t.DependsOn...),
			Files:       append([]string{}, t.Files...),
			Acceptance:  compactPlanText(t.Acceptance, 420),
		})
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
	return d == "approve" || d == "go" || d == "yes" || d == "ok"
}

// IsPlanReplan reports whether user wants a replan.
func IsPlanReplan(ans PlanApproveAnswer) bool {
	d := strings.ToLower(strings.TrimSpace(ans.Decision))
	return d == "replan" || d == "reject" || d == "no"
}

func compactPlanText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-3]) + "..."
}
