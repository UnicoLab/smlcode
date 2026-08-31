package plan

import (
	"reflect"
	"strings"
	"testing"
)

// A mixed Go + React repository with both language packs installed — the case
// a single run-level specialist gets wrong for half the board.
func mixedStack(id string) bool {
	switch id {
	case "go-worker", "go-reviewer", "go-tester",
		"react-worker", "react-reviewer", "react-tester",
		"ts-worker", "worker", "reviewer", "tester":
		return true
	}
	return false
}

func mixedPolicy() RoutePolicy {
	return RoutePolicy{
		Available:       mixedStack,
		DefaultWorker:   "go-worker", // what langpick would pick for a Go repo
		DefaultReviewer: "reviewer",
		DefaultTester:   "tester",
		SquadWorker: func(id string) string {
			switch id {
			case "backend":
				return "go-worker"
			case "frontend":
				return "react-worker"
			}
			return ""
		},
	}
}

// The headline case: one board, two languages, correct specialist on each task.
func TestRoutingAdaptsPerTaskInAMixedRepo(t *testing.T) {
	cases := []struct {
		name     string
		task     Task
		wantRole string
		wantRev  string
		wantTest string
	}{
		{"go file gets the go specialist",
			Task{ID: "T1", Role: RoleWorker, Squad: "backend", Files: []string{"cmd/server/main.go"}},
			"go-worker", "go-reviewer", "go-tester"},
		{"tsx file gets react — NOT the run-level go default",
			Task{ID: "T2", Role: RoleWorker, Squad: "frontend", Files: []string{"web/src/App.tsx"}},
			"react-worker", "react-reviewer", "react-tester"},
		{"majority of files decides",
			Task{ID: "T3", Role: RoleWorker, Files: []string{"a.go", "b.tsx", "c.tsx", "d.tsx"}},
			"react-worker", "react-reviewer", "react-tester"},
		{"no files falls back to the squad's worker",
			Task{ID: "T4", Role: RoleWorker, Squad: "frontend"},
			"react-worker", "reviewer", "tester"},
		{"no files and no squad falls back to the run default",
			Task{ID: "T5", Role: RoleWorker},
			"go-worker", "reviewer", "tester"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RouteTask(tc.task, mixedPolicy())
			if got.Role != tc.wantRole {
				t.Errorf("Role = %q (%s), want %q", got.Role, got.Reason, tc.wantRole)
			}
			if got.Reviewer != tc.wantRev {
				t.Errorf("Reviewer = %q, want %q", got.Reviewer, tc.wantRev)
			}
			if got.Tester != tc.wantTest {
				t.Errorf("Tester = %q, want %q", got.Tester, tc.wantTest)
			}
		})
	}
}

// The files are the fact; the squad label can be stale. Same reasoning
// langpick.go uses to prefer the repository over a word in the query.
func TestFilesOutrankTheSquadLabel(t *testing.T) {
	got := RouteTask(Task{
		ID: "T1", Role: RoleWorker, Squad: "backend", // says Go
		Files: []string{"web/src/App.tsx", "web/src/List.tsx"}, // but it is React
	}, mixedPolicy())
	if got.Role != "react-worker" {
		t.Fatalf("Role = %q (%s), want react-worker", got.Role, got.Reason)
	}
	if !strings.Contains(got.Reason, "files are react") {
		t.Errorf("the reason should name the evidence, got %q", got.Reason)
	}
}

// Routing is not entitled to second-guess a deliberate assignment.
func TestRoutingKeepsAnExplicitSpecialist(t *testing.T) {
	got := RouteTask(Task{
		ID: "T1", Role: "ts-worker", Files: []string{"web/src/App.tsx"},
	}, mixedPolicy())
	if got.Role != "ts-worker" {
		t.Fatalf("Role = %q, want the explicitly chosen ts-worker", got.Role)
	}
	if got.Changed {
		t.Error("keeping a role is not a change")
	}
	if !strings.Contains(got.Reason, "already names") {
		t.Errorf("Reason = %q", got.Reason)
	}
}

// A tester task is not a worker task; turning it into one loses the phase.
func TestRoutingLeavesNonImplementerRolesAlone(t *testing.T) {
	for _, role := range []string{RoleTester, RoleReviewer, RoleExplorer, RolePlanner} {
		got := RouteTask(Task{ID: "T1", Role: role, Files: []string{"main.go"}}, mixedPolicy())
		if got.Role != role {
			t.Errorf("%s was rerouted to %q", role, got.Role)
		}
	}
}

// Naming an agent that is not registered fails to dispatch — worse than a
// generic worker doing it slightly less well.
func TestRoutingNeverNamesAnUnregisteredAgent(t *testing.T) {
	onlyGeneric := RoutePolicy{Available: func(id string) bool { return id == RoleWorker }}
	for _, files := range [][]string{{"main.rs"}, {"App.tsx"}, {"main.go"}, nil} {
		got := RouteTask(Task{ID: "T1", Role: RoleWorker, Files: files}, onlyGeneric)
		if got.Role != RoleWorker {
			t.Errorf("files %v routed to unregistered %q", files, got.Role)
		}
		if got.Reviewer != "" || got.Tester != "" {
			t.Errorf("no registered reviewer/tester, but got %q/%q", got.Reviewer, got.Tester)
		}
	}
	// A nil registry must not panic or invent an agent.
	if got := RouteTask(Task{ID: "T1", Role: RoleWorker, Files: []string{"main.go"}}, RoutePolicy{}); got.Role != RoleWorker {
		t.Errorf("with no registry at all, Role = %q", got.Role)
	}
}

// A router whose answer depends on map iteration staffs a different team on
// every run.
func TestRoutingIsDeterministic(t *testing.T) {
	task := Task{ID: "T1", Role: RoleWorker, Files: []string{"a.go", "b.tsx"}}
	first := RouteTask(task, mixedPolicy())
	for i := 0; i < 50; i++ {
		if got := RouteTask(task, mixedPolicy()); got.Role != first.Role {
			t.Fatalf("run %d routed to %q, first run said %q", i, got.Role, first.Role)
		}
	}
}

func TestLanguageOf(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"main.go"}, "go"},
		{[]string{"App.tsx"}, "react"},
		{[]string{"api.ts"}, "ts"},
		{[]string{"a.go", "b.go", "c.tsx"}, "go"},
		{[]string{"README.md"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := LanguageOf(tc.files); got != tc.want {
			t.Errorf("LanguageOf(%v) = %q, want %q", tc.files, got, tc.want)
		}
	}
}

func TestRouteBoardStampsAndTallies(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Squad: "backend", Files: []string{"cmd/main.go"}},
		{ID: "T2", Role: RoleWorker, Squad: "frontend", Files: []string{"web/App.tsx"}},
		{ID: "T3", Role: RoleWorker, Squad: "backend", Files: []string{"internal/db.go"}},
		{ID: "T4", Role: RoleTester, Files: []string{"cmd/main.go"}},
	}
	tally, byTask := RouteBoard(tasks, mixedPolicy())

	want := map[string]string{"T1": "go-worker", "T2": "react-worker", "T3": "go-worker", "T4": RoleTester}
	for _, task := range tasks {
		if task.Role != want[task.ID] {
			t.Errorf("%s = %q, want %q (%s)", task.ID, task.Role, want[task.ID], byTask[task.ID].Reason)
		}
	}
	if !reflect.DeepEqual(tally, map[string]int{"go-worker": 2, "react-worker": 1, RoleTester: 1}) {
		t.Errorf("tally = %v", tally)
	}
	// Deterministic rendering, so the same board reads the same way twice.
	if got := TallyLine(tally); got != "go-worker=2 react-worker=1 tester=1" {
		t.Errorf("TallyLine = %q", got)
	}
	if got := TallyLine(nil); got != "no tasks routed" {
		t.Errorf("TallyLine(nil) = %q", got)
	}
}

func TestRouteBoardReportsWhatItChanged(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"main.go"}},  // worker -> go-worker
		{ID: "T2", Role: "go-worker", Files: []string{"main.go"}}, // already right
	}
	_, byTask := RouteBoard(tasks, mixedPolicy())
	if !byTask["T1"].Changed {
		t.Error("T1 was rerouted and should say so")
	}
	if byTask["T2"].Changed {
		t.Error("T2 kept its role; that is not a change")
	}
}

// The language packs register `<lang>-corrector` too, and a corrector is an
// implementer: it writes the fix. Classifying it as prose is what made the
// explicit-specialist rung unreachable.
func TestCorrectorFamilyIsAnImplementerRole(t *testing.T) {
	for _, role := range []string{"worker", "corrector", "go-worker", "go-corrector", "react-worker", ""} {
		if !isImplementerRole(role) {
			t.Errorf("%q should be an implementer role", role)
		}
	}
	for _, role := range []string{"tester", "reviewer", "explorer", "planner", "go-tester", "go-reviewer"} {
		if isImplementerRole(role) {
			t.Errorf("%q should NOT be an implementer role", role)
		}
	}
}

// A generic corrector on a .tsx file must get the React corrector's prompt, not
// the run-level Go one.
func TestGenericCorrectorIsRoutedByLanguage(t *testing.T) {
	got := RouteTask(Task{ID: "T1", Role: RoleCorrector, Files: []string{"web/src/App.tsx"}}, mixedPolicy())
	if got.Role != "react-worker" {
		t.Fatalf("Role = %q (%s), want react-worker", got.Role, got.Reason)
	}
}

// A team's reviewer and tester have to reach the loop, not just the org chart.
// They are editable on the approval card and on the Teams page, and a seat the
// UI offers and routing ignores is the worst kind of dead control: the user
// sets it, nothing changes, and nothing says why.
func TestSquadReviewerAndTesterReachTheLoop(t *testing.T) {
	registered := map[string]bool{
		"worker": true, "reviewer": true, "tester": true,
		"backend-reviewer": true, "backend-tester": true,
	}
	p := RoutePolicy{
		Available:       func(id string) bool { return registered[id] },
		DefaultWorker:   "worker",
		DefaultReviewer: "reviewer",
		DefaultTester:   "tester",
		SquadReviewer:   func(id string) string { return map[string]string{"backend": "backend-reviewer"}[id] },
		SquadTester:     func(id string) string { return map[string]string{"backend": "backend-tester"}[id] },
	}

	// A file type with no language pack registered: the squad's choice is the
	// next rung down, and it is the one that answers.
	got := RouteTask(Task{ID: "T1", Squad: "backend", Files: []string{"Makefile"}}, p)
	if got.Reviewer != "backend-reviewer" {
		t.Errorf("reviewer = %q, want the team's own", got.Reviewer)
	}
	if got.Tester != "backend-tester" {
		t.Errorf("tester = %q, want the team's own", got.Tester)
	}

	// A task on no team falls through to the run defaults.
	got = RouteTask(Task{ID: "T2", Files: []string{"Makefile"}}, p)
	if got.Reviewer != "reviewer" || got.Tester != "tester" {
		t.Errorf("unassigned task: reviewer=%q tester=%q", got.Reviewer, got.Tester)
	}

	// A team naming an agent the harness cannot dispatch falls through rather
	// than producing a task nothing can review.
	p.SquadReviewer = func(string) string { return "uninstalled-reviewer" }
	got = RouteTask(Task{ID: "T3", Squad: "backend", Files: []string{"Makefile"}}, p)
	if got.Reviewer != "reviewer" {
		t.Errorf("reviewer = %q, want the run default when the team names a ghost", got.Reviewer)
	}
}

// The language of a task's own files still outranks its team, the same way it
// outranks the team's worker: a squad label can be stale about a file, an
// extension cannot.
func TestFileLanguageOutranksTheTeamsReviewer(t *testing.T) {
	registered := map[string]bool{"worker": true, "reviewer": true, "go-reviewer": true, "backend-reviewer": true}
	p := RoutePolicy{
		Available:       func(id string) bool { return registered[id] },
		DefaultReviewer: "reviewer",
		SquadReviewer:   func(string) string { return "backend-reviewer" },
	}
	got := RouteTask(Task{ID: "T1", Squad: "backend", Files: []string{"cmd/main.go"}}, p)
	if got.Reviewer != "go-reviewer" {
		t.Errorf("reviewer = %q, want the language specialist", got.Reviewer)
	}
}
