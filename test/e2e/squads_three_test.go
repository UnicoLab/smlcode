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

// Three teams, three languages, one application.
//
// "Build an app with a Go backend serving a React frontend" is the headline
// case, but it is the EASY one: two lanes, one seam. This is the shape a real
// product has — a Go API, a React SPA and a Python ETL job that feeds it — and
// it is where the parts that look fine with two teams start to matter:
//
//   - three disjoint lanes to validate rather than one pair;
//   - three language specialists to route to, from file extensions alone;
//   - a fence that must keep the team NOT in the wave out, not just "the other
//     one";
//   - two providers on the contract instead of one, so a broken seam has to be
//     attributed rather than guessed.

const (
	goFile     = "cmd/server/main.go"
	reactFile  = "web/src/App.tsx"
	pythonFile = "etl/load.py"
)

type threeTeamModel struct {
	mu      sync.Mutex
	byRole  map[string]int
	prompts map[string][]string
}

func (m *threeTeamModel) record(role, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byRole == nil {
		m.byRole = map[string]int{}
		m.prompts = map[string][]string{}
	}
	m.byRole[role]++
	m.prompts[role] = append(m.prompts[role], prompt)
}

func (m *threeTeamModel) calls() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.byRole {
		out[k] = v
	}
	return out
}

func (m *threeTeamModel) promptsFor(role string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.prompts[role]...)
}

const threeSquadPlanJSON = `{"squads":[` +
	`{"id":"backend","owns":["cmd/**","internal/**","go.mod"],"acceptance":"echo backend-ok",` +
	`"charter":"Go HTTP API","name":"Backend","worker":"go-worker"},` +
	`{"id":"frontend","owns":["web/**"],"acceptance":"echo frontend-ok",` +
	`"charter":"React SPA","name":"Frontend","worker":"react-worker"},` +
	`{"id":"data","owns":["etl/**"],"acceptance":"echo data-ok",` +
	`"charter":"Python ETL","name":"Data","worker":"python-worker"}],` +
	`"contract":{"interfaces":[` +
	`{"id":"GET /api/todos","provider":"backend","consumers":["frontend"],"spec":"200 -> [{id,title,done}]"},` +
	`{"id":"todos.parquet","provider":"data","consumers":["backend"],"spec":"columns: id,title,done"}],` +
	`"summary":"JSON over /api, parquet on disk"},` +
	`"integration":{"acceptance":"echo integrated","notes":["the API reads the parquet the ETL writes"]},` +
	`"summary":"Todo app: Go API + React SPA + Python ETL"}`

const threeSplitJSON = `{"tasks":[` +
	`{"id":"T1","title":"serve the todo API","description":"Create ` + goFile +
	` with package main and func main.","role":"worker","files":["` + goFile + `"],` +
	`"acceptance":"` + goFile + ` exists","depends_on":[]},` +
	`{"id":"T2","title":"todo list view","description":"Create ` + reactFile +
	` exporting a default App component.","role":"worker","files":["` + reactFile + `"],` +
	`"acceptance":"` + reactFile + ` exists","depends_on":[]},` +
	`{"id":"T3","title":"load todos","description":"Create ` + pythonFile +
	` with a load() function.","role":"worker","files":["` + pythonFile + `"],` +
	`"acceptance":"` + pythonFile + ` exists","depends_on":[]}]}`

func (m *threeTeamModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		content = threeSquadPlanJSON
	case "splitter", "tasks":
		content = threeSplitJSON
	case "worker", "deep", "corrector", "editor":
		// Each worker writes the half it was briefed on, and nothing else.
		target, body := threeTarget(all)
		if hasTools && !sawToolResult {
			call = toolCall("ws_write", map[string]any{"path": target, "content": body})
		} else {
			content = `{"status":"done","summary":"wrote ` + target + `","files_changed":["` + target + `"],"notes":""}`
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

// threeTarget picks the half from the worker's OWN task description.
//
// Not from a substring search over the whole prompt: the pack carries a shared
// handoff naming sibling tasks' files, so a scan for "etl/load.py" matches the
// React worker's pack too — and a fake that writes whatever it sees mentioned
// is not modeling a worker, it is modeling a worker that ignores its brief.
func threeTarget(prompt string) (path, body string) {
	switch {
	case strings.Contains(prompt, "Create "+pythonFile):
		return pythonFile, "def load():\n    return []\n"
	case strings.Contains(prompt, "Create "+reactFile):
		return reactFile, "export default function App() { return null }\n"
	case strings.Contains(prompt, "Create "+goFile):
		return goFile, "package main\n\nfunc main() {}\n"
	}
	// A scaffolding task the language pack added: write whatever it named.
	for _, f := range []string{"tests/test_smoke.py", "main.py", "requirements.txt"} {
		if strings.Contains(prompt, f) {
			return f, "# generated\n"
		}
	}
	return goFile, "package main\n\nfunc main() {}\n"
}

func TestThreeTeamsThreeLanguagesOneApplication(t *testing.T) {
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

	model := &threeTeamModel{}
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
	cfg.MaxParallel = 3 // all three teams may run at once
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
		events = append(events, ev.Phase+"/"+ev.Kind+": "+ev.Message)
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
	res, err := h.Run(ctx, "build a todo app: a Go API serving a React frontend, fed by a Python ETL job")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("model calls: %v", model.calls())
	t.Logf("summary: %s", res.Summary)
	for _, task := range res.Board.Tasks {
		t.Logf("TASK %s role=%s squad=%q col=%s files=%v title=%q", task.ID, task.Role, task.Squad, task.Column, task.Files, task.Title)
	}

	// 1. Three teams were assembled and validated. An overlap anywhere would
	//    have collapsed the run to a single stream.
	saved, ok, lerr := squads.Load(filepath.Join(root, ".slmcode"))
	if lerr != nil || !ok {
		t.Fatalf("the squad plan should be on disk: ok=%v err=%v", ok, lerr)
	}
	if got := saved.IDs(); len(got) != 3 {
		t.Fatalf("saved squads = %v, want three teams", got)
	}

	// 2. Every task landed with its owning team AND its language specialist.
	//    A single run-level pick cannot do this: one repo, three languages.
	want := map[string]struct{ squad, rolePrefix string }{
		goFile:     {"backend", "go-"},
		reactFile:  {"frontend", "react-"},
		pythonFile: {"data", "python-"},
	}
	seen := map[string]bool{}
	for _, task := range res.Board.Tasks {
		for _, f := range task.Files {
			exp, tracked := want[f]
			if !tracked {
				continue
			}
			seen[f] = true
			if task.Squad != exp.squad {
				t.Errorf("%s: squad = %q, want %q", f, task.Squad, exp.squad)
			}
			if !strings.HasPrefix(strings.ToLower(task.Role), exp.rolePrefix) {
				t.Errorf("%s: role = %q, want a %s specialist", f, task.Role, exp.rolePrefix)
			}
		}
	}
	for f := range want {
		if !seen[f] {
			t.Errorf("no task ever owned %s", f)
		}
	}

	// 2b. A manifest routes by its NAME. `requirements.txt` has no extension to
	//     go on, and used to land on whatever the run picked as its default —
	//     a Go worker editing a Python dependency list.
	for _, task := range res.Board.Tasks {
		for _, f := range task.Files {
			if strings.HasSuffix(f, "requirements.txt") &&
				!strings.HasPrefix(strings.ToLower(task.Role), "python-") {
				t.Errorf("%s: role = %q for %s, want the Python specialist", task.ID, task.Role, f)
			}
		}
	}

	// 3. All three halves are on disk. This is the point.
	for _, f := range []string{goFile, reactFile, pythonFile} {
		if _, serr := os.Stat(filepath.Join(root, f)); serr != nil {
			t.Errorf("%s was never written: %v", f, serr)
		}
	}

	// 4. The contract froze BOTH seams before anyone started — the reason
	//    three teams can build at once without inventing three answers.
	contract, cerr := os.ReadFile(filepath.Join(root, ".slmcode", squads.ContractFile)) // #nosec G304 -- temp fixture
	if cerr != nil {
		t.Fatalf("CONTRACT.md must exist: %v", cerr)
	}
	for _, clause := range []string{"GET /api/todos", "todos.parquet", "Provided by: `data`"} {
		if !strings.Contains(string(contract), clause) {
			t.Errorf("CONTRACT.md is missing %q", clause)
		}
	}

	// 5. Each worker was briefed on ITS half and told to stay out of the
	//    others. A worker handed all three charters spends its attention on
	//    the two teams it is not on.
	briefs := model.promptsFor("worker")
	if len(briefs) == 0 {
		t.Fatal("no worker prompts recorded")
	}
	for _, b := range briefs {
		mine := strings.Count(b, "Your team")
		if mine > 1 {
			t.Errorf("a worker was handed more than one charter:\n%s", b)
		}
	}

	// 6. And the run reports honestly: three teams, all complete.
	evMu.Lock()
	joined := strings.Join(events, "\n")
	evMu.Unlock()
	if !strings.Contains(joined, "3 squads") {
		t.Errorf("the charter event does not report three teams:\n%s", firstMatching(events, "charter"))
	}
	for _, team := range []string{"backend", "frontend", "data"} {
		if !strings.Contains(joined, team+" 1/1 done") {
			t.Errorf("%s never reported complete:\n%s", team, firstMatching(events, "squads:"))
		}
	}
	if n := countOpen(res.Board.Tasks); n != 0 {
		t.Errorf("%d task(s) left open on a run where every half succeeded", n)
	}
}

func firstMatching(events []string, needle string) string {
	var out []string
	for _, e := range events {
		if strings.Contains(e, needle) {
			out = append(out, e)
		}
	}
	return strings.Join(out, "\n")
}

func countOpen(tasks []plan.Task) int {
	n := 0
	for _, t := range tasks {
		t.Normalize()
		if t.Column != plan.ColDone {
			n++
		}
	}
	return n
}
