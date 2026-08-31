package teams

import (
	"reflect"
	"strings"
	"testing"
)

// roster is the shape a real library has: two disjoint halves plus a third team
// whose territory collides with one of them, which is the case the selector has
// to resolve rather than hand downstream.
func roster() []Team {
	return []Team{
		{
			ID: "backend-go", Owns: []string{"cmd/**", "internal/**", "go.mod"},
			Acceptance: "go test ./...", Worker: "go-worker",
			Match: Match{Keywords: []string{"backend", "api", "go"}, Files: []string{"go.mod"}, Extensions: []string{".go"}},
		},
		{
			ID: "frontend-react", Owns: []string{"web/**"},
			Acceptance: "npm --prefix web run build", Worker: "react-worker",
			Match: Match{Keywords: []string{"frontend", "react", "ui"}, Files: []string{"web/package.json"}, Extensions: []string{".tsx"}},
		},
		{
			ID: "backend-node", Owns: []string{"cmd/**"},
			Match: Match{Keywords: []string{"node", "express"}},
		},
		{
			ID: "docs", Owns: []string{"docs/**"},
			Match: Match{Keywords: []string{"docs"}, Priority: -1},
		},
	}
}

func ids(sel Selection) []string { return sel.IDs() }

func evidenceFor(sel Selection, id string) (Evidence, bool) {
	for _, ev := range sel.Evidence {
		if ev.TeamID == id {
			return ev, true
		}
	}
	return Evidence{}, false
}

// The case the whole feature exists for: a query that names both halves, in a
// repository that contains both, with no model in the loop.
func TestSelectPicksBothHalvesOfAFullStackRequest(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "Add a todo list: a Go API and a React frontend that consumes it",
		Files: []string{"go.mod", "cmd/server/main.go", "web/package.json", "web/src/App.tsx"},
	}, Options{})

	if !sel.Enabled() {
		t.Fatalf("both halves must be selected, got %v", ids(sel))
	}
	if got := ids(sel); !reflect.DeepEqual(got, []string{"backend-go", "frontend-react"}) {
		t.Fatalf("selected=%v — highest evidence first, ties on id", got)
	}
	ev, ok := evidenceFor(sel, "backend-go")
	if !ok || len(ev.Reasons) == 0 {
		t.Fatal("a selection with no stated reason cannot be argued with")
	}
	joined := strings.Join(ev.Reasons, " | ")
	for _, want := range []string{"go.mod", ".go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons must name the evidence, got %q", joined)
		}
	}
}

// One team is the single-stream pipeline wearing a hat. Selecting it and paying
// the contract + integration overhead buys nothing.
func TestSelectRefusesToCallOneTeamAPlan(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "tidy up the Go handlers",
		Files: []string{"go.mod", "cmd/server/main.go"},
	}, Options{})

	if sel.Enabled() {
		t.Fatalf("one matching domain is not a team structure, got %v", ids(sel))
	}
	if len(sel.Teams) != 1 || sel.Teams[0].ID != "backend-go" {
		t.Fatalf("the one match must still be reported: %v", ids(sel))
	}
}

// Overlap is the safety property. Two teams claiming one path means two agents
// writing one file in parallel and one edit silently gone — so the weaker claim
// is dropped HERE, where the run stays parallel, rather than by Validate, which
// could only reject the whole plan and fall back to one stream.
func TestSelectDropsTheWeakerClaimOnOverlappingTerritory(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "build the express node api and the react ui",
		Files: []string{"go.mod", "cmd/server/main.go", "web/package.json"},
	}, Options{})

	for _, id := range ids(sel) {
		if id == "backend-node" {
			t.Fatalf("backend-node claims cmd/** which backend-go already took: %v", ids(sel))
		}
	}
	ev, ok := evidenceFor(sel, "backend-node")
	if !ok {
		t.Fatal("a team that scored and lost must still appear in the evidence")
	}
	if ev.Selected {
		t.Fatal("it must not be marked selected")
	}
	if ev.Conflict != "backend-go" {
		t.Fatalf("conflict=%q — the user cannot fix an overlap they cannot see", ev.Conflict)
	}
	if !strings.Contains(strings.Join(ev.Reasons, " "), "cmd/**") {
		t.Fatalf("the reason must name the contested glob: %v", ev.Reasons)
	}
}

// "api" must not fire on "rapids". A preselection that triggers on noise is
// worse than none: the user now has to notice it and undo it.
func TestKeywordsMatchOnWordBoundaries(t *testing.T) {
	lib := []Team{
		{ID: "backend-go", Owns: []string{"cmd/**"}, Match: Match{Keywords: []string{"api", "go"}}},
		{ID: "frontend-react", Owns: []string{"web/**"}, Match: Match{Keywords: []string{"ui"}}},
	}
	sel := Select(lib, Signals{Query: "handle rapids and cargo in the guide"}, Options{})
	if len(sel.Teams) != 0 {
		t.Fatalf("substring noise selected %v", ids(sel))
	}

	sel = Select(lib, Signals{Query: "the API and the UI"}, Options{})
	if !sel.Enabled() {
		t.Fatalf("real word hits must select, got %v", ids(sel))
	}
}

// An explicit choice is an instruction, not a hypothesis: it is selected on no
// evidence at all, and it wins the contested paths against anything scored.
func TestPinnedTeamsAreSelectedWithoutEvidenceAndWinConflicts(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "add a route",
		Files: []string{"go.mod", "cmd/server/main.go"},
	}, Options{Pinned: []string{"backend-node", "docs"}})

	got := ids(sel)
	if len(got) < 2 || got[0] != "backend-node" || got[1] != "docs" {
		t.Fatalf("pinned teams must come first, in the order given: %v", got)
	}
	for _, id := range got {
		if id == "backend-go" {
			t.Fatalf("backend-go claims cmd/**, which pinned backend-node already took: %v", got)
		}
	}
	ev, _ := evidenceFor(sel, "backend-node")
	if !ev.Pinned || !ev.Selected {
		t.Fatalf("pinned evidence=%+v", ev)
	}
}

// A negative priority is the escape hatch for a team only its author can place:
// never automatic, always available by hand.
func TestNegativePriorityOptsOutOfAutomaticSelectionOnly(t *testing.T) {
	sel := Select(roster(), Signals{Query: "update the docs and the go api", Files: []string{"go.mod"}}, Options{})
	for _, id := range ids(sel) {
		if id == "docs" {
			t.Fatal("a negative-priority team must never be auto-selected")
		}
	}
	if _, ok := evidenceFor(sel, "docs"); ok {
		t.Fatal("it must not even be scored — a score implies it was in the running")
	}

	sel = Select(roster(), Signals{Query: "update the docs"}, Options{Pinned: []string{"docs", "frontend-react"}})
	if !reflect.DeepEqual(ids(sel), []string{"docs", "frontend-react"}) {
		t.Fatalf("pinning must still work: %v", ids(sel))
	}
}

// Past a handful, teams stop being parallel halves and become a partition
// nobody asked for — and every extra team runs its acceptance command on every
// wave.
func TestSelectHonoursTheTeamCap(t *testing.T) {
	lib := []Team{
		{ID: "a", Owns: []string{"a/**"}, Match: Match{Keywords: []string{"all"}}},
		{ID: "b", Owns: []string{"b/**"}, Match: Match{Keywords: []string{"all"}}},
		{ID: "c", Owns: []string{"c/**"}, Match: Match{Keywords: []string{"all"}}},
	}
	sel := Select(lib, Signals{Query: "all of it"}, Options{Max: 2})
	if len(sel.Teams) != 2 {
		t.Fatalf("cap ignored: %v", ids(sel))
	}
	ev, ok := evidenceFor(sel, "c")
	if !ok || ev.Selected {
		t.Fatalf("the team past the cap must be reported unselected: %+v", ev)
	}
}

// Preselection feeds ownership, routing and the write deny list. Two runs of
// one query picking different teams would make every one of those unreproducible.
func TestSelectIsDeterministicOnTies(t *testing.T) {
	lib := []Team{
		{ID: "zeta", Owns: []string{"z/**"}, Match: Match{Keywords: []string{"both"}}},
		{ID: "alpha", Owns: []string{"a/**"}, Match: Match{Keywords: []string{"both"}}},
	}
	want := []string{"alpha", "zeta"}
	for i := 0; i < 20; i++ {
		if got := ids(Select(lib, Signals{Query: "both"}, Options{})); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: %v", i, got)
		}
	}
}

// A bare marker must be found wherever it lives — a monorepo's
// services/api/go.mod is still evidence of a Go project, and demanding
// `**/go.mod` from every author is a rule they forget once and debug for an hour.
func TestMarkerFilesMatchNestedPaths(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "work on it",
		Files: []string{"services/api/go.mod", "services/api/main.go", "web/package.json", "web/src/a.tsx"},
	}, Options{})
	if !sel.Enabled() {
		t.Fatalf("nested markers must count: %v", ids(sel))
	}
}

// Unknown ids are ignored rather than fatal: a pipeline preset outlives the
// library it was written against, and a run that refuses to start over a stale
// team id is a worse answer than one that runs with one fewer team.
func TestPinningAnUnknownTeamIsNotFatal(t *testing.T) {
	sel := Select(roster(), Signals{
		Query: "a go api and a react frontend",
		Files: []string{"go.mod", "web/package.json"},
	}, Options{Pinned: []string{"team-that-was-deleted"}})

	if !sel.Enabled() {
		t.Fatalf("the rest of the selection must survive: %v", ids(sel))
	}
	if _, ok := evidenceFor(sel, "team-that-was-deleted"); ok {
		t.Fatal("an id that resolves to nothing must not appear as a scored team")
	}
}

func TestSelectOnAnEmptyLibraryIsQuiet(t *testing.T) {
	sel := Select(nil, Signals{Query: "anything"}, Options{Pinned: []string{"backend-go"}})
	if len(sel.Teams) != 0 || len(sel.Evidence) != 0 || sel.Enabled() {
		t.Fatalf("empty library must select nothing: %+v", sel)
	}
}

// A team with no Match at all is authored, not detected: it is never
// auto-selected, but pinning it must still work.
func TestTeamWithNoMatchIsManualOnly(t *testing.T) {
	lib := []Team{
		{ID: "manual", Owns: []string{"m/**"}},
		{ID: "auto", Owns: []string{"a/**"}, Match: Match{Keywords: []string{"thing"}}},
	}
	sel := Select(lib, Signals{Query: "the thing"}, Options{})
	if !reflect.DeepEqual(ids(sel), []string{"auto"}) {
		t.Fatalf("selected=%v", ids(sel))
	}
	sel = Select(lib, Signals{Query: "the thing"}, Options{Pinned: []string{"manual"}})
	if !reflect.DeepEqual(ids(sel), []string{"manual", "auto"}) {
		t.Fatalf("selected=%v", ids(sel))
	}
}

// Two entries under one id can reach a roster — a user directory and a project
// one both holding a stale copy. Scoring it twice makes the second report a
// territory conflict with itself, which is a message nobody could act on.
func TestSelectScoresEachTeamOnce(t *testing.T) {
	dup := Team{ID: "backend-go", Owns: []string{"cmd/**"}, Match: Match{Keywords: []string{"api"}}}
	sel := Select([]Team{dup, dup, {ID: "frontend-react", Owns: []string{"web/**"},
		Match: Match{Keywords: []string{"ui"}}}}, Signals{Query: "the api and the ui"}, Options{})

	if got := ids(sel); !reflect.DeepEqual(got, []string{"backend-go", "frontend-react"}) {
		t.Fatalf("selected = %v", got)
	}
	if len(sel.Evidence) != 2 {
		t.Fatalf("evidence = %+v — one row per team, not per roster entry", sel.Evidence)
	}
	for _, ev := range sel.Evidence {
		if ev.Conflict == ev.TeamID {
			t.Fatalf("%s reported a conflict with itself", ev.TeamID)
		}
	}
}
