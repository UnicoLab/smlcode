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

// Both halves green and the application broken.
//
// This is the failure the frozen contract exists to prevent and the one the
// integration step exists to catch: the backend returns `{items:[…]}` while the
// contract — and therefore the frontend — says a bare array. Every squad's own
// acceptance passes. `go test ./...` passes. `npm run build` passes. The
// assembled app does not work.
//
// A warning at the end of a "successful" run is the worst possible handling of
// that, so this drives the whole path: integration runs, fails, and raises a
// ticket the PROVIDER owns, carrying the command, the output that names the
// seam, and the contract clause at stake.

// seamModel builds both halves, and the backend deliberately drifts from the
// frozen contract.
type seamModel struct {
	mu      sync.Mutex
	byRole  map[string]int
	prompts map[string][]string
	root    string
}

func (m *seamModel) record(role, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byRole == nil {
		m.byRole = map[string]int{}
		m.prompts = map[string][]string{}
	}
	m.byRole[role]++
	m.prompts[role] = append(m.prompts[role], prompt)
}

func (m *seamModel) calls() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.byRole {
		out[k] = v
	}
	return out
}

// The integration command is a script the fake writes; it fails while the
// backend still returns the wrong shape, which is the whole point.
const seamIntegration = "sh scripts/integrate.sh"

const seamPlanJSON = `{"squads":[` +
	`{"id":"backend","owns":["cmd/**","internal/**","go.mod"],"acceptance":"echo backend-ok",` +
	`"charter":"Go HTTP API","name":"Backend","worker":"go-worker"},` +
	`{"id":"frontend","owns":["web/**"],"acceptance":"echo frontend-ok",` +
	`"charter":"React SPA","name":"Frontend","worker":"react-worker"}],` +
	`"contract":{"interfaces":[{"id":"GET /api/todos","provider":"backend","consumers":["frontend"],` +
	`"spec":"200 -> [{id,title,done}] — a bare array, not an envelope"}],"summary":"JSON over /api"},` +
	`"integration":{"acceptance":"` + seamIntegration + `","notes":["the API serves web/dist"]},` +
	`"summary":"Todo app: Go API + React SPA"}`

func (m *seamModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if serveModelsList(w, r) {
		return
	}
	req, all, system, hasTools, sawToolResult := readChatRequest(w, r)
	role := pmRoleOf(system, all)
	m.record(role, all)

	var content string
	var call map[string]any
	switch role {
	case "manager":
		content = seamPlanJSON
	case "tester", "verifier":
		// Each half passes its OWN acceptance. That is the trap.
		if hasTools && !sawToolResult {
			call = toolCall("ws_shell", map[string]any{"command": "ls"})
		} else {
			content = "Observation: ws_shell `ls` exit status 0\n" +
				`{"passed":true,"commands":["echo backend-ok","echo frontend-ok"],` +
				`"summary":"each half passes its own acceptance","failures":[]}`
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

func TestABrokenSeamBecomesATicketTheProviderOwns(t *testing.T) {
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
	// The join step: it reports the drift the way a real integration check
	// would — naming the file, the shape it found and the shape it wanted.
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo 'cmd/server/main.go:12: GET /api/todos returned {\"items\":[]}, the contract says a bare array'\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(root, "scripts", "integrate.sh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	model := &seamModel{root: root}
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
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.MaxParallel = 2
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 30 * time.Second
	cfg.ShellAllow = append(cfg.ShellAllow, "sh")
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
	// `go test ./...` would finish the run before the halves are ever joined.
	cfg.QAGate = false
	cfg.QAGateCommand = ""
	cfg.ThinkPasses = 0
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var evMu sync.Mutex
	var events []string
	orch.OnEvent(func(ev orchestrator.Event) {
		evMu.Lock()
		defer evMu.Unlock()
		switch ev.Kind {
		case "phase", "output", "loop", "warning", "success":
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
	t.Logf("summary: %s", res.Summary)

	// 1. The join step actually ran. Two green halves are not a green app, and
	//    a plan with an integration command that never runs is a promise the
	//    harness did not keep.
	evMu.Lock()
	joined := strings.Join(events, "\n")
	evMu.Unlock()
	if !strings.Contains(joined, "joining the halves") {
		t.Fatalf("the halves were never joined:\n%s", joined)
	}
	if !strings.Contains(joined, "INTEGRATION FAILED") {
		t.Fatalf("a broken seam passed integration:\n%s", joined)
	}

	// 2. It produced a ticket, not just a warning at the end of a "successful"
	//    run — and the PROVIDER owns it, because the consumer built against
	//    text it was handed.
	var ticket *plan.Task
	for i := range res.Board.Tasks {
		if strings.Contains(res.Board.Tasks[i].Description, "integration gate rejected") {
			ticket = &res.Board.Tasks[i]
			break
		}
	}
	if ticket == nil {
		t.Fatalf("no integration ticket on the board; tasks=%s", taskSummary(&res.Board))
	}
	// backend-go, not backend: the LIBRARY named the teams and the model wrote
	// `backend` in its contract. That the seam owner still resolves to the real
	// team is the point — a provider reference that missed by a suffix would
	// leave the ticket on nobody.
	if ticket.Squad != "backend-go" {
		t.Errorf("ticket squad = %q, want the team that provides the broken interface", ticket.Squad)
	}
	// A .go file means the Go specialist, not a generic worker.
	if !strings.HasPrefix(strings.ToLower(ticket.Role), "go-") {
		t.Errorf("ticket role = %q, want the specialist the implicated file calls for", ticket.Role)
	}

	// 3. The evidence that used to be thrown away for a synthetic "qa_gate red".
	for _, want := range []string{
		seamIntegration,      // how to reproduce it
		"the contract says",  // what the join step printed
		"GET /api/todos",     // which clause is at stake
		"cmd/server/main.go", // where
	} {
		if !strings.Contains(ticket.Description, want) {
			t.Errorf("the integration ticket is missing %q:\n%s", want, ticket.Description)
		}
	}
	if len(ticket.Files) == 0 || !strings.Contains(strings.Join(ticket.Files, ","), "cmd/") {
		t.Errorf("ticket files = %v, want the owning team's implicated file", ticket.Files)
	}

	// 4. The run says so where people read it. Without this the summary is
	//    "0 failed" over a broken application and the Fixes tab is empty —
	//    the exact silence that makes a user stop trusting either.
	if !strings.Contains(res.Summary, "1 defect found") {
		t.Errorf("the summary hides a broken application: %q", res.Summary)
	}
	if res.Repairs == nil || res.Repairs.Found != 1 || res.Repairs.Resolved != 0 {
		t.Errorf("Repairs = %+v, want the seam defect recorded and unresolved", res.Repairs)
	}

	// 5. And the frozen contract really did reach disk before either half ran,
	//    which is the only reason both halves could have agreed in the first
	//    place.
	contract, err := os.ReadFile(filepath.Join(root, ".slmcode", squads.ContractFile)) // #nosec G304 -- temp fixture
	if err != nil {
		t.Fatalf("CONTRACT.md must exist: %v", err)
	}
	if !strings.Contains(string(contract), "a bare array, not an envelope") {
		t.Errorf("the contract text the halves built against is not on disk:\n%s", contract)
	}
}
