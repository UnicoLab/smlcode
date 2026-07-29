package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Status values for atomic tasks.
const (
	StatusPending    = "pending"
	StatusReady      = "ready"
	StatusRunning    = "running"
	StatusReview     = "review"
	StatusCorrecting = "correcting"
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

// Role identifies which specialist should execute a task.
const (
	RoleExplorer  = "explorer"
	RolePlanner   = "planner"
	RoleWorker    = "worker"
	RoleReviewer  = "reviewer"
	RoleCorrector = "corrector"
	RoleContext   = "context"
	RoleTester    = "tester"
)

// Task is an atomic, SLM-sized unit of work.
type Task struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Role        string          `json:"role"`
	Assignee    string          `json:"assignee,omitempty"`
	Column      string          `json:"column"`
	Status      string          `json:"status"`
	Priority    int             `json:"priority,omitempty"`
	DependsOn   []string        `json:"depends_on,omitempty"`
	Files       []string        `json:"files,omitempty"`
	Acceptance  string          `json:"acceptance,omitempty"`
	Checklist   []ChecklistItem `json:"checklist,omitempty"`
	Output      string          `json:"output,omitempty"`
	Review      string          `json:"review,omitempty"`
	Retries     int             `json:"retries"`
	Error       string          `json:"error,omitempty"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
	Notes       string          `json:"notes,omitempty"`
}

// Plan is the high-level strategy before task splitting.
type Plan struct {
	Summary     string   `json:"summary"`
	Goals       []string `json:"goals"`
	Assumptions []string `json:"assumptions"`
	Risks       []string `json:"risks"`
	Steps       []string `json:"steps"`
	Raw         string   `json:"raw,omitempty"`
}

// Board holds the current plan + task list.
type Board struct {
	Plan  Plan   `json:"plan"`
	Tasks []Task `json:"tasks"`
}

// ReadyTasks returns executable tasks (ready_to_dev with deps met).
func (b *Board) ReadyTasks() []Task {
	return b.ExecutableTasks()
}

// UpdateTask replaces a task by ID.
func (b *Board) UpdateTask(updated Task) {
	updated.Normalize()
	updated.UpdatedAt = time.Now().Format(time.RFC3339)
	for i, t := range b.Tasks {
		if t.ID == updated.ID {
			b.Tasks[i] = updated
			return
		}
	}
	b.Tasks = append(b.Tasks, updated)
}

// RemoveTask deletes a task by ID.
func (b *Board) RemoveTask(id string) bool {
	for i, t := range b.Tasks {
		if t.ID == id {
			b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
			return true
		}
	}
	return false
}

// NextID generates a unique task id.
func (b *Board) NextID() string {
	max := 0
	for _, t := range b.Tasks {
		var n int
		if _, err := fmt.Sscanf(t.ID, "T%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("T%d", max+1)
}

// Get returns a task by ID.
func (b *Board) Get(id string) (Task, bool) {
	for _, t := range b.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// AllDone reports whether agents have nothing left in the pipeline.
// Human backlog (to_scope / scoped) does not block completion.
func (b *Board) AllDone() bool {
	if len(b.Tasks) == 0 {
		return false
	}
	return !b.AgentWorkRemaining()
}

// FailedCount returns how many tasks failed / blocked.
func (b *Board) FailedCount() int {
	n := 0
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column == ColBlocked || t.Status == StatusFailed {
			n++
		}
	}
	return n
}

// ToMarkdown renders the board for TASKS.md / PLAN.md persistence.
func (b *Board) ToMarkdown() (planMD, tasksMD string) {
	var p strings.Builder
	p.WriteString("# Plan\n\n")
	p.WriteString("## Summary\n\n")
	p.WriteString(b.Plan.Summary)
	p.WriteString("\n\n## Goals\n\n")
	for _, g := range b.Plan.Goals {
		p.WriteString("- " + g + "\n")
	}
	p.WriteString("\n## Steps\n\n")
	for i, s := range b.Plan.Steps {
		p.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
	}
	if len(b.Plan.Assumptions) > 0 {
		p.WriteString("\n## Assumptions\n\n")
		for _, a := range b.Plan.Assumptions {
			p.WriteString("- " + a + "\n")
		}
	}
	if len(b.Plan.Risks) > 0 {
		p.WriteString("\n## Risks\n\n")
		for _, r := range b.Plan.Risks {
			p.WriteString("- " + r + "\n")
		}
	}

	var t strings.Builder
	t.WriteString("# Tasks\n\n")
	t.WriteString("| ID | Title | Column | Role | Checklist | Depends |\n")
	t.WriteString("|----|-------|--------|------|-----------|---------|\n")
	for _, task := range b.Tasks {
		task.Normalize()
		deps := strings.Join(task.DependsOn, ",")
		done, total := task.ChecklistProgress()
		check := fmt.Sprintf("%d/%d", done, total)
		t.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			task.ID, escapePipe(task.Title), task.Column, task.Role, check, deps))
	}
	t.WriteString("\n## Details\n")
	for _, task := range b.Tasks {
		task.Normalize()
		t.WriteString(fmt.Sprintf("\n### %s — %s\n\n", task.ID, task.Title))
		t.WriteString(fmt.Sprintf("- **Column:** %s\n", task.Column))
		t.WriteString(fmt.Sprintf("- **Role:** %s\n", task.Role))
		if task.Assignee != "" {
			t.WriteString(fmt.Sprintf("- **Assignee:** %s\n", task.Assignee))
		}
		t.WriteString(fmt.Sprintf("- **Status:** %s\n", task.Status))
		if len(task.Files) > 0 {
			t.WriteString(fmt.Sprintf("- **Files:** %s\n", strings.Join(task.Files, ", ")))
		}
		if task.Acceptance != "" {
			t.WriteString(fmt.Sprintf("- **Acceptance:** %s\n", task.Acceptance))
		}
		if len(task.Checklist) > 0 {
			t.WriteString("\n#### Checklist\n\n")
			for _, c := range task.Checklist {
				mark := "[ ]"
				if c.Done {
					mark = "[x]"
				}
				t.WriteString(fmt.Sprintf("- %s %s (`%s`)\n", mark, c.Text, c.ID))
			}
		}
		t.WriteString("\n")
		t.WriteString(task.Description)
		t.WriteString("\n")
		if task.Notes != "" {
			t.WriteString("\n#### Notes\n\n")
			t.WriteString(task.Notes)
			t.WriteString("\n")
		}
		if task.Output != "" {
			t.WriteString("\n#### Output\n\n")
			t.WriteString(task.Output)
			t.WriteString("\n")
		}
		if task.Review != "" {
			t.WriteString("\n#### Review\n\n")
			t.WriteString(task.Review)
			t.WriteString("\n")
		}
		if task.Error != "" {
			t.WriteString("\n#### Error\n\n")
			t.WriteString(task.Error)
			t.WriteString("\n")
		}
	}
	return p.String(), t.String()
}

func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", "/")
}

// ParsePlanJSON extracts a Plan from model output (JSON or fenced JSON).
func ParsePlanJSON(raw string) (Plan, error) {
	raw = extractJSON(raw)
	var p Plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		// Fallback: treat entire text as summary/steps
		return Plan{
			Summary: firstLine(raw),
			Steps:   splitLines(raw),
			Raw:     raw,
		}, nil
	}
	p.Raw = raw
	return p, nil
}

// ParseTasksJSON extracts tasks from model output.
func ParseTasksJSON(raw string) ([]Task, error) {
	raw = extractJSON(raw)
	var payload struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		var list []Task
		if err2 := json.Unmarshal([]byte(raw), &list); err2 != nil {
			return nil, fmt.Errorf("parse tasks: %w", err)
		}
		payload.Tasks = list
	}
	for i := range payload.Tasks {
		if payload.Tasks[i].ID == "" {
			payload.Tasks[i].ID = fmt.Sprintf("T%d", i+1)
		}
		if payload.Tasks[i].Role == "" {
			payload.Tasks[i].Role = RoleWorker
		}
		// Auto-split tasks start ready for agents unless column set
		if payload.Tasks[i].Column == "" {
			if payload.Tasks[i].Status == StatusPending || payload.Tasks[i].Status == "" {
				payload.Tasks[i].Column = ColReadyToDev
			}
		}
		payload.Tasks[i].Normalize()
	}
	return payload.Tasks, nil
}

// ReviewResult is the structured output of the reviewer specialist.
type ReviewResult struct {
	Approved bool     `json:"approved"`
	Score    int      `json:"score"`
	Issues   []string `json:"issues"`
	Summary  string   `json:"summary"`
}

// ParseReviewJSON extracts a review result.
func ParseReviewJSON(raw string) ReviewResult {
	extracted := extractJSON(raw)
	var r ReviewResult
	if err := json.Unmarshal([]byte(extracted), &r); err != nil {
		lower := strings.ToLower(raw)
		r.Approved = strings.Contains(lower, `"approved":true`) ||
			strings.Contains(lower, `"approved": true`) ||
			strings.Contains(lower, "approved: true") ||
			(strings.Contains(lower, "approve") && !strings.Contains(lower, "not approve") &&
				!strings.Contains(lower, "reject") && !strings.Contains(lower, "<function"))
		r.Summary = strings.TrimSpace(firstLine(raw))
		if !r.Approved {
			r.Issues = []string{firstLine(raw)}
		}
	}
	return r
}

// WorkerLooksComplete detects SLM worker/explorer success signals.
func WorkerLooksComplete(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`) {
		return true
	}
	if strings.Contains(lower, "dry-run: would") && (strings.Contains(lower, "edit") || strings.Contains(lower, "write")) {
		return true
	}
	// Explorer often returns paths without status JSON
	if strings.Contains(lower, ".go") && (strings.Contains(lower, "found") ||
		strings.Contains(lower, "hello.go") || strings.Contains(lower, `"files"`) ||
		strings.Contains(lower, "path")) {
		return true
	}
	return false
}

var fencedJSON = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\}|\\[.*?\\])\\s*```")

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if m := fencedJSON.FindStringSubmatch(s); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	// Find first { ... } or [ ... ]
	startObj := strings.Index(s, "{")
	startArr := strings.Index(s, "[")
	start := -1
	endChar := byte('}')
	if startObj >= 0 && (startArr < 0 || startObj < startArr) {
		start = startObj
		endChar = '}'
	} else if startArr >= 0 {
		start = startArr
		endChar = ']'
	}
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 && c == endChar {
				return s[start : i+1]
			}
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*0123456789. ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
