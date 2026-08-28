package e2e_test

import (
	"context"
	"encoding/json"
	"io"
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

// A whole run through the squad path against an in-process fake model: the
// manager assembles two teams, the contract lands on disk, the splitter's tasks
// are routed to owners and language specialists, and BOTH halves get written.
//
// Nothing here is stubbed inside the harness. The only fake is the model.

const (
	backendFile  = "cmd/server/main.go"
	frontendFile = "web/src/App.tsx"
)

// squadModel answers per role, and records the prompts each role received so
// the test can assert on what the workers were actually told.
type squadModel struct {
	mu      sync.Mutex
	byRole  map[string]int
	prompts map[string][]string
}

func (m *squadModel) record(role, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byRole == nil {
		m.byRole = map[string]int{}
		m.prompts = map[string][]string{}
	}
	m.byRole[role]++
	m.prompts[role] = append(m.prompts[role], prompt)
}

func (m *squadModel) promptsFor(role string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.prompts[role]...)
}

func (m *squadModel) calls() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.byRole {
		out[k] = v
	}
	return out
}

func (m *squadModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "data": []map[string]any{{"id": fakeModelID, "object": "model"}},
		})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(raw, &req)

	var all strings.Builder
	sawToolResult := false
	for _, msg := range req.Messages {
		all.WriteString(msg.Content)
		all.WriteByte('\n')
		if msg.Role == "tool" {
			sawToolResult = true
		}
	}
	system := ""
	if len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	role := squadRoleOf(system, all.String())
	m.record(role, all.String())

	content, call := squadAnswerFor(role, all.String(), len(req.Tools) > 0, sawToolResult)
	if req.Stream {
		writeStreamedCompletion(w, content, call)
		return
	}
	writeCompletion(w, content, call)
}

// squadRoleOf identifies the asking specialist from its own output contract.
func squadRoleOf(system, all string) string {
	switch {
	// The manager's contract is the only one that names "squads".
	case strings.Contains(system, `"squads":[{"id":"backend"`):
		return "manager"
	case strings.Contains(system, `"passed"`):
		return "tester"
	case strings.Contains(system, `{"approved"`), strings.Contains(system, "never approved"):
		return "reviewer"
	case strings.Contains(system, `"tasks":[{"id":"T1"`):
		return "splitter"
	case strings.Contains(system, `"steps":["step one"`):
		return "planner"
	case strings.Contains(system, `"relevant_files"`):
		return "explorer"
	case strings.Contains(system, `"doc_files"`):
		return "docs"
	case strings.Contains(system, `"files_changed"`):
		return "worker"
	}
	if m := packRoleRe.FindStringSubmatch(all); m != nil {
		r := m[1]
		for _, prefix := range []string{"go-", "python-", "react-", "ts-"} {
			r = strings.TrimPrefix(r, prefix)
		}
		return r
	}
	return "prose"
}

const squadPlanJSON = `{"squads":[` +
	`{"id":"backend","owns":["cmd/**","internal/**","go.mod"],"acceptance":"echo backend-ok",` +
	`"charter":"Go HTTP API","name":"Backend","worker":"go-worker"},` +
	`{"id":"frontend","owns":["web/**"],"acceptance":"echo frontend-ok",` +
	`"charter":"React SPA","name":"Frontend","worker":"react-worker"}],` +
	`"contract":{"interfaces":[{"id":"GET /api/todos","provider":"backend","consumers":["frontend"],` +
	`"spec":"200 -> [{id,title,done}]"}],"summary":"JSON over /api"},` +
	`"integration":{"acceptance":"echo integrated","notes":["the API serves web/dist"]},` +
	`"summary":"Todo app: Go API + React SPA"}`

func squadAnswerFor(role, all string, hasTools, sawToolResult bool) (string, map[string]any) {
	switch role {
	case "manager":
		return squadPlanJSON, nil
	case "explorer":
		return `{"summary":"empty repo","relevant_files":["go.mod"],"key_symbols":[],"risks":[],"notes":""}`, nil
	case "docs":
		return `{"summary":"none","doc_files":[],"conventions":[],"apis":[],"gaps":[]}`, nil
	case "architect":
		return `{"approach":"go api + react spa","components":["` + backendFile + `","` + frontendFile +
			`"],"interfaces":["GET /api/todos"],"risks":[],"non_goals":[]}`, nil
	case "planner", "plan":
		return `{"summary":"build both halves","steps":["create the Go API","create the React SPA"],` +
			`"goals":[],"assumptions":[],"risks":[]}`, nil
	case "splitter", "tasks":
		// One task per half, each naming only its own files — which is what
		// lets them run in the same wave.
		return `{"tasks":[` +
			`{"id":"T1","title":"create the Go API","description":"Create ` + backendFile +
			` with package main and func main.","role":"worker","files":["` + backendFile +
			`"],"acceptance":"` + backendFile + ` exists","depends_on":[]},` +
			`{"id":"T2","title":"create the React SPA","description":"Create ` + frontendFile +
			` exporting a default App component.","role":"worker","files":["` + frontendFile +
			`"],"acceptance":"` + frontendFile + ` exists","depends_on":[]}]}`, nil
	case "worker", "deep", "corrector", "editor":
		if hasTools && !sawToolResult {
			// Write whichever half this worker was briefed on.
			if strings.Contains(all, frontendFile) {
				return "", toolCall("ws_write", map[string]any{
					"path": frontendFile, "content": "export default function App() { return null }\n",
				})
			}
			return "", toolCall("ws_write", map[string]any{
				"path": backendFile, "content": "package main\n\nfunc main() {}\n",
			})
		}
		if strings.Contains(all, frontendFile) {
			return `{"status":"done","summary":"created the SPA","files_changed":["` + frontendFile + `"],"notes":""}`, nil
		}
		return `{"status":"done","summary":"created the API","files_changed":["` + backendFile + `"],"notes":""}`, nil
	case "reviewer", "reviewer-strict":
		return `{"approved":true,"score":90,"summary":"file written as specified","issues":[]}`, nil
	case "tester", "verifier":
		if hasTools && !sawToolResult {
			return "", toolCall("ws_shell", map[string]any{"command": "ls"})
		}
		return "Observation: ws_shell `ls` exit status 0\n" +
			`{"passed":true,"commands":["ls"],"summary":"both halves present","failures":[]}`, nil
	}
	return "- A Go API and a React SPA.\n", nil
}

func TestSquadsEndToEndAgainstAFakeModel(t *testing.T) {
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

	model := &squadModel{}
	server := httptest.NewServer(model)
	defer server.Close()

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = fakeModelID
	cfg.APIKey = "test-key"
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	cfg.Squads = true // the path under test
	cfg.ClarifyMode = "off"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2 // both squads may run at once
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
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		t.Fatalf("install orchestrator: %v", cerr)
	}
	defer func() { _ = h.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := h.Run(ctx, "build a todo app: a Go backend serving a React frontend")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("model calls: %v", model.calls())

	// 1. The manager ran and its plan was accepted.
	if model.calls()["manager"] == 0 {
		t.Fatal("the manager specialist was never asked to assemble squads")
	}
	slmDir := filepath.Join(root, ".slmcode")
	saved, ok, err := squads.Load(slmDir)
	if err != nil || !ok {
		t.Fatalf("the squad plan should be on disk: ok=%v err=%v", ok, err)
	}
	if got := saved.IDs(); len(got) != 2 || got[0] != "backend" || got[1] != "frontend" {
		t.Fatalf("saved squads = %v", got)
	}

	// 2. The contract was frozen to disk BEFORE the workers ran.
	contract, err := os.ReadFile(filepath.Join(slmDir, squads.ContractFile)) // #nosec G304 -- temp fixture
	if err != nil {
		t.Fatalf("CONTRACT.md must exist: %v", err)
	}
	for _, want := range []string{"FROZEN", "GET /api/todos", "Provided by: `backend`", "Consumed by: `frontend`"} {
		if !strings.Contains(string(contract), want) {
			t.Errorf("CONTRACT.md is missing %q", want)
		}
	}

	for _, task := range res.Board.Tasks {
		t.Logf("task %s role=%s squad=%s col=%s status=%s files=%v err=%q review=%q",
			task.ID, task.Role, task.Squad, task.Column, task.Status, task.Files,
			firstLineOf(task.Error), firstLineOf(task.Review))
	}

	// 3. BOTH halves were written. This is the whole point.
	for _, f := range []string{backendFile, frontendFile} {
		body, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(f))) // #nosec G304 -- temp fixture
		if rerr != nil {
			t.Fatalf("%s was never written: %v", f, rerr)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			t.Errorf("%s is empty", f)
		}
	}

	// 4. Each task carries its owning squad.
	if len(res.Board.Tasks) == 0 {
		t.Fatal("the run returned an empty board")
	}
	bySquad := map[string]int{}
	for _, task := range res.Board.Tasks {
		if task.Squad != "" {
			bySquad[task.Squad]++
		}
	}
	if bySquad["backend"] == 0 || bySquad["frontend"] == 0 {
		t.Errorf("both squads should own work, got %v (tasks: %s)", bySquad, taskSummary(&res.Board))
	}

	// 5. The workers were briefed on their own lane and the other team's.
	joined := strings.Join(model.promptsFor("worker"), "\n---\n")
	for _, want := range []string{"Your squad", "do not edit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("worker prompts never carried %q", want)
		}
	}
	// The frontend worker must have been told what it consumes.
	if !strings.Contains(joined, "GET /api/todos") {
		t.Error("no worker was told about the interface it depends on")
	}
}

func taskSummary(b *plan.Board) string {
	var out []string
	for _, t := range b.Tasks {
		out = append(out, t.ID+"["+t.Role+"/"+t.Squad+"]")
	}
	return strings.Join(out, " ")
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
