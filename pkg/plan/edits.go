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
	// Interfaces edit the frozen contract — the one artifact a two-team run
	// cannot recover from getting wrong, because both halves build against it
	// and neither can ask the other later. A user who can see that the route is
	// `/api/todos` and the model wrote `/todos` must be able to fix that here;
	// the alternative is approving a plan they know is wrong, or replanning the
	// whole board over one string.
	Interfaces       []InterfaceEdit `json:"interfaces,omitempty"`
	RemoveInterfaces []string        `json:"remove_interfaces,omitempty"`
}

// InterfaceEdit changes one clause of the frozen contract. Nil fields are
// untouched; New marks a clause the user added rather than edited.
type InterfaceEdit struct {
	ID       string  `json:"id"`
	Provider *string `json:"provider,omitempty"`
	Spec     *string `json:"spec,omitempty"`
	// Rename moves the clause to a new id — an interface id IS its name (a
	// route, a symbol), so correcting a wrong route means renaming.
	Rename    *string  `json:"rename,omitempty"`
	Consumers []string `json:"consumers,omitempty"`
	// ConsumersSet distinguishes "not provided" from "cleared", the same way
	// FilesSet does for a task.
	ConsumersSet bool `json:"consumers_set,omitempty"`
	New          bool `json:"new,omitempty"`
}

// SquadEdit changes one virtual team. Nil fields are untouched.
type SquadEdit struct {
	ID         string  `json:"id"`
	Name       *string `json:"name,omitempty"`
	Charter    *string `json:"charter,omitempty"`
	Acceptance *string `json:"acceptance,omitempty"`
	Worker     *string `json:"worker,omitempty"`
	Reviewer   *string `json:"reviewer,omitempty"`
	Tester     *string `json:"tester,omitempty"`
	// Manager is the agent that triages this team's rejected work.
	Manager *string  `json:"manager,omitempty"`
	Owns    []string `json:"owns,omitempty"`
	OwnsSet bool     `json:"owns_set,omitempty"`
	// Agents is the team's open roster — as many as its author wants. Paired
	// with AgentsSet for the same reason Owns is: a bare empty list cannot say
	// whether the user cleared it or never opened the field.
	Agents    []string `json:"agents,omitempty"`
	AgentsSet bool     `json:"agents_set,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	SkillsSet bool     `json:"skills_set,omitempty"`
	// New marks a squad the user added rather than edited.
	New bool `json:"new,omitempty"`
}

// Empty reports whether these edits would change nothing.
func (e PlanEdits) Empty() bool {
	return len(e.Tasks) == 0 && len(e.AddTasks) == 0 && len(e.RemoveTasks) == 0 &&
		len(e.Squads) == 0 && len(e.RemoveSquads) == 0 &&
		len(e.Interfaces) == 0 && len(e.RemoveInterfaces) == 0
}

// TouchesSquads reports whether the org chart or the contract is edited.
func (e PlanEdits) TouchesSquads() bool {
	return len(e.Squads) > 0 || len(e.RemoveSquads) > 0 ||
		len(e.Interfaces) > 0 || len(e.RemoveInterfaces) > 0
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

	// A new task's incoming ID is a CLIENT REFERENCE, never a board id.
	//
	// The editor has to name a task before the board has assigned it one — that
	// is the only way "this existing task now waits on the task I just added"
	// can be expressed at all. Treating that placeholder as a real id would be
	// worse than useless: AddTask REPLACES a task whose id matches, so a client
	// that happened to send "T2" would silently overwrite the board's T2.
	// So the placeholder is stripped, the board assigns the id, and every
	// dependency that named the placeholder is rewritten to it.
	assigned := map[string]string{}
	for _, nt := range edits.AddTasks {
		ref := strings.ToUpper(strings.TrimSpace(nt.ID))
		nt.ID = ""
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
		added := board.AddTask(nt)
		if ref != "" && ref != strings.ToUpper(added.ID) {
			assigned[ref] = added.ID
		}
	}
	if len(assigned) > 0 {
		resolveNewDeps(board, assigned)
	}

	if len(edits.RemoveTasks) > 0 {
		problems = append(problems, removeTasks(board, edits.RemoveTasks)...)
	}
	return problems
}

// resolveNewDeps rewrites dependencies that named a not-yet-created task.
//
// Without this, "make T1 wait on the task I just added" leaves T1 depending on
// a placeholder id no task carries — and the board's blocked-propagation parks
// T1 forever waiting on something that will never arrive, which looks exactly
// like the harness hanging.
func resolveNewDeps(board *Board, assigned map[string]string) {
	for i := range board.Tasks {
		deps := board.Tasks[i].DependsOn
		if len(deps) == 0 {
			continue
		}
		changed := false
		next := make([]string, 0, len(deps))
		for _, d := range deps {
			if real, ok := assigned[strings.ToUpper(strings.TrimSpace(d))]; ok {
				next = append(next, real)
				changed = true
				continue
			}
			next = append(next, d)
		}
		if changed {
			board.Tasks[i].DependsOn = cleanEditList(next)
		}
	}
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
