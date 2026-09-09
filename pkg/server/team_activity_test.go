package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/teams"
)

// twoTeamPlan is the org chart the tests below run against.
func twoTeamPlan() squads.Plan {
	return squads.Plan{
		Summary: "Go API + React SPA",
		Squads: []squads.Squad{
			{ID: "backend-go", Owns: []string{"cmd/**"}, Acceptance: "go test ./...", Manager: "backend-triage"},
			{ID: "frontend-react", Owns: []string{"web/**"}, Acceptance: "npm run build"},
		},
		Contract: squads.Contract{Interfaces: []squads.Interface{
			{ID: "GET /api/todos", Provider: "backend-go", Consumers: []string{"frontend-react"}},
		}},
	}
}

// The classifier pins the exact shapes the orchestrator and loop emit. A
// message that changes there falls off the timeline; this is where that is
// noticed.
func TestTeamActivityClassifiesTheRunsTeamEvents(t *testing.T) {
	p := twoTeamPlan()
	taskTeams := map[string]string{"T3": "backend-go"}
	events := []activitySource{
		{Phase: "charter", Kind: "phase", Message: "team library: 2 teams selected: backend-go, frontend-react"},
		{Phase: "charter", Kind: "agent_start", Agent: "manager", Message: `team backend-go — workspace has "go.mod"`},
		{Phase: "charter", Kind: "phase", Message: "frozen: GET /api/todos — provided by backend-go, consumed by frontend-react"},
		{Phase: "charter", Kind: "phase", Message: "squad assignment: 4 assigned · 1 straddling"},
		{Phase: "split", Kind: "agent_start", Agent: "go-worker", TaskID: "T3", Message: "assigned go-worker — files are Go"},
		{Phase: "coord", Kind: "debug", Agent: "backend-triage", TaskID: "T3", Message: "backend-triage proposes go-corrector — the handler returns a bare slice"},
		{Phase: "plan", Kind: "output", Agent: "go-corrector", TaskID: "T3", Message: "T3 reassigned from go-worker to go-corrector — compile error the worker could not resolve"},
		{Phase: "execute", Kind: "phase", Message: "squads: backend-go 1/4 working · frontend-react 3/3 done"},
		{Phase: "execute", Kind: "phase", Level: "warning", Message: "frontend-react is waiting on backend-go to deliver \"GET /api/todos\" — this is a contract dependency, not a task defect"},
		{Kind: "coord", Agent: "loop", Message: "wave 2: T3(backend-go), T5(frontend-react) — teams live: backend-go, frontend-react"},
		{Phase: "verify", Kind: "phase", Message: "team backend-go is green: go test ./..."},
		{Phase: "verify", Kind: "phase", Level: "warning", Message: "team frontend-react is RED — its own half does not pass: exit 1"},
		{Phase: "integrate", Kind: "phase", Message: "every squad is green — joining the halves: make e2e"},
		{Phase: "charter", Kind: "output", Message: "squad plan edited: backend-go + frontend-react"},
		{Phase: "charter", Kind: "output", TaskID: "T9", Message: "T9 assigned to team frontend-react by hand"},
		// Noise the timeline must not carry.
		{Phase: "execute", Kind: "tool", Message: "ws_read cmd/main.go"},
		{Phase: "execute", Kind: "token", Message: "func"},
		{Phase: "plan", Kind: "phase", Message: "planning 6 tasks"},
	}
	got := deriveTeamActivity(events, &p, taskTeams)

	type want struct{ kind, team string }
	wants := []want{
		{ActivitySelection, ""},
		{ActivitySelection, "backend-go"},
		{ActivityContract, "backend-go"},
		{ActivityRouting, ""},
		{ActivityRouting, "backend-go"},
		{ActivityTriage, "backend-go"},
		{ActivityReassign, "backend-go"},
		// Lines about the whole run — progress, a wave, an edit naming both
		// teams — carry no stamp: filing them under the first team would
		// make its filter lie.
		{ActivityProgress, ""},
		{ActivityStall, "frontend-react"},
		{ActivityWave, ""},
		{ActivityGate, "backend-go"},
		{ActivityGate, "frontend-react"},
		{ActivityIntegration, ""},
		{ActivityEdit, ""},
		{ActivityEdit, "frontend-react"},
	}
	if len(got) != len(wants) {
		var lines []string
		for _, e := range got {
			lines = append(lines, e.Kind+"/"+e.Team+": "+e.Message)
		}
		t.Fatalf("got %d entries, want %d:\n%s", len(got), len(wants), strings.Join(lines, "\n"))
	}
	for i, w := range wants {
		if got[i].Kind != w.kind || got[i].Team != w.team {
			t.Errorf("entry %d: kind=%s team=%q, want %s/%q (%s)", i, got[i].Kind, got[i].Team, w.kind, w.team, got[i].Message)
		}
	}
	// The triage verdict keeps its manager; the selection line drops the
	// charter voice so "decisions by X" is not polluted.
	if got[5].Agent != "backend-triage" {
		t.Fatalf("triage agent=%q", got[5].Agent)
	}
	if got[1].Agent != "" {
		t.Fatalf("selection agent should be cleared, got %q", got[1].Agent)
	}

	rows := summarizeManagers(&p, got, nil, "triage")
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[0].Team != "backend-go" || rows[0].Manager != "backend-triage" || rows[0].Default ||
		rows[0].Decisions != 1 || rows[0].Moved != 1 {
		t.Fatalf("backend row=%+v", rows[0])
	}
	if rows[1].Manager != "triage" || !rows[1].Default || rows[1].Stalls != 1 {
		t.Fatalf("frontend row=%+v", rows[1])
	}
}

// The endpoint reads the live ring buffer and the saved plan, so the Teams
// page can show what the managers did on the run that just finished.
func TestTeamActivityEndpointServesTheCurrentRun(t *testing.T) {
	s, root := teamServer(t)
	if err := squads.Save(filepath.Join(root, ".slmcode"), twoTeamPlan()); err != nil {
		t.Fatal(err)
	}
	s.emit(stream.Event{Phase: "charter", Kind: "phase", Message: "team library: 2 teams selected: backend-go, frontend-react"})
	s.emit(stream.Event{Phase: "coord", Kind: "debug", Agent: "backend-triage", TaskID: "T1",
		Message: "backend-triage proposes go-corrector — compile error"})
	s.emit(stream.Event{Phase: "execute", Kind: "tool", Message: "ws_read x"})

	rec := do(t, s, http.MethodGet, "/api/teams/activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	entries, _ := body["entries"].([]interface{})
	if len(entries) != 2 {
		t.Fatalf("entries=%v", body["entries"])
	}
	counts, _ := body["counts"].(map[string]interface{})
	if counts[ActivityTriage] != float64(1) {
		t.Fatalf("counts=%v", counts)
	}
	managers, _ := body["managers"].([]interface{})
	if len(managers) != 2 {
		t.Fatalf("managers=%v", body["managers"])
	}
	teams, _ := body["teams"].([]interface{})
	if len(teams) != 2 {
		t.Fatalf("teams=%v", body["teams"])
	}
}

// A team's manager is resolved the way the run resolves it: named and
// triage-capable, or the run default — and the page is told which.
func TestTeamViewReportsTheEffectiveManager(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodGet, "/api/teams/backend-go", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["effective_manager"] != "triage" || body["manager_default"] != true {
		t.Fatalf("builtin without a manager: %v / %v", body["effective_manager"], body["manager_default"])
	}

	// Naming an agent that cannot triage does not make it a manager.
	rec = do(t, s, http.MethodPut, "/api/teams/backend-go", map[string]interface{}{
		"id": "backend-go", "owns": []string{"cmd/**"}, "manager": "go-worker",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body = decode(t, rec)
	if body["manager"] != "go-worker" || body["effective_manager"] != "triage" || body["manager_default"] != true {
		t.Fatalf("worker as manager should fall back: %v", body)
	}

	list := decode(t, do(t, s, http.MethodGet, "/api/teams", nil))
	if list["default_manager"] != "triage" {
		t.Fatalf("default_manager=%v", list["default_manager"])
	}
}

// One click gives a team its own project manager: a triage-capable agent with
// the team written into its prompt, and the team pointed at it.
func TestCreateTeamManagerWritesAgentAndSeat(t *testing.T) {
	s, root := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/backend-go/manager", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["manager"] != "backend-go-triage" || body["created"] != true {
		t.Fatalf("body=%v", body)
	}
	team, _ := body["team"].(map[string]interface{})
	if team["manager"] != "backend-go-triage" || team["effective_manager"] != "backend-go-triage" || team["manager_default"] != false {
		t.Fatalf("team=%v", team)
	}
	managers, _ := body["managers"].([]interface{})
	found := false
	for _, m := range managers {
		if m == "backend-go-triage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new manager is not triage-capable: %v", managers)
	}

	// The agent file carries the team's charter and people, on top of the
	// builtin rules the decoding grammar is derived from.
	agentBody := readFile(t, filepath.Join(root, ".slmcode", "agents", "backend-go-triage.yaml"))
	for _, want := range []string{"project manager of the Backend", "go-worker", "cmd/**", "Pick from the ROSTER"} {
		if !strings.Contains(agentBody, want) {
			t.Fatalf("agent prompt lacks %q:\n%s", want, agentBody)
		}
	}
	if !strings.Contains(agentBody, "tools: false") {
		t.Fatalf("a manager must have no tools, or it answers the worker contract:\n%s", agentBody)
	}

	// Idempotent: the agent is kept, the seat is (re)written.
	rec = do(t, s, http.MethodPost, "/api/teams/backend-go/manager", nil)
	if rec.Code != http.StatusOK || decode(t, rec)["created"] != false {
		t.Fatalf("second call: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, s, http.MethodPost, "/api/teams/no-such-team/manager", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown team: status=%d", rec.Code)
	}
}

// Assigning a task to a team by hand goes through the same rule the wave
// fence applies: ownership decides, so an assignment the next save would undo
// is refused with the reason instead.
func TestPatchTaskAssignsATeamUnlessOwnershipDisagrees(t *testing.T) {
	s, root := teamServer(t)
	if err := squads.Save(filepath.Join(root, ".slmcode"), twoTeamPlan()); err != nil {
		t.Fatal(err)
	}
	add := func(id string, files []string) {
		t.Helper()
		rec := do(t, s, http.MethodPost, "/api/tasks", map[string]interface{}{
			"id": id, "title": id, "role": "worker", "files": files,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("add %s: status=%d body=%s", id, rec.Code, rec.Body.String())
		}
	}
	add("T1", nil)
	add("T2", []string{"cmd/main.go"})

	// No files: whatever a human says sticks.
	rec := do(t, s, http.MethodPatch, "/api/tasks/T1", map[string]interface{}{"squad": "frontend-react"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec)["squad"]; got != "frontend-react" {
		t.Fatalf("squad=%v", got)
	}
	// Files the backend owns: the frontend cannot have it, and the reason
	// names the owner.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T2", map[string]interface{}{"squad": "frontend-react"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owned by backend-go") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	// The owning team is fine, and so is un-assigning.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T2", map[string]interface{}{"squad": "backend-go"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodPatch, "/api/tasks/T1", map[string]interface{}{"squad": ""})
	if rec.Code != http.StatusOK || decode(t, rec)["squad"] != nil {
		t.Fatalf("un-assign: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Un-assigning a task the backend's territory owns would be undone on
	// the next save, so it is refused the same way.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T2", map[string]interface{}{"squad": ""})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cannot be un-assigned") {
		t.Fatalf("un-assign owned task: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A move that also changes the files is judged on the new files.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T2", map[string]interface{}{
		"squad": "frontend-react", "files": []string{"web/src/App.tsx"},
	})
	if rec.Code != http.StatusOK || decode(t, rec)["squad"] != "frontend-react" {
		t.Fatalf("files+squad: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A team that is not on the chart is a 400, not a silent stamp.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T1", map[string]interface{}{"squad": "platform"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown team: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Any other patch leaves the stamp alone.
	rec = do(t, s, http.MethodPatch, "/api/tasks/T2", map[string]interface{}{"title": "renamed"})
	if rec.Code != http.StatusOK || decode(t, rec)["squad"] != "frontend-react" {
		t.Fatalf("title patch moved the stamp: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// The preselect preview says what the selection does to the run and who
// staffs each team, in the same words the composition will carry.
func TestPreselectReportsModeAndStaffing(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/preselect", map[string]interface{}{
		"query": "add a Go API endpoint and the React page that calls it",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["mode"] != "parallel" {
		t.Fatalf("mode=%v note=%v", body["mode"], body["note"])
	}
	staffed, _ := body["teams"].([]interface{})
	if len(staffed) != 2 {
		t.Fatalf("teams=%v", body["teams"])
	}
	first, _ := staffed[0].(map[string]interface{})
	if first["manager"] != "triage" || first["manager_default"] != true {
		t.Fatalf("staffing=%v", first)
	}

	// A pin is additive: the workspace's own markers still put the other half
	// on the run, and the pinned team leads.
	rec = do(t, s, http.MethodPost, "/api/teams/preselect", map[string]interface{}{
		"query": "tidy the module manifest", "pinned": []string{"frontend-react"},
	})
	body = decode(t, rec)
	selected, _ := body["selected"].([]interface{})
	if body["mode"] != "parallel" || len(selected) == 0 || selected[0] != "frontend-react" {
		t.Fatalf("pinned first: mode=%v selected=%v", body["mode"], body["selected"])
	}
}

// The mode line is the one sentence the page leads with, so each shape has
// its own words.
func TestTeamModeNoteSaysWhatTheSelectionDoes(t *testing.T) {
	one := teams.Selection{Teams: []teams.Team{{ID: "backend-go"}}}
	two := teams.Selection{Teams: []teams.Team{{ID: "backend-go"}, {ID: "frontend-react"}}}
	if mode, note := teamModeNote(teams.Selection{}, true); mode != "" || !strings.Contains(note, "no team matched") {
		t.Fatalf("none: %q %q", mode, note)
	}
	if mode, note := teamModeNote(one, true); mode != "single" || !strings.Contains(note, "staffs the run") {
		t.Fatalf("one: %q %q", mode, note)
	}
	if mode, note := teamModeNote(two, true); mode != "parallel" || !strings.Contains(note, "in parallel") {
		t.Fatalf("two: %q %q", mode, note)
	}
	if mode, note := teamModeNote(two, false); mode != "" || !strings.Contains(note, "squads: false") {
		t.Fatalf("two, teams off: %q %q", mode, note)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The plan on disk is a project fact; the timeline only attributes it to a
// run whose board was built under it.
func TestActivityIgnoresAPlanTheRunDidNotUse(t *testing.T) {
	s, root := teamServer(t)
	if err := squads.Save(filepath.Join(root, ".slmcode"), twoTeamPlan()); err != nil {
		t.Fatal(err)
	}
	// An empty board is the post-Activate, pre-run state: the chart shows.
	body := decode(t, do(t, s, http.MethodGet, "/api/teams/activity", nil))
	if managers, _ := body["managers"].([]interface{}); len(managers) != 2 {
		t.Fatalf("empty board should show the activated chart: %v", body["managers"])
	}
	// A board with tasks and no team stamps ran as one stream: no managers.
	rec := do(t, s, http.MethodPost, "/api/tasks", map[string]interface{}{"id": "T1", "title": "solo", "role": "worker"})
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	body = decode(t, do(t, s, http.MethodGet, "/api/teams/activity", nil))
	if managers, _ := body["managers"].([]interface{}); len(managers) != 0 {
		t.Fatalf("single-stream run must not report the saved chart's managers: %v", body["managers"])
	}
	if teams, _ := body["teams"].([]interface{}); len(teams) != 0 {
		t.Fatalf("teams=%v", body["teams"])
	}
}
