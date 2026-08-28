package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// The manager path, end to end against an in-process fake model.
//
// A tester that rejects the SAME defect twice is the case that used to loop: the
// deterministic router hands the second ticket back to the agent that just
// failed at it, carrying a ticket whose only new content is that it failed
// again. This drives the whole chain — tester rejects, ticket is raised, the
// same defect comes back, the backend team's own project manager decides who
// takes it and what to do differently — with nothing stubbed inside the harness.

// pmModel answers like squadModel, except its tester rejects the first two
// gate runs with one identical defect and its manager staffs the backend team
// with its own project manager.
type pmModel struct {
	mu      sync.Mutex
	byRole  map[string]int
	prompts map[string][]string
	// root is the repo under test: the tester's verdict is read off DISK rather
	// than counted, so it is the same answer however many times the harness
	// asks — a speculative race, a re-verify after a fix, a retry all agree.
	root string
}

func (m *pmModel) record(role, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byRole == nil {
		m.byRole = map[string]int{}
		m.prompts = map[string][]string{}
	}
	m.byRole[role]++
	m.prompts[role] = append(m.prompts[role], prompt)
}

func (m *pmModel) calls() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.byRole {
		out[k] = v
	}
	return out
}

func (m *pmModel) promptsFor(role string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.prompts[role]...)
}

// theBugIsFixed reports whether the encoder defect is gone from disk.
func (m *pmModel) theBugIsFixed() bool {
	body, err := os.ReadFile(filepath.Join(m.root, backendFile)) // #nosec G304 -- temp fixture
	return err == nil && strings.Contains(string(body), "json.NewEncoder")
}

// testerVerdict rejects the same defect until it is actually fixed on disk.
func (m *pmModel) testerVerdict() string {
	if m.theBugIsFixed() {
		return `{"passed":true,"commands":["go build ./..."],"summary":"both halves present","failures":[]}`
	}
	return `{"passed":false,"commands":["go build ./..."],` +
		`"summary":"the API does not compile",` +
		`"failures":["` + backendFile + `:7: undefined: json.NewEncoder"]}`
}

// pmSquadPlanJSON staffs the backend team with its own project manager.
const pmSquadPlanJSON = `{"squads":[` +
	`{"id":"backend","owns":["cmd/**","internal/**","go.mod"],"acceptance":"echo backend-ok",` +
	`"charter":"Go HTTP API","name":"Backend","worker":"go-worker","manager":"triage"},` +
	`{"id":"frontend","owns":["web/**"],"acceptance":"echo frontend-ok",` +
	`"charter":"React SPA","name":"Frontend","worker":"react-worker"}],` +
	`"contract":{"interfaces":[{"id":"GET /api/todos","provider":"backend","consumers":["frontend"],` +
	`"spec":"200 -> [{id,title,done}]"}],"summary":"JSON over /api"},` +
	`"integration":{"acceptance":"echo integrated","notes":["the API serves web/dist"]},` +
	`"summary":"Todo app: Go API + React SPA"}`

func (m *pmModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if serveModelsList(w, r) {
		return
	}
	req, all, system, hasTools, sawToolResult := readChatRequest(w, r)
	if req == nil {
		return
	}

	role := pmRoleOf(system, all)
	m.record(role, all)

	var content string
	var call map[string]any
	switch role {
	case "manager":
		content = pmSquadPlanJSON
	case "triage":
		// The manager's verdict: somebody else, and what to do differently.
		// It picks off the ROSTER it was shown, as a real one must — an agent
		// that is not on it cannot be dispatched, and the harness refuses the
		// verdict rather than parking the ticket on nobody.
		content = `{"assignee":"` + pickFromRoster(all, "go-worker") + `",` +
			`"reason":"the worker cannot see its own encoder bug",` +
			`"guidance":"Set Content-Type, then json.NewEncoder(w).Encode(todos). Do not build the JSON by hand.",` +
			`"priority":"high"}`
	case "tester", "verifier":
		if hasTools && !sawToolResult {
			call = toolCall("ws_shell", map[string]any{"command": "ls"})
		} else {
			content = "Observation: ws_shell `ls` exit status 0\n" + m.testerVerdict()
		}
	case "corrector":
		// The specialist the manager picked: it writes the fix the guidance
		// described, which is what finally turns the tester green.
		if hasTools && !sawToolResult {
			call = toolCall("ws_write", map[string]any{
				"path": backendFile,
				"content": "package main\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n)\n\n" +
					"func main() {\n\thttp.HandleFunc(\"/api/todos\", func(w http.ResponseWriter, r *http.Request) {\n" +
					"\t\tw.Header().Set(\"Content-Type\", \"application/json\")\n" +
					"\t\t_ = json.NewEncoder(w).Encode([]any{})\n\t})\n}\n",
			})
		} else {
			content = `{"status":"done","summary":"encoded with json.NewEncoder","files_changed":["` +
				backendFile + `"],"notes":""}`
		}
	default:
		content, call = squadAnswerFor(role, all, hasTools, sawToolResult)
	}

	if req.Stream {
		writeStreamedCompletion(w, content, call)
		return
	}
	writeCompletion(w, content, call)
}

// pickFromRoster returns the first agent on the prompt's ROSTER that is not the
// one holding the task, mirroring what the triage prompt demands.
func pickFromRoster(prompt, holding string) string {
	i := strings.Index(prompt, "## ROSTER")
	if i < 0 {
		return plan.RoleCorrector
	}
	for _, line := range strings.Split(prompt[i:], "\n") {
		id, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
		if !ok {
			continue
		}
		// Entries are labeled: "- go-corrector (Go specialist)".
		id = strings.TrimSpace(strings.SplitN(id, " (", 2)[0])
		if id == "" || strings.EqualFold(id, holding) {
			continue
		}
		return id
	}
	return plan.RoleCorrector
}

// pmRoleOf adds the project manager to the role detection, which every other
// specialist shares with the plain squad harness.
func pmRoleOf(system, all string) string {
	// The triage contract is the only one that names an assignee.
	if strings.Contains(system, `{"assignee"`) || strings.Contains(system, "One delivery was rejected") {
		return "triage"
	}
	if strings.Contains(system, `"squads":[{"id":"backend"`) {
		return "manager"
	}
	return squadRoleOf(system, all)
}

func TestARejectedDeliveryReachesTheProjectManager(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A Go corrector the run can actually dispatch. Without one the only Go
	// implementer is the go-worker that already failed, and there is no
	// specialist for the manager to hand the ticket to.
	agentsDir := filepath.Join(root, ".slmcode", "agents")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "go-corrector.yaml"), []byte(
		"id: go-corrector\ntitle: Go corrector\ndescription: Fixes failing Go code.\n"+
			"system_prompt: |\n  Somebody else's Go code is failing. Fix it.\n"+
			"tools: true\nmax_iter: 8\ntemperature: 0.12\nmax_tokens: 2048\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The tester rejects the same defect until it is actually fixed on disk:
	// the first rejection raises the ticket, the next makes it a repeat — which
	// is what the manager exists for.
	model := &pmModel{root: root}
	server := httptest.NewServer(model)
	defer server.Close()

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = fakeModelID
	cfg.APIKey = "test-key"
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	cfg.Squads = true
	cfg.ClarifyMode = "off"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 0 // no planner revision: the ticket path is what is under test
	cfg.MaxParallel = 2
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 30 * time.Second
	cfg.Normalize()

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	// The auto-applied Go pack turns the QA gate back on, and a green
	// `go test ./...` would finish the run between waves before the tester ever
	// rejects anything. The tester path is what is under test here.
	cfg.QAGate = false
	cfg.QAGateCommand = ""
	cfg.ThinkPasses = 0
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The narrative, kept for a failure and silent otherwise. A run this long
	// is unreadable from assertion messages alone, and re-running it with
	// logging bolted back on is how an hour goes.
	var evMu sync.Mutex
	var events []string
	orch.OnEvent(func(ev orchestrator.Event) {
		evMu.Lock()
		defer evMu.Unlock()
		switch ev.Kind {
		case "phase", "output", "loop", "success":
			events = append(events, ev.Phase+"/"+ev.Kind+": "+ev.Message)
		}
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		evMu.Lock()
		defer evMu.Unlock()
		for _, e := range events {
			t.Logf("EVENT %s", e)
		}
	})
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		t.Fatalf("install orchestrator: %v", cerr)
	}
	defer func() { _ = h.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	res, err := h.Run(ctx, "build a todo app: a Go backend serving a React frontend")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("model calls: %v", model.calls())
	for _, task := range res.Board.Tasks {
		t.Logf("task %s role=%s squad=%s col=%s key=%q notes=%q",
			task.ID, task.Role, task.Squad, task.Column,
			plan.CorrectionKeyOf(task), firstLineOf(task.Notes))
	}

	// 1. The team was staffed with its own manager, and that survived to disk.
	saved, ok, err := squads.Load(filepath.Join(root, ".slmcode"))
	if err != nil || !ok {
		t.Fatalf("the squad plan should be on disk: ok=%v err=%v", ok, err)
	}
	if got := squads.StaffingFor(&saved, "backend").Manager; got != "triage" {
		t.Fatalf("backend manager = %q, want the one the plan named", got)
	}

	// 2. The tester's rejections raised a correction ticket rather than an
	//    alarm, and the repeat reached the project manager.
	if model.calls()["triage"] == 0 {
		t.Fatal("a repeat defect never reached the project manager")
	}

	// 3. The manager was given what makes it better than the router: which team
	//    it answers for, the roster, and the tester's actual finding.
	asked := model.promptsFor("triage")
	if len(asked) == 0 {
		t.Fatal("no triage prompt was recorded")
	}
	got := asked[0]
	for _, want := range []string{
		"project manager for the backend team",
		"ROSTER — pick exactly one of these",
		"undefined: json.NewEncoder",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the triage prompt is missing %q:\n%s", want, got)
		}
	}
	// The roster leads with the language of the failing files. Sorted by name
	// the generics would come first, and a small model takes the first
	// plausible entry it reads.
	if gi, ri := strings.Index(got, "- go-"), strings.Index(got, "- react-"); gi < 0 || ri < 0 || gi > ri {
		t.Errorf("the Go specialists should come before the React ones:\n%s", got)
	}
	if gi, ci := strings.Index(got, "- go-"), strings.Index(got, "- corrector"); gi < 0 || (ci >= 0 && gi > ci) {
		t.Errorf("a generic agent outranked the Go specialists:\n%s", got)
	}
	if !strings.Contains(got, "(Go specialist)") || !strings.Contains(got, "(generic)") {
		t.Errorf("the roster is not labeled, so the model cannot tell them apart:\n%s", got)
	}

	// 4. The verdict was applied: the ticket changed hands to whoever the
	//    manager picked off the roster, and carries the guidance the last
	//    attempt did not have.
	var reassigned *plan.Task
	for i := range res.Board.Tasks {
		if strings.Contains(res.Board.Tasks[i].Notes, "reassigned-to: ") {
			reassigned = &res.Board.Tasks[i]
			break
		}
	}
	if reassigned == nil {
		t.Fatal("no ticket changed hands after the manager's verdict")
	}
	// Not the agent that already failed at this twice — and not a generic one
	// either: a generic corrector handed a failing Go handler brings nothing
	// the Go worker that already failed did not have.
	if strings.EqualFold(reassigned.Role, "go-worker") {
		t.Errorf("Role = %q — the ticket went back to the agent that just failed", reassigned.Role)
	}
	if !strings.HasPrefix(strings.ToLower(reassigned.Role), "go-") {
		t.Errorf("Role = %q, want a Go specialist for a failing .go file", reassigned.Role)
	}
	if !strings.Contains(reassigned.Notes, "routed by the project manager") {
		t.Errorf("the handoff was not attributed to the manager: %q", reassigned.Notes)
	}
	if got := plan.CorrectionAttemptOf(*reassigned); got < 2 {
		t.Errorf("correction attempt = %d, want the repeat to be recorded", got)
	}
	if !strings.Contains(reassigned.Description, "From the project manager") ||
		!strings.Contains(reassigned.Description, "json.NewEncoder(w).Encode(todos)") {
		t.Errorf("the manager's guidance did not reach the next agent:\n%s", reassigned.Description)
	}
	// The direction goes above the evidence, or it gets skimmed past.
	pmAt := strings.Index(reassigned.Description, "From the project manager")
	failAt := strings.Index(reassigned.Description, "## What failed")
	if failAt >= 0 && pmAt > failAt {
		t.Error("the guidance should come before the failure evidence, not after it")
	}

	// 5. And the run still finished with both halves on disk: triage is a
	//    staffing decision inside the run, not a detour out of it.
	for _, f := range []string{backendFile, frontendFile} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s was never written: %v", f, err)
		}
	}

	// 6. THE POINT OF ALL OF IT: the specialist the manager picked actually
	//    ran, and the defect is gone.
	//
	// A run that ticketed the defect, picked the right specialist and then
	// stopped without running it has done the analysis and thrown it away. The
	// user sees "finished" over a bug still on disk and a ticket nobody
	// touched, which is worse than never having triaged at all.
	if !model.theBugIsFixed() {
		t.Error("the run finished with the defect still on disk — the manager's pick never ran")
	}
	for _, task := range res.Board.Tasks {
		if task.Column == plan.ColReadyToDev && plan.CorrectionKeyOf(task) != "" {
			t.Errorf("%s: the run finished leaving a correction ticket unstarted (role=%s)",
				task.ID, task.Role)
		}
	}
}
