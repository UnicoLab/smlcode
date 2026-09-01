package orchestrator

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/squads"
)

func gatedOrchestrator(t *testing.T, gates ...TeamGate) *Orchestrator {
	t.Helper()
	o := &Orchestrator{squadPlan: &squads.Plan{Squads: []squads.Squad{
		{ID: "backend-go"}, {ID: "frontend-react"},
	}}}
	for _, g := range gates {
		o.teamGates.set(g)
	}
	return o
}

// The measured case: two tasks done, both halves green by their own commands,
// and the run reported flat FAILURE because a seam tester escalated after a
// local reviewer scored its prose 0. The software was built and each half was
// proved; the user was told it failed.
func TestBothHalvesGreenIsMeasuredEvidence(t *testing.T) {
	o := gatedOrchestrator(t,
		TeamGate{Team: "backend-go", Ran: true, OK: true},
		TeamGate{Team: "frontend-react", Ran: true, OK: true})

	if !o.allHalvesProved() {
		t.Fatal("two green halves were not counted as evidence")
	}
	if names := o.provedHalfNames(); len(names) != 2 || names[0] != "backend-go" {
		t.Errorf("provedHalfNames = %v", names)
	}
}

// An UNVERIFIED gate is the case this must never round up. A command that could
// not run said nothing about that half, and the whole point of separating
// UNVERIFIED from RED is that it is not evidence in either direction —
// counting it here would undo that at the one place it decides the verdict.
func TestAnUnverifiedHalfIsNotEvidence(t *testing.T) {
	for name, gates := range map[string][]TeamGate{
		"one half never ran its check": {
			{Team: "backend-go", Ran: true, OK: true},
			{Team: "frontend-react", Ran: false, Summary: "this project defines no such check"},
		},
		"one half is red": {
			{Team: "backend-go", Ran: true, OK: true},
			{Team: "frontend-react", Ran: true, OK: false},
		},
		"neither ran": {
			{Team: "backend-go", Ran: false},
			{Team: "frontend-react", Ran: false},
		},
	} {
		if gatedOrchestrator(t, gates...).allHalvesProved() {
			t.Errorf("%s: was counted as proof", name)
		}
	}
}

// "Both halves proved" needs both halves to exist and be proved. One team doing
// all the work is not a teams run, and a single green gate must not license
// walking past an escalation.
func TestOneProvedHalfIsNotBothHalves(t *testing.T) {
	o := gatedOrchestrator(t, TeamGate{Team: "backend-go", Ran: true, OK: true})
	if o.allHalvesProved() {
		t.Error("a single green half licensed an escalation walk-past")
	}
}

func TestNoSquadPlanProvesNothing(t *testing.T) {
	o := &Orchestrator{}
	if o.allHalvesProved() {
		t.Error("a run without teams claimed both halves were proved")
	}
	var nilO *Orchestrator
	if nilO.allHalvesProved() {
		t.Error("a nil orchestrator claimed proof")
	}
}
