package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// The query this whole feature exists for: a Go backend and a React frontend,
// built at the same time, meeting at a frozen HTTP contract.
const managerOutput = `Here is the org chart.
` + "```json" + `
{"squads":[
  {"id":"backend","owns":["cmd/**","internal/**","go.mod"],"acceptance":"go build ./... && go test ./...","charter":"net/http API serving the SPA","name":"Backend","worker":"go-worker"},
  {"id":"frontend","owns":["web/**"],"acceptance":"npm --prefix web run build","charter":"Vite + React client","name":"Frontend","worker":"react-worker"}],
 "contract":{"interfaces":[
   {"id":"GET /api/todos","provider":"backend","consumers":["frontend"],"spec":"200 -> [{\"id\":string,\"title\":string,\"done\":bool}]"},
   {"id":"POST /api/todos","provider":"backend","consumers":["frontend"],"spec":"{\"title\":string} -> 201 {\"id\":string}"}],
  "summary":"JSON over /api"},
 "integration":{"acceptance":"go build ./... && npm --prefix web run build","notes":["the API serves web/dist at /"]},
 "summary":"Todo app: Go API + React SPA"}
` + "```"

func parsedManagerPlan(t *testing.T) squads.Plan {
	t.Helper()
	p, err := squads.Parse(managerOutput)
	if err != nil {
		t.Fatalf("the manager's own output must parse: %v", err)
	}
	if problems := p.Validate(); problems.Errors() {
		t.Fatalf("the manager's own output must validate:\n%s", strings.Join(problems.Strings(), "\n"))
	}
	return p
}

// End to end over the real package: a model's raw answer becomes a validated
// plan, a contract on disk, and a routed board.
func TestSquadsTurnAManagerAnswerIntoARoutedBoard(t *testing.T) {
	p := parsedManagerPlan(t)

	if got := p.IDs(); len(got) != 2 || got[0] != "backend" || got[1] != "frontend" {
		t.Fatalf("squads = %v", got)
	}

	slmDir := t.TempDir()
	if err := squads.Save(slmDir, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The contract must be on disk BEFORE any worker runs: two squads working
	// concurrently cannot ask each other what the seam is.
	body, err := os.ReadFile(filepath.Join(slmDir, squads.ContractFile))
	if err != nil {
		t.Fatalf("CONTRACT.md must exist: %v", err)
	}
	for _, want := range []string{"FROZEN", "GET /api/todos", "POST /api/todos",
		"Provided by: `backend`", "Consumed by: `frontend`", "npm --prefix web run build"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("CONTRACT.md is missing %q", want)
		}
	}

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "todo store", Files: []string{"internal/store/todo.go"}},
		{ID: "T2", Title: "http routes", Files: []string{"cmd/server/main.go"}},
		{ID: "T3", Title: "todo list view", Files: []string{"web/src/TodoList.tsx"}},
		{ID: "T4", Title: "api client", Files: []string{"web/src/api.ts"}},
		{ID: "T5", Title: "wire the SPA into the binary", Files: []string{"cmd/server/main.go", "web/vite.config.ts"}},
	}}
	rep := squads.AssignBoard(&p, board.Tasks)

	want := map[string]string{"T1": "backend", "T2": "backend", "T3": "frontend", "T4": "frontend", "T5": ""}
	for _, task := range board.Tasks {
		if task.Squad != want[task.ID] {
			t.Errorf("%s routed to %q, want %q", task.ID, task.Squad, want[task.ID])
		}
	}
	// T5 is the seam. Handing it to either team is how one of them acquires
	// permission to rewrite the other's half.
	if len(rep.Straddling) != 1 || rep.Straddling[0] != "T5" {
		t.Errorf("Straddling = %v, want [T5]", rep.Straddling)
	}
	if rep.Assigned["backend"] != 2 || rep.Assigned["frontend"] != 2 {
		t.Errorf("Assigned = %v", rep.Assigned)
	}
}

// The manager's coordination job: notice that one team is blocked on an
// interface the other has not delivered, and hold integration until both halves
// are actually green.
func TestManagerNoticesACrossTeamStallAndGatesIntegration(t *testing.T) {
	p := parsedManagerPlan(t)

	building := []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColInProgress},
		{ID: "T3", Squad: "frontend", Column: plan.ColBlocked},
	}
	stalls := squads.WaitingOn(&p, building)
	if len(stalls) == 0 {
		t.Fatal("a blocked consumer with an unfinished provider is a contract stall")
	}
	if stalls[0].Squad != "frontend" || stalls[0].Provider != "backend" {
		t.Errorf("stall = %+v", stalls[0])
	}
	if gate := squads.ReadyForIntegration(&p, building); gate.Ready {
		t.Error("integration must not run while a half is still being built")
	}

	done := []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone},
		{ID: "T3", Squad: "frontend", Column: plan.ColDone},
	}
	gate := squads.ReadyForIntegration(&p, done)
	if !gate.Ready {
		t.Fatalf("both halves green must open the gate: %s", gate.Reason)
	}
	if !strings.Contains(gate.Command, "npm --prefix web run build") {
		t.Errorf("the gate must run the integration acceptance, got %q", gate.Command)
	}
	if len(squads.WaitingOn(&p, done)) != 0 {
		t.Error("no stalls once everything is delivered")
	}
}
