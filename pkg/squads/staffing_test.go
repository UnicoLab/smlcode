package squads

import (
	"reflect"
	"strings"
	"testing"
)

func staffedPlan() *Plan {
	p := &Plan{
		Summary: "Go API + React SPA",
		Squads: []Squad{
			{
				ID: "backend", Name: "Backend", Owns: []string{"cmd/**", "internal/**"},
				Acceptance: "go test ./...", Worker: "go-worker", Reviewer: "go-reviewer",
				Manager: "backend-pm",
			},
			{
				ID: "frontend", Name: "Frontend", Owns: []string{"web/**"},
				Acceptance: "npm run build", Worker: "react-worker",
			},
		},
	}
	p.Normalize()
	return p
}

func TestStaffingNamesTheTeamsOwnPeople(t *testing.T) {
	got := StaffingFor(staffedPlan(), "backend")
	if got.Squad != "backend" || got.Manager != "backend-pm" {
		t.Fatalf("StaffingFor = %+v", got)
	}
	// Most specific first: the worker before the reviewer.
	if want := []string{"go-worker", "go-reviewer"}; !reflect.DeepEqual(got.Members, want) {
		t.Errorf("Members = %v, want %v", got.Members, want)
	}
}

// Empty is the answer for every single-stream run, which is most of them. A
// zero Staffing means "the run's defaults decide", and the caller must be able
// to ask without checking first.
func TestStaffingIsEmptyWhenNobodyAnswersForTheTask(t *testing.T) {
	p := staffedPlan()
	for _, id := range []string{"", "nope", "  "} {
		if got := StaffingFor(p, id); got.Squad != "" || got.Manager != "" || len(got.Members) > 0 {
			t.Errorf("StaffingFor(%q) = %+v, want the zero value", id, got)
		}
	}
	if got := StaffingFor(nil, "backend"); got.Squad != "" {
		t.Errorf("StaffingFor(nil) = %+v, want the zero value", got)
	}
}

func TestATeamWithoutAManagerFallsBackToTheRunDefault(t *testing.T) {
	if got := StaffingFor(staffedPlan(), "frontend"); got.Manager != "" {
		t.Errorf("Manager = %q, want empty so the run's default answers", got.Manager)
	}
}

// The headline: a manager triaging the backend's failing Go work sees the
// backend's people first, instead of a flat roster in which the React half's
// worker is as prominent as its own.
func TestColleaguesPutTheTasksOwnTeamFirst(t *testing.T) {
	roster := []string{"corrector", "go-reviewer", "react-worker", "worker", "go-worker"}
	got := Colleagues(staffedPlan(), "backend", roster)
	if want := []string{"go-worker", "go-reviewer", "corrector", "react-worker", "worker"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Colleagues = %v, want %v", got, want)
	}
}

// An ordering, not a filter. The whole reason a delivery was rejected may be
// that this team lacks the skill the fix needs; a manager forbidden from
// looking outside could only pick between agents that already failed.
func TestColleaguesNeverDropsAnybody(t *testing.T) {
	roster := []string{"corrector", "react-worker", "worker", "go-worker", "go-reviewer"}
	got := Colleagues(staffedPlan(), "backend", roster)
	if len(got) != len(roster) {
		t.Fatalf("Colleagues returned %d of %d agents: %v", len(got), len(roster), got)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range roster {
		if seen[id] != 1 {
			t.Errorf("%q appears %d times, want exactly once", id, seen[id])
		}
	}
}

func TestColleaguesLeavesAnUnstaffedRosterAlone(t *testing.T) {
	roster := []string{"worker", "corrector"}
	for _, squad := range []string{"", "frontend-typo"} {
		if got := Colleagues(staffedPlan(), squad, roster); !reflect.DeepEqual(got, roster) {
			t.Errorf("Colleagues(%q) = %v, want the roster unchanged", squad, got)
		}
	}
	if got := Colleagues(staffedPlan(), "backend", nil); got != nil {
		t.Errorf("Colleagues(nil roster) = %v, want nil", got)
	}
}

// A squad's own staff may not be dispatchable at all — the roster is built from
// what the factory can create, and a plan can name an agent that no longer
// exists. Ordering must not resurrect it.
func TestColleaguesOnlyOrdersAgentsTheRosterAlreadyHad(t *testing.T) {
	got := Colleagues(staffedPlan(), "backend", []string{"worker", "corrector"})
	for _, id := range got {
		if id == "go-worker" || id == "go-reviewer" {
			t.Errorf("Colleagues invented %q, which the caller did not offer", id)
		}
	}
}

// ── Whose territory a defect is in ───────────────────────────────────────

func TestLaneNamesTheTeamThatOwnsTheWholeDefect(t *testing.T) {
	p := staffedPlan()
	if got := LaneOf(p, []string{"cmd/server/main.go", "internal/store/todo.go"}); got != "backend" {
		t.Errorf("LaneOf = %q, want backend", got)
	}
	if got := LaneOf(p, []string{"web/src/App.tsx"}); got != "frontend" {
		t.Errorf("LaneOf = %q, want frontend", got)
	}
}

// A defect on the seam belongs to both halves, and one that lands nowhere
// belongs to whoever the board says. Either way the lane cannot narrow it, and
// saying so is what keeps the check from filtering work it has no business
// filtering.
func TestLaneIsEmptyWhenItCannotNarrowTheDefect(t *testing.T) {
	p := staffedPlan()
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"straddles both teams", []string{"cmd/server/main.go", "web/src/App.tsx"}},
		{"nobody owns it", []string{"README.md"}},
		{"one owned, one not", []string{"cmd/server/main.go", "Makefile"}},
		{"nothing named", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LaneOf(p, tc.paths); got != "" {
				t.Errorf("LaneOf = %q, want empty", got)
			}
		})
	}
	if got := LaneOf(nil, []string{"cmd/server/main.go"}); got != "" {
		t.Errorf("LaneOf(nil plan) = %q, want empty", got)
	}
}

// ── Who owes a broken seam ───────────────────────────────────────────────
//
// An integration failure is the defect the whole design exists to catch: every
// squad's own acceptance green and the assembled application still broken. It
// is also where a generic "the gate is red" ticket is least useful — nobody
// owns it, no specialist is implied, and whoever picks it up has to rediscover
// which half is lying about its own interface.

func contractPlan(ifaces ...Interface) *Plan {
	p := staffedPlan()
	p.Contract.Interfaces = ifaces
	p.Normalize()
	return p
}

// The provider owes the clause: a consumer built against text it was handed.
func TestTheProviderOwesTheSeam(t *testing.T) {
	p := contractPlan(Interface{ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"}})
	squad, clauses := SeamOwner(p, "expected [] got {items:[]}")
	if squad != "backend" {
		t.Errorf("squad = %q, want backend", squad)
	}
	if len(clauses) != 1 || clauses[0] != "GET /api/todos" {
		t.Errorf("clauses = %v, want the interface at stake", clauses)
	}
}

// With several providers the failure text decides, by naming a team or a path
// inside its lane.
func TestSeveralProvidersAreBrokenByWhatTheOutputNames(t *testing.T) {
	p := contractPlan(
		Interface{ID: "GET /api/todos", Provider: "backend"},
		Interface{ID: "window.mountApp", Provider: "frontend"},
	)
	if got, _ := SeamOwner(p, "web/src/App.tsx:12: mountApp is not a function"); got != "frontend" {
		t.Errorf("squad = %q, want the team whose lane the output names", got)
	}
	if got, _ := SeamOwner(p, "backend handler returned 500"); got != "backend" {
		t.Errorf("squad = %q, want the team the output names outright", got)
	}
}

// A guess is worse than nothing: an unassigned ticket with real evidence beats
// one parked on the wrong team.
func TestAnUnattributableSeamStaysUnassigned(t *testing.T) {
	p := contractPlan(
		Interface{ID: "GET /api/todos", Provider: "backend"},
		Interface{ID: "window.mountApp", Provider: "frontend"},
	)
	if got, clauses := SeamOwner(p, "exit status 1"); got != "" || clauses != nil {
		t.Errorf("SeamOwner = %q/%v, want no guess", got, clauses)
	}
	if got, _ := SeamOwner(nil, "anything"); got != "" {
		t.Errorf("SeamOwner(nil) = %q", got)
	}
	if got, _ := SeamOwner(staffedPlan(), "anything"); got != "" {
		t.Errorf("a plan with no contract owes nothing: %q", got)
	}
}

func TestPathsInReadsCompilerAndBundlerOutput(t *testing.T) {
	out := `web/src/App.tsx:12:5 - error TS2339
cmd/server/main.go:41: undefined: json.NewEncoder
    at /abs/path/node_modules/x.js:1
see https://example.com/docs/a.html
ok  	github.com/x/y	0.1s`
	got := PathsIn(out)
	want := map[string]bool{"web/src/App.tsx": true, "cmd/server/main.go": true}
	for _, g := range got {
		delete(want, g)
		// Absolute paths and URLs are not repo-relative files.
		if strings.HasPrefix(g, "/") || strings.Contains(g, "://") {
			t.Errorf("PathsIn returned %q, which is not a repo path", g)
		}
	}
	if len(want) > 0 {
		t.Errorf("PathsIn = %v, missing %v", got, want)
	}
}

func TestPathsInIsQuietOnOutputWithNoPaths(t *testing.T) {
	for _, in := range []string{"", "exit status 1", "FAIL", "make: *** [all] Error 2"} {
		if got := PathsIn(in); len(got) != 0 {
			t.Errorf("PathsIn(%q) = %v, want nothing", in, got)
		}
	}
}
