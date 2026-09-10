package composer

import (
	"strings"
	"testing"
)

// A team is written once and a pipeline is chosen per run: the pipeline has a
// test phase and the team names no tester. The seat is filled from the
// pipeline, and the gap is said in words.
func TestFillSeatsBorrowsWhatTheTeamLeftEmpty(t *testing.T) {
	d := SeatDefaults{Worker: "worker", Reviewer: "reviewer", Tester: "go-tester"}
	seats := FillSeats("go-worker", "", "", "backend-triage", false, d)
	want := []SeatFill{
		{Role: "worker", Agent: "go-worker", Source: SeatFromTeam},
		{Role: "reviewer", Agent: "reviewer", Source: SeatFromPipeline},
		{Role: "tester", Agent: "go-tester", Source: SeatFromPipeline},
		{Role: "manager", Agent: "backend-triage", Source: SeatFromTeam},
	}
	if len(seats) != len(want) {
		t.Fatalf("seats=%+v", seats)
	}
	for i := range want {
		if seats[i] != want[i] {
			t.Fatalf("seat %d = %+v, want %+v", i, seats[i], want[i])
		}
	}
	gaps := Gaps("backend-go", seats)
	if len(gaps) != 2 || !strings.Contains(gaps[1], "names no tester") || !strings.Contains(gaps[1], "go-tester takes its tester seat") {
		t.Fatalf("gaps=%v", gaps)
	}

	// No pipeline pick either: the harness default, reported as such.
	seats = FillSeats("", "", "", "", true, SeatDefaults{})
	if seats[2] != (SeatFill{Role: "tester", Agent: "tester", Source: SeatFromDefault}) {
		t.Fatalf("default tester=%+v", seats[2])
	}
	if seats[3] != (SeatFill{Role: "manager", Agent: "triage", Source: SeatFromDefault}) {
		t.Fatalf("default manager=%+v", seats[3])
	}
	if got := Gaps("x", seats); len(got) != 3 || !strings.Contains(got[0], "the default worker") {
		t.Fatalf("gaps=%v", got)
	}
}

func TestSeatDefaultsComeFromTheCompositionsBoundRoles(t *testing.T) {
	c := Composition{
		Execute: ExecuteChoice{DefaultRole: "react-worker", Reviewer: "react-reviewer"},
		Phases:  []PhaseChoice{{ID: "execute", Agent: "react-worker", Enabled: true}, {ID: "test", Agent: "react-tester", Enabled: true}},
	}
	if d := c.SeatDefaultsOf(); d != (SeatDefaults{Worker: "react-worker", Reviewer: "react-reviewer", Tester: "react-tester"}) {
		t.Fatalf("defaults=%+v", d)
	}
	c.Phases[1].Enabled = false
	if c.TesterAgent() != "" {
		t.Fatalf("a disabled test phase lends no tester")
	}
}
