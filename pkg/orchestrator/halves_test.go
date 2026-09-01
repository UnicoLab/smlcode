package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// selectiveGate answers each command on its own merits, which is the only way
// to build the shape under test: both TEAM commands green while the run as a
// whole fails.
type selectiveGate struct{ green map[string]bool }

func (g *selectiveGate) run(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
	ok := g.green[cmd]
	sr := quality.SmokeResult{Ran: true, Command: cmd, OK: ok}
	if ok {
		sr.Summary = quality.SmokePassedMarker + ": " + cmd
	} else {
		sr.Summary = quality.SmokeFailedMarker
	}
	return sr
}

func halvesPlan() *squads.Plan {
	return &squads.Plan{Squads: []squads.Squad{
		{ID: "backend-go", Owns: []string{"cmd/**"}, Acceptance: "go build ./..."},
		{ID: "frontend-react", Owns: []string{"web/**"}, Acceptance: "npm run build"},
	}}
}

func gatedPlanOrchestrator(t *testing.T, gates ...TeamGate) *Orchestrator {
	t.Helper()
	o := &Orchestrator{squadPlan: halvesPlan()}
	for _, g := range gates {
		o.teamGates.set(g)
	}
	return o
}

// Two halves, each proved by running its own acceptance command, is measured
// evidence: an exit status on the real tree rather than a model's opinion of a
// write-up.
func TestBothHalvesGreenIsMeasuredEvidence(t *testing.T) {
	o := gatedPlanOrchestrator(t,
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
// not run said NOTHING about that half — that is the entire reason UNVERIFIED
// is separate from RED — so it is not evidence in either direction.
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
		"only one half reported at all": {
			{Team: "backend-go", Ran: true, OK: true},
		},
	} {
		if gatedPlanOrchestrator(t, gates...).allHalvesProved() {
			t.Errorf("%s: was counted as proof of both halves", name)
		}
	}
}

func TestNoSquadPlanProvesNothing(t *testing.T) {
	if (&Orchestrator{}).allHalvesProved() {
		t.Error("a run without teams claimed both halves were proved")
	}
	var nilO *Orchestrator
	if nilO.allHalvesProved() {
		t.Error("a nil orchestrator claimed proof")
	}
}

// escalatedTeamsBoard is the measured shape: both teams' implementation tasks
// done, and a seam TESTER escalated to the human backlog.
func escalatedTeamsBoard() *plan.Board {
	b := doneBoard()
	b.Tasks[0].Squad = "backend-go"
	b.Tasks = append(b.Tasks,
		plan.Task{ID: "T2", Title: "render the todos", Column: plan.ColDone,
			Role: plan.RoleWorker, Squad: "frontend-react"},
		plan.Task{ID: "T3", Title: "verify the halves meet", Column: plan.ColToScope,
			Role: plan.RoleTester, Error: "review rejected after max retries"})
	return b
}

// A failing teams run must say which halves were nonetheless PROVED.
//
// The verdict is untouched — the run failed and the exit code stands. What a
// person needs is the SHAPE of the failure: "everything is broken" and "both
// halves compile and pass their own tests, and what failed is the join between
// them" call for completely different next moves, and the summary said only the
// first. Measured live: `team backend-go is green`, `team frontend-react is
// green`, reported as a flat failure indistinguishable from a run where nothing
// worked at all.
func TestAFailingRunSaysWhichHalvesWereProved(t *testing.T) {
	// Both halves' own commands pass; everything else — including the QA
	// objective — does not. That is the measured shape.
	gate := &selectiveGate{green: map[string]bool{
		"go build ./...": true, "npm run build": true,
	}}
	o := objectiveOrch(t, gate, &countingExec{},
		func(c *config.Config) { c.QAGate = true })
	sink := newMsgSink(o)
	o.squadPlan = halvesPlan()

	res := finalize(t, o, escalatedTeamsBoard())

	if res.Success {
		t.Fatal("the verdict changed — this must only ADD information to a failure")
	}
	joined := sink.text()
	if !strings.Contains(joined, "backend-go") || !strings.Contains(joined, "frontend-react") {
		t.Errorf("a failing run did not name the halves that proved themselves:\n%s", joined)
	}
	if !strings.Contains(joined, "BETWEEN them") {
		t.Errorf("the shape of the failure was not stated:\n%s", joined)
	}
}

// Nothing claimed when nothing proved itself.
func TestNoHalvesProvedSaysNothing(t *testing.T) {
	// Nothing green anywhere, so no half can claim to have proved itself.
	o := objectiveOrch(t, &selectiveGate{green: map[string]bool{}}, &countingExec{}, nil)
	sink := newMsgSink(o)
	o.squadPlan = halvesPlan()

	finalize(t, o, escalatedTeamsBoard())

	if joined := sink.text(); strings.Contains(joined, "proved themselves") {
		t.Errorf("claimed a half proved itself when none did:\n%s", joined)
	}
}

// msgSink collects emitted messages. The orchestrator fires events from several
// goroutines, so this needs a lock — without one the race detector fails the
// test rather than the code.
type msgSink struct {
	mu   sync.Mutex
	msgs []string
}

func newMsgSink(o *Orchestrator) *msgSink {
	s := &msgSink{}
	o.onEvent = func(e Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.msgs = append(s.msgs, e.Message)
	}
	return s
}

func (s *msgSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.msgs, "\n")
}
