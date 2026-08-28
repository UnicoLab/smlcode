package squads

import (
	"reflect"
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
