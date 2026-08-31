package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// twoTeams is the shape the gate exists for: two halves, each with its own
// command, each provable on its own.
func twoTeams() *squads.Plan {
	return &squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm --prefix web run build"},
	}}
}

func boardWithBothHalves() *plan.Board {
	return &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone, Status: "done", Files: []string{"cmd/main.go"}},
		{ID: "T2", Squad: "frontend", Column: plan.ColDone, Status: "done", Files: []string{"web/App.tsx"}},
	}}
}

// gateOrchestrator wires a fake smoke runner so the gate's decisions can be
// exercised without a shell.
func gateOrchestrator(t *testing.T, smoke func(cmd string) quality.SmokeResult) (*Orchestrator, *recorder) {
	t.Helper()
	o, rec := routingOrchestrator(t, twoTeams())
	o.qaSmoke = func(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
		return smoke(cmd)
	}
	return o, rec
}

// The whole point of the gate: green means the half was PROVED, by running the
// command the team says proves it.
func TestEachHalfIsProvedByItsOwnCommand(t *testing.T) {
	var ran []string
	o, rec := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		ran = append(ran, cmd)
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})

	failed := o.runTeamAcceptance(context.Background(), boardWithBothHalves())

	if len(failed) != 0 {
		t.Fatalf("both halves passed, got failures %v", failed)
	}
	if len(ran) != 2 || ran[0] != "go test ./..." || ran[1] != "npm --prefix web run build" {
		t.Fatalf("commands run = %v — each team's own, in plan order", ran)
	}
	gates := o.TeamGates()
	if len(gates) != 2 {
		t.Fatalf("gates = %+v", gates)
	}
	for _, g := range gates {
		if !g.Ran || !g.OK {
			t.Errorf("%s = %+v, want proved green", g.Team, g)
		}
	}
	if !strings.Contains(rec.text(), "is green") {
		t.Error("a proved half must say so — per-team green is what a watcher is looking for")
	}
}

// The failure this whole gate could have introduced: `npm run build` where
// node_modules was never installed exits non-zero and says NOTHING about the
// code. Scoring that red sends a corrector to rewrite source that was never at
// fault, burns the retry budget, and shows a red team for something no model
// can fix — the "fail, fail, fail" a local run must not produce.
func TestAbsentToolingLeavesAHalfUnverifiedRatherThanRed(t *testing.T) {
	o, rec := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		if strings.HasPrefix(cmd, "npm") {
			return quality.SmokeResult{
				Ran: true, OK: false, Command: cmd,
				Summary: "FAILED: exit status 254",
				Output:  "npm error code ENOENT\nnpm error command not found: npm",
			}
		}
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})

	failed := o.runTeamAcceptance(context.Background(), boardWithBothHalves())

	if len(failed) != 0 {
		t.Fatalf("a missing toolchain must not fail a team, got %v", failed)
	}
	var frontend TeamGate
	for _, g := range o.TeamGates() {
		if g.Team == "frontend" {
			frontend = g
		}
	}
	if frontend.Ran {
		t.Fatalf("frontend = %+v, want UNVERIFIED", frontend)
	}
	joined := rec.text()
	if !strings.Contains(joined, "UNVERIFIED") {
		t.Errorf("the reason must be stated, not implied: %s", joined)
	}
	// And no ticket: there is nothing for a worker to fix.
	board := boardWithBothHalves()
	o.runTeamAcceptance(context.Background(), board)
	if len(board.Tasks) != 2 {
		t.Errorf("a ticket was raised for absent tooling: %d tasks", len(board.Tasks))
	}
}

// A half that genuinely does not build is red, owned by the team that owns the
// files, and integration is not attempted — joining a known-broken half proves
// nothing and reports the seam as the fault.
func TestARedHalfRaisesATicketInItsOwnLane(t *testing.T) {
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		if strings.HasPrefix(cmd, "go test") {
			return quality.SmokeResult{
				Ran: true, OK: false, Command: cmd,
				Summary: "FAILED: cmd/main.go:7: undefined: json.NewEncoder",
				Output:  "cmd/main.go:7: undefined: json.NewEncoder\nweb/App.tsx:1: unrelated",
			}
		}
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Summary: "ok"}
	})
	board := boardWithBothHalves()

	failed := o.runTeamAcceptance(context.Background(), board)

	if len(failed) != 1 || failed[0] != "backend" {
		t.Fatalf("failed = %v, want just backend", failed)
	}
	var ticket *plan.Task
	for i := range board.Tasks {
		if board.Tasks[i].ID != "T1" && board.Tasks[i].ID != "T2" {
			ticket = &board.Tasks[i]
		}
	}
	if ticket == nil {
		t.Fatal("a red half must raise a ticket somebody owns")
	}
	if ticket.Squad != "backend" {
		t.Errorf("ticket squad = %q — it must land in the lane that owns the files", ticket.Squad)
	}
	// The output named a frontend file too. A ticket carrying another team's
	// paths is one the wave's deny list refuses on exactly the files it was
	// told to fix.
	for _, f := range ticket.Files {
		if strings.HasPrefix(f, "web/") {
			t.Errorf("ticket names %s, which the frontend owns", f)
		}
	}
}

// A team with no work did nothing to prove, and running its command would
// report on the state of the repository rather than on this run.
func TestATeamWithNoWorkIsNotGated(t *testing.T) {
	calls := 0
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		calls++
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd}
	})
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone, Status: "done", Files: []string{"cmd/main.go"}},
	}}

	o.runTeamAcceptance(context.Background(), board)

	if calls != 1 {
		t.Fatalf("ran %d commands — only the team with work is gated", calls)
	}
}

// A team with no acceptance command is unproved and says so once. It is a
// warning at plan time; repeating it as a gate FAILURE would punish a plan the
// harness already accepted and run.
func TestATeamWithNoCommandIsUnprovedNotFailed(t *testing.T) {
	p := twoTeams()
	p.Squads[1].Acceptance = ""
	o, rec := routingOrchestrator(t, p)
	o.qaSmoke = func(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd}
	}

	failed := o.runTeamAcceptance(context.Background(), boardWithBothHalves())

	if len(failed) != 0 {
		t.Fatalf("no command is not a failure, got %v", failed)
	}
	if !strings.Contains(rec.text(), "no acceptance command") {
		t.Error("an unproved half must say so")
	}
}

// A single-stream run has no halves to prove, and a gate that ran anyway would
// re-run the project's tests for no reason.
func TestNoGateWithoutTwoTeams(t *testing.T) {
	calls := 0
	o, _ := gateOrchestrator(t, func(cmd string) quality.SmokeResult {
		calls++
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd}
	})
	o.squadPlan = &squads.Plan{Squads: []squads.Squad{{ID: "solo", Owns: []string{"**"}, Acceptance: "make test"}}}

	if got := o.runTeamAcceptance(context.Background(), boardWithBothHalves()); got != nil {
		t.Fatalf("failed = %v", got)
	}
	if calls != 0 {
		t.Fatalf("ran %d commands on a single-stream run", calls)
	}
}
