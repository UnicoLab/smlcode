package orchestrator

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
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
		hasGoReactRoles, nil)

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

	first := rewriteBoardFromTesterWith(board, "q", failures, "1 failed", "go test ./...", "boom", hasGoReactRoles, nil)
	// Simulate the ticket still being open when the gate runs again.
	second := rewriteBoardFromTesterWith(&first, "q", failures, "1 failed", "go test ./...", "boom", hasGoReactRoles, nil)

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
		"cargo test", "error[E0308]", func(string) bool { return false }, nil)

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
		"go-worker", "go-reviewer", "go-tester", "go-corrector",
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

// ── Approval-time editing ────────────────────────────────────────────────
//
// The gate used to offer approve or replan. Replan throws the whole board away
// to fix one wrong file path, so people approved plans they could see were
// slightly wrong. These pin the third answer.

func editableOrchestrator(t *testing.T) (*Orchestrator, *recorder) {
	t.Helper()
	p := e2ePlan(t)
	o, rec := routingOrchestrator(t, p)
	return o, rec
}

func TestApplyPlanEditsChangesTheBoardTheUserSaw(t *testing.T) {
	o, rec := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "api", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"cmd/main.go"}},
		{ID: "T2", Title: "ui", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"web/App.tsx"}},
	}}
	title := "serve the todo API"
	role := "go-worker"
	o.applyPlanEdits(board, &plan.PlanEdits{Tasks: []plan.TaskEdit{
		{ID: "T1", Title: &title, Role: &role},
	}})

	if board.Tasks[0].Title != title || board.Tasks[0].Role != "go-worker" {
		t.Errorf("edit not applied: %+v", board.Tasks[0])
	}
	if out := rec.text(); !strings.Contains(out, "applied plan edits") {
		t.Errorf("the edit should be reported:\n%s", out)
	}
}

// A role the harness cannot staff produces a task that never starts. Refuse it
// where the user can still see why.
func TestPlanEditsRefuseAnUnstaffableRole(t *testing.T) {
	o, rec := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: "go-worker", Column: plan.ColReadyToDev, Files: []string{"cmd/main.go"}},
	}}
	role := "cobol-worker"
	o.applyPlanEdits(board, &plan.PlanEdits{Tasks: []plan.TaskEdit{{ID: "T1", Role: &role}}})

	if board.Tasks[0].Role == "cobol-worker" {
		t.Error("an unregistered agent was accepted")
	}
	if out := rec.text(); !strings.Contains(out, "not a registered agent") {
		t.Errorf("the refusal should be visible:\n%s", out)
	}
}

func TestPlanEditsCanRestaffASquad(t *testing.T) {
	o, rec := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Role: "go-worker", Column: plan.ColReadyToDev, Files: []string{"cmd/main.go"}},
	}}
	acc := "go test ./... -race"
	o.applyPlanEdits(board, &plan.PlanEdits{Squads: []plan.SquadEdit{
		{ID: "backend", Acceptance: &acc},
	}})

	back, ok := o.squadPlan.Squad("backend")
	if !ok || back.Acceptance != acc {
		t.Errorf("squad edit not applied: %+v", back)
	}
	if out := rec.text(); !strings.Contains(out, "squad plan edited") {
		t.Errorf("the squad edit should be reported:\n%s", out)
	}
	// And it survives a reload — the run may be resumed.
	saved, found, err := squads.Load(o.cfg.SlmDir())
	if err != nil || !found {
		t.Fatalf("the edited plan must be saved: found=%v err=%v", found, err)
	}
	if s, _ := saved.Squad("backend"); s.Acceptance != acc {
		t.Errorf("the saved plan does not carry the edit: %+v", s)
	}
}

// A squad edit that breaks disjoint ownership is refused WHOLE, and that must
// not silently discard the unrelated task edits made in the same pass.
func TestARefusedSquadEditKeepsTheTaskEdits(t *testing.T) {
	o, rec := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "api", Role: "go-worker", Column: plan.ColReadyToDev, Files: []string{"cmd/main.go"}},
	}}
	title := "kept"
	o.applyPlanEdits(board, &plan.PlanEdits{
		Tasks: []plan.TaskEdit{{ID: "T1", Title: &title}},
		Squads: []plan.SquadEdit{
			{ID: "backend", Owns: []string{"cmd/**", "web/**"}, OwnsSet: true}, // collides
		},
	})

	if board.Tasks[0].Title != "kept" {
		t.Error("a valid task edit was discarded because a squad edit failed")
	}
	back, _ := o.squadPlan.Squad("backend")
	if back.OwnsPath("web/src/App.tsx") {
		t.Error("the overlapping ownership edit was applied")
	}
	out := rec.text()
	if !strings.Contains(out, "squad edit REFUSED") || !strings.Contains(out, "both claim") {
		t.Errorf("the user must be told their org-chart edit was refused, and why:\n%s", out)
	}
}

func TestApplyPlanEditsIsInertWithNothingToDo(t *testing.T) {
	o, rec := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{{ID: "T1", Role: "go-worker", Column: plan.ColReadyToDev}}}
	o.applyPlanEdits(board, nil)
	o.applyPlanEdits(board, &plan.PlanEdits{})
	o.applyPlanEdits(nil, &plan.PlanEdits{Tasks: []plan.TaskEdit{{ID: "T1"}}})
	if out := rec.text(); strings.Contains(out, "applied plan edits") {
		t.Errorf("nothing to do should say nothing:\n%s", out)
	}
}

// The approval card has to carry the org chart and the agents the harness can
// actually staff, or the UI is guessing.
func TestApprovalCardCarriesTheTeamsAndTheStaffableAgents(t *testing.T) {
	o, _ := editableOrchestrator(t)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Squad: "backend"}, {ID: "T2", Squad: "backend"}, {ID: "T3", Squad: "frontend"},
	}}
	view := o.squadsAskView(board)
	if view == nil || len(view.Squads) != 2 {
		t.Fatalf("the card should carry both squads, got %+v", view)
	}
	byID := map[string]plan.PlanSquad{}
	for _, s := range view.Squads {
		byID[s.ID] = s
	}
	// An idle team must be visible on the card, not only in the event log.
	if byID["backend"].TaskCount != 2 || byID["frontend"].TaskCount != 1 {
		t.Errorf("task counts = backend:%d frontend:%d",
			byID["backend"].TaskCount, byID["frontend"].TaskCount)
	}
	if len(view.Interfaces) == 0 || view.Interfaces[0].Provider != "backend" {
		t.Errorf("the contract should be on the card: %+v", view.Interfaces)
	}

	agents := o.staffableAgents()
	if len(agents) == 0 {
		t.Fatal("the card must offer the agents this run can staff")
	}
	found := map[string]bool{}
	for _, a := range agents {
		found[a] = true
	}
	for _, want := range []string{"go-worker", "react-worker", plan.RoleWorker} {
		if !found[want] {
			t.Errorf("staffable agents is missing %q", want)
		}
	}

	// A single-stream run has no org chart to show.
	solo := &Orchestrator{cfg: o.cfg}
	if solo.squadsAskView(board) != nil {
		t.Error("no squad plan, no squads on the card")
	}
}

// ── Manager triage ───────────────────────────────────────────────────────

func TestTriageRosterOffersOnlyImplementers(t *testing.T) {
	o, _ := routingOrchestrator(t, nil)
	roster := o.triageRoster()
	if len(roster) == 0 {
		t.Fatal("the manager must have someone to choose from")
	}
	has := map[string]bool{}
	for _, id := range roster {
		has[id] = true
	}
	for _, want := range []string{"go-worker", "react-worker", plan.RoleWorker, plan.RoleCorrector} {
		if !has[want] {
			t.Errorf("roster is missing the implementer %q", want)
		}
	}
	// Triage decides who WRITES the fix. Offering a reviewer or a planner
	// invites an answer the loop would then refuse.
	for _, unwanted := range []string{plan.RoleTester, plan.RoleReviewer, "planner", "go-reviewer", "triage"} {
		if has[unwanted] {
			t.Errorf("roster should not offer the non-implementer %q", unwanted)
		}
	}
}

func TestTriageIsSkippedWithNoRoster(t *testing.T) {
	o, _ := routingOrchestrator(t, nil)
	if _, ok := o.triageRejectedDelivery(context.Background(), loop.TriageRequest{}); ok {
		t.Error("with nothing to choose from there is nothing to decide")
	}
	solo := &Orchestrator{}
	if _, ok := solo.triageRejectedDelivery(context.Background(), loop.TriageRequest{Roster: []string{"worker"}}); ok {
		t.Error("no factory, no triage")
	}
}

// ── Repeat tickets go past the project manager ───────────────────────────
//
// The tester path routes tickets by language: a failing .go file to go-worker,
// a failing .tsx to react-worker. That is the right FIRST answer and the wrong
// second one — when the same defect comes back, the deterministic route hands
// it to the agent that just failed at it, which is the loop that made tester
// failures feel like noise rather than progress.

// ticket builds a first-attempt correction ticket. ticketAt makes it the nth.
func ticket(id, key, col, role string) plan.Task { return ticketAt(id, key, col, role, 1) }

func ticketAt(id, key, col, role string, attempt int) plan.Task {
	t := plan.Task{
		ID: id, Column: col, Role: role, Squad: "backend",
		Title:       "fix the todo handler",
		Review:      "tester feedback: handler returns 500",
		Description: "The tester gate rejected this work.\n\n## What failed\n- todo_test.go:41 want 200, got 500\n- the body is not JSON\n\n## Reproduce it\n```\ngo test ./...\n```\n",
		Files:       []string{"internal/http/todo.go"},
	}
	plan.StampCorrectionKey(&t, key)
	plan.StampCorrectionAttempt(&t, attempt)
	return t
}

// A first ticket is the router's to own: it has not been tried yet, and a model
// call to confirm the obvious choice is pure latency.
func TestAFirstTicketIsNotSentToTheManager(t *testing.T) {
	// A manager IS wired and would happily reassign — the gate is what stops
	// it, not the absence of anyone to ask.
	exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"encoder bug"}`}
	o := managedOrchestrator(t, exec, nil)
	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{ticket("C1", key, plan.ColReadyToDev, "go-worker")}}

	if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
		t.Errorf("triaged %d first-attempt tickets, want 0", moved)
	}
	if board.Tasks[0].Role != "go-worker" {
		t.Errorf("Role = %q, want the router's pick left alone", board.Tasks[0].Role)
	}
	// And no model call was spent confirming the obvious choice.
	if exec.askedAgent != "" {
		t.Errorf("asked %q about a first attempt; that is pure latency", exec.askedAgent)
	}
}

// The gate itself: a second ticket for the same defect is a repeat, and repeats
// are what the manager exists to break.
func TestASecondTicketForTheSameDefectIsARepeat(t *testing.T) {
	o, _ := routingOrchestrator(t, nil)
	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if got := plan.CorrectionAttemptOf(board.Tasks[1]); got != 2 {
		t.Fatalf("fixture is wrong: attempt = %d, want 2", got)
	}
	// No model is wired here, so triage cannot answer — the ticket must survive
	// exactly as the router left it rather than being parked or blanked.
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
		t.Errorf("moved %d tickets with no manager available, want 0", moved)
	}
	if board.Tasks[1].Role != "go-worker" || board.Tasks[1].Column != plan.ColReadyToDev {
		t.Errorf("an unanswerable triage changed the ticket: %+v", board.Tasks[1])
	}
}

func TestOnlyReadyTicketsAreTriaged(t *testing.T) {
	const key = "tester|handler returns 500|internal/http/todo.go"
	for _, col := range []string{plan.ColDone, plan.ColInProgress, plan.ColInReview, plan.ColBlocked} {
		exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"encoder bug"}`}
		o := managedOrchestrator(t, exec, nil)
		board := &plan.Board{Tasks: []plan.Task{
			ticket("C1", key, plan.ColDone, "go-worker"),
			ticketAt("C2", key, col, "go-worker", 2),
		}}
		if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
			t.Errorf("column %q: triaged %d tickets, want 0", col, moved)
		}
		// Reassigning work an agent is holding takes it off them mid-flight.
		if exec.askedAgent != "" {
			t.Errorf("column %q: asked the manager about a ticket nobody can pick up", col)
		}
	}
}

// A task that is not a correction has no defect history, and reassigning one
// mid-flight would take work off an agent that is doing it.
func TestPlainTasksAreNeverTouchedByTicketTriage(t *testing.T) {
	o := managedOrchestrator(t, &triageExec{reply: `{"assignee":"go-corrector","reason":"x"}`}, nil)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColReadyToDev, Role: "go-worker", Title: "todo store"},
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
		t.Errorf("triaged %d non-correction tasks, want 0", moved)
	}
}

func TestTicketTriageIsSafeWithNothingWiredUp(t *testing.T) {
	var nilOrch *Orchestrator
	if got := nilOrch.triageRepeatTickets(context.Background(), &plan.Board{}); got != 0 {
		t.Errorf("nil orchestrator triaged %d tickets", got)
	}
	o, _ := routingOrchestrator(t, nil)
	if got := o.triageRepeatTickets(context.Background(), nil); got != 0 {
		t.Errorf("nil board triaged %d tickets", got)
	}
	solo := &Orchestrator{}
	if got := solo.triageRepeatTickets(context.Background(), &plan.Board{}); got != 0 {
		t.Errorf("no factory triaged %d tickets", got)
	}
}

// The manager is handed the tester's actual findings, not a re-derived summary.
func TestTheTicketBodyIsWhatTheManagerReviews(t *testing.T) {
	task := ticket("C2", "k", plan.ColReadyToDev, "go-worker")
	if got := ticketHeadline(task); got != "tester feedback: handler returns 500" {
		t.Errorf("ticketHeadline = %q", got)
	}
	got := ticketFindings(task)
	want := []string{"todo_test.go:41 want 200, got 500", "the body is not JSON"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ticketFindings = %v, want %v", got, want)
	}
	// The bullet list stops at the next heading: the reproduce command is
	// evidence, not a finding.
	for _, f := range got {
		if strings.Contains(f, "go test") {
			t.Errorf("ticketFindings leaked past the section: %q", f)
		}
	}
	if ticketFindings(plan.Task{Description: "no sections here"}) != nil {
		t.Error("a ticket with no findings section has no findings")
	}
}

func TestATicketWithNoReviewFallsBackToItsTitle(t *testing.T) {
	task := ticket("C2", "k", plan.ColReadyToDev, "go-worker")
	task.Review = ""
	if got := ticketHeadline(task); got != "fix the todo handler" {
		t.Errorf("ticketHeadline = %q, want the title", got)
	}
}

// triageExec answers every dispatch with one canned triage verdict, recording
// which agent was asked and what it was shown.
type triageExec struct {
	askedAgent string
	gotInput   string
	reply      string
	err        error
}

func (e *triageExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	if len(reqs) > 0 {
		e.askedAgent = reqs[0].AgentID
		e.gotInput = reqs[0].Input
	}
	if e.err != nil {
		return nil, e.err
	}
	return []ggagent.SubAgentResult{{Output: e.reply}}, nil
}

func managedOrchestrator(t *testing.T, exec *triageExec, squadPlan *squads.Plan) *Orchestrator {
	t.Helper()
	o, _ := routingOrchestrator(t, squadPlan)
	o.cfg = config.Default(t.TempDir())
	o.executor = exec
	o.shared = ggagent.NewSharedState()
	return o
}

// The headline: the second attempt at a defect goes to somebody else, with the
// manager's direction above the evidence — instead of back to the agent that
// could not fix it, holding a ticket whose only new content is that it failed
// again.
func TestARepeatTicketIsReassignedByTheManager(t *testing.T) {
	exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"the worker cannot see the encoder bug",` +
		`"guidance":"Set Content-Type before writing the body, then json.NewEncoder(w).Encode(todos)."}`}
	o := managedOrchestrator(t, exec, nil)

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}

	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("triaged %d tickets, want 1", moved)
	}
	got := board.Tasks[1]
	if got.Role != "go-corrector" {
		t.Errorf("Role = %q, want the manager's pick", got.Role)
	}
	// The guidance is the one thing here the last attempt did not already have,
	// so burying it under the failure dump is how it gets skimmed past.
	pmAt := strings.Index(got.Description, "From the project manager")
	failAt := strings.Index(got.Description, "## What failed")
	if pmAt < 0 || failAt < 0 || pmAt > failAt {
		t.Errorf("the manager's direction is not above the evidence:\n%s", got.Description)
	}
	if !strings.Contains(got.Description, "json.NewEncoder") {
		t.Errorf("the guidance did not reach the next agent:\n%s", got.Description)
	}
	if !strings.Contains(got.Notes, "reassigned-to: go-corrector") {
		t.Errorf("the handoff was not recorded: %q", got.Notes)
	}
	// The ticket keeps its identity: same defect, same files, same key.
	if plan.CorrectionKeyOf(got) != key || got.ID != "C2" {
		t.Errorf("the ticket lost its identity: %+v", got)
	}
	// And the manager was shown the tester's actual findings.
	if !strings.Contains(exec.gotInput, "todo_test.go:41 want 200, got 500") {
		t.Errorf("the manager was not shown what failed:\n%s", exec.gotInput)
	}
}

// A verdict the loop would refuse leaves the ticket alone rather than parking
// it on an agent that cannot be dispatched.
func TestAnUnusableTicketVerdictLeavesTheRoutersPick(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
	}{
		{"unregistered agent", `{"assignee":"cobol-worker","reason":"why not"}`},
		{"re-picks the agent that just failed", `{"assignee":"go-worker","reason":"try again"}`},
		{"names nobody", `{"reason":"unsure"}`},
		{"unreadable", `I think go-corrector should take it.`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := managedOrchestrator(t, &triageExec{reply: tc.reply}, nil)
			const key = "tester|handler returns 500|internal/http/todo.go"
			board := &plan.Board{Tasks: []plan.Task{
				ticket("C1", key, plan.ColDone, "go-worker"),
				ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
			}}
			if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
				t.Fatalf("applied an unusable verdict to %d tickets", moved)
			}
			if board.Tasks[1].Role != "go-worker" {
				t.Errorf("Role = %q, want the router's pick kept", board.Tasks[1].Role)
			}
		})
	}
}

// A team may name its own manager. The one that answers has to be the one that
// knows the team's people.
func TestTheTeamsOwnManagerAnswersForItsTickets(t *testing.T) {
	p := &squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"internal/**"}, Acceptance: "go test ./...",
			Worker: "go-worker", Manager: "backend-triage"},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build", Worker: "react-worker"},
	}}
	p.Normalize()

	exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"encoder bug"}`}
	o := managedOrchestrator(t, exec, p)
	o.factory.ExtraCustoms = append(o.factory.ExtraCustoms, agents.CustomSpec{
		ID: "backend-triage", Title: "Backend PM", SystemPrompt: agents.PromptTriage, MaxIter: 2,
	})

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("triaged %d tickets, want 1", moved)
	}
	if exec.askedAgent != "backend-triage" {
		t.Errorf("asked %q, want the team's own manager", exec.askedAgent)
	}
	if !strings.Contains(exec.gotInput, "project manager for the backend team") {
		t.Errorf("the manager was not told which team it answers for:\n%s", exec.gotInput)
	}
}

// An agent that answers a different contract is refused BEFORE the model call:
// the decoding grammar comes from its own prompt, so its reply could not be
// read as a verdict however long it took to produce.
func TestAnIneligibleManagerFallsBackToTheRunDefault(t *testing.T) {
	p := &squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"internal/**"}, Acceptance: "go test ./...", Manager: "go-worker"},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build"},
	}}
	p.Normalize()

	exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"encoder bug"}`}
	o := managedOrchestrator(t, exec, p)

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("triaged %d tickets, want 1", moved)
	}
	if exec.askedAgent != agents.RoleTriage {
		t.Errorf("asked %q, want the run's default manager", exec.askedAgent)
	}
}

// The triage prompt asks for a specialist over a generic. Asking is not enough:
// it is the rule a small model skips most often, and the cost of skipping it is
// a correction that brings nothing the failed attempt did not have — a generic
// corrector handed a failing Go handler has no more Go knowledge than the go
// worker that already failed.
func TestAGenericVerdictIsUpgradedToTheLanguageSpecialist(t *testing.T) {
	exec := &triageExec{reply: `{"assignee":"corrector","reason":"needs a fresh pair of hands"}`}
	o := managedOrchestrator(t, exec, nil)

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("triaged %d tickets, want 1", moved)
	}
	if got := board.Tasks[1].Role; got != "go-corrector" {
		t.Errorf("Role = %q, want the Go specialist over the generic corrector", got)
	}
	if !strings.Contains(board.Tasks[1].Notes, "reassigned-to: go-corrector") {
		t.Errorf("the notes record the wrong assignee: %q", board.Tasks[1].Notes)
	}

	// The manager saw a ranked, labeled roster rather than an alphabetical one
	// with the generics on top.
	gi := strings.Index(exec.gotInput, "- go-")
	ci := strings.Index(exec.gotInput, "- corrector")
	if gi < 0 || ci < 0 || gi > ci {
		t.Errorf("the roster did not lead with the task's language:\n%s", exec.gotInput)
	}
	if !strings.Contains(exec.gotInput, "(Go specialist)") {
		t.Errorf("the roster is not labeled:\n%s", exec.gotInput)
	}
}

// A manager that deliberately reached for another language's expert has a
// reason the file extensions cannot see.
func TestASpecialistVerdictIsHonoredAsGiven(t *testing.T) {
	exec := &triageExec{reply: `{"assignee":"react-worker","reason":"the seam is on the web side"}`}
	o := managedOrchestrator(t, exec, nil)

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("triaged %d tickets, want 1", moved)
	}
	if got := board.Tasks[1].Role; got != "react-worker" {
		t.Errorf("Role = %q, want the manager's deliberate cross-language pick", got)
	}
}

// ── One team's defect must not reopen another team's finished work ───────
//
// The reopen heuristics are text matches, and text matches leak across teams.
// The frozen contract makes it worse rather than better: it is attached as
// acceptance criteria to BOTH halves, so one clause of shared text is enough
// for a backend compile error to reopen the frontend's completed work. The
// frontend then re-runs, fails at a defect it does not own and cannot see, and
// the run ends reporting "frontend 0/1 working" over a half that was correct.

func twoLanePlan() *squads.Plan {
	p := &squads.Plan{Squads: []squads.Squad{
		{ID: "backend", Owns: []string{"cmd/**", "internal/**"}, Acceptance: "go test ./...", Worker: "go-worker"},
		{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build", Worker: "react-worker"},
	}}
	p.Normalize()
	return p
}

// The shared acceptance text is what makes both halves match. That is the leak.
func twoLaneBoard() *plan.Board {
	const shared = "the contract clause GET /api/todos returns 200 with [{id,title,done}]"
	return &plan.Board{Tasks: []plan.Task{
		{
			ID: "T1", Role: "go-worker", Squad: "backend", Title: "serve the todo API",
			Files: []string{"cmd/server/main.go"}, Acceptance: shared,
			Column: plan.ColDone, Status: plan.StatusDone,
		},
		{
			ID: "T2", Role: "react-worker", Squad: "frontend", Title: "todo list view",
			Files: []string{"web/src/App.tsx"}, Acceptance: shared,
			Column: plan.ColDone, Status: plan.StatusDone,
		},
	}}
}

func TestABackendDefectLeavesTheFrontendsWorkAlone(t *testing.T) {
	failures := []string{"cmd/server/main.go:7: undefined: json.NewEncoder"}
	summary := "the contract clause GET /api/todos returns 200 with [{id,title,done}] is not met"

	got := rewriteBoardFromTesterWith(twoLaneBoard(), "build a todo app", failures, summary,
		"go build ./...", "undefined: json.NewEncoder", nil, twoLanePlan())

	byID := map[string]plan.Task{}
	for _, task := range got.Tasks {
		byID[task.ID] = task
	}
	if byID["T1"].Column != plan.ColReadyToDev {
		t.Errorf("T1 column = %q, want the owning team's task reopened", byID["T1"].Column)
	}
	if byID["T2"].Column != plan.ColDone {
		t.Errorf("T2 column = %q — a backend defect reopened the frontend's finished work",
			byID["T2"].Column)
	}
	if strings.Contains(byID["T2"].Notes, "REOPENED") {
		t.Errorf("T2 was reopened for another team's defect: %q", byID["T2"].Notes)
	}
}

// The lane only narrows things when it CAN. A defect on the seam belongs to
// both halves, and refusing to reopen either would leave it with nobody.
func TestADefectOnTheSeamStillReachesBothTeams(t *testing.T) {
	failures := []string{
		"cmd/server/main.go:7: returns a bare array",
		"web/src/App.tsx:12: expects {items:[...]}",
	}
	got := rewriteBoardFromTesterWith(twoLaneBoard(), "build a todo app", failures,
		"the two halves disagree about the response shape", "", "", nil, twoLanePlan())

	for _, task := range got.Tasks {
		if task.Column != plan.ColReadyToDev {
			t.Errorf("%s column = %q, want both halves of a seam defect reopened", task.ID, task.Column)
		}
	}
}

// With no org chart the heuristics decide alone, exactly as they always did.
func TestWithoutSquadsNothingIsFiltered(t *testing.T) {
	board := twoLaneBoard()
	for i := range board.Tasks {
		board.Tasks[i].Squad = ""
	}
	failures := []string{"cmd/server/main.go:7: undefined: json.NewEncoder"}
	got := rewriteBoardFromTesterWith(board, "build a todo app", failures,
		"the contract clause GET /api/todos returns 200 with [{id,title,done}] is not met",
		"", "", nil, nil)
	reopened := 0
	for _, task := range got.Tasks {
		if task.Column == plan.ColReadyToDev {
			reopened++
		}
	}
	if reopened == 0 {
		t.Error("with no squads the heuristics must still reopen implicated work")
	}
}

// An unassigned task has no team to be outside of, and refusing to reopen it
// would leave a real defect with nobody on it.
func TestAnUnassignedTaskIsNeverOutsideTheLane(t *testing.T) {
	if outsideLane("backend", plan.Task{ID: "T9"}) {
		t.Error("a task with no squad is not outside anybody's lane")
	}
	if outsideLane("", plan.Task{ID: "T9", Squad: "frontend"}) {
		t.Error("with no lane there is nothing to be outside of")
	}
	if !outsideLane("backend", plan.Task{ID: "T9", Squad: "frontend"}) {
		t.Error("another team's task IS outside the lane")
	}
	if outsideLane("backend", plan.Task{ID: "T9", Squad: "BACKEND"}) {
		t.Error("the lane check must not be case-sensitive")
	}
}

// ── One handoff per ticket ───────────────────────────────────────────────
//
// A second manager verdict would be a third agent guessing at work two others
// could not do. That is not a staffing problem any more — it is a scoping
// problem, and that is what a human is being asked to see.
func TestATicketIsReStaffedAtMostOnce(t *testing.T) {
	exec := &triageExec{reply: `{"assignee":"go-corrector","reason":"encoder bug"}`}
	o := managedOrchestrator(t, exec, nil)

	const key = "tester|handler returns 500|internal/http/todo.go"
	board := &plan.Board{Tasks: []plan.Task{
		ticket("C1", key, plan.ColDone, "go-worker"),
		ticketAt("C2", key, plan.ColReadyToDev, "go-worker", 2),
	}}
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 1 {
		t.Fatalf("first triage moved %d tickets, want 1", moved)
	}

	// The defect comes back a third time. The ticket has spent its handoff.
	plan.StampCorrectionAttempt(&board.Tasks[1], 3)
	board.Tasks[1].Column = plan.ColReadyToDev
	exec.askedAgent = ""
	if moved := o.triageRepeatTickets(context.Background(), board); moved != 0 {
		t.Errorf("second triage moved %d tickets, want 0", moved)
	}
	if exec.askedAgent != "" {
		t.Errorf("asked %q for a second verdict on a ticket that already changed hands", exec.askedAgent)
	}
}
