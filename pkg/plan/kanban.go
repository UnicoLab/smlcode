package plan

import (
	"fmt"
	"strings"
	"time"
)

// Kanban columns for human + agent orchestration.
const (
	ColToScope    = "to_scope"
	ColScoped     = "scoped"
	ColReadyToDev = "ready_to_dev"
	ColInProgress = "in_progress"
	ColInReview   = "in_review"
	ColDone       = "done"
	ColBlocked    = "blocked"
)

// Columns returns ordered kanban columns for UI/CLI.
func Columns() []string {
	return []string{
		ColToScope, ColScoped, ColReadyToDev, ColInProgress, ColInReview, ColDone, ColBlocked,
	}
}

// ColumnLabel is a human-readable column name.
func ColumnLabel(col string) string {
	switch col {
	case ColToScope:
		return "To scope"
	case ColScoped:
		return "Scoped"
	case ColReadyToDev:
		return "Ready to dev"
	case ColInProgress:
		return "In progress"
	case ColInReview:
		return "In review"
	case ColDone:
		return "Done"
	case ColBlocked:
		return "Blocked"
	default:
		return col
	}
}

// ChecklistItem is a tickable acceptance / sub-step for SLM-sized work.
type ChecklistItem struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	Updated string `json:"updated,omitempty"`
}

// Normalize fills Column/Status defaults and keeps them in sync.
func (t *Task) Normalize() {
	if t.Role == "" {
		t.Role = RoleWorker
	}
	if t.Column == "" {
		t.Column = columnFromStatus(t.Status)
	}
	if t.Status == "" {
		t.Status = statusFromColumn(t.Column)
	}
	// Prefer column as source of truth when both set inconsistently for agents
	t.Status = statusFromColumn(t.Column)
	if t.Priority == 0 {
		t.Priority = 3
	}
	t.Criteria = NormalizeCriteria(t.Criteria)
	// Keep the prose surface in sync so every consumer that reads Acceptance —
	// board markdown, worker prompts, review packs, the acceptance smoke scan —
	// sees a structured task exactly as it sees a prose one. Only synthesized
	// when the model supplied criteria and no prose; a model that wrote both
	// keeps its own words.
	if strings.TrimSpace(t.Acceptance) == "" && len(t.Criteria) > 0 {
		t.Acceptance = CriteriaText(t.Criteria)
	}
	for i := range t.Checklist {
		if t.Checklist[i].ID == "" {
			t.Checklist[i].ID = fmt.Sprintf("c%d", i+1)
		}
	}
}

// MoveTo sets the kanban column and synced status.
func (t *Task) MoveTo(col string) {
	col = strings.TrimSpace(strings.ToLower(col))
	col = normalizeColumnAlias(col)
	t.Column = col
	t.Status = statusFromColumn(col)
	t.UpdatedAt = time.Now().Format(time.RFC3339)
}

// Delegate assigns a specialist role (worker, reviewer, explorer, …).
func (t *Task) Delegate(role string) {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		role = RoleWorker
	}
	t.Role = role
	t.Assignee = role
	t.UpdatedAt = time.Now().Format(time.RFC3339)
}

// SetChecklistItem toggles or updates a checklist row.
func (t *Task) SetChecklistItem(id string, done bool, text string) bool {
	for i := range t.Checklist {
		if t.Checklist[i].ID == id || (id == "" && t.Checklist[i].Text == text) {
			t.Checklist[i].Done = done
			if text != "" {
				t.Checklist[i].Text = text
			}
			t.Checklist[i].Updated = time.Now().Format(time.RFC3339)
			t.UpdatedAt = t.Checklist[i].Updated
			return true
		}
	}
	return false
}

// AddChecklist appends a checklist item.
func (t *Task) AddChecklist(text string) ChecklistItem {
	item := ChecklistItem{
		ID:   fmt.Sprintf("c%d", len(t.Checklist)+1),
		Text: text,
	}
	t.Checklist = append(t.Checklist, item)
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	return item
}

// ChecklistProgress returns done/total.
func (t *Task) ChecklistProgress() (done, total int) {
	total = len(t.Checklist)
	for _, c := range t.Checklist {
		if c.Done {
			done++
		}
	}
	return
}

func columnFromStatus(status string) string {
	switch status {
	case StatusPending:
		return ColToScope
	case StatusReady:
		return ColReadyToDev
	case StatusRunning, StatusCorrecting:
		return ColInProgress
	case StatusReview:
		return ColInReview
	case StatusDone, StatusSkipped:
		return ColDone
	case StatusFailed:
		return ColBlocked
	default:
		if status == "" {
			return ColToScope
		}
		return ColToScope
	}
}

func statusFromColumn(col string) string {
	switch col {
	case ColToScope, ColScoped:
		return StatusPending
	case ColReadyToDev:
		return StatusReady
	case ColInProgress:
		return StatusRunning
	case ColInReview:
		return StatusReview
	case ColDone:
		return StatusDone
	case ColBlocked:
		return StatusFailed
	default:
		return StatusPending
	}
}

func normalizeColumnAlias(col string) string {
	switch col {
	case "todo", "backlog", "scope", "to-scope":
		return ColToScope
	case "scoped", "specced":
		return ColScoped
	case "ready", "ready-to-dev", "dev-ready", "queued":
		return ColReadyToDev
	case "doing", "progress", "wip", "in-progress", "running":
		return ColInProgress
	case "review", "in-review", "qa":
		return ColInReview
	case "complete", "completed", "finished":
		return ColDone
	case "fail", "failed", "block", "blocked":
		return ColBlocked
	default:
		return col
	}
}

// ByColumn groups tasks for kanban rendering.
func (b *Board) ByColumn() map[string][]Task {
	boardMu.RLock()
	defer boardMu.RUnlock()
	out := map[string][]Task{}
	for _, c := range Columns() {
		out[c] = nil
	}
	for _, t := range b.Tasks {
		t.Normalize()
		out[t.Column] = append(out[t.Column], t)
	}
	return out
}

// ExecutableTasks are ready for the agent loop (ready_to_dev, deps satisfied).
//
// It takes the WRITE lock because computing readiness also settles it: a task
// whose dependency can never be satisfied is moved to blocked rather than left
// in ready_to_dev for a scheduler that will never pick it. See PropagateBlocked.
func (b *Board) ExecutableTasks() []Task {
	boardMu.Lock()
	defer boardMu.Unlock()
	return b.executableTasksLocked()
}

func (b *Board) executableTasksLocked() []Task {
	// Settle the unsatisfiable tasks before deciding who is ready, so the two
	// answers cannot disagree: whatever is not returned below is either waiting
	// on live work or has just been moved to blocked with the reason attached.
	b.propagateBlockedLocked()
	// ONLY done satisfies a dependency. Blocked used to count as satisfied so
	// that one failed locate task could not freeze the board — but a dependent
	// released that way runs on a foundation that was never built, which is how
	// a single failure becomes a whole wave of them.
	satisfied := map[string]bool{}
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column == ColDone {
			satisfied[t.ID] = true
		}
	}
	var ready []Task
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column != ColReadyToDev {
			continue
		}
		ok := true
		for _, dep := range t.DependsOn {
			if !satisfied[dep] {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, t)
		}
	}
	return ready
}

// AgentWorkRemaining is true while agents still have work in the pipe.
func (b *Board) AgentWorkRemaining() bool {
	boardMu.RLock()
	defer boardMu.RUnlock()
	return b.agentWorkRemainingLocked()
}

func (b *Board) agentWorkRemainingLocked() bool {
	for _, t := range b.Tasks {
		t.Normalize()
		switch t.Column {
		case ColReadyToDev, ColInProgress, ColInReview:
			return true
		}
	}
	return false
}

// HumanBacklogRemaining is true when tasks wait for human scoping.
func (b *Board) HumanBacklogRemaining() bool {
	boardMu.RLock()
	defer boardMu.RUnlock()
	for _, t := range b.Tasks {
		t.Normalize()
		switch t.Column {
		case ColToScope, ColScoped:
			return true
		}
	}
	return false
}
