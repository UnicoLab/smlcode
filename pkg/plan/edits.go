package plan

import (
	"fmt"
	"sort"
	"strings"
)

// ── Editing the proposed plan ────────────────────────────────────────────
//
// The approval gate used to offer two answers: approve, or replan. Replan
// throws the whole board away and pays for another planning pass to fix one
// wrong file path or one task assigned to the wrong specialist — so in practice
// people approved a plan they could see was slightly wrong and let the run
// discover it the expensive way.
//
// Edits are the third answer. They are applied by the harness, not by the
// model, so what the user saw is what runs.
//
// Every field that can be edited is a POINTER. A UI that sends back only the
// fields it touched must not blank the rest, and "set this to empty" has to
// stay expressible — those are different requests and a plain string cannot
// tell them apart.

// TaskEdit changes one task on the proposed board. Nil fields are untouched.
type TaskEdit struct {
	ID          string   `json:"id"`
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Role        *string  `json:"role,omitempty"`
	Squad       *string  `json:"squad,omitempty"`
	Acceptance  *string  `json:"acceptance,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Files       []string `json:"files,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	// FilesSet / DependsSet distinguish "not provided" from "cleared". A slice
	// cannot: both arrive as nil.
	FilesSet   bool `json:"files_set,omitempty"`
	DependsSet bool `json:"depends_set,omitempty"`
}

// PlanEdits is the full set of changes a human made to the proposed plan.
type PlanEdits struct {
	Tasks       []TaskEdit `json:"tasks,omitempty"`
	AddTasks    []Task     `json:"add_tasks,omitempty"`
	RemoveTasks []string   `json:"remove_tasks,omitempty"`
	// Squads are applied by pkg/squads, which owns ownership validation. They
	// travel here so one answer carries the whole edit.
	Squads       []SquadEdit `json:"squads,omitempty"`
	RemoveSquads []string    `json:"remove_squads,omitempty"`
}

// SquadEdit changes one virtual team. Nil fields are untouched.
type SquadEdit struct {
	ID         string   `json:"id"`
	Name       *string  `json:"name,omitempty"`
	Charter    *string  `json:"charter,omitempty"`
	Acceptance *string  `json:"acceptance,omitempty"`
	Worker     *string  `json:"worker,omitempty"`
	Reviewer   *string  `json:"reviewer,omitempty"`
	Owns       []string `json:"owns,omitempty"`
	OwnsSet    bool     `json:"owns_set,omitempty"`
	// New marks a squad the user added rather than edited.
	New bool `json:"new,omitempty"`
}

// Empty reports whether these edits would change nothing.
func (e PlanEdits) Empty() bool {
	return len(e.Tasks) == 0 && len(e.AddTasks) == 0 && len(e.RemoveTasks) == 0 &&
		len(e.Squads) == 0 && len(e.RemoveSquads) == 0
}

// ApplyTaskEdits applies the task half of the edits to a board.
//
// Returns a human-readable list of what it refused and why. Refusals are
// reported rather than fatal: a UI that sends one stale task id must not cost
// the user every other edit they made in the same pass.
//
// roleExists gates role changes. Naming an agent that is not registered fails
// to dispatch, so a role the harness cannot staff is refused here rather than
// at execute time, where the only symptom is a task that never starts.
func ApplyTaskEdits(board *Board, edits PlanEdits, roleExists func(string) bool) []string {
	if board == nil {
		return []string{"no board to edit"}
	}
	var problems []string

	index := map[string]int{}
	for i, t := range board.Tasks {
		index[strings.ToUpper(strings.TrimSpace(t.ID))] = i
	}

	for _, e := range edits.Tasks {
		key := strings.ToUpper(strings.TrimSpace(e.ID))
		i, ok := index[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("no task %q on the board — edit ignored", e.ID))
			continue
		}
		t := board.Tasks[i]
		if e.Title != nil {
			t.Title = strings.TrimSpace(*e.Title)
		}
		if e.Description != nil {
			t.Description = strings.TrimSpace(*e.Description)
		}
		if e.Acceptance != nil {
			t.Acceptance = strings.TrimSpace(*e.Acceptance)
		}
		if e.Priority != nil {
			t.Priority = *e.Priority
		}
		if e.Squad != nil {
			t.Squad = strings.TrimSpace(*e.Squad)
		}
		if e.Role != nil {
			role := strings.ToLower(strings.TrimSpace(*e.Role))
			switch {
			case role == "":
				problems = append(problems, fmt.Sprintf("%s: empty role ignored", t.ID))
			case roleExists != nil && !roleExists(role):
				problems = append(problems, fmt.Sprintf(
					"%s: %q is not a registered agent — role unchanged", t.ID, role))
			default:
				t.Role = role
			}
		}
		if e.FilesSet {
			t.Files = cleanEditList(e.Files)
		}
		if e.DependsSet {
			t.DependsOn = cleanEditList(e.DependsOn)
		}
		t.Normalize()
		board.Tasks[i] = t
	}

	for _, nt := range edits.AddTasks {
		nt.Title = strings.TrimSpace(nt.Title)
		if nt.Title == "" {
			problems = append(problems, "a new task with no title was ignored")
			continue
		}
		if nt.Role == "" {
			nt.Role = RoleWorker
		}
		role := strings.ToLower(strings.TrimSpace(nt.Role))
		if roleExists != nil && !roleExists(role) {
			problems = append(problems, fmt.Sprintf(
				"new task %q names unregistered agent %q — using %s", nt.Title, role, RoleWorker))
			role = RoleWorker
		}
		nt.Role = role
		if nt.Column == "" {
			nt.Column = ColReadyToDev
		}
		nt.Normalize()
		board.AddTask(nt)
	}

	if len(edits.RemoveTasks) > 0 {
		problems = append(problems, removeTasks(board, edits.RemoveTasks)...)
	}
	return problems
}

// removeTasks deletes tasks and repairs the dependencies that named them.
//
// A dangling depends_on is worse than the task the user deleted: the board's
// blocked-propagation would park every dependent forever waiting on an id that
// no longer exists, which looks like the harness hanging.
func removeTasks(board *Board, ids []string) []string {
	var problems []string
	gone := map[string]bool{}
	for _, id := range ids {
		gone[strings.ToUpper(strings.TrimSpace(id))] = true
	}

	kept := make([]Task, 0, len(board.Tasks))
	removed := map[string]bool{}
	for _, t := range board.Tasks {
		key := strings.ToUpper(strings.TrimSpace(t.ID))
		if gone[key] {
			removed[key] = true
			continue
		}
		kept = append(kept, t)
	}
	for id := range gone {
		if !removed[id] {
			problems = append(problems, fmt.Sprintf("no task %q on the board — nothing removed", id))
		}
	}

	for i := range kept {
		var deps []string
		var dropped []string
		for _, d := range kept[i].DependsOn {
			if removed[strings.ToUpper(strings.TrimSpace(d))] {
				dropped = append(dropped, d)
				continue
			}
			deps = append(deps, d)
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			problems = append(problems, fmt.Sprintf(
				"%s no longer depends on %s (removed)", kept[i].ID, strings.Join(dropped, ", ")))
			kept[i].DependsOn = deps
		}
	}
	board.Tasks = kept
	return problems
}

func cleanEditList(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
