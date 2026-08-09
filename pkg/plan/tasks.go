package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/repair"
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
	RoleExplorer    = "explorer"
	RolePlanner     = "planner"
	RoleWorker      = "worker"
	RoleReviewer    = "reviewer"
	RoleCorrector   = "corrector"
	RoleContext     = "context"
	RoleTester      = "tester"
	RolePlaceholder = "placeholder" // detect/fill stubs; flag precise gaps
	RoleEscalate    = "escalate"    // HITL timeout arbitrator (re_scope/retry/mark_done/abort)
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

// Board holds the current plan + task list for one user query/turn.
// QueryID scopes this board to a single interaction — each new query gets a
// fresh board rewrite (not an in-place patch of a forever-global board).
type Board struct {
	QueryID string `json:"query_id,omitempty"`
	Query   string `json:"query,omitempty"`
	Plan    Plan   `json:"plan"`
	Tasks   []Task `json:"tasks"`
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

// FailedCount counts blocked tasks and escalated failures (error set while not done).
func (b *Board) FailedCount() int {
	n := 0
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column == ColBlocked || t.Status == StatusFailed {
			n++
			continue
		}
		if t.Error != "" && t.Column != ColDone {
			n++
		}
	}
	return n
}

// ToMarkdown renders the board for TASKS.md / PLAN.md persistence.
func (b *Board) ToMarkdown() (planMD, tasksMD string) {
	var p strings.Builder
	p.WriteString("# Plan\n\n")
	if b.QueryID != "" || b.Query != "" {
		p.WriteString("## Query scope\n\n")
		if b.QueryID != "" {
			p.WriteString("- **Query ID:** " + b.QueryID + "\n")
		}
		if b.Query != "" {
			p.WriteString("- **Query:** " + strings.TrimSpace(b.Query) + "\n")
		}
		p.WriteString("\n")
	}
	summary := strings.TrimSpace(b.Plan.Summary)
	if looksLikeRawJSON(summary) {
		summary = "Plan parsed incompletely — see Raw appendix."
	}
	p.WriteString("## Summary\n\n")
	if summary == "" {
		p.WriteString("_No summary yet._")
	} else {
		p.WriteString(summary)
	}
	p.WriteString("\n\n## Goals\n\n")
	if len(b.Plan.Goals) == 0 {
		p.WriteString("_None listed._\n")
	}
	for _, g := range b.Plan.Goals {
		p.WriteString("- " + g + "\n")
	}
	p.WriteString("\n## Steps\n\n")
	steps := b.Plan.Steps
	if len(steps) == 0 {
		p.WriteString("_None listed._\n")
	}
	for i, s := range steps {
		if looksLikeRawJSON(s) {
			continue
		}
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
	if raw := strings.TrimSpace(b.Plan.Raw); raw != "" &&
		(looksLikeRawJSON(b.Plan.Summary) || (len(b.Plan.Goals) == 0 && len(b.Plan.Steps) == 0)) {
		p.WriteString("\n## Raw planner output\n\n```json\n")
		p.WriteString(truncateMD(raw, 4000))
		p.WriteString("\n```\n")
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
	t.WriteString("\n## Details\n\n")
	t.WriteString("_Lean view for SLM context. Full outputs live in `board.json` / Studio task drawer._\n")
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
			t.WriteString(fmt.Sprintf("- **Acceptance:** %s\n", truncateMD(task.Acceptance, 280)))
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
		desc := leanTaskDescription(task.Description)
		if desc != "" {
			t.WriteString("\n")
			t.WriteString(truncateMD(desc, 500))
			t.WriteString("\n")
		}
		if task.Notes != "" {
			t.WriteString("\n#### Notes\n\n")
			t.WriteString(truncateMD(task.Notes, 400))
			t.WriteString("\n")
		}
		if task.Output != "" {
			t.WriteString("\n#### Output\n\n")
			t.WriteString(truncateMD(task.Output, 400))
			t.WriteString("\n")
		}
		if task.Review != "" {
			t.WriteString("\n#### Review\n\n")
			t.WriteString(truncateMD(task.Review, 280))
			t.WriteString("\n")
		}
		if task.Error != "" {
			t.WriteString("\n#### Error\n\n")
			t.WriteString(truncateMD(task.Error, 280))
			t.WriteString("\n")
		}
	}
	return p.String(), t.String()
}

func leanTaskDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
	}
	if strings.HasPrefix(desc, "# Scoped context") {
		// Drop fat packs that used to bloat TASKS.md for every persist.
		return ""
	}
	return desc
}

func truncateMD(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", "/")
}

// ParsePlanJSON extracts a Plan from model output (JSON or fenced JSON).
func ParsePlanJSON(raw string) (Plan, error) {
	original := strings.TrimSpace(raw)
	raw = extractJSON(raw)
	if fixed, err := repair.RepairJSON(raw); err == nil {
		raw = fixed
	}
	// Flexible decode: SLMs often emit steps as objects instead of strings.
	var flex struct {
		Summary     string          `json:"summary"`
		Goals       []string        `json:"goals"`
		Assumptions []string        `json:"assumptions"`
		Risks       []string        `json:"risks"`
		Steps       json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal([]byte(raw), &flex); err != nil {
		return Plan{
			Summary: humanPlanFallback(original, raw),
			Steps:   humanPlanSteps(original, raw),
			Raw:     original,
		}, nil
	}
	p := Plan{
		Summary:     strings.TrimSpace(flex.Summary),
		Goals:       flex.Goals,
		Assumptions: flex.Assumptions,
		Risks:       flex.Risks,
		Steps:       parsePlanSteps(flex.Steps),
		Raw:         raw,
	}
	if looksLikeRawJSON(p.Summary) || strings.HasPrefix(p.Summary, `"summary"`) {
		p.Summary = humanPlanFallback(original, raw)
	}
	var cleanSteps []string
	for _, s := range p.Steps {
		s = strings.TrimSpace(s)
		if s == "" || looksLikeRawJSON(s) {
			continue
		}
		cleanSteps = append(cleanSteps, s)
	}
	p.Steps = cleanSteps
	return p, nil
}

func parsePlanSteps(raw json.RawMessage) []string {
	if len(bytesTrimSpace(raw)) == 0 {
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil {
		return asStrings
	}
	var asObjs []map[string]interface{}
	if err := json.Unmarshal(raw, &asObjs); err != nil {
		return nil
	}
	var out []string
	for _, obj := range asObjs {
		for _, key := range []string{"description", "task", "action", "details", "text", "title", "step"} {
			if v, ok := obj[key]; ok {
				switch t := v.(type) {
				case string:
					if s := strings.TrimSpace(t); s != "" && key != "step" {
						out = append(out, s)
						goto next
					}
				case float64:
					// skip numeric step ids
				}
			}
		}
		// Fallback: first string field.
		for _, v := range obj {
			if s, ok := v.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
					break
				}
			}
		}
	next:
	}
	return out
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func looksLikeRawJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return true
	}
	if strings.HasPrefix(s, "```") {
		return true
	}
	return strings.Contains(s, `"summary"`) && strings.Contains(s, `"steps"`)
}

func humanPlanFallback(original, extracted string) string {
	for _, candidate := range []string{original, extracted} {
		for _, line := range strings.Split(candidate, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || looksLikeRawJSON(line) || strings.HasPrefix(line, "```") {
				continue
			}
			line = strings.TrimLeft(line, "-*# ")
			if line != "" {
				return truncateMD(line, 240)
			}
		}
	}
	return "Plan available (structured fields incomplete — see raw appendix)."
}

func humanPlanSteps(original, extracted string) []string {
	var out []string
	for _, candidate := range []string{original, extracted} {
		for _, line := range splitLines(candidate) {
			if looksLikeRawJSON(line) || strings.HasPrefix(line, "```") {
				continue
			}
			out = append(out, line)
			if len(out) >= 6 {
				return out
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

// ParseTasksJSON extracts tasks from model output.
func ParseTasksJSON(raw string) ([]Task, error) {
	raw = extractJSON(raw)
	if fixed, err := repair.RepairJSON(raw); err == nil {
		raw = fixed
	}
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

// TesterResult is the structured output of the tester specialist.
type TesterResult struct {
	Passed   bool     `json:"passed"`
	Commands []string `json:"commands,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Failures []string `json:"failures,omitempty"`
	Issues   []string `json:"issues,omitempty"` // alias some models use
}

// ParseTesterJSON extracts a tester result. Empty, malformed, or ambiguous
// finalize output is ALWAYS treated as failed — never a silent pass.
func ParseTesterJSON(raw string) TesterResult {
	if strings.TrimSpace(raw) == "" {
		return TesterResult{
			Passed:   false,
			Summary:  "empty tester finalize",
			Failures: []string{"empty or missing tester JSON — treat as failed; rewrite plan/tasks"},
		}
	}
	extracted := extractJSON(raw)
	if fixed, err := repair.RepairJSON(extracted); err == nil {
		extracted = fixed
	}
	var r TesterResult
	if err := json.Unmarshal([]byte(extracted), &r); err == nil {
		if len(r.Failures) == 0 && len(r.Issues) > 0 {
			r.Failures = r.Issues
		}
		eTrue := hasPassedTrue(extracted)
		eFalse := hasPassedFalse(extracted)
		if !r.Passed && len(r.Failures) == 0 && eFalse {
			if s := firstLine(r.Summary); s != "" {
				r.Failures = []string{s}
			} else {
				r.Failures = []string{"tester reported passed=false"}
			}
		}
		if r.Passed && len(r.Failures) > 0 {
			r.Passed = false
		}
		if r.Passed && !TesterHasShellEvidence(raw) && !testerDiskPass(extracted+raw+r.Summary) {
			r.Passed = false
			r.Summary = firstNonEmpty(r.Summary, "tester claimed pass without execution")
			r.Failures = []string{"passed:true without ws_shell / smoke execution trace — treat as failed"}
		}
		if !r.Passed && !eTrue && len(r.Failures) == 0 {
			r.Summary = firstNonEmpty(r.Summary, "malformed or incomplete tester JSON")
			r.Failures = []string{"tester finalize missing passed:true — treat as failed; rewrite plan/tasks"}
		}
		return r
	}
	// JSON unmarshal failed — fall back to text-based heuristics.
	lower := strings.ToLower(raw)
	switch {
	case hasPassedTrue(lower):
		r.Passed = true
		if !TesterHasShellEvidence(raw) && !testerDiskPass(raw) {
			r.Passed = false
			r.Failures = []string{"passed:true without ws_shell / smoke execution trace — treat as failed"}
		}
	case hasPassedFalse(lower):
		r.Passed = false
		r.Failures = []string{firstLine(raw)}
	case strings.Contains(lower, "does not work") || strings.Contains(lower, "doesn't work") ||
		strings.Contains(lower, "not working") || strings.Contains(lower, "failed") ||
		strings.Contains(lower, "test failed") || strings.Contains(lower, "still broken"):
		r.Passed = false
		r.Failures = []string{firstLine(raw)}
	default:
		r.Passed = false
		r.Summary = firstLine(raw)
		if r.Summary == "" {
			r.Summary = "malformed tester finalize"
		}
		r.Failures = []string{"tester output unclear or malformed — treat as failed until rewritten"}
	}
	return r
}

// hasPassedTrue checks for all variations of a JSON-like "passed: true" boolean.
// Handles: "passed":true, "passed": true, "passed":True, passed:true, "passed":"true", etc.
func hasPassedTrue(s string) bool {
	return regexp.MustCompile(`(?i)"?passed"?\s*:\s*(true|True|"true")`).MatchString(s)
}

// hasPassedFalse checks for all variations of a JSON-like "passed: false" boolean.
func hasPassedFalse(s string) bool {
	return regexp.MustCompile(`(?i)"?passed"?\s*:\s*(false|False|"false")`).MatchString(s)
}

// TesterFailed reports whether verification should drive a plan/task rewrite.
// Empty/malformed finalize counts as failed (never a silent skip).
func TesterFailed(raw string) bool {
	return !ParseTesterJSON(raw).Passed
}

// TesterHasShellEvidence reports whether raw tester output contains a real
// execution trace (ws_shell observation, deterministic smoke, exit codes,
// or common test runner output patterns).
func TesterHasShellEvidence(raw string) bool {
	lower := strings.ToLower(raw)
	markers := []string{
		"observation:",
		"exit error:",
		"exit status",
		"exit code",
		"## deterministic smoke",
		"ws_shell",
		"py_compile",
		"compileall",
		"python -m pytest",
		"python -m py_compile",
		"go test",
		"npm test",
		"cargo test",
		"pytest",
		"ran ",
		"ran\n",
		"executed",
		"stdout",
		"stderr",
		"command:",
		"$ ",
		"running",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	if strings.Contains(lower, "passed") && strings.Contains(lower, "observation:") {
		return true
	}
	// Shell commands often produce output with these patterns
	if strings.Contains(lower, "ok") && (strings.Contains(lower, "test") || strings.Contains(lower, "run")) {
		return true
	}
	return false
}

func testerDiskPass(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "rename") ||
		strings.Contains(lower, "verified on disk") || strings.Contains(lower, "on disk")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ParseReviewJSON extracts a review result.
func ParseReviewJSON(raw string) ReviewResult {
	extracted := extractJSON(raw)
	if fixed, err := repair.RepairJSON(extracted); err == nil {
		extracted = fixed
	}
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
