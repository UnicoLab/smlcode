package squads

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func twoTeamPlan() *Plan {
	return &Plan{Squads: []Squad{
		{ID: "backend", Owns: []string{"cmd/**", "internal/**"}, Acceptance: "go test ./..."},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build"},
	}}
}

// The live shape, twice, against two different local models: every task naming
// both halves' files, so not one belonged to anybody. The org chart was right,
// the contract was frozen, both files were edited, the run reported success —
// and the teams did nothing.
func TestSplitStraddlersCutsATaskAlongTheSeam(t *testing.T) {
	p := twoTeamPlan()
	tasks := []plan.Task{{
		ID: "T1", Role: plan.RoleWorker, Title: "serve and render the todos",
		Description: "Add a JSON endpoint and a component that fetches it.",
		Acceptance:  "the list renders",
		Files:       []string{"cmd/server/main.go", "web/src/App.tsx"},
	}}

	out, note := SplitStraddlers(p, tasks)

	if len(out) != 2 {
		t.Fatalf("got %d tasks, want one per team: %+v", len(out), out)
	}
	if note == "" {
		t.Fatal("a cut must be reported — it changes the board the user approved")
	}
	// Plan order, so the backend's piece is always the backend's piece.
	if out[0].Squad != "backend" || out[1].Squad != "frontend" {
		t.Fatalf("squads = %q, %q — want plan order", out[0].Squad, out[1].Squad)
	}
	for _, piece := range out {
		if len(piece.Files) != 1 {
			t.Fatalf("%s carries %v — a piece holds only its own team's files", piece.ID, piece.Files)
		}
		owner, owned := p.Owner(piece.Files[0])
		if !owned || owner != piece.Squad {
			t.Errorf("%s is stamped %q but holds %s, owned by %q",
				piece.ID, piece.Squad, piece.Files[0], owner)
		}
		// A piece told only about its own half writes against a seam it cannot
		// see; one told nothing about the boundary edits across it and is
		// refused at the tool layer.
		if !strings.Contains(piece.Description, "Add a JSON endpoint") {
			t.Errorf("%s lost the original description", piece.ID)
		}
		if !strings.Contains(piece.Description, "another team's") {
			t.Errorf("%s was not told there is a boundary: %q", piece.ID, piece.Description)
		}
		if piece.ID == "T1" {
			t.Errorf("a piece reused the parent id — the board would have two tasks called T1")
		}
		if !strings.HasPrefix(piece.ID, "T1-") {
			t.Errorf("%s is not traceable to its parent", piece.ID)
		}
	}

	// And the whole point: after the cut, both teams have work.
	rep := AssignBoard(p, out)
	if rep.Assigned["backend"] != 1 || rep.Assigned["frontend"] != 1 {
		t.Fatalf("assigned = %v, want one task each", rep.Assigned)
	}
	if len(rep.Straddling) != 0 {
		t.Fatalf("still straddling: %v", rep.Straddling)
	}
}

// Four refusals, each because the cut would destroy something.
func TestSplitStraddlersRefusesWhatItCannotCutSafely(t *testing.T) {
	p := twoTeamPlan()
	both := []string{"cmd/server/main.go", "web/src/App.tsx"}

	for name, tasks := range map[string][]plan.Task{
		// A tester verifying that the halves MEET is doing the one job that is
		// genuinely about both. Two half-testers each verify nothing.
		"a tester on the seam": {
			{ID: "T1", Role: plan.RoleTester, Files: both},
		},
		// The dependents named one id. Rewriting that into "all the pieces"
		// changes the wave graph on a guess about which piece they meant.
		"a task others wait on": {
			{ID: "T1", Role: plan.RoleWorker, Files: both},
			{ID: "T2", Role: plan.RoleWorker, Files: []string{"web/x.tsx"}, DependsOn: []string{"T1"}},
		},
		// A file nobody owns cannot go to a piece, and dropping it would
		// silently narrow the work.
		"a file no team owns": {
			{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/main.go", "web/a.tsx", "Makefile"}},
		},
		// Already in one lane — Assign handles it, and a cut would be a no-op.
		"one team's files": {
			{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go", "internal/b.go"}},
		},
		"a single file": {
			{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go"}},
		},
	} {
		before := len(tasks)
		out, note := SplitStraddlers(p, tasks)
		if len(out) != before || note != "" {
			t.Errorf("%s: cut into %d tasks (%q) — it must be left alone", name, len(out), note)
		}
	}
}

// A single-stream run has no boundary to cut along, and cutting anyway would
// invent two tasks out of one for no reason.
func TestSplitStraddlersNeedsTwoTeams(t *testing.T) {
	tasks := []plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go", "web/b.tsx"}}}
	for name, p := range map[string]*Plan{
		"no plan":  nil,
		"one team": {Squads: []Squad{{ID: "solo", Owns: []string{"**"}}}},
	} {
		out, note := SplitStraddlers(p, tasks)
		if len(out) != 1 || note != "" {
			t.Errorf("%s: cut into %d (%q)", name, len(out), note)
		}
	}
}

// Three teams, three pieces — the rule is per team, not "split in two".
func TestSplitStraddlersHandlesMoreThanTwoTeams(t *testing.T) {
	p := &Plan{Squads: []Squad{
		{ID: "backend", Owns: []string{"cmd/**"}},
		{ID: "frontend", Owns: []string{"web/**"}},
		{ID: "data", Owns: []string{"etl/**"}},
	}}
	tasks := []plan.Task{{
		ID: "T1", Role: "go-worker",
		Files: []string{"cmd/a.go", "web/b.tsx", "etl/c.py", "cmd/d.go"},
	}}

	out, _ := SplitStraddlers(p, tasks)

	if len(out) != 3 {
		t.Fatalf("got %d pieces, want one per team: %+v", len(out), out)
	}
	// The backend's two files stay together in its one piece.
	if len(out[0].Files) != 2 {
		t.Fatalf("backend piece = %v, want both of its files", out[0].Files)
	}
}

// A piece must never collide with an id the board already uses, or the board
// ends up with two tasks answering to one name.
func TestSplitStraddlersNeverReusesAnExistingID(t *testing.T) {
	p := twoTeamPlan()
	tasks := []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go", "web/b.tsx"}},
		{ID: "T1-BACKEND", Role: plan.RoleWorker, Files: []string{"internal/x.go"}},
	}

	out, _ := SplitStraddlers(p, tasks)

	seen := map[string]bool{}
	for _, task := range out {
		if seen[task.ID] {
			t.Fatalf("duplicate id %q on the board: %+v", task.ID, out)
		}
		seen[task.ID] = true
	}
}

// A task sitting outside every lane looks identical whether the harness chose
// that or failed to notice it. Each refusal must say which, in its own words —
// the message this replaced asserted every straddler "belongs to integration",
// which is true of a seam tester and false of a task that merely had a
// dependent.
func TestCutRefusalSaysWhyATaskWasLeftWhole(t *testing.T) {
	p := twoTeamPlan()
	both := []string{"cmd/server/main.go", "web/src/App.tsx"}

	for name, c := range map[string]struct {
		tasks []plan.Task
		want  string
	}{
		"a tester on the seam": {
			[]plan.Task{{ID: "T1", Role: plan.RoleTester, Files: both}},
			"halves meet",
		},
		"a task others wait on": {
			[]plan.Task{
				{ID: "T1", Role: plan.RoleWorker, Files: both},
				{ID: "T2", Role: plan.RoleWorker, Files: []string{"web/x.tsx"}, DependsOn: []string{"T1"}},
			},
			"other tasks depend on it",
		},
		"a file no team owns": {
			[]plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go", "web/b.tsx", "Makefile"}}},
			"no team owns",
		},
	} {
		got := CutRefusal(p, c.tasks, "T1")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: reason = %q, want it to mention %q", name, got, c.want)
		}
	}
}

// It speaks only about tasks genuinely on a seam. A task that was cut, or that
// was never straddling, has nothing to explain — and a reason offered for one
// would be a reason for something that did not happen.
func TestCutRefusalIsSilentWhenThereIsNothingToExplain(t *testing.T) {
	p := twoTeamPlan()
	for name, c := range map[string]struct {
		tasks []plan.Task
		id    string
	}{
		"a task that gets cut": {
			[]plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go", "web/b.tsx"}}}, "T1"},
		"a task in one lane": {
			[]plan.Task{{ID: "T1", Role: plan.RoleTester, Files: []string{"cmd/a.go", "internal/b.go"}}}, "T1"},
		"a single file": {
			[]plan.Task{{ID: "T1", Role: plan.RoleTester, Files: []string{"cmd/a.go"}}}, "T1"},
		"not on the board": {
			[]plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/a.go"}}}, "T9"},
	} {
		if got := CutRefusal(p, c.tasks, c.id); got != "" {
			t.Errorf("%s: explained %q when there was nothing to explain", name, got)
		}
	}
}
