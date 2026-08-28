package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/repair"
	"github.com/UnicoLab/slmcode/pkg/schema"
)

// repairRole runs the schema-aware repair ladder for a contract role and falls
// back to the generic JSON repair when the ladder cannot produce anything.
// Schema awareness is what turns `"passed":"yes"` from a silent parse failure
// into a coerced boolean, and it records per-rung telemetry in repair.Stats.
func repairRole(raw, role string) string {
	if fixed, _, err := repair.RepairRole(raw, role); err == nil && len(fixed) > 0 {
		return string(fixed)
	}
	if fixed, err := repair.RepairJSON(raw); err == nil {
		return fixed
	}
	return raw
}

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
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Role        string `json:"role"`
	Assignee    string `json:"assignee,omitempty"`
	// Squad names the virtual team that owns this task (see pkg/squads).
	// Empty on every single-stream run, which is the overwhelming majority:
	// squads are opt-in and only assemble when a query genuinely spans two
	// domains that can be built at the same time.
	Squad      string   `json:"squad,omitempty"`
	Column     string   `json:"column"`
	Status     string   `json:"status"`
	Priority   int      `json:"priority,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Files      []string `json:"files,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	// Criteria is the structured form of Acceptance: individually addressable,
	// individually verifiable conditions. See criteria.go.
	//
	// Acceptance stays the compatibility surface — Normalize synthesizes it
	// from Criteria when only the structured form was supplied, so every
	// consumer that predates this field keeps working unchanged.
	Criteria  []Criterion     `json:"criteria,omitempty"`
	Checklist []ChecklistItem `json:"checklist,omitempty"`
	Output    string          `json:"output,omitempty"`
	Review    string          `json:"review,omitempty"`
	// Retries counts correction rounds WITHIN one review ladder. It restarts
	// at zero every time the task is dispatched to a wave, so it is not — and
	// never was — a bound on how many times a task may be attempted overall.
	Retries int `json:"retries"`
	// GateRetries counts how many times the escalate gate answered "retry" for
	// this task. Unlike Retries it accumulates for the life of the board, and
	// it is what bounds the gate loop: answering "retry" forever used to
	// re-enter an identical ladder until RunBoard's 200-round guard tripped.
	GateRetries int `json:"gate_retries,omitempty"`
	// AttemptLog is the "attempt N failed because X" ledger carried across gate
	// retries. A retry that changes nothing is an infinite loop with extra
	// steps; this is the state the next attempt is told not to repeat.
	AttemptLog []string `json:"attempt_log,omitempty"`
	Error      string   `json:"error,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	Notes      string   `json:"notes,omitempty"`
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

// boardMu serializes every Board mutation and every Tasks traversal.
//
// pkg/loop calls Get / UpdateTask from N review goroutines while the
// orchestrator's coordinator may append; UpdateTask's append can reallocate the
// backing array under a concurrent range. The lock is package-level rather than
// a Board field on purpose: Board is copied by value at every boundary
// (Result.Board, LiveStore.Replace, session.SaveTurnBoard), and a sync.RWMutex
// field would make each of those copies a `go vet` copylocks violation.
// One process holds one live board, so contention is not a concern.
//
// Exported methods must never take the lock twice on one goroutine (Go's
// RWMutex forbids recursive read locking), so every locking method delegates to
// an unexported *Locked helper that the other locked methods can reuse.
var boardMu sync.RWMutex

// ReadyTasks returns executable tasks (ready_to_dev with deps met).
// Write lock, not read: see ExecutableTasks.
func (b *Board) ReadyTasks() []Task {
	boardMu.Lock()
	defer boardMu.Unlock()
	return b.executableTasksLocked()
}

// UpdateTask replaces a task by ID.
func (b *Board) UpdateTask(updated Task) {
	updated.Normalize()
	updated.UpdatedAt = time.Now().Format(time.RFC3339)
	boardMu.Lock()
	defer boardMu.Unlock()
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
	boardMu.Lock()
	defer boardMu.Unlock()
	for i, t := range b.Tasks {
		if t.ID == id {
			b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
			return true
		}
	}
	return false
}

// NextID generates a unique task id.
//
// Prefer AddTask when the id is immediately used for an append: NextID followed
// by a bare `board.Tasks = append(...)` is two unsynchronized steps and two
// concurrent callers hand out the same id.
func (b *Board) NextID() string {
	boardMu.RLock()
	defer boardMu.RUnlock()
	return b.nextIDLocked()
}

func (b *Board) nextIDLocked() string {
	max := 0
	for _, t := range b.Tasks {
		var n int
		if _, err := fmt.Sscanf(t.ID, "T%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("T%d", max+1)
}

// AddTask appends a task, allocating its ID under the same lock so two
// concurrent adders cannot mint the same one. An ID already set on t is kept.
// Returns the stored task.
func (b *Board) AddTask(t Task) Task {
	boardMu.Lock()
	defer boardMu.Unlock()
	if strings.TrimSpace(t.ID) == "" {
		t.ID = b.nextIDLocked()
	}
	t.Normalize()
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	for i := range b.Tasks {
		if b.Tasks[i].ID == t.ID {
			b.Tasks[i] = t
			return t
		}
	}
	b.Tasks = append(b.Tasks, t)
	return t
}

// Get returns a task by ID.
func (b *Board) Get(id string) (Task, bool) {
	boardMu.RLock()
	defer boardMu.RUnlock()
	for _, t := range b.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// AllTasks returns a copy of every task, taken under the board lock.
//
// Callers that only need to READ the whole board — progress detectors,
// renderers, reporters — must use this rather than ranging over b.Tasks
// directly: UpdateTask appends, and an append can reallocate the backing array
// under a concurrent range.
func (b *Board) AllTasks() []Task {
	boardMu.RLock()
	defer boardMu.RUnlock()
	return append([]Task(nil), b.Tasks...)
}

// AllDone reports whether agents have nothing left in the pipeline.
// Human backlog (to_scope / scoped) does not block completion.
func (b *Board) AllDone() bool {
	boardMu.RLock()
	defer boardMu.RUnlock()
	if len(b.Tasks) == 0 {
		return false
	}
	return !b.agentWorkRemainingLocked()
}

// FailedCount counts blocked tasks and escalated failures (error set while not done).
func (b *Board) FailedCount() int {
	boardMu.RLock()
	defer boardMu.RUnlock()
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
	boardMu.RLock()
	defer boardMu.RUnlock()
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
		fmt.Fprintf(&p, "%d. %s\n", i+1, s)
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
		fmt.Fprintf(&t, "| %s | %s | %s | %s | %s | %s |\n",
			task.ID, escapePipe(task.Title), task.Column, task.Role, check, deps)
	}
	t.WriteString("\n## Details\n\n")
	// Name a command every renderer has, not one UI's panel: TASKS.md is
	// persisted and read by people who never open Studio. The Studio drawer is
	// still mentioned, but as an alternative rather than the only route — and
	// deliberately not as the phrase "Studio task drawer", which the terminal
	// renderer rewrites wholesale.
	t.WriteString("_Lean view for SLM context. Full outputs live in `board.json` — " +
		"run `slmcode task show <id>`, or open the task in Studio._\n")
	for _, task := range b.Tasks {
		task.Normalize()
		fmt.Fprintf(&t, "\n### %s — %s\n\n", task.ID, task.Title)
		fmt.Fprintf(&t, "- **Column:** %s\n", task.Column)
		fmt.Fprintf(&t, "- **Role:** %s\n", task.Role)
		if task.Assignee != "" {
			fmt.Fprintf(&t, "- **Assignee:** %s\n", task.Assignee)
		}
		fmt.Fprintf(&t, "- **Status:** %s\n", task.Status)
		if len(task.Files) > 0 {
			fmt.Fprintf(&t, "- **Files:** %s\n", strings.Join(task.Files, ", "))
		}
		if task.Acceptance != "" {
			fmt.Fprintf(&t, "- **Acceptance:** %s\n", truncateMD(task.Acceptance, 280))
		}
		if len(task.Checklist) > 0 {
			t.WriteString("\n#### Checklist\n\n")
			for _, c := range task.Checklist {
				mark := "[ ]"
				if c.Done {
					mark = "[x]"
				}
				fmt.Fprintf(&t, "- %s %s (`%s`)\n", mark, c.Text, c.ID)
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
	raw = repairRole(extractJSON(raw), schema.RolePlan)
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
	raw = repairRole(extractJSON(raw), schema.RoleTasks)
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

	// NoVerdict marks a result the HARNESS synthesized because the reviewer
	// never produced a readable verdict — an empty reply, prose, a tool-call
	// echo, a truncation. It is not serialized: it describes where this value
	// came from, not what the reviewer decided.
	//
	// It exists because the default-deny below is byte-identical to a
	// considered `{"approved":false,"score":0}` rejection, and the whole ladder
	// downstream — the `review approved=false score=0` event, the retry
	// counter, the corrector prompt — used to treat the two the same. A model
	// that cannot format JSON was therefore indistinguishable from a model that
	// had judged the work and found it wanting, and correct, test-passing code
	// was sent back for correction rounds that could not help it.
	NoVerdict bool `json:"-"`
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
	extracted := repairRole(extractJSON(raw), schema.RoleTester)
	var r TesterResult
	if err := json.Unmarshal([]byte(extracted), &r); err == nil {
		if len(r.Failures) == 0 && len(r.Issues) > 0 {
			r.Failures = r.Issues
		}
		eTrue := hasPassedTrue(extracted)
		eFalse := hasPassedFalse(extracted)
		// Trust an explicit passed:true anywhere in the finalize (SLMs often wrap
		// the JSON in prose or emit it just outside the parsed object), as long as
		// the object did not carry an explicit passed:false.
		if !r.Passed && !eFalse && hasPassedTrue(raw) {
			r.Passed = true
			r.Failures = nil
		}
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
		// Check the non-JSON surrounding text (Observation traces, smoke output, etc.)
		// — commands[] inside JSON alone does NOT count as execution evidence.
		nonJSONRaw := strings.TrimSpace(strings.Replace(raw, extracted, "", 1))
		if r.Passed && !TesterHasShellEvidence(nonJSONRaw) && !testerDiskPass(extracted+nonJSONRaw+r.Summary) {
			r.Passed = false
			r.Summary = firstNonEmpty(r.Summary, "tester claimed pass without execution")
			r.Failures = []string{TesterNoEvidenceFailure}
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
			r.Failures = []string{TesterNoEvidenceFailure}
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

// SmokeSectionHeader mirrors quality.SmokeSectionHeader. pkg/quality imports
// pkg/plan, so the constant cannot be imported back; TestSmokeSectionHeaderMatchesQuality
// in pkg/orchestrator asserts the two stay identical.
const SmokeSectionHeader = "## Deterministic smoke"

// shellEvidenceMarkers are frames the HARNESS emits, not prose the model can
// write. Everything here is either appended by the harness itself
// (SmokeSectionHeader), a tool-call/tool-result frame (ws_shell, Observation:),
// or a runner exit report.
//
// English participles ("running", "executed", "ran "), bare command names
// ("go test", "pytest") and stream names ("stdout") used to live here, which
// made the "real execution trace" requirement satisfiable by a sentence:
// *"Running the test suite… OK, all tests pass"* passed the gate with no tool
// call at all. Since this is the only thing between a hallucinating tester and
// completeRun, the bar is now a marker the model cannot mint.
var shellEvidenceMarkers = []string{
	strings.ToLower(SmokeSectionHeader), // harness-appended deterministic smoke
	"ws_shell",                          // tool call / tool result frame
	"exit error:",                       // executor's non-zero exit report
}

// reactShellObservation matches a ReAct tool-result frame that is about a SHELL
// command.
//
// The bare substring "observation:" used to be on the list above, and it does
// not belong there. It is the frame emitted for EVERY tool call — ws_read,
// ws_list, ws_glob — so "I read the file" satisfied a gate whose whole job is
// to insist something was EXECUTED; and it is not harness-minted at all, so a
// tester with no tool calls could type the nine characters into its prose and
// pass. (The comment above claimed "a marker the model cannot mint" while this
// entry made that false.)
//
// The frame now has to be line-anchored AND name a shell — the tool id, or the
// exit line a runner prints when a command finishes.
var reactShellObservation = regexp.MustCompile(
	`(?im)^[ \t]*>?[ \t]*observation:.*(?:\bws_shell\b|\bexit[ _]?(?:code|status)\b|\bexit error\b)`)

// TesterNoEvidenceFailure is the failure recorded when a tester claims
// passed:true with no execution trace beside the JSON. It is exported because
// a caller that repairs the finalize JSON first (pkg/loop.parseTesterOutput)
// throws the trace away in the process, and needs to recognize THIS failure in
// order to re-check the evidence against the unrepaired output.
const TesterNoEvidenceFailure = "passed:true without ws_shell / smoke execution trace — treat as failed"

// exitCodeLine matches a real runner exit report: `exit status 1`,
// `exit code: 0`, `exit_code=2`.
var exitCodeLine = regexp.MustCompile(`(?i)\bexit[ _]?(?:code|status)\s*[:=]?\s*-?\d+`)

// TesterHasShellEvidence reports whether raw tester output carries a
// harness-controlled execution frame: the deterministic smoke section the
// harness appends, a ws_shell tool-result frame, a ReAct observation frame that
// names a shell, or a runner exit-code line.
//
// Prose claims of having run something are deliberately NOT evidence — and
// neither is a bare `Observation:`, which any tool call produces and any model
// can type.
func TesterHasShellEvidence(raw string) bool {
	lower := strings.ToLower(raw)
	for _, m := range shellEvidenceMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	if reactShellObservation.MatchString(lower) {
		return true
	}
	return exitCodeLine.MatchString(lower)
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

// ReviewMalformedIssue is the issue recorded when a reviewer's finalize cannot
// be parsed. Callers may match on it to distinguish "the reviewer found
// problems" from "the reviewer never produced a verdict".
const ReviewMalformedIssue = "reviewer returned malformed or truncated JSON — treated as a rejection"

// approvedTrue / approvedFalse match an EXPLICIT verdict field, not the English
// word. `"approve"` is a substring of `"approved"`, so the old prose heuristic
// read `{"approved": false, "issues": ["stub code in parser.go` (a reviewer that
// hit max_tokens mid-array) as an APPROVAL, and did the same for
// "I would approve this once tests pass."
var (
	approvedTrue  = regexp.MustCompile(`(?i)"?approved"?\s*:\s*(?:true|"true"|"yes")`)
	approvedFalse = regexp.MustCompile(`(?i)"?approved"?\s*:\s*(?:false|"false"|"no")`)
)

// ParseReviewJSON extracts a review result.
//
// Default-deny: truncation mid-`issues[]` is the most common SLM reviewer
// failure (the reviewer's MaxTokens is 768) and it must fail CLOSED. Anything
// unparsable is a rejection carrying ReviewMalformedIssue, mirroring
// ParseTesterJSON's stance. The single exception is an explicit
// `"approved": true` with no explicit `"approved": false` anywhere — a verdict
// the model actually stated rather than one inferred from prose.
func ParseReviewJSON(raw string) ReviewResult {
	extracted := repairRole(verdictJSON(raw), schema.RoleReview)
	// The verdict key must be PRESENT, not merely absent-and-therefore-false.
	// extractJSON happily lifts `{"path":"main.go"}` out of an echoed tool call,
	// which unmarshals without error into a zero ReviewResult — a silent
	// "rejection" carrying no issue, which downstream reads as reviewer noise.
	var probe struct {
		Approved json.RawMessage `json:"approved"`
	}
	var r ReviewResult
	if err := json.Unmarshal([]byte(extracted), &r); err == nil {
		if perr := json.Unmarshal([]byte(extracted), &probe); perr == nil && len(probe.Approved) > 0 {
			return r
		}
	}
	r = ReviewResult{Summary: strings.TrimSpace(firstLine(raw))}
	if approvedTrue.MatchString(raw) && !approvedFalse.MatchString(raw) {
		r.Approved = true
		return r
	}
	r.Approved = false
	r.NoVerdict = true
	r.Issues = []string{ReviewMalformedIssue}
	if line := firstLine(raw); line != "" {
		r.Issues = append(r.Issues, line)
	}
	if r.Summary == "" {
		r.Summary = ReviewMalformedIssue
	}
	return r
}

// maxVerdictCandidates bounds the scan below. A reviewer runs at max_tokens=768,
// so a reply with a dozen JSON objects in it is already pathological.
const maxVerdictCandidates = 12

// approvedKey matches the verdict KEY in any of the shapes a small model emits
// it — quoted, bare, single-quoted. It is deliberately textual: the candidate it
// selects is handed to the repair ladder afterwards, so it must be able to pick
// a document that does not parse yet.
var approvedKey = regexp.MustCompile(`(?i)['"]?approved['"]?\s*:`)

// verdictJSON returns the JSON document in raw that actually carries the
// reviewer's verdict, falling back to extractJSON's first-document behavior.
//
// extractJSON returns the FIRST balanced document, and for a reviewer that is
// routinely the wrong one: formatReviewPrompt hands the reviewer the worker's
// own `{"status":"done","files_changed":[…]}` under "## Agent output", and a
// small model restates it before judging. The verdict that followed was then
// discarded, the echoed worker JSON was parsed in its place, that object had no
// "approved" key, and a real approval was reported as approved=false score=0.
//
// So: walk the balanced documents in order and take the first that names the
// verdict key. Nothing is invented — when no candidate names it, this is
// byte-for-byte extractJSON.
func verdictJSON(raw string) string {
	s := strings.TrimSpace(raw)
	first := extractJSON(s)
	if approvedKey.MatchString(first) {
		return first
	}
	at := strings.IndexByte(s, '{')
	for n := 0; at >= 0 && n < maxVerdictCandidates; n++ {
		if doc := balancedSpan(s, at); doc != "" && approvedKey.MatchString(doc) {
			return doc
		}
		next := strings.IndexByte(s[at+1:], '{')
		if next < 0 {
			break
		}
		at += 1 + next
	}
	return first
}

// WorkerLooksComplete detects SLM worker/explorer success signals.
func WorkerLooksComplete(output string) bool {
	lower := strings.ToLower(output)
	// Handle all variations of status:done that SLMs might output
	if strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`) ||
		strings.Contains(lower, `"status":"Done"`) || strings.Contains(lower, `"status": "Done"`) ||
		strings.Contains(lower, `status:done`) || strings.Contains(lower, `status: done`) {
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
	if startObj >= 0 && (startArr < 0 || startObj < startArr) {
		start = startObj
	} else if startArr >= 0 {
		start = startArr
	}
	if doc := balancedSpan(s, start); doc != "" {
		return doc
	}
	return s
}

// balancedSpan returns the balanced JSON document opening at s[start], or ""
// when start is out of range or the document never closes.
func balancedSpan(s string, start int) string {
	if start < 0 || start >= len(s) {
		return ""
	}
	endChar := byte('}')
	if s[start] == '[' {
		endChar = ']'
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
	return ""
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
