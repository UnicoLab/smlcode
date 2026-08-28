package plan

import (
	"reflect"
	"strings"
	"testing"
)

func editBoard() *Board {
	return &Board{Tasks: []Task{
		{ID: "T1", Title: "api", Role: "go-worker", Column: ColReadyToDev, Files: []string{"main.go"}},
		{ID: "T2", Title: "ui", Role: RoleWorker, Column: ColReadyToDev, DependsOn: []string{"T1"}},
		{ID: "T3", Title: "tests", Role: RoleTester, Column: ColReadyToDev, DependsOn: []string{"T1", "T2"}},
	}}
}

func known(id string) bool {
	switch id {
	case "go-worker", "react-worker", "worker", "tester":
		return true
	}
	return false
}

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

func TestApplyTaskEditsChangesWhatTheUserTouched(t *testing.T) {
	b := editBoard()
	problems := ApplyTaskEdits(b, PlanEdits{Tasks: []TaskEdit{{
		ID:         "T2",
		Title:      strp("  build the SPA  "),
		Role:       strp("react-worker"),
		Squad:      strp("frontend"),
		Acceptance: strp("npm run build"),
		Priority:   intp(2),
		Files:      []string{"web/src/App.tsx", "", "web/src/App.tsx"},
		FilesSet:   true,
	}}}, known)

	if len(problems) != 0 {
		t.Fatalf("a valid edit should be clean, got %v", problems)
	}
	got := b.Tasks[1]
	if got.Title != "build the SPA" || got.Role != "react-worker" || got.Squad != "frontend" ||
		got.Acceptance != "npm run build" || got.Priority != 2 {
		t.Errorf("edit not applied: %+v", got)
	}
	if !reflect.DeepEqual(got.Files, []string{"web/src/App.tsx"}) {
		t.Errorf("files should dedupe and drop blanks, got %v", got.Files)
	}
	// Untouched fields survive.
	if b.Tasks[0].Title != "api" || b.Tasks[2].Role != RoleTester {
		t.Error("an edit to one task changed another")
	}
}

// A UI that sends only what it touched must not blank everything else, and
// "clear this" has to stay expressible. A plain slice cannot say both.
func TestOmittedListsAreUntouchedAndSetListsAreHonoured(t *testing.T) {
	b := editBoard()
	// No FilesSet: files are not mentioned, so they stay.
	ApplyTaskEdits(b, PlanEdits{Tasks: []TaskEdit{{ID: "T1", Title: strp("renamed")}}}, known)
	if !reflect.DeepEqual(b.Tasks[0].Files, []string{"main.go"}) {
		t.Errorf("omitting files cleared them: %v", b.Tasks[0].Files)
	}
	// FilesSet with an empty list means "clear".
	ApplyTaskEdits(b, PlanEdits{Tasks: []TaskEdit{{ID: "T1", FilesSet: true}}}, known)
	if len(b.Tasks[0].Files) != 0 {
		t.Errorf("an explicit clear was ignored: %v", b.Tasks[0].Files)
	}
}

// A role the harness cannot staff fails to dispatch, and the only symptom is a
// task that never starts. Refuse it where the user can see it.
func TestUnregisteredRolesAreRefusedNotApplied(t *testing.T) {
	b := editBoard()
	problems := ApplyTaskEdits(b, PlanEdits{Tasks: []TaskEdit{
		{ID: "T1", Role: strp("rust-worker")},
		{ID: "T2", Role: strp("")},
	}}, known)

	if b.Tasks[0].Role != "go-worker" {
		t.Errorf("T1 role was changed to an unregistered agent: %q", b.Tasks[0].Role)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "not a registered agent") {
		t.Errorf("the user must be told why, got:\n%s", joined)
	}
	if !strings.Contains(joined, "empty role ignored") {
		t.Errorf("an empty role should be reported, got:\n%s", joined)
	}
}

// One stale id must not cost the user every other edit in the same pass.
func TestAStaleEditDoesNotDiscardTheRest(t *testing.T) {
	b := editBoard()
	problems := ApplyTaskEdits(b, PlanEdits{Tasks: []TaskEdit{
		{ID: "T9", Title: strp("ghost")},
		{ID: "T1", Title: strp("kept")},
	}}, known)
	if b.Tasks[0].Title != "kept" {
		t.Error("a good edit was discarded because another was stale")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "no task \"T9\"") {
		t.Errorf("problems = %v", problems)
	}
}

// A dangling depends_on parks every dependent forever waiting on an id that no
// longer exists, which looks exactly like the harness hanging.
func TestRemovingATaskRepairsTheDependenciesThatNamedIt(t *testing.T) {
	b := editBoard()
	problems := ApplyTaskEdits(b, PlanEdits{RemoveTasks: []string{"T1"}}, known)

	if len(b.Tasks) != 2 {
		t.Fatalf("expected 2 tasks after removal, got %d", len(b.Tasks))
	}
	for _, task := range b.Tasks {
		for _, d := range task.DependsOn {
			if strings.EqualFold(d, "T1") {
				t.Errorf("%s still depends on the removed T1", task.ID)
			}
		}
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "no longer depends on T1") {
		t.Errorf("the repair should be reported, got:\n%s", joined)
	}
}

func TestAddTaskDefaultsAndValidation(t *testing.T) {
	b := editBoard()
	problems := ApplyTaskEdits(b, PlanEdits{AddTasks: []Task{
		{Title: "new work", Files: []string{"x.go"}},
		{Title: "bad agent", Role: "ghost-worker"},
		{Title: "   "}, // no title at all
	}}, known)

	var added []Task
	for _, task := range b.Tasks {
		if task.Title == "new work" || task.Title == "bad agent" {
			added = append(added, task)
		}
	}
	if len(added) != 2 {
		t.Fatalf("expected 2 added tasks, got %d", len(added))
	}
	for _, task := range added {
		if task.Role == "" || task.Column == "" || task.ID == "" {
			t.Errorf("added task is not workable: %+v", task)
		}
		if task.Title == "bad agent" && task.Role != RoleWorker {
			t.Errorf("an unregistered agent should fall back to worker, got %q", task.Role)
		}
	}
	if !strings.Contains(strings.Join(problems, "\n"), "no title") {
		t.Errorf("a titleless task should be reported, got %v", problems)
	}
}

func TestApplyTaskEditsIsSafeOnNothing(t *testing.T) {
	if got := ApplyTaskEdits(nil, PlanEdits{}, known); len(got) != 1 {
		t.Errorf("a nil board should report one problem, got %v", got)
	}
	b := editBoard()
	if got := ApplyTaskEdits(b, PlanEdits{}, nil); len(got) != 0 {
		t.Errorf("no edits, no problems, got %v", got)
	}
	if len(b.Tasks) != 3 {
		t.Error("an empty edit set changed the board")
	}
}

func TestPlanEditsEmpty(t *testing.T) {
	if !(PlanEdits{}).Empty() {
		t.Error("a zero PlanEdits is empty")
	}
	if (PlanEdits{RemoveTasks: []string{"T1"}}).Empty() {
		t.Error("a removal is not empty")
	}
	if (PlanEdits{Squads: []SquadEdit{{ID: "backend"}}}).Empty() {
		t.Error("a squad edit is not empty")
	}
}
