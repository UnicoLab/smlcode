package orchestrator

import (
	"context"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// recorder captures the event stream so a test can assert on what a watching
// human would actually see.
type recorder struct {
	mu     sync.Mutex
	events []stream.Event
}

func (r *recorder) handle(e stream.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, e := range r.events {
		b.WriteString(e.Phase + " " + e.Message + "\n")
	}
	return b.String()
}

func integrationOrchestrator(t *testing.T, p *squads.Plan, smokeOK bool) (*Orchestrator, *recorder, *[]string) {
	t.Helper()
	rec := &recorder{}
	var ran []string
	o := &Orchestrator{
		cfg:       &config.Config{Root: t.TempDir()},
		squadPlan: p,
		onEvent:   rec.handle,
		qaSmoke: func(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
			ran = append(ran, cmd)
			return quality.SmokeResult{Ran: true, OK: smokeOK, Command: cmd,
				Summary: "integration " + map[bool]string{true: "passed", false: "failed"}[smokeOK]}
		},
	}
	return o, rec, &ran
}

func e2ePlan(t *testing.T) *squads.Plan {
	t.Helper()
	p := squads.Plan{
		Squads: []squads.Squad{
			{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
			{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm --prefix web run build"},
		},
		Contract: squads.Contract{Interfaces: []squads.Interface{
			{ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"}, Spec: "200 -> []"},
		}},
		Integration: squads.Integration{Acceptance: "go test ./... && npm --prefix web run build"},
	}
	p.Normalize()
	if probs := p.Validate(); probs.Errors() {
		t.Fatalf("fixture plan must be valid:\n%s", strings.Join(probs.Strings(), "\n"))
	}
	return &p
}

func bothGreen() *plan.Board {
	return &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone},
		{ID: "T2", Squad: "frontend", Column: plan.ColDone},
	}}
}

func TestIntegrationRunsOnceBothHalvesAreGreen(t *testing.T) {
	p := e2ePlan(t)
	o, rec, ran := integrationOrchestrator(t, p, true)

	if failed := o.runSquadIntegration(context.Background(), bothGreen()); failed {
		t.Fatalf("a passing integration must not report failure:\n%s", rec.text())
	}
	if len(*ran) != 1 || (*ran)[0] != p.Integration.Acceptance {
		t.Fatalf("integration ran %v, want the plan's acceptance command", *ran)
	}
	if out := rec.text(); !strings.Contains(out, "the halves fit together") {
		t.Errorf("expected a passing integration event:\n%s", out)
	}
}

// The failure this whole structure exists to catch: both squads green, the
// assembled application broken.
func TestIntegrationFailureIsReportedAsARunFailure(t *testing.T) {
	o, rec, _ := integrationOrchestrator(t, e2ePlan(t), false)

	if failed := o.runSquadIntegration(context.Background(), bothGreen()); !failed {
		t.Fatal("a red integration must fail the run — both halves green with a broken app is the whole point")
	}
	out := rec.text()
	if !strings.Contains(out, "INTEGRATION FAILED") || !strings.Contains(out, "The seam is wrong") {
		t.Errorf("the failure must name the seam:\n%s", out)
	}
}

func TestIntegrationWaitsForEveryHalf(t *testing.T) {
	o, rec, ran := integrationOrchestrator(t, e2ePlan(t), true)

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColDone},
		{ID: "T2", Squad: "frontend", Column: plan.ColInProgress},
	}}
	if failed := o.runSquadIntegration(context.Background(), board); failed {
		t.Error("skipping is not failing")
	}
	// Running it against a half still being written produces failures about
	// missing files rather than about the seam — noise that trains everyone to
	// ignore the one gate that matters.
	if len(*ran) != 0 {
		t.Fatalf("integration must not run early, ran %v", *ran)
	}
	if out := rec.text(); !strings.Contains(out, "integration skipped") || !strings.Contains(out, "still building") {
		t.Errorf("expected a skip explaining why:\n%s", out)
	}
}

func TestIntegrationSaysSoWhenThePlanHasNoJoinCommand(t *testing.T) {
	p := e2ePlan(t)
	p.Integration.Acceptance = ""
	o, rec, ran := integrationOrchestrator(t, p, true)

	if failed := o.runSquadIntegration(context.Background(), bothGreen()); failed {
		t.Error("a missing command is not a failure")
	}
	if len(*ran) != 0 {
		t.Fatalf("nothing to run, but ran %v", *ran)
	}
	if out := rec.text(); !strings.Contains(out, "never checked together") {
		t.Errorf("a plan with no join command must say the halves went unchecked:\n%s", out)
	}
}

// Squads are opt-in and everything about them is non-fatal: a single-stream run
// must not gain an integration step it never asked for.
func TestIntegrationIsInertOnASingleStreamRun(t *testing.T) {
	o, _, ran := integrationOrchestrator(t, nil, true)
	if failed := o.runSquadIntegration(context.Background(), bothGreen()); failed {
		t.Error("no plan, no integration, no failure")
	}
	if len(*ran) != 0 {
		t.Fatalf("nothing should have run, got %v", *ran)
	}
}

func TestRouteBoardToSquadsStampsAndReports(t *testing.T) {
	p := e2ePlan(t)
	o, rec, _ := integrationOrchestrator(t, p, true)

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Files: []string{"web/src/App.tsx"}},
		{ID: "T3", Files: []string{"cmd/server/main.go", "web/src/api.ts"}}, // the seam
		{ID: "T4", Files: []string{"Makefile"}},                             // unowned
	}}
	o.routeBoardToSquads(p, board)

	want := map[string]string{"T1": "backend", "T2": "frontend", "T3": "", "T4": ""}
	for _, task := range board.Tasks {
		if task.Squad != want[task.ID] {
			t.Errorf("%s = %q, want %q", task.ID, task.Squad, want[task.ID])
		}
	}
	out := rec.text()
	for _, want := range []string{"squad assignment", "backend=1", "frontend=1",
		"span both squads", "no squad owns"} {
		if !strings.Contains(out, want) {
			t.Errorf("routing report is missing %q:\n%s", want, out)
		}
	}
}

func TestReportSquadProgressSurfacesTheCrossTeamStall(t *testing.T) {
	p := e2ePlan(t)
	o, rec, _ := integrationOrchestrator(t, p, true)

	o.reportSquadProgress(p, &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend", Column: plan.ColInProgress},
		{ID: "T2", Squad: "frontend", Column: plan.ColBlocked},
	}})

	out := rec.text()
	if !strings.Contains(out, "squads: backend 0/1") {
		t.Errorf("expected per-squad progress:\n%s", out)
	}
	// Retrying the consumer's tasks forever is the wrong response, so the
	// report has to distinguish this from a task defect.
	if !strings.Contains(out, "waiting on backend") || !strings.Contains(out, "not a task defect") {
		t.Errorf("expected the stall to be named as a contract dependency:\n%s", out)
	}
}

// ── Correction tickets ───────────────────────────────────────────────────
//
// A failing tester used to produce a red notification and a generic "Fix tester
// failures" task assigned to `worker`. These pin the replacement: one ticket,
// routed to the specialist whose language broke, carrying the evidence.

func hasGoReactRoles(id string) bool {
	return id == "go-worker" || id == "react-worker"
}

func TestTesterFailureRaisesARoutedCorrectionTicket(t *testing.T) {
	board := &plan.Board{
		QueryID: "q1",
		Tasks: []plan.Task{
			{ID: "T1", Title: "store", Role: plan.RoleWorker, Column: plan.ColDone,
				Squad: "backend", Files: []string{"internal/store/todo.go"}},
		},
	}
	out := rewriteBoardFromTesterWith(board, "build a todo app",
		[]string{"TestTodoStore_Add: want 1 item, got 0"},
		"1 of 9 tests failed",
		"go test ./internal/store/",
		"--- FAIL: TestTodoStore_Add\n    store_test.go:41: want 1 item, got 0\n",
		hasGoReactRoles)

	var ticket *plan.Task
	for i := range out.Tasks {
		if strings.Contains(out.Tasks[i].Notes, "correction ticket") {
			ticket = &out.Tasks[i]
		}
	}
	if ticket == nil {
		t.Fatalf("no correction ticket was raised:\n%+v", out.Tasks)
	}
	// Routed to the language that broke, not to the generic worker.
	if ticket.Role != "go-worker" {
		t.Errorf("ticket role = %q, want go-worker", ticket.Role)
	}
	// And to the team that owns the file.
	if ticket.Squad != "backend" {
		t.Errorf("ticket squad = %q, want backend", ticket.Squad)
	}
	// Carrying the evidence a fixer needs.
	for _, want := range []string{"go test ./internal/store/", "store_test.go:41", "want 1 item, got 0"} {
		if !strings.Contains(ticket.Description, want) {
			t.Errorf("ticket is missing %q:\n%s", want, ticket.Description)
		}
	}
	if !strings.Contains(ticket.Acceptance, "go test ./internal/store/") {
		t.Errorf("acceptance should name the command, got %q", ticket.Acceptance)
	}
}

// Three gate runs for one unresolved break must not stack three identical
// tickets — that is what made the board look like it was losing ground.
func TestRepeatedTesterFailuresDoNotStackTickets(t *testing.T) {
	board := &plan.Board{QueryID: "q1", Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"main.go"}},
	}}
	failures := []string{"TestX failed"}

	first := rewriteBoardFromTesterWith(board, "q", failures, "1 failed", "go test ./...", "boom", hasGoReactRoles)
	// Simulate the ticket still being open when the gate runs again.
	second := rewriteBoardFromTesterWith(&first, "q", failures, "1 failed", "go test ./...", "boom", hasGoReactRoles)

	n := 0
	for _, task := range second.Tasks {
		if strings.Contains(task.Notes, "correction ticket") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one unresolved defect produced %d tickets", n)
	}
}

func TestCorrectionTicketFallsBackWhenNoSpecialistExists(t *testing.T) {
	board := &plan.Board{QueryID: "q1", Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Column: plan.ColDone, Files: []string{"main.rs"}},
	}}
	out := rewriteBoardFromTesterWith(board, "q", []string{"cargo test failed"}, "",
		"cargo test", "error[E0308]", func(string) bool { return false })

	for _, task := range out.Tasks {
		if strings.Contains(task.Notes, "correction ticket") && task.Role != plan.RoleWorker {
			t.Errorf("with no rust specialist registered the ticket must go to the generic worker, got %q", task.Role)
		}
	}
}

// ── Per-task specialist routing ──────────────────────────────────────────

// routingOrchestrator builds an orchestrator whose factory really does know the
// Go and React language packs — registered the same way the block loader
// registers them, so HasRole answers from the same path production uses. A test
// that skipped because the packs were absent would prove nothing about routing.
func routingOrchestrator(t *testing.T, squadPlan *squads.Plan) (*Orchestrator, *recorder) {
	t.Helper()
	rec := &recorder{}
	f := agents.NewFactory(nil, nil, "m", "p")
	for _, id := range []string{
		"go-worker", "go-reviewer", "go-tester",
		"react-worker", "react-reviewer", "react-tester",
	} {
		f.ExtraCustoms = append(f.ExtraCustoms, agents.CustomSpec{
			ID: id, Title: id, SystemPrompt: "test specialist", MaxIter: 2,
		})
	}
	o := &Orchestrator{
		cfg:       &config.Config{Root: t.TempDir()},
		factory:   f,
		squadPlan: squadPlan,
		onEvent:   rec.handle,
	}
	return o, rec
}

// The headline: one board, two languages, the right specialist on each task —
// which a single run-level pick cannot do.
func TestBoardIsRoutedPerTaskInAMixedRepo(t *testing.T) {
	o, rec := routingOrchestrator(t, nil)
	for _, id := range []string{"go-worker", "react-worker"} {
		if !o.factory.HasRole(id) {
			t.Fatalf("the fixture must register %s, or this test proves nothing", id)
		}
	}

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleWorker, Files: []string{"cmd/server/main.go"}},
		{ID: "T2", Role: plan.RoleWorker, Files: []string{"web/src/App.tsx"}},
		{ID: "T3", Role: plan.RoleTester, Files: []string{"cmd/server/main.go"}},
	}}
	o.routeBoardToSpecialists(board)

	want := map[string]string{"T1": "go-worker", "T2": "react-worker", "T3": plan.RoleTester}
	for _, task := range board.Tasks {
		if task.Role != want[task.ID] {
			t.Errorf("%s = %q, want %q", task.ID, task.Role, want[task.ID])
		}
	}
	if out := rec.text(); !strings.Contains(out, "routed") {
		t.Errorf("a reroute should be reported:\n%s", out)
	}
}

func TestRoutingIsSilentWhenItChangesNothing(t *testing.T) {
	o, rec := routingOrchestrator(t, nil)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: plan.RoleTester, Files: []string{"README.md"}},
	}}
	o.routeBoardToSpecialists(board)
	if out := rec.text(); strings.Contains(out, "routed") {
		t.Errorf("nothing changed, so nothing should be reported:\n%s", out)
	}
}

func TestRoutingIsSafeWithoutAFactory(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{Root: t.TempDir()}}
	board := &plan.Board{Tasks: []plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"main.go"}}}}
	o.routeBoardToSpecialists(board) // must not panic
	if board.Tasks[0].Role != plan.RoleWorker {
		t.Errorf("without a registry the role must stay put, got %q", board.Tasks[0].Role)
	}
	o.routeBoardToSpecialists(nil)
}

// The manager's per-squad worker is consulted when the files say nothing.
func TestSquadWorkerLookupFeedsRouting(t *testing.T) {
	p := e2ePlan(t)
	p.Squads[1].Worker = "react-worker"
	o, _ := routingOrchestrator(t, p)

	lookup := o.squadWorkerLookup()
	if lookup == nil {
		t.Fatal("a squad plan must expose its worker choices")
	}
	if got := lookup("frontend"); got != "react-worker" {
		t.Errorf("lookup(frontend) = %q", got)
	}
	if got := lookup("nope"); got != "" {
		t.Errorf("unknown squad = %q, want empty", got)
	}
	if (&Orchestrator{}).squadWorkerLookup() != nil {
		t.Error("no plan, no lookup")
	}
}
