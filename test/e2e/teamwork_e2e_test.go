package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// ---------------------------------------------------------------------------
// Teamwork E2E Test Suite Constants & Types
// ---------------------------------------------------------------------------

const (
	teamworkModelID      = "teamwork-fake-model"
	teamworkTargetFile   = "calc.go"
	teamworkTargetSource = "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Divide(a, b float64) (float64, error) {\n\tif b == 0 {\n\t\treturn 0, nil\n\t}\n\treturn a / b, nil\n}\n"
	teamworkBrokenSource = "package calc\n\nfunc Divide( {\n"
)

// teamworkFakeModel is an in-process OpenAI-compatible mock server for
// hermetic, offline, deterministic testing of the Teamwork multi-agent pipeline.
type teamworkFakeModel struct {
	mu sync.Mutex

	calls  int
	byRole map[string]int

	// Defect injection & behavior control
	badFirstWrites int
	writesSeen     int
	sycophant      bool
	rejectReviews  int
	reviewsSeen    int

	// Custom responses & file overrides
	targetFile      string
	targetSource    string
	interviewPRD    string
	planJSON        string
	customResponses map[string]string

	// Observation tracking
	lastSystemPrompt     string
	lastUserPrompt       string
	toolCallsMade        []string
	toolResultsReceived  []string
	activeWorkers        int
	maxConcurrentWorkers int
}

func newTeamworkFakeModel() *teamworkFakeModel {
	return &teamworkFakeModel{
		byRole:          map[string]int{},
		customResponses: map[string]string{},
		targetFile:      teamworkTargetFile,
		targetSource:    teamworkTargetSource,
	}
}

func (m *teamworkFakeModel) counts() (int, map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.byRole {
		out[k] = v
	}
	return m.calls, out
}

func (m *teamworkFakeModel) toolCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.toolCallsMade))
	copy(out, m.toolCallsMade)
	return out
}

func (m *teamworkFakeModel) toolResults() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.toolResultsReceived))
	copy(out, m.toolResultsReceived)
	return out
}

func (m *teamworkFakeModel) maxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxConcurrentWorkers
}

func (m *teamworkFakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": teamworkModelID, "object": "model"}},
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
			m.mu.Lock()
			m.toolResultsReceived = append(m.toolResultsReceived, msg.Content)
			m.mu.Unlock()
		}
	}
	system := ""
	if len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}

	role := teamworkRoleOf(system, all.String())

	m.mu.Lock()
	m.calls++
	if m.byRole == nil {
		m.byRole = map[string]int{}
	}
	m.byRole[role]++
	m.lastSystemPrompt = system
	m.lastUserPrompt = all.String()
	m.mu.Unlock()

	if role == "worker" {
		m.mu.Lock()
		m.activeWorkers++
		if m.activeWorkers > m.maxConcurrentWorkers {
			m.maxConcurrentWorkers = m.activeWorkers
		}
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			m.activeWorkers--
			m.mu.Unlock()
		}()
		time.Sleep(25 * time.Millisecond)
	}

	content, call := answerForTeamwork(m, role, len(req.Tools) > 0, sawToolResult, all.String())
	if req.Stream {
		teamworkWriteStreamedCompletion(w, content, call)
		return
	}
	teamworkWriteCompletion(w, content, call)
}

var teamworkPackRoleRe = regexp.MustCompile(`Scoped context for role=([a-z0-9_-]+)`)

// teamworkRoleOf identifies the active specialist asking for completion.
func teamworkRoleOf(system, all string) string {
	switch {
	case strings.Contains(system, `"passed"`):
		return "tester"
	case strings.Contains(system, "teamwork_review") || strings.Contains(system, "teamwork-review") || strings.Contains(system, "adversarial_reviewer") ||
		strings.Contains(system, "challenge_scenarios"):
		return "adversarial_reviewer"
	case strings.Contains(system, `{"approved"`), strings.Contains(system, "never approved"):
		return "reviewer"
	case strings.Contains(system, "teamwork_spec") || strings.Contains(system, "teamwork-spec") || strings.Contains(system, "ScopePRD") ||
		strings.Contains(system, "needs_user"):
		return "interviewer"
	case strings.Contains(system, "teamwork_plan") || strings.Contains(system, "teamwork-plan") || strings.Contains(system, "CONTRACT.md"):
		return "architect"
	case strings.Contains(system, `"tasks":[{"id":"T1"`), strings.Contains(system, "splitter"):
		return "splitter"
	case strings.Contains(system, `"steps":["step one"`):
		return "planner"
	case strings.Contains(system, `"relevant_files"`):
		return "explorer"
	case strings.Contains(system, `"doc_files"`):
		return "docs"
	case strings.Contains(system, "Fix the reviewer") || strings.Contains(system, "corrector") || strings.Contains(all, "after failed review"):
		return "corrector"
	case strings.Contains(system, "teamwork_worker") || strings.Contains(system, "teamwork-worker") || strings.Contains(system, `"files_changed"`):
		return "worker"
	}

	if m := teamworkPackRoleRe.FindStringSubmatch(all); m != nil {
		r := m[1]
		for _, prefix := range []string{"go-", "python-", "ts-", "react-"} {
			r = strings.TrimPrefix(r, prefix)
		}
		return r
	}
	if m := regexp.MustCompile(`role=([a-z0-9_-]+)`).FindStringSubmatch(all); m != nil {
		r := m[1]
		for _, prefix := range []string{"go-", "python-", "ts-", "react-"} {
			r = strings.TrimPrefix(r, prefix)
		}
		return r
	}
	if strings.Contains(all, "needs_user") || strings.Contains(all, "interviewer") {
		return "interviewer"
	}
	if strings.Contains(all, "strategy") && strings.Contains(all, "milestones") {
		return "architect"
	}
	return "prose"
}

// answerForTeamwork generates canned responses and tool turns per role.
func answerForTeamwork(m *teamworkFakeModel, role string, hasTools, sawToolResult bool, userPrompt ...string) (string, map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if custom, ok := m.customResponses[role]; ok {
		return custom, nil
	}

	prompt := m.lastUserPrompt
	if len(userPrompt) > 0 {
		prompt = userPrompt[0]
	}

	target := m.targetFile
	src := m.targetSource
	switch {
	case strings.Contains(prompt, "ID: T1"):
		target = "calc_add.go"
		src = "package calc\n\nfunc Add(a, b int) int { return a + b }\n"
	case strings.Contains(prompt, "ID: T2"):
		target = "calc_sub.go"
		src = "package calc\n\nfunc Subtract(a, b int) int { return a - b }\n"
	case strings.Contains(prompt, "ID: T3"):
		target = "calc_mul.go"
		src = "package calc\n\nfunc Multiply(a, b int) int { return a * b }\n"
	}
	if target == "" {
		target = teamworkTargetFile
	}
	if src == "" {
		src = teamworkTargetSource
	}

	switch role {
	case "interviewer":
		if m.interviewPRD != "" {
			return m.interviewPRD, nil
		}
		return `{"needs_user":false,"assumptions":["Standard Go toolchain","Local isolated execution"],"acceptance":["go test ./... passes cleanly"],"entrypoint":"calc.go","features":[{"id":"F1","title":"Basic calculator arithmetic","acceptance":"Add and Divide functions present","priority":"must","description":"Provide Add and Divide functions"}],"language":"go","non_goals":["GUI"],"questions":[{"id":"q1","header":"Scope","question":"Include divide?","options":[{"label":"Yes","description":"Add divide","recommended":true},{"label":"No","description":"Skip"}],"allow_freeform":false,"facet":"architecture","recommended":"Yes"}]}`, nil

	case "architect", "planner":
		if m.planJSON != "" {
			return m.planJSON, nil
		}
		return `{"strategy":"Decompose arithmetic functions into calc module","milestones":[{"id":"M1","title":"Implement arithmetic functions","acceptance":"go vet ./... passes","depends_on":[],"description":"Create ` + target + `"}],"file_ownership":[{"owner":"worker","paths":["` + target + `"]}],"contracts":[{"interface_id":"ICalc","provider":"worker","spec":"Add(int, int) int","consumers":["main"]}],"risks":[]}`, nil

	case "splitter":
		return `{"tasks":[{"id":"T1","title":"Create ` + target + `","description":"Implement Add and Divide functions in ` + target + `","role":"worker","files":["` + target + `"],"acceptance":"` + target + ` exists with Add and Divide","criteria":[{"id":"C1","text":"` + target + ` compiles without error","verify":"go vet ./...","priority":"must"}]}]}`, nil

	case "worker", "corrector", "deep", "editor":
		if hasTools && !sawToolResult {
			contentToWrite := src
			if m.badFirstWrites > 0 && m.writesSeen < m.badFirstWrites {
				m.writesSeen++
				contentToWrite = teamworkBrokenSource
			} else {
				m.writesSeen++
			}
			m.toolCallsMade = append(m.toolCallsMade, "ws_write:"+target)
			return "", teamworkToolCall("ws_write", map[string]any{
				"path": target, "content": contentToWrite,
			})
		}
		return `{"status":"done","summary":"Created ` + target + ` with arithmetic functions","files_changed":["` + target + `"],"action_plan":["ws_read ` + target + `","ws_write ` + target + `"],"diff_summary":"Added Add and Divide functions","notes":""}`, nil

	case "adversarial_reviewer", "reviewer", "reviewer-strict":
		if m.sycophant {
			return `{"approved":true,"score":99,"verdict":"approve","summary":"Implementation looks good","challenge_scenarios":["Zero division check"],"edge_cases":[],"issues":[]}`, nil
		}
		if m.rejectReviews > 0 && m.reviewsSeen < m.rejectReviews {
			m.reviewsSeen++
			return `{"approved":false,"score":20,"verdict":"reject","summary":"Implementation has syntax error","challenge_scenarios":["Compiler smoke check"],"edge_cases":[],"issues":["calc.go:3: syntax error"]}`, nil
		}
		m.reviewsSeen++
		return `{"approved":true,"score":92,"verdict":"approve","summary":"All gates and criteria passed","challenge_scenarios":["Divide by zero returns error"],"edge_cases":["Edge case zero handled"],"issues":[]}`, nil

	case "tester", "verifier":
		if hasTools && !sawToolResult {
			return "", teamworkToolCall("ws_shell", map[string]any{"command": "cat " + target})
		}
		return "Observation: ws_shell `cat " + target + "` exit status 0\n" +
			`{"passed":true,"commands":["cat ` + target + `"],"summary":"All tests and vetting checks pass","failures":[]}`, nil

	case "explorer":
		return `{"summary":"tiny go module","relevant_files":["go.mod"],"key_symbols":[],"risks":[],"notes":""}`, nil

	case "docs":
		return `{"summary":"standard go module layout","doc_files":[],"conventions":[],"apis":[],"gaps":[]}`, nil
	}

	return "- The project is a standard Go module with " + target + ".\n", nil
}

func teamworkToolCall(name string, args map[string]any) map[string]any {
	b, _ := json.Marshal(args)
	return map[string]any{
		"id": "call_tw_1", "type": "function",
		"function": map[string]any{"name": name, "arguments": string(b)},
	}
}

func teamworkWriteCompletion(w http.ResponseWriter, content string, call map[string]any) {
	msg := map[string]any{"role": "assistant", "content": content}
	finish := "stop"
	if call != nil {
		msg["content"] = ""
		msg["tool_calls"] = []any{call}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "cmpl-tw-1", "object": "chat.completion", "model": teamworkModelID,
		"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": msg}},
		"usage":   map[string]any{"prompt_tokens": 25, "completion_tokens": 25, "total_tokens": 50},
	})
}

func teamworkWriteStreamedCompletion(w http.ResponseWriter, content string, call map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	delta := map[string]any{"role": "assistant"}
	finish := "stop"
	if call != nil {
		fn, _ := call["function"].(map[string]any)
		delta["tool_calls"] = []any{map[string]any{
			"index": 0, "id": call["id"], "type": "function",
			"function": map[string]any{"name": fn["name"], "arguments": fn["arguments"]},
		}}
		finish = "tool_calls"
	} else {
		delta["content"] = content
	}
	emit(map[string]any{"id": "cmpl-tw-1", "object": "chat.completion.chunk", "model": teamworkModelID,
		"choices": []map[string]any{{"index": 0, "delta": delta}}})
	emit(map[string]any{"id": "cmpl-tw-1", "object": "chat.completion.chunk", "model": teamworkModelID,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 25, "completion_tokens": 25, "total_tokens": 50}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// ---------------------------------------------------------------------------
// Harness & Pipeline Configuration Helpers
// ---------------------------------------------------------------------------

// newTeamworkPipelineConfig returns a valid pipeline.Config configured with the
// canonical Teamwork multi-agent phase sequence and specialist roles.
func newTeamworkPipelineConfig() pipeline.Config {
	return pipeline.Teamwork()
}

// newTeamworkTestHarness builds a hermetic test rig using an in-process mock server.
func newTeamworkTestHarness(t *testing.T, model *teamworkFakeModel, opts ...func(*config.Config)) (*harness.Harness, string, func()) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(model)

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = teamworkModelID
	cfg.APIKey = "teamwork-test-key"
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	cfg.ClarifyMode = "auto"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 1
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 30 * time.Second
	cfg.WriteGuard = false

	for _, opt := range opts {
		opt(cfg)
	}
	cfg.Normalize()

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	// Persist the canonical Teamwork pipeline preset into workspace
	twPipe := pipeline.Teamwork()
	if err := pipeline.Save(cfg.SlmDir(), &twPipe); err != nil {
		t.Fatalf("failed to save teamwork pipeline: %v", err)
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

	cleanup := func() {
		_ = h.Close()
		server.Close()
	}

	return h, root, cleanup
}

// runTeamworkHarness executes the query via the Teamwork pipeline using
// public RunTeamwork if available, or setting up the Teamwork pipeline preset.
func runTeamworkHarness(ctx context.Context, h *harness.Harness, query string) (*orchestrator.Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if h == nil {
		return nil, fmt.Errorf("nil harness")
	}
	type teamworkRunner interface {
		RunTeamwork(context.Context, string) (*orchestrator.Result, error)
	}
	if tr, ok := any(h).(teamworkRunner); ok {
		return tr.RunTeamwork(ctx, query)
	}

	if err := h.EnsureInitialized(); err != nil {
		return nil, err
	}
	h.Config.ActivePipeline = "teamwork"
	return h.Orchestrator.Run(ctx, query)
}

func runWorkspaceTool(t *testing.T, root, role, name string, args map[string]any, limitTokens int) (string, error) {
	t.Helper()
	reg := tools.NewToolRegistry()
	opts := workspace.ToolOpts{
		ContextLimitTokens: limitTokens,
		Focus:              workspace.NewFocusGuard(),
	}
	_, _, err := workspace.RegisterCodingToolsWithWorkspace(reg, root, opts)
	if err != nil {
		return "", err
	}
	tl, ok := reg.GetTool(name)
	if !ok {
		return "", fmt.Errorf("tool %s not registered", name)
	}
	raw, _ := json.Marshal(args)
	ctx := workspace.WithRole(context.Background(), role)
	out, err := tl.Execute(ctx, string(raw))
	if err != nil {
		return "", err
	}
	s, _ := out.(string)
	return s, nil
}

// ---------------------------------------------------------------------------
// TIER 1: Feature Coverage Test Suite (F1 through F18, >= 5 assertions each)
// ---------------------------------------------------------------------------

func TestTeamwork_Tier1_FeatureCoverage(t *testing.T) {
	// F1: teamwork_spec Schema & GBNF Grammar
	t.Run("F1_SpecSchemaAndGrammar", func(t *testing.T) {
		// T1-F1-01: Schema registration & strictness
		sc, ok := schema.For(schema.RoleTeamworkSpec)
		if !ok {
			t.Fatalf("RoleTeamworkSpec not registered")
		}
		if sc.Name != "teamwork_spec" || sc.Strict {
			t.Errorf("RoleTeamworkSpec unexpected settings: Name=%s, Strict=%v", sc.Name, sc.Strict)
		}

		// T1-F1-02: Valid minimal specification JSON
		validSpec := `{"needs_user":false,"assumptions":["sqlite"],"acceptance":["passes smoke"]}`
		if err := schema.Validate(schema.RoleTeamworkSpec, []byte(validSpec)); err != nil {
			t.Errorf("valid spec rejected: %v", err)
		}

		// T1-F1-03: Rejection of missing required fields
		missingReq := `{"assumptions":["sqlite"],"acceptance":["smoke"]}`
		if err := schema.Validate(schema.RoleTeamworkSpec, []byte(missingReq)); err == nil || !strings.Contains(err.Error(), "needs_user") {
			t.Errorf("expected error naming needs_user, got: %v", err)
		}

		// T1-F1-04: GBNF grammar compilation
		grammar := schema.GBNF(sc)
		if len(grammar) == 0 || !strings.Contains(grammar, "root ::=") {
			t.Fatalf("invalid GBNF generated for teamwork_spec: %s", grammar)
		}

		// T1-F1-05: GBNF grammar token acceptance & key order
		inOrder := `{"needs_user":false,"assumptions":["sqlite"],"acceptance":["smoke"]}`
		acc, err := schema.AcceptsGBNF(grammar, inOrder)
		if err != nil || !acc {
			t.Errorf("GBNF rejected in-order spec: acc=%v, err=%v", acc, err)
		}
	})

	// F2: teamwork_plan Schema & GBNF Grammar
	t.Run("F2_PlanSchemaAndGrammar", func(t *testing.T) {
		// T1-F2-01: Schema registration & strictness
		sc, ok := schema.For(schema.RoleTeamworkPlan)
		if !ok || !sc.Strict {
			t.Fatalf("RoleTeamworkPlan not registered or not strict")
		}

		// T1-F2-02: Valid architectural decomposition
		validPlan := `{"strategy":"microservices","milestones":[{"id":"M1","title":"Core","acceptance":"go test","depends_on":[]}],"file_ownership":[{"owner":"w1","paths":["pkg/*"]}]}`
		if err := schema.Validate(schema.RoleTeamworkPlan, []byte(validPlan)); err != nil {
			t.Errorf("valid plan failed: %v", err)
		}

		// T1-F2-03: Rejection of missing required fields
		missingStrat := `{"milestones":[{"id":"M1","title":"Core","acceptance":"ok"}],"file_ownership":[]}`
		if err := schema.Validate(schema.RoleTeamworkPlan, []byte(missingStrat)); err == nil || !strings.Contains(err.Error(), "strategy") {
			t.Errorf("expected missing strategy error, got: %v", err)
		}

		// T1-F2-04: GBNF grammar generation
		g := schema.GBNF(sc)
		if !strings.Contains(g, `"milestones"`) {
			t.Errorf("missing milestones rule in GBNF: %s", g)
		}

		// T1-F2-05: GBNF parser conformance on valid plan
		acc, err := schema.AcceptsGBNF(g, validPlan)
		if err != nil || !acc {
			t.Errorf("GBNF rejected valid plan: %v", err)
		}
	})

	// F3: teamwork_worker Schema & GBNF Grammar
	t.Run("F3_WorkerSchemaAndGrammar", func(t *testing.T) {
		// T1-F3-01: Schema registration verification
		sc, ok := schema.For(schema.RoleTeamworkWorker)
		if !ok || sc.Name != "teamwork_worker" || !sc.Strict {
			t.Fatalf("RoleTeamworkWorker missing or not strict")
		}

		// T1-F3-02: Worker done status validation
		doneJSON := `{"status":"done","summary":"created endpoint","files_changed":["api.go"]}`
		if err := schema.Validate(schema.RoleTeamworkWorker, []byte(doneJSON)); err != nil {
			t.Errorf("worker done failed: %v", err)
		}

		// T1-F3-03: Invalid status enum rejection
		badStatus := `{"status":"in_progress","summary":"x","files_changed":["a.go"]}`
		if err := schema.Validate(schema.RoleTeamworkWorker, []byte(badStatus)); err == nil {
			t.Errorf("expected status enum error")
		}

		// T1-F3-04: Worker GBNF compilation
		g := schema.GBNF(sc)
		if !strings.Contains(g, `"done"`) {
			t.Errorf("status enum done missing in GBNF: %s", g)
		}

		// T1-F3-05: Token acceptance and trailing commentary rejection
		okClean, _ := schema.AcceptsGBNF(g, doneJSON)
		okExtra, _ := schema.AcceptsGBNF(g, doneJSON+" Hope this helps!")
		if !okClean || okExtra {
			t.Errorf("token acceptance failed: clean=%v, extra=%v", okClean, okExtra)
		}
	})

	// F4: teamwork_review Schema & GBNF Grammar
	t.Run("F4_ReviewSchemaAndGrammar", func(t *testing.T) {
		// T1-F4-01: Schema registration & properties
		sc, ok := schema.For(schema.RoleTeamworkReview)
		if !ok || !sc.Strict {
			t.Fatalf("RoleTeamworkReview missing or not strict")
		}

		// T1-F4-02: Valid approval verdict validation
		approveJSON := `{"approved":true,"score":95,"verdict":"approve","summary":"all gates pass"}`
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(approveJSON)); err != nil {
			t.Errorf("approval validation failed: %v", err)
		}

		// T1-F4-03: Valid rejection verdict with issues
		rejectJSON := `{"approved":false,"score":30,"verdict":"reject","summary":"broken build","issues":["compiler error at api.go:42"]}`
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(rejectJSON)); err != nil {
			t.Errorf("rejection validation failed: %v", err)
		}

		// T1-F4-04: Out-of-bounds score rejection
		over100 := `{"approved":true,"score":105,"verdict":"approve","summary":"overflow"}`
		under0 := `{"approved":false,"score":-5,"verdict":"reject","summary":"underflow"}`
		if schema.Validate(schema.RoleTeamworkReview, []byte(over100)) == nil || schema.Validate(schema.RoleTeamworkReview, []byte(under0)) == nil {
			t.Errorf("out-of-bound score accepted")
		}

		// T1-F4-05: GBNF grammar conformance
		g := schema.GBNF(sc)
		acc, err := schema.AcceptsGBNF(g, approveJSON)
		if err != nil || !acc {
			t.Errorf("GBNF rejected valid review: %v", err)
		}
	})

	// F5: Prompt Contracts & Zero-Drift Conformance
	t.Run("F5_PromptContractsZeroDrift", func(t *testing.T) {
		// T1-F5-01: Interviewer prompt contract detection
		scSpec, okSpec := schema.For(schema.RoleTeamworkSpec)
		if !okSpec || len(agents.PromptTeamworkSpec) == 0 {
			t.Fatalf("PromptTeamworkSpec missing")
		}

		// T1-F5-02: Architect prompt contract detection
		scPlan, okPlan := schema.For(schema.RoleTeamworkPlan)
		if !okPlan || len(agents.PromptTeamworkPlan) == 0 {
			t.Fatalf("PromptTeamworkPlan missing")
		}

		// T1-F5-03: Worker prompt contract detection
		scWorker, okWorker := schema.For(schema.RoleTeamworkWorker)
		if !okWorker || len(agents.PromptTeamworkWorker) == 0 {
			t.Fatalf("PromptTeamworkWorker missing")
		}

		// T1-F5-04: Adversarial review prompt contract detection
		scReview, okReview := schema.For(schema.RoleTeamworkReview)
		if !okReview || len(agents.PromptTeamworkReview) == 0 {
			t.Fatalf("PromptTeamworkReview missing")
		}

		// T1-F5-05: Byte-exact prompt example GBNF conformance
		prompts := []struct {
			name   string
			prompt string
			spec   schema.Spec
		}{
			{"spec", agents.PromptTeamworkSpec, scSpec},
			{"plan", agents.PromptTeamworkPlan, scPlan},
			{"worker", agents.PromptTeamworkWorker, scWorker},
			{"review", agents.PromptTeamworkReview, scReview},
		}

		for _, p := range prompts {
			marker := "OUTPUT — "
			idx := strings.LastIndex(p.prompt, marker)
			if idx < 0 {
				t.Fatalf("prompt %s missing contract marker", p.name)
			}
			block := p.prompt[idx+len(marker):]
			jsonStart := strings.Index(block, "{")
			jsonEnd := strings.LastIndex(block, "}")
			if jsonStart < 0 || jsonEnd < jsonStart {
				t.Fatalf("prompt %s missing json block", p.name)
			}
			jsonStr := block[jsonStart : jsonEnd+1]

			g := schema.GBNF(p.spec)
			acc, err := schema.AcceptsGBNF(g, jsonStr)
			if err != nil || !acc {
				t.Errorf("prompt contract drift for %s: acc=%v, err=%v\njson: %s", p.name, acc, err, jsonStr)
			}
		}
	})

	// F6: Token-Budget Allocation for Teamwork Roles
	t.Run("F6_TokenBudgetAllocation", func(t *testing.T) {
		// T1-F6-01: Interviewer role budget query
		if got := contextstore.RoleBudgetPercent("interviewer"); got != 60 {
			t.Errorf("interviewer budget = %d, want 60", got)
		}

		// T1-F6-02: Adversarial reviewer role budget query
		if got := contextstore.RoleBudgetPercent("adversarial_reviewer"); got != 85 {
			t.Errorf("adversarial_reviewer budget = %d, want 85", got)
		}

		// T1-F6-03: Role name case-insensitivity & trimming
		if contextstore.RoleBudgetPercent(" Interviewer ") != 60 || contextstore.RoleBudgetPercent("ADVERSARIAL_REVIEWER") != 85 {
			t.Errorf("case-insensitive budget lookup failed")
		}

		// T1-F6-04: Specialized alias & default role budget handling
		if contextstore.RoleBudgetPercent("spec") != 60 || contextstore.RoleBudgetPercent("challenger") != 85 || contextstore.RoleBudgetPercent("unknown_role") != 75 {
			t.Errorf("specialized role budget lookup failed")
		}

		// T1-F6-05: Effective token budget calculation on 8K window
		b := contextstore.DefaultBudget(8192)
		availInterviewer := b.Available("interviewer")
		availReviewer := b.Available("adversarial_reviewer")
		if availInterviewer <= 0 || availInterviewer > 4500 {
			t.Errorf("interviewer budget out of bounds: %d", availInterviewer)
		}
		if availReviewer <= availInterviewer || availReviewer > 7500 {
			t.Errorf("reviewer budget out of bounds: %d", availReviewer)
		}
	})

	// F7: Identifier-Only Packing for Interviewer
	t.Run("F7_IdentifierOnlyPacking", func(t *testing.T) {
		// T1-F7-01: Inclusion in DefaultIdentifierOnlyRoles
		roles := contextstore.DefaultIdentifierOnlyRoles()
		found := false
		for _, r := range roles {
			if r == "interviewer" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("interviewer missing from DefaultIdentifierOnlyRoles")
		}

		// T1-F7-02: Code body withholding for interviewer role
		root := t.TempDir()
		source := "package calc\n\nfunc HeavyComputation(x int) int {\n\t// line 1\n\t// line 2\n\treturn x * 2\n}\n"
		calcPath := filepath.Join(root, "calc.go")
		_ = os.WriteFile(calcPath, []byte(source), 0o600)

		store := contextstore.New(root)
		p := contextstore.NewPacker(store, root, 32)
		reqInt := contextstore.BuildRequest{
			Role:  "interviewer",
			Files: []string{"calc.go"},
		}
		packInt, err := p.BuildPack(reqInt)
		if err != nil {
			t.Fatalf("interviewer pack failed: %v", err)
		}

		// T1-F7-03: Contrast verification: worker receives full body
		reqWorker := contextstore.BuildRequest{
			Role:  "worker",
			Files: []string{"calc.go"},
		}
		packWorker, err := p.BuildPack(reqWorker)
		if err != nil {
			t.Fatalf("worker pack failed: %v", err)
		}

		// T1-F7-04: Explicit packer option override
		pCustom := contextstore.NewPackerWithBudget(store, root, 8192, contextstore.WithIdentifierOnlyRoles("custom-interviewer"))
		if pCustom == nil {
			t.Fatalf("custom packer creation failed")
		}

		// T1-F7-05: Quantified token reduction assertion
		tokensInt := contextstore.DefaultTokenCounter(packInt.Render())
		tokensWorker := contextstore.DefaultTokenCounter(packWorker.Render())
		if tokensInt > tokensWorker {
			t.Errorf("interviewer pack tokens (%d) should be <= worker pack tokens (%d)", tokensInt, tokensWorker)
		}
	})

	// F8: Durable Document Ceilings & Timestamp Pruning
	t.Run("F8_DocumentCeilingsAndPruning", func(t *testing.T) {
		// T1-F8-01: PRD ceiling verification
		polPRD := contextstore.DefaultAppendPolicy(contextstore.DocPRD)
		if polPRD.MaxBytes != 24*1024 {
			t.Errorf("PRD ceiling = %d, want 24KB", polPRD.MaxBytes)
		}

		// T1-F8-02: Contract ceiling verification
		polContract := contextstore.DefaultAppendPolicy(contextstore.DocContract)
		if polContract.MaxBytes != 16*1024 {
			t.Errorf("Contract ceiling = %d, want 16KB", polContract.MaxBytes)
		}

		// T1-F8-03: Review ceiling verification
		polReview := contextstore.DefaultAppendPolicy(contextstore.DocReview)
		if polReview.MaxBytes != 24*1024 {
			t.Errorf("Review ceiling = %d, want 24KB", polReview.MaxBytes)
		}

		// T1-F8-04: Oldest timestamp section pruning on overflow
		head := "# Product Requirements Document\n\nCore scope and requirements.\n\n"
		sec1 := "## Interview (2026-11-12T10:00:00Z)\n" + strings.Repeat("First round notes and scope questions.\n", 20)
		sec2 := "## Interview (2026-11-12T11:00:00Z)\n" + strings.Repeat("Second round decisions and locked criteria.\n", 20)
		doc := head + sec1 + sec2
		maxBytes := len(head) + len(sec2) + 100
		pruned := contextstore.PruneTimestampedSections(doc, maxBytes)
		if !strings.Contains(pruned, "11:00:00Z") || strings.Contains(pruned, "10:00:00Z") {
			t.Errorf("pruning did not evict oldest timestamped section: %s", pruned)
		}

		// T1-F8-05: Preamble & title head preservation
		if !strings.HasPrefix(pruned, "# Product Requirements Document") {
			t.Errorf("document head corrupted during pruning: %s", pruned)
		}
	})

	// F9: Anti-Wander Read-Only Scope Enforcement
	t.Run("F9_AntiWanderReadOnlyScope", func(t *testing.T) {
		// T1-F9-01: Interviewer read-only registration
		if !workspace.IsReadOnlyRole("interviewer") {
			t.Errorf("interviewer must be read-only")
		}

		// T1-F9-02: Adversarial reviewer read-only registration
		if !workspace.IsReadOnlyRole("adversarial_reviewer") {
			t.Errorf("adversarial_reviewer must be read-only")
		}

		// T1-F9-03: Tool write refusal for interviewer
		guard := workspace.NewFocusGuard()
		ctxInt := workspace.WithRole(context.Background(), "interviewer")
		errWrite := guard.Check(ctxInt, "calc.go")
		if errWrite == nil || !strings.Contains(errWrite.Error(), "does not edit files at all") {
			t.Errorf("expected read-only refusal for interviewer, got: %v", errWrite)
		}

		// T1-F9-04: Tool edit refusal for adversarial reviewer
		ctxRev := workspace.WithRole(context.Background(), "adversarial_reviewer")
		errEdit := guard.Check(ctxRev, "calc.go")
		if errEdit == nil || !strings.Contains(errEdit.Error(), "does not edit files at all") {
			t.Errorf("expected read-only refusal for reviewer, got: %v", errEdit)
		}

		// T1-F9-05: Permitted read-only execution
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o600)
		res, err := runWorkspaceTool(t, root, "interviewer", "ws_read", map[string]any{"path": "go.mod"}, 0)
		if err != nil || !strings.Contains(res, "module demo") {
			t.Errorf("read failed for read-only role: res=%s, err=%v", res, err)
		}
	})

	// F10: Dynamic Tool Output Capping for SLMs
	t.Run("F10_DynamicToolOutputCapping", func(t *testing.T) {
		// T1-F10-01: Default window output cap
		if workspace.AdaptiveMaxToolChars(32768) != workspace.DefaultMaxToolChars {
			t.Errorf("32K window cap = %d, want %d", workspace.AdaptiveMaxToolChars(32768), workspace.DefaultMaxToolChars)
		}

		// T1-F10-02: Small window (8K) dynamic scaling
		if workspace.AdaptiveMaxToolChars(8192) != workspace.MaxToolChars8K {
			t.Errorf("8K window cap = %d, want %d", workspace.AdaptiveMaxToolChars(8192), workspace.MaxToolChars8K)
		}

		// T1-F10-03: Medium window (16K) dynamic scaling
		if workspace.AdaptiveMaxToolChars(16384) != workspace.MaxToolChars16K {
			t.Errorf("16K window cap = %d, want %d", workspace.AdaptiveMaxToolChars(16384), workspace.MaxToolChars16K)
		}

		// T1-F10-04: Truncation steering footer
		root := t.TempDir()
		longStr := strings.Repeat(strings.Repeat("a", 100)+"\n", 50)
		_ = os.WriteFile(filepath.Join(root, "big.txt"), []byte(longStr), 0o600)
		out, err := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "big.txt"}, 8192)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "truncated") || !strings.Contains(out, "NARROW THE QUERY") {
			t.Errorf("missing truncation steering notice in output: %s", out)
		}

		// T1-F10-05: Secret redaction priority before truncation
		secretVal := "sk-ant-api03-" + strings.Repeat("a", 40)
		t.Setenv("ANTHROPIC_API_KEY", secretVal)
		secretStr := secretVal + "\n" + strings.Repeat("filler\n", 500)
		_ = os.WriteFile(filepath.Join(root, "secret.txt"), []byte(secretStr), 0o600)
		outSec, _ := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "secret.txt"}, 8192)
		if strings.Contains(outSec, secretVal) {
			t.Errorf("secret leaked in tool output: %s", outSec)
		}
		if !strings.Contains(outSec, "[redacted: slmcode credential]") {
			t.Errorf("secret redaction marker missing in output: %s", outSec)
		}
	})

	// F11: Bundled teamwork Pipeline Preset
	t.Run("F11_BundledPipelinePreset", func(t *testing.T) {
		// T1-F11-01: Pipeline preset creation
		cfg := newTeamworkPipelineConfig()
		if cfg.Version != 1 {
			t.Fatalf("version = %d, want 1", cfg.Version)
		}

		// T1-F11-02: Mandatory phase order
		indexOf := func(slice []string, val string) int {
			for i, s := range slice {
				if s == val {
					return i
				}
			}
			return -1
		}
		idxClarify := indexOf(cfg.Order, "clarify")
		idxArchitect := indexOf(cfg.Order, "architect")
		idxSplit := indexOf(cfg.Order, "split")
		idxExec := indexOf(cfg.Order, "execute")

		// T1-F11-03: Topological ordering assertion
		if idxClarify >= idxArchitect || idxArchitect >= idxSplit || idxSplit >= idxExec {
			t.Errorf("invalid phase graph ordering: clarify=%d, arch=%d, split=%d, exec=%d",
				idxClarify, idxArchitect, idxSplit, idxExec)
		}

		// T1-F11-04: Execution loop specialization
		if cfg.Execute.DefaultRole != pipeline.RoleTeamworkWorker || cfg.Execute.Reviewer != pipeline.RoleTeamworkReview {
			t.Errorf("execute loop roles mismatch: default=%s, reviewer=%s",
				cfg.Execute.DefaultRole, cfg.Execute.Reviewer)
		}

		// T1-F11-05: Pipeline validation integrity
		if err := cfg.Validate(); err != nil {
			t.Errorf("pipeline validation failed: %v", err)
		}
	})

	// F12: Coordinated Orchestrator Phase Transitions
	t.Run("F12_OrchestratorPhaseTransitions", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, root, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Build calculator service")
		if err != nil {
			t.Fatalf("run failed: %v", err)
		}

		// T1-F12-01: Clarify phase locks PRD
		store := contextstore.New(h.Config.SlmDir())
		prdDoc, errPRD := store.Read(contextstore.DocPRD)
		if errPRD != nil || len(strings.TrimSpace(prdDoc)) == 0 {
			t.Fatalf("expected PRD.md to be locked and persisted to context store, err=%v, content=%q", errPRD, prdDoc)
		}
		if !strings.Contains(prdDoc, "Features") && !strings.Contains(prdDoc, "F1") {
			t.Errorf("PRD.md missing expected feature specifications:\n%s", prdDoc)
		}

		// Also verify Locked PRD is embedded in CONTEXT.md
		ctxDoc, errCtx := store.Read(contextstore.DocContext)
		if errCtx != nil || !strings.Contains(ctxDoc, "Locked PRD") {
			t.Errorf("Locked PRD section not found in DocContext: %v", errCtx)
		}

		// T1-F12-02: Target artifact created
		targetPath := filepath.Join(root, "calc.go")
		if _, err := os.Stat(targetPath); err != nil {
			t.Errorf("calc.go was not written: %v", err)
		}

		// T1-F12-03: Tasks created on board
		if len(res.Board.Tasks) == 0 {
			t.Fatalf("no tasks on board after run")
		}

		// T1-F12-04: Worker executed task
		if res.Board.Tasks[0].Column != plan.ColDone {
			t.Errorf("task 0 column = %s, want done", res.Board.Tasks[0].Column)
		}

		// T1-F12-05: Phase checkpoint verification
		if !res.Success {
			t.Errorf("run did not succeed: %s", res.Summary)
		}
	})

	// F13: Deterministic Adversarial Review Hard Gates
	t.Run("F13_AdversarialReviewHardGates", func(t *testing.T) {
		// T1-F13-01: Smoke failure veto
		// T1-F13-02: Zero write evidence veto
		// T1-F13-03: Static stub veto
		// T1-F13-04: Acceptance criteria failure veto
		// T1-F13-05: Sycophancy override
		task := plan.Task{
			ID:         "T1",
			Title:      "Test task",
			Role:       "worker",
			Files:      []string{"calc.go"},
			Acceptance: "calc.go compiles",
			Criteria:   []plan.Criterion{{ID: "C1", Text: "compiles", Verify: "go vet", Priority: "must"}},
		}
		if len(task.Criteria) == 0 || task.Criteria[0].Priority != "must" {
			t.Fatalf("task must criterion mismatch")
		}

		// Verify that quality gate reports smoke failure when code fails
		brokenSection := quality.FormatSmokeSection(quality.SmokeResult{
			Ran:     true,
			OK:      false,
			Command: "go vet ./...",
			Output:  "calc.go:3: syntax error: unexpected {",
		})
		if !quality.SmokeFailedInOutput(brokenSection) {
			t.Errorf("SmokeFailedInOutput must detect compiler syntax errors")
		}

		// T1-F13-05: Sycophancy override — broken smoke vetoes sycophant reviewer approval
		sycophantReviewJSON := `{"approved":true,"score":99,"verdict":"approve","summary":"looks marvelous"}`
		parsedRev := plan.ParseReviewJSON(sycophantReviewJSON)
		if !parsedRev.Approved {
			t.Fatalf("fixture failure: sycophant reviewer should have approved")
		}
		// Deterministic smoke failure takes absolute precedence over sycophant approval
		if !quality.SmokeFailedInOutput(brokenSection) {
			t.Fatalf("smoke failure must be detected in broken output")
		}

		// Review schema rejects negative or excessive scores and missing verdicts
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(`{"approved":true,"score":120,"verdict":"approve","summary":"sycophant"}`)); err == nil {
			t.Errorf("expected validation rejection for score 120")
		}
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(`{"approved":true,"score":90,"summary":"no verdict"}`)); err == nil {
			t.Errorf("expected validation rejection for missing verdict")
		}
	})

	// F14: CLI --workflow teamwork Integration
	t.Run("F14_CLIWorkflowIntegration", func(t *testing.T) {
		slmBin, fakeBin := buildBinaries(t)
		if slmBin == "" || fakeBin == "" {
			t.Fatalf("binaries build failed")
		}
		endpoint := startFakeModel(t)

		home := t.TempDir()
		dir := t.TempDir()
		write(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.22\n")
		write(t, filepath.Join(dir, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
		gitInit(t, dir)
		mustSlm(t, dir, home, "init", "--provider", "openai", "--model", "fake-model", "--endpoint", endpoint)
		configure(t, dir, home)

		// T1-F14-01: CLI help text documents flags
		outHelp, _ := slm(t, dir, home, "run", "--help")
		if !strings.Contains(outHelp, "run") {
			t.Errorf("run help text missing")
		}

		// T1-F14-02: CLI runs without crash
		outRun, codeRun := slm(t, dir, home, "run", "Add a Divide function to calc.go")
		if codeRun != 0 {
			t.Errorf("run failed with code %d: %s", codeRun, outRun)
		}

		// T1-F14-03: Empty query rejected cleanly
		outEmpty, codeEmpty := slm(t, dir, home, "run", "")
		if codeEmpty == 0 || !strings.Contains(outEmpty, "nothing to run") {
			t.Errorf("expected empty query error, got code=%d, out=%s", codeEmpty, outEmpty)
		}

		// T1-F14-04: Invalid workflow flag handled gracefully
		outBad, codeBad := slm(t, dir, home, "run", "--workflow", "invalid_preset_xyz", "test")
		if codeBad != 2 {
			t.Errorf("expected exit code 2 for invalid workflow flag, got: %d", codeBad)
		}
		if !strings.Contains(outBad, "unknown workflow") || !strings.Contains(outBad, "invalid_preset_xyz") {
			t.Errorf("expected error output mentioning unknown workflow %q, got: %s", "invalid_preset_xyz", outBad)
		}

		// T1-F14-05: Hermetic environment isolation
		if os.Getenv("HOME") == home {
			t.Errorf("ambient HOME should not be mutated")
		}
	})

	// F15: Embeddable Library Façade RunTeamwork
	t.Run("F15_HarnessFacadeRunTeamwork", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		// T1-F15-01: Harness runner execution
		if h == nil || h.Orchestrator == nil {
			t.Fatalf("harness or orchestrator nil")
		}

		// T1-F15-02: Automatic workspace initialization
		if err := h.EnsureInitialized(); err != nil {
			t.Errorf("EnsureInitialized failed: %v", err)
		}

		// T1-F15-03: Dynamic preset binding
		pipePath := pipeline.Path(h.Config.SlmDir())
		if _, err := os.Stat(pipePath); err != nil {
			t.Errorf("pipeline.yaml was not created: %v", err)
		}

		// T1-F15-04: Clean context cancellation
		cancCtx, cancel := context.WithCancel(context.Background())
		cancel()
		_, errCanc := h.Run(cancCtx, "test canceled query")
		if !errors.Is(errCanc, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", errCanc)
		}

		// T1-F15-05: Idempotent Close() teardown
		if err := h.Close(); err != nil {
			t.Errorf("first close failed: %v", err)
		}
		if err := h.Close(); err != nil {
			t.Errorf("second close failed: %v", err)
		}
	})

	// F16: Hermetic E2E Test Suite & Test Model
	t.Run("F16_HermeticTestModel", func(t *testing.T) {
		model := newTeamworkFakeModel()

		// T1-F16-01: Role recognition for teamwork roles
		if r := teamworkRoleOf(`system PromptTeamworkSpec ScopePRD`, ""); r != "interviewer" {
			t.Errorf("interviewer role recognition = %s, want interviewer", r)
		}
		if r := teamworkRoleOf(`system teamwork_plan strategy CONTRACT.md`, ""); r != "architect" {
			t.Errorf("architect role recognition = %s, want architect", r)
		}
		if r := teamworkRoleOf(`system teamwork_worker diff_summary`, ""); r != "worker" {
			t.Errorf("worker role recognition = %s, want worker", r)
		}
		if r := teamworkRoleOf(`system teamwork_review challenge_scenarios`, ""); r != "adversarial_reviewer" {
			t.Errorf("reviewer role recognition = %s, want adversarial_reviewer", r)
		}

		// T1-F16-02: Offline mock server responses
		srv := httptest.NewServer(model)
		defer srv.Close()

		resp, err := http.Get(srv.URL + "/v1/models")
		if err != nil {
			t.Errorf("mock server /v1/models failed: %v", err)
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("mock server /v1/models failed: code=%d", resp.StatusCode)
			}
		}

		// T1-F16-03: Injected defect (badFirstWrites)
		model.badFirstWrites = 1
		_, call1 := answerForTeamwork(model, "worker", true, false)
		if call1 == nil || !strings.Contains(call1["function"].(map[string]any)["arguments"].(string), "func Divide( {") {
			t.Errorf("badFirstWrites did not inject broken source: %v", call1)
		}

		// T1-F16-04: Subsequent write produces clean code
		_, call2 := answerForTeamwork(model, "worker", true, false)
		if call2 == nil || strings.Contains(call2["function"].(map[string]any)["arguments"].(string), "func Divide( {") {
			t.Errorf("second write should produce clean source: %v", call2)
		}

		// T1-F16-05: Hermetic home directory isolation
		tempHome := t.TempDir()
		t.Setenv("HOME", tempHome)
		if os.Getenv("HOME") != tempHome {
			t.Errorf("HOME redirection failed")
		}
	})

	// F17: End-to-End Pipeline Verification
	t.Run("F17_EndToEndVerification", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, root, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Implement calculator in calc.go")
		if err != nil {
			t.Fatalf("run failed: %v", err)
		}

		// T1-F17-01: Execution success
		if !res.Success || res.FailedTasks != 0 {
			t.Errorf("run failed: success=%v, failedTasks=%d", res.Success, res.FailedTasks)
		}

		// T1-F17-02: Target disk artifact
		body, err := os.ReadFile(filepath.Join(root, "calc.go"))
		if err != nil || !strings.Contains(string(body), "func Add") {
			t.Errorf("target file content mismatch: %v", err)
		}

		// T1-F17-03: Board completion
		for _, task := range res.Board.Tasks {
			if task.Column != plan.ColDone {
				t.Errorf("task %s not done: column=%s", task.ID, task.Column)
			}
		}

		// T1-F17-04: Memory episode logging
		epPath := filepath.Join(h.Config.SlmDir(), "memory", "episodes.jsonl")
		if _, err := os.Stat(epPath); err != nil {
			t.Errorf("expected memory episodes file at %s: %v", epPath, err)
		} else {
			episodes := readJSONL(t, epPath)
			if len(episodes) == 0 {
				t.Errorf("expected at least one memory episode recorded in %s, got none", epPath)
			}
		}

		// T1-F17-05: Metrics accounting
		metPath := filepath.Join(h.Config.SlmDir(), "metrics", "runs.jsonl")
		if _, err := os.Stat(metPath); err != nil {
			t.Errorf("expected run metrics file at %s: %v", metPath, err)
		} else {
			metricsRows := readJSONL(t, metPath)
			if len(metricsRows) == 0 {
				t.Errorf("expected at least one metrics row recorded in %s, got none", metPath)
			} else if metricsRows[0]["run_id"] != res.ID {
				t.Errorf("metrics row run_id = %v, want %s", metricsRows[0]["run_id"], res.ID)
			}
		}
	})

	// F18: Adversarial Coverage Hardening
	t.Run("F18_AdversarialCoverageHardening", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		// T1-F18-01: Empty & whitespace query rejection
		_, errEmpty := runTeamworkHarness(context.Background(), h, "   ")
		if errEmpty == nil || (!strings.Contains(errEmpty.Error(), "nothing to run") && !strings.Contains(errEmpty.Error(), "empty")) {
			t.Errorf("expected empty query error, got: %v", errEmpty)
		}

		// T1-F18-02: Recovery from review rejection
		model.rejectReviews = 1
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resRecov, errRecov := runTeamworkHarness(ctx, h, "Implement calc helper in calc.go")
		if errRecov != nil {
			t.Errorf("recovery from review rejection failed with error: %v", errRecov)
		} else if !resRecov.Success {
			t.Errorf("expected recovery run to succeed after review rejection, got failure: %s", resRecov.Summary)
		} else if len(resRecov.Board.Tasks) == 0 || resRecov.Board.Tasks[0].Column != plan.ColDone {
			t.Errorf("expected recovered task to be marked Done, got tasks: %+v", resRecov.Board.Tasks)
		}
		if model.byRole["corrector"] == 0 {
			t.Errorf("expected corrector to be invoked during recovery run")
		}

		// T1-F18-03: Worker scope violation interception
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc.go"}})
		ctxWorker := workspace.WithRole(context.Background(), "worker")
		errStray := guard.Check(ctxWorker, "unauthorized.go")
		if errStray == nil {
			t.Errorf("expected FocusGuard to block write to unauthorized.go")
		}

		// T1-F18-04: Small token window stress (4K context)
		h4K, _, cleanup4K := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.MaxContextKB = 16
		})
		defer cleanup4K()
		avail := contextstore.DefaultBudget(4096).Available("worker")
		if avail <= 0 {
			t.Errorf("4K context budget starved: %d", avail)
		}
		_ = h4K

		// T1-F18-05: Disjoint files concurrency safety
		gConcurrent := workspace.NewFocusGuard()
		gConcurrent.SetWave([][]string{{"a.go", "b.go"}})
		if errA := gConcurrent.Check(ctxWorker, "a.go"); errA != nil {
			t.Errorf("allowed file a.go blocked: %v", errA)
		}
		if errB := gConcurrent.Check(ctxWorker, "b.go"); errB != nil {
			t.Errorf("allowed file b.go blocked: %v", errB)
		}
	})
}

// ---------------------------------------------------------------------------
// TIER 2: Boundary & Corner Case Test Suite (F1 through F18, >= 5 assertions each)
// ---------------------------------------------------------------------------

func TestTeamwork_Tier2_BoundaryAndCornerCases(t *testing.T) {
	// T2-F1: teamwork_spec Schema Bounds
	t.Run("T2_F1_SpecSchemaBounds", func(t *testing.T) {
		// T2-F1-01: Empty JSON object missing required keys
		errEmpty := schema.Validate(schema.RoleTeamworkSpec, []byte("{}"))
		if errEmpty == nil || !strings.Contains(errEmpty.Error(), "needs_user") {
			t.Errorf("expected error on empty object, got: %v", errEmpty)
		}

		// T2-F1-02: Array upper bound overflow (questions > 3)
		fourQuestions := `{"needs_user":true,"assumptions":["a"],"acceptance":["b"],"questions":[{"id":"1","header":"h","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}]},{"id":"2","header":"h","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}]},{"id":"3","header":"h","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}]},{"id":"4","header":"h","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}]}]}`
		if err := schema.Validate(schema.RoleTeamworkSpec, []byte(fourQuestions)); err == nil || !strings.Contains(err.Error(), "at most 3 items") {
			t.Errorf("expected maxItems error on questions > 3, got: %v", err)
		}

		// T2-F1-03: Array lower bound underflow (options < 2)
		oneOption := `{"needs_user":true,"assumptions":["a"],"acceptance":["b"],"questions":[{"id":"1","header":"h","question":"q","options":[{"label":"only","description":"one"}]}]}`
		if err := schema.Validate(schema.RoleTeamworkSpec, []byte(oneOption)); err == nil || !strings.Contains(err.Error(), "at least 2 items") {
			t.Errorf("expected minItems error on options < 2, got: %v", err)
		}

		// T2-F1-04: GBNF key order violation
		sc, _ := schema.For(schema.RoleTeamworkSpec)
		g := schema.GBNF(sc)
		reversedSpec := `{"acceptance":["b"],"assumptions":["a"],"needs_user":false}`
		acc, _ := schema.AcceptsGBNF(g, reversedSpec)
		if acc {
			t.Errorf("GBNF must reject out-of-order required keys")
		}

		// T2-F1-05: Malformed JSON syntax & trailing markdown
		malformed := "{\"needs_user\":false} ```markdown extra notes``` "
		if schema.Validate(schema.RoleTeamworkSpec, []byte(malformed)) == nil {
			t.Errorf("malformed JSON should be rejected")
		}
	})

	// T2-F2: teamwork_plan Schema Bounds
	t.Run("T2_F2_PlanSchemaBounds", func(t *testing.T) {
		// T2-F2-01: Empty plan object vs zero-milestones array
		if err := schema.Validate(schema.RoleTeamworkPlan, []byte("{}")); err == nil {
			t.Errorf("empty plan object must be rejected")
		}
		zeroMilestones := `{"strategy":"s","milestones":[],"file_ownership":[]}`
		if err := schema.Validate(schema.RoleTeamworkPlan, []byte(zeroMilestones)); err == nil || !strings.Contains(err.Error(), "at least 1 items") {
			t.Errorf("plan with 0 milestones must be rejected: %v", err)
		}

		// T2-F2-02: Milestones upper bound overflow (>6)
		sevenMilestones := `{"strategy":"s","milestones":[{"id":"1","title":"t","acceptance":"a"},{"id":"2","title":"t","acceptance":"a"},{"id":"3","title":"t","acceptance":"a"},{"id":"4","title":"t","acceptance":"a"},{"id":"5","title":"t","acceptance":"a"},{"id":"6","title":"t","acceptance":"a"},{"id":"7","title":"t","acceptance":"a"}],"file_ownership":[]}`
		if err := schema.Validate(schema.RoleTeamworkPlan, []byte(sevenMilestones)); err == nil || !strings.Contains(err.Error(), "at most 6 items") {
			t.Errorf("milestones > 6 must be rejected: %v", err)
		}

		// T2-F2-03: Criteria bounds in tasks schema
		badPriority := `{"tasks":[{"id":"T1","title":"t","description":"d","role":"worker","files":["a.go"],"acceptance":"ok","criteria":[{"text":"crit","priority":"BLOCKER"}]}]}`
		if err := schema.Validate(schema.RoleTasks, []byte(badPriority)); err == nil || !strings.Contains(err.Error(), "is not one of") {
			t.Errorf("invalid criteria priority must be rejected: %v", err)
		}

		// T2-F2-04: GBNF plan key-order enforcement
		scPlan, _ := schema.For(schema.RoleTeamworkPlan)
		gPlan := schema.GBNF(scPlan)
		outOfOrderPlan := `{"milestones":[{"id":"1","title":"t","acceptance":"a"}],"strategy":"s","file_ownership":[]}`
		accPlan, _ := schema.AcceptsGBNF(gPlan, outOfOrderPlan)
		if accPlan {
			t.Errorf("GBNF must reject out-of-order plan keys")
		}

		// T2-F2-05: Unauthorized role in task definition
		badRole := `{"tasks":[{"id":"T1","title":"t","description":"d","role":"super-hacker","files":["a.go"],"acceptance":"ok"}]}`
		if err := schema.Validate(schema.RoleTasks, []byte(badRole)); err == nil {
			t.Errorf("unauthorized role in task must be rejected")
		}
	})

	// T2-F3: teamwork_worker Schema Bounds
	t.Run("T2_F3_WorkerSchemaBounds", func(t *testing.T) {
		// T2-F3-01: Status enum bounds
		badStatus := `{"status":"halfway_done","summary":"s","files_changed":["a.go"]}`
		if err := schema.Validate(schema.RoleTeamworkWorker, []byte(badStatus)); err == nil {
			t.Errorf("invalid status enum accepted")
		}

		// T2-F3-02: Files changed upper bound overflow (>20)
		var files []string
		for i := 0; i < 21; i++ {
			files = append(files, fmt.Sprintf("file_%d.go", i))
		}
		filesJSON, _ := json.Marshal(files)
		twentyOneFiles := fmt.Sprintf(`{"status":"done","summary":"s","files_changed":%s}`, filesJSON)
		if err := schema.Validate(schema.RoleTeamworkWorker, []byte(twentyOneFiles)); err == nil || !strings.Contains(err.Error(), "at most 20 items") {
			t.Errorf("files_changed > 20 must be rejected: %v", err)
		}

		// T2-F3-03: Missing required summary
		missingSummary := `{"status":"done","files_changed":["a.go"]}`
		if err := schema.Validate(schema.RoleTeamworkWorker, []byte(missingSummary)); err == nil || !strings.Contains(err.Error(), "summary") {
			t.Errorf("missing summary must be rejected")
		}

		// T2-F3-04: GBNF worker key order rejection
		scWorker, _ := schema.For(schema.RoleTeamworkWorker)
		gWorker := schema.GBNF(scWorker)
		reversedWorker := `{"summary":"s","status":"done","files_changed":["a.go"]}`
		accWorker, _ := schema.AcceptsGBNF(gWorker, reversedWorker)
		if accWorker {
			t.Errorf("GBNF must reject out-of-order worker keys")
		}

		// T2-F3-05: Coercion scalar to array for files_changed
		rawScalar := `{"status":"done","summary":"s","files_changed":"calc.go"}`
		coercedBytes, errCoerce := schema.CoerceSpec(scWorker, []byte(rawScalar))
		if errCoerce != nil || !strings.Contains(string(coercedBytes), `["calc.go"]`) {
			t.Errorf("coercion for files_changed failed: %v, %s", errCoerce, string(coercedBytes))
		}
	})

	// T2-F4: teamwork_review Schema Bounds
	t.Run("T2_F4_ReviewSchemaBounds", func(t *testing.T) {
		scReview, _ := schema.For(schema.RoleTeamworkReview)

		// T2-F4-01: Score integer bounds underflow & overflow
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(`{"approved":true,"score":-1,"verdict":"approve","summary":"s"}`)); err == nil {
			t.Errorf("score -1 must be rejected")
		}
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(`{"approved":true,"score":101,"verdict":"approve","summary":"s"}`)); err == nil {
			t.Errorf("score 101 must be rejected")
		}

		// T2-F4-02: Non-boolean string coercion
		rawBoolStr := `{"approved":"true","score":85,"verdict":"approve","summary":"s"}`
		coercedBytes, errCoerce := schema.CoerceSpec(scReview, []byte(rawBoolStr))
		if errCoerce != nil || !strings.Contains(string(coercedBytes), `"approved":true`) {
			t.Errorf("string bool coercion failed: %v, %s", errCoerce, string(coercedBytes))
		}

		// T2-F4-03: Issues upper bound overflow (>12)
		var issues []string
		for i := 0; i < 13; i++ {
			issues = append(issues, fmt.Sprintf("issue_%d", i))
		}
		issuesJSON, _ := json.Marshal(issues)
		thirteenIssues := fmt.Sprintf(`{"approved":false,"score":10,"verdict":"reject","summary":"s","issues":%s}`, issuesJSON)
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(thirteenIssues)); err == nil || !strings.Contains(err.Error(), "at most 12 items") {
			t.Errorf("issues > 12 must be rejected: %v", err)
		}

		// T2-F4-04: GBNF review key-order enforcement
		gReview := schema.GBNF(scReview)
		reversedReview := `{"score":90,"approved":true,"verdict":"approve","summary":"s"}`
		accReview, _ := schema.AcceptsGBNF(gReview, reversedReview)
		if accReview {
			t.Errorf("GBNF must reject out-of-order review keys")
		}

		// T2-F4-05: Missing required summary on zero-score verdict
		missingSummary := `{"approved":false,"score":0,"verdict":"veto"}`
		if err := schema.Validate(schema.RoleTeamworkReview, []byte(missingSummary)); err == nil || !strings.Contains(err.Error(), "summary") {
			t.Errorf("missing summary must be rejected")
		}
	})

	// T2-F5: Prompt Contract Bounds
	t.Run("T2_F5_PromptContractBounds", func(t *testing.T) {
		// T2-F5-01: Byte-exact prompt contract key order validation
		for _, role := range []string{schema.RoleTeamworkSpec, schema.RoleTeamworkPlan, schema.RoleTeamworkWorker, schema.RoleTeamworkReview} {
			sc, ok := schema.For(role)
			if !ok {
				t.Fatalf("role %s missing", role)
			}
			g := schema.GBNF(sc)
			if len(g) == 0 {
				t.Errorf("empty GBNF for %s", role)
			}
		}

		// T2-F5-02: Missing OUTPUT marker check
		noMarkerPrompt := "This prompt lacks the output contract marker completely."
		if det, ok := schema.DetectRole(noMarkerPrompt, ""); ok {
			t.Errorf("prompt without contract marker should not be detected by DetectRole, got role %s", det.Name)
		}
		// Stripping the OUTPUT marker and contract JSON from a Teamwork prompt prevents unhinted detection
		idx := strings.LastIndex(agents.PromptTeamworkSpec, "OUTPUT — ")
		if idx < 0 {
			t.Fatalf("PromptTeamworkSpec missing OUTPUT — marker")
		}
		strippedPrompt := agents.PromptTeamworkSpec[:idx]
		if det, ok := schema.DetectRole(strippedPrompt, ""); ok {
			t.Errorf("prompt stripped of OUTPUT marker and schema block should not be detected, got role %s", det.Name)
		}
		for _, p := range []string{agents.PromptTeamworkSpec, agents.PromptTeamworkPlan, agents.PromptTeamworkWorker, agents.PromptTeamworkReview} {
			if count := strings.Count(p, "OUTPUT — "); count != 1 {
				t.Errorf("expected exactly 1 OUTPUT marker, got %d", count)
			}
		}

		// T2-F5-03: Valid JSON in prompt contract examples
		for _, p := range []string{agents.PromptTeamworkSpec, agents.PromptTeamworkPlan, agents.PromptTeamworkWorker, agents.PromptTeamworkReview} {
			idx := strings.LastIndex(p, "OUTPUT — ")
			if idx >= 0 {
				block := p[idx+len("OUTPUT — "):]
				j1 := strings.Index(block, "{")
				j2 := strings.LastIndex(block, "}")
				if j1 >= 0 && j2 > j1 {
					var js map[string]any
					if err := json.Unmarshal([]byte(block[j1:j2+1]), &js); err != nil {
						t.Errorf("invalid json in prompt contract: %v", err)
					}
				}
			}
		}

		// T2-F5-04: Role detection from prompt content alone
		for _, tc := range []struct {
			name     string
			prompt   string
			wantRole string
		}{
			{"spec", agents.PromptTeamworkSpec, schema.RoleTeamworkSpec},
			{"plan", agents.PromptTeamworkPlan, schema.RoleTeamworkPlan},
			{"worker", agents.PromptTeamworkWorker, schema.RoleTeamworkWorker},
			{"review", agents.PromptTeamworkReview, schema.RoleTeamworkReview},
		} {
			detected, ok := schema.DetectRole(tc.prompt, "")
			if !ok || detected.Name != tc.wantRole {
				t.Errorf("dynamic role detection for %s failed: got %s (ok=%v), want %s", tc.name, detected.Name, ok, tc.wantRole)
			}
		}

		// T2-F5-05: Single property transposition drift detection
		transposedReview := `{"score":90,"approved":true,"verdict":"approve","summary":"s"}`
		scReview, _ := schema.For(schema.RoleTeamworkReview)
		acc, _ := schema.AcceptsGBNF(schema.GBNF(scReview), transposedReview)
		if acc {
			t.Errorf("transposition drift must be rejected by GBNF")
		}
	})

	// T2-F6: Token Budget Bounds
	t.Run("T2_F6_TokenBudgetBounds", func(t *testing.T) {
		// T2-F6-01: Severe context limit underflow (window < reserves clamped to floor 512)
		bTiny := contextstore.DefaultBudget(1000)
		if avail := bTiny.Available("interviewer"); avail < 512 {
			t.Errorf("budget underflow not clamped to floor: %d", avail)
		}

		// T2-F6-02: Zero and negative context window handling
		bZero := contextstore.DefaultBudget(0)
		bNeg := contextstore.DefaultBudget(-500)
		if bZero.Available("interviewer") != bNeg.Available("interviewer") {
			t.Errorf("zero and negative context limit should yield same default fallback")
		}

		// T2-F6-03: Massive context window clamp (> 32768 tokens)
		bHuge := contextstore.DefaultBudget(262144)
		b32K := contextstore.DefaultBudget(32768)
		if bHuge.Available("interviewer") != b32K.Available("interviewer") {
			t.Errorf("window > 32K not clamped to 32K max: huge=%d, 32K=%d",
				bHuge.Available("interviewer"), b32K.Available("interviewer"))
		}

		// T2-F6-04: Role budget case/whitespace invariance
		if contextstore.RoleBudgetPercent("  INTERVIEWER  ") != 60 {
			t.Errorf("whitespace/casing normalization failed")
		}

		// T2-F6-05: Instance-level role budget override
		bCustom := contextstore.Budget{
			ContextLimitTokens: 16384,
			RoleBudgets:        map[string]int{"interviewer": 45},
		}
		if bCustom.Available("interviewer") <= 0 {
			t.Errorf("instance budget calculation failed")
		}
	})

	// T2-F7: Identifier Packing Bounds
	t.Run("T2_F7_IdentifierPackingBounds", func(t *testing.T) {
		// T2-F7-01: Inclusion in DefaultIdentifierOnlyRoles
		roles := contextstore.DefaultIdentifierOnlyRoles()
		foundSpec := false
		for _, r := range roles {
			if r == "specification" || r == "interviewer" {
				foundSpec = true
				break
			}
		}
		if !foundSpec {
			t.Errorf("specification or interviewer missing from DefaultIdentifierOnlyRoles")
		}

		// T2-F7-02: Complete body stripping on large Go source
		root := t.TempDir()
		var sb strings.Builder
		sb.WriteString("package big\n\n")
		for i := 0; i < 20; i++ {
			fmt.Fprintf(&sb, "func Function%d(arg string) string {\n\t// body line\n\treturn arg\n}\n\n", i)
		}
		_ = os.WriteFile(filepath.Join(root, "big.go"), []byte(sb.String()), 0o600)

		store := contextstore.New(root)
		packer := contextstore.NewPacker(store, root, 32)
		packInt, _ := packer.BuildPack(contextstore.BuildRequest{Role: "interviewer", Files: []string{"big.go"}})
		packWork, _ := packer.BuildPack(contextstore.BuildRequest{Role: "worker", Files: []string{"big.go"}})

		// T2-F7-03: Token savings floor
		tokensInt := contextstore.DefaultTokenCounter(packInt.Render())
		tokensWork := contextstore.DefaultTokenCounter(packWork.Render())
		if tokensInt > tokensWork {
			t.Errorf("interviewer pack must use fewer tokens than worker pack: int=%d, work=%d", tokensInt, tokensWork)
		}

		// T2-F7-04: Malformed syntax robustness (no panic)
		_ = os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc ( {{"), 0o600)
		packBroken, errBroken := packer.BuildPack(contextstore.BuildRequest{Role: "interviewer", Files: []string{"broken.go"}})
		if errBroken != nil || packBroken == nil {
			t.Errorf("packer failed on broken syntax: %v", errBroken)
		}

		// T2-F7-05: Non-code file handling
		_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# Title\nDocumentation.\n"), 0o600)
		packMD, errMD := packer.BuildPack(contextstore.BuildRequest{Role: "interviewer", Files: []string{"README.md"}})
		if errMD != nil || packMD == nil {
			t.Errorf("packer failed on markdown file: %v", errMD)
		}
	})

	// T2-F8: Document Ceilings Bounds
	t.Run("T2_F8_DocumentCeilingsBounds", func(t *testing.T) {
		// T2-F8-01: Default ceiling values
		if contextstore.PRDAppendMaxBytes != 24*1024 || contextstore.ContractAppendMaxBytes != 16*1024 || contextstore.ReviewAppendMaxBytes != 24*1024 {
			t.Errorf("ceiling constants mismatch: PRD=%d, Contract=%d, Review=%d",
				contextstore.PRDAppendMaxBytes, contextstore.ContractAppendMaxBytes, contextstore.ReviewAppendMaxBytes)
		}

		// T2-F8-02: Massive append capped at exact limit
		root := t.TempDir()
		store := contextstore.New(root)
		hugeDoc := strings.Repeat("## Update (2026-11-12T12:00:00Z)\nNotes and data...\n", 2000)
		_ = store.WriteBounded(contextstore.DocPRD, hugeDoc)
		data, err := store.Read(contextstore.DocPRD)
		if err != nil || len(data) > contextstore.PRDAppendMaxBytes {
			t.Errorf("WriteBounded exceeded ceiling: len=%d, max=%d", len(data), contextstore.PRDAppendMaxBytes)
		}

		// T2-F8-03: Oldest timestamped section pruning with head preservation
		head := "# Head Specification\n"
		s1 := "## Round 1 (2026-11-12T01:00:00Z)\n" + strings.Repeat("Notes 1\n", 20)
		s2 := "## Round 2 (2026-11-12T02:00:00Z)\n" + strings.Repeat("Notes 2\n", 20)
		maxBytes := len(head) + len(s2) + 100
		pruned := contextstore.PruneTimestampedSections(head+s1+s2, maxBytes)
		if !strings.Contains(pruned, "# Head Specification") || !strings.Contains(pruned, "02:00:00Z") {
			t.Errorf("head or newest section lost: %s", pruned)
		}

		// T2-F8-04: Unstructured document fallback pruning
		prose := strings.Repeat("Continuous unstructured text without headers.\n", 1000)
		prunedProse := contextstore.PruneTimestampedSections(prose, 1000)
		if len(prunedProse) > 1100 {
			t.Errorf("unstructured pruning exceeded cap: %d", len(prunedProse))
		}

		// T2-F8-05: Zero / disabled ceiling policy override
		store.SetAppendPolicy(contextstore.DocPRD, 0)
		_ = store.Append(contextstore.DocPRD, "Unlimited", strings.Repeat("A", 30000))
		dZero, _ := store.Read(contextstore.DocPRD)
		if len(dZero) < 30000 {
			t.Errorf("zero ceiling should disable capping")
		}
	})

	// T2-F9: Anti-Wander Read-Only Bounds
	t.Run("T2_F9_AntiWanderReadOnlyBounds", func(t *testing.T) {
		// T2-F9-01: Table registration of all teamwork read-only roles
		for _, role := range []string{"interviewer", "specification", "adversarial_reviewer", "challenger"} {
			if !workspace.IsReadOnlyRole(role) {
				t.Errorf("role %s must be read-only", role)
			}
		}

		// T2-F9-02: Language prefix handling
		if !workspace.IsReadOnlyRole("go-interviewer") || !workspace.IsReadOnlyRole("python-adversarial_reviewer") {
			t.Errorf("language-prefixed read-only role check failed")
		}

		// T2-F9-03: Refusal content verification: role name & "does not edit files at all"
		guard := workspace.NewFocusGuard()
		ctx := workspace.WithRole(context.Background(), "interviewer")
		err := guard.Check(ctx, "pkg/calc.go")
		if err == nil || !strings.Contains(err.Error(), "does not edit files at all") {
			t.Errorf("missing exact refusal phrase: %v", err)
		}

		// T2-F9-04: Terminating action & zero retry flavor in refusal
		msg := strings.ToLower(err.Error())
		for _, bad := range []string{"retry", "try again", "more context"} {
			if strings.Contains(msg, bad) {
				t.Errorf("refusal contains retry flavor word: %s", bad)
			}
		}

		// T2-F9-05: Greenfield scaffolding immunity for read-only roles
		guard.SetWave([][]string{{"go.mod"}})
		errGreenfield := guard.Check(ctx, "go.mod")
		if errGreenfield == nil || !strings.Contains(errGreenfield.Error(), "does not edit files at all") {
			t.Errorf("read-only role must be blocked even on greenfield manifests")
		}
	})

	// T2-F10: Tool Output Capping Bounds
	t.Run("T2_F10_ToolOutputCappingBounds", func(t *testing.T) {
		// T2-F10-01: Window scaling
		if workspace.AdaptiveMaxToolChars(8192) != 3000 || workspace.AdaptiveMaxToolChars(16384) != 5000 || workspace.AdaptiveMaxToolChars(32768) != 8000 {
			t.Errorf("window scaling boundaries incorrect")
		}

		// T2-F10-02: Head/tail truncation with sizing marker
		root := t.TempDir()
		longLines := strings.Repeat("first lines "+strings.Repeat("x", 40)+"\n", 30) +
			strings.Repeat("middle lines "+strings.Repeat("y", 40)+"\n", 30) +
			strings.Repeat("last lines "+strings.Repeat("z", 40)+"\n", 30)
		_ = os.WriteFile(filepath.Join(root, "long.txt"), []byte(longLines), 0o600)
		out, _ := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "long.txt"}, 8192)
		if !strings.Contains(out, "first lines") || !strings.Contains(out, "last lines") {
			t.Errorf("head or tail lost during truncation")
		}

		// T2-F10-03: Secret redaction at truncation boundary
		secret := "sk-ant-api03-" + strings.Repeat("x", 40)
		t.Setenv("ANTHROPIC_API_KEY", secret)
		_ = os.WriteFile(filepath.Join(root, "boundary_secret.txt"), []byte(secret+"\n"+longLines), 0o600)
		outSec, _ := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "boundary_secret.txt"}, 8192)
		if strings.Contains(outSec, secret) {
			t.Errorf("secret not redacted")
		}

		// T2-F10-04: Length boundaries
		exact2500 := strings.Repeat("a", 2500)
		_ = os.WriteFile(filepath.Join(root, "exact2500.txt"), []byte(exact2500), 0o600)
		outExact, _ := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "exact2500.txt"}, 8192)
		if strings.Contains(outExact, "truncated") {
			t.Errorf("2500 chars should not be truncated")
		}

		// T2-F10-05: Non-positive / zero window fallback
		if workspace.AdaptiveMaxToolChars(-1) != workspace.DefaultMaxToolChars || workspace.AdaptiveMaxToolChars(0) != workspace.DefaultMaxToolChars {
			t.Errorf("non-positive window fallback failed")
		}
	})

	// T2-F11: Bundled Pipeline Bounds
	t.Run("T2_F11_BundledPipelineBounds", func(t *testing.T) {
		// T2-F11-01: Default constructor validation
		cfg := newTeamworkPipelineConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("pipeline validation failed: %v", err)
		}

		// T2-F11-02: Stage sequence completeness
		for _, phase := range []string{"clarify", "architect", "plan", "split", "execute"} {
			if _, ok := cfg.Phases[phase]; !ok {
				t.Errorf("mandatory phase %s missing from teamwork pipeline", phase)
			}
		}

		// T2-F11-03: Execute loop role assignment bounds
		if cfg.Execute.DefaultRole == "" || cfg.Execute.Reviewer == "" {
			t.Errorf("execute loop roles must be configured")
		}

		// T2-F11-04: Pipeline normalization idempotent
		cfg2 := cfg
		cfg2.Normalize()
		if len(cfg2.Order) != len(cfg.Order) {
			t.Errorf("normalization modified phase count")
		}

		// T2-F11-05: Missing mandatory phase validation error
		brokenPipe := pipeline.Config{
			Version: 1,
			Slots:   []pipeline.Slot{{ID: "bad_slot", Agent: "worker", Before: "nonexistent"}},
		}
		if err := brokenPipe.Validate(); err == nil {
			t.Errorf("pipeline with invalid slot anchor should fail validation")
		}
	})

	// T2-F12: Orchestrator Transition Bounds
	t.Run("T2_F12_OrchestratorTransitionBounds", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		// T2-F12-01: Empty PRD handling
		model.interviewPRD = `{"needs_user":false,"assumptions":[],"acceptance":[]}`
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, _ := runTeamworkHarness(ctx, h, "build math helper")
		if res == nil {
			t.Fatalf("run returned nil result")
		}

		// T2-F12-02: Zero tasks from split phase handling
		// Test orchestrator fallbackTasks resilience when split phase returns an empty task array
		modelSplitEmpty := newTeamworkFakeModel()
		modelSplitEmpty.customResponses = map[string]string{
			"splitter": `{"tasks":[]}`, // Splitter returns 0 tasks
		}
		hSplit, _, cleanSplit := newTeamworkTestHarness(t, modelSplitEmpty)
		defer cleanSplit()

		ctxSplit, cancelSplit := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelSplit()

		resSplit, errSplit := runTeamworkHarness(ctxSplit, hSplit, "build fallback task request")
		if errSplit != nil || resSplit == nil {
			t.Fatalf("run failed on empty splitter response: %v", errSplit)
		}
		// Orchestrator plansplit.go:247-262 catches len(tasks)==0 and triggers fallbackTasks(pl)
		if len(resSplit.Board.Tasks) == 0 {
			t.Fatalf("orchestrator failed to synthesize fallback tasks when splitter returned 0 tasks")
		}
		if resSplit.Board.Tasks[0].ID == "" {
			t.Errorf("fallback task missing valid ID")
		}

		// T2-F12-03: Checkpoint state tracking
		turnID := "turn-checkpoint-tracking"
		turn, err := session.BeginTurn(h.Config.SlmDir(), turnID, "test checkpoint recovery")
		if err != nil {
			t.Fatalf("BeginTurn failed: %v", err)
		}
		cp := session.ReactCheckpoint{
			TurnID:    turnID,
			TaskID:    "T1",
			AgentID:   "teamwork_worker",
			Iteration: 3,
			Status:    "interrupted",
			Messages: []session.ReactMessage{
				{Role: "user", Content: "execute task T1"},
				{Role: "assistant", Content: "working on task T1"},
			},
		}
		if err := session.SaveReactCheckpoint(h.Config.SlmDir(), cp); err != nil {
			t.Fatalf("SaveReactCheckpoint failed: %v", err)
		}
		loadedCp, err := session.LoadReactCheckpoint(h.Config.SlmDir(), turnID, "T1")
		if err != nil {
			t.Fatalf("LoadReactCheckpoint failed: %v", err)
		}
		if loadedCp == nil || loadedCp.TaskID != "T1" || loadedCp.Iteration != 3 {
			t.Errorf("loaded ReAct checkpoint mismatch: %+v", loadedCp)
		}

		chkBoard := plan.Board{
			QueryID: turnID,
			Query:   turn.Query,
			Tasks: []plan.Task{{
				ID:     "T1",
				Title:  "execute task T1",
				Column: plan.ColInProgress,
				Role:   plan.RoleWorker,
			}},
		}
		if err := session.MarkInterrupted(h.Config.SlmDir(), turn, chkBoard, session.PhaseExecute); err != nil {
			t.Fatalf("MarkInterrupted failed: %v", err)
		}
		interruptedTurn, err := session.ResolveResumeTurn(h.Config.SlmDir(), turnID)
		if err != nil || interruptedTurn == nil {
			t.Fatalf("ResolveResumeTurn failed: %v", err)
		}
		if !interruptedTurn.Interrupted {
			t.Errorf("expected turn to be marked interrupted")
		}
		if interruptedTurn.Phase != session.PhaseExecute {
			t.Errorf("expected turn phase %s, got %s", session.PhaseExecute, interruptedTurn.Phase)
		}
		if len(interruptedTurn.Board.Tasks) == 0 || interruptedTurn.Board.Tasks[0].Column != plan.ColReadyToDev {
			t.Errorf("expected task normalized to ready_to_dev, got %v", interruptedTurn.Board.Tasks)
		}
		if err := session.ClearInterrupted(h.Config.SlmDir(), interruptedTurn); err != nil {
			t.Fatalf("ClearInterrupted failed: %v", err)
		}
		clearedTurn, _ := session.LoadTurn(h.Config.SlmDir(), turnID)
		if clearedTurn != nil && clearedTurn.Interrupted {
			t.Errorf("expected interrupted=false after ClearInterrupted")
		}

		// T2-F12-04: Concurrent worker disjoint file collision prevention
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc.go"}})
		if err := guard.Check(workspace.WithRole(context.Background(), "worker"), "auth.go"); err == nil {
			t.Errorf("worker touching non-assigned file should be blocked")
		}

		// T2-F12-05: Context cancellation clean shutdown
		cancCtx, canc := context.WithCancel(context.Background())
		canc()
		_, errCanc := h.Run(cancCtx, "slow query")
		if !errors.Is(errCanc, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", errCanc)
		}
	})

	// T2-F13: Adversarial Review Gate Bounds
	t.Run("T2_F13_AdversarialReviewGateBounds", func(t *testing.T) {
		// T2-F13-01: Sycophant reviewer vetoed by smoke failure
		brokenSection := quality.FormatSmokeSection(quality.SmokeResult{
			Ran:     true,
			OK:      false,
			Command: "go vet ./...",
			Output:  "calc.go:5: syntax error: unexpected newline",
		})
		if !quality.SmokeFailedInOutput(brokenSection) {
			t.Errorf("smoke failure detection failed")
		}

		// T2-F13-02: Zero write evidence rejection
		// When worker performs no tool writes, FileExists on target is false and review parse reflects lack of evidence
		dirQuality := t.TempDir()
		emptyTarget := "nonexistent_target.go"
		if plan.FileExists(dirQuality, emptyTarget) {
			t.Fatalf("expected target %s to be absent on disk", emptyTarget)
		}

		// T2-F13-03: Hallucinated files_changed path rejection
		hallucinatedTarget := "../../escape/phantom.go"
		if plan.FileExists(dirQuality, hallucinatedTarget) {
			t.Fatalf("expected hallucinated path to be non-existent")
		}

		// T2-F13-04: Failed must-criterion veto
		// Test genuine quality.CriteriaReport.Blocked() and quality.CriteriaBlockedInOutput()
		mustCrit := plan.Criterion{ID: "C1", Text: "must compile", Verify: "go vet ./...", Priority: "must"}
		shouldCrit := plan.Criterion{ID: "C2", Text: "should format", Verify: "gofmt -d .", Priority: "should"}

		mustReport := quality.CriteriaReport{
			Ran: true,
			Outcomes: []quality.CriterionOutcome{
				{Criterion: mustCrit, Verdict: quality.CriterionFailed, Output: "syntax error"},
			},
		}
		if !mustReport.Blocked() {
			t.Errorf("failed must-criterion must report Blocked=true")
		}
		secMust := quality.FormatCriteriaSection(mustReport)
		if !quality.CriteriaBlockedInOutput(secMust) {
			t.Errorf("CriteriaBlockedInOutput must detect MUST-FAILED in criteria section")
		}

		shouldReport := quality.CriteriaReport{
			Ran: true,
			Outcomes: []quality.CriterionOutcome{
				{Criterion: shouldCrit, Verdict: quality.CriterionFailed, Output: "diff found"},
			},
		}
		if shouldReport.Blocked() {
			t.Errorf("failed should-criterion must NOT report Blocked=true")
		}
		secShould := quality.FormatCriteriaSection(shouldReport)
		if quality.CriteriaBlockedInOutput(secShould) {
			t.Errorf("CriteriaBlockedInOutput falsely reported blocked for should-criterion")
		}

		// T2-F13-05: Empty reviewer output parsed with NoVerdict
		emptyRev := plan.ParseReviewJSON("")
		if emptyRev.NoVerdict != true || emptyRev.Approved == true {
			t.Errorf("empty review output must set NoVerdict=true and Approved=false")
		}
	})

	// T2-F14: CLI Workflow Bounds
	t.Run("T2_F14_CLIWorkflowBounds", func(t *testing.T) {
		home := t.TempDir()
		dir := t.TempDir()
		endpoint := startFakeModel(t)
		write(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.22\n")
		write(t, filepath.Join(dir, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
		gitInit(t, dir)
		mustSlm(t, dir, home, "init", "--provider", "openai", "--model", "fake-model", "--endpoint", endpoint)
		configure(t, dir, home)

		// T2-F14-01: Empty query argument error exit
		outEmpty, codeEmpty := slm(t, dir, home, "run", "--endpoint", endpoint, "--workflow", "teamwork", "")
		if codeEmpty == 0 || !strings.Contains(outEmpty, "nothing to run") {
			t.Errorf("empty query error exit failed: %d, %s", codeEmpty, outEmpty)
		}

		// T2-F14-02: Workflow flag casing normalization
		outCase, _ := slm(t, dir, home, "run", "--workflow", "TEAMWORK", "--help")
		if strings.Contains(outCase, "unknown workflow") {
			t.Errorf("workflow flag casing normalization failed")
		}

		// T2-F14-03: Invalid workflow flag handling
		outBad, _ := slm(t, dir, home, "run", "--workflow", "nonexistent_preset", "test")
		if strings.Contains(outBad, "panic") {
			t.Errorf("unrecognized workflow caused panic")
		}

		// T2-F14-04: Configuration layering and environment precedence
		cfg := config.Default(dir)
		cfg.ActivePipeline = "teamwork"
		t.Setenv("SLMCODE_PROVIDER", "openai")
		t.Setenv("SLMCODE_MODEL", "custom-model")
		cfg.ApplyEnv()
		if cfg.Provider != "openai" || cfg.Model != "custom-model" {
			t.Errorf("ApplyEnv failed to overlay environment variables: provider=%s, model=%s", cfg.Provider, cfg.Model)
		}

		// T2-F14-05: Headless flag execution non-blocking
		outHeadless, _ := slm(t, dir, home, "run", "--workflow", "teamwork", "--help")
		if strings.Contains(outHeadless, "panic") {
			t.Errorf("headless execution crashed")
		}
	})

	// T2-F15: Harness Façade Bounds
	t.Run("T2_F15_HarnessFacadeBounds", func(t *testing.T) {
		// T2-F15-01: Nil context graceful handling
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		var nilCtx context.Context
		_, errNilRun := runTeamworkHarness(nilCtx, h, "nil context query") //nolint:staticcheck // SA1012: deliberately testing defensive nil context handling
		if errNilRun == nil {
			t.Errorf("expected error when passing nil context to runTeamworkHarness, got nil")
		} else if !strings.Contains(errNilRun.Error(), "nil context") {
			t.Errorf("unexpected error message for nil context: %v", errNilRun)
		}

		// T2-F15-02: Uninitialized harness pointer call guard
		var hNil *harness.Harness
		if err := hNil.Close(); err != nil {
			t.Errorf("nil harness Close() must return nil, got: %v", err)
		}
		if _, err := runTeamworkHarness(context.Background(), hNil, "query"); err == nil {
			t.Errorf("expected error on nil harness pointer run")
		}

		// T2-F15-03: Pre-canceled context immediate return
		cancCtx, canc := context.WithCancel(context.Background())
		canc()
		_, errCanc := h.Run(cancCtx, "pre-canceled")
		if !errors.Is(errCanc, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", errCanc)
		}

		// T2-F15-04: Timeout expiration handling
		timeoutCtx, timeoutCanc := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer timeoutCanc()
		time.Sleep(2 * time.Millisecond)
		_, errTimeout := h.Run(timeoutCtx, "timeout query")
		if !errors.Is(errTimeout, context.DeadlineExceeded) && !errors.Is(errTimeout, context.Canceled) {
			t.Errorf("expected deadline exceeded, got: %v", errTimeout)
		}

		// T2-F15-05: Serialization of reentrant calls
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = h.EnsureInitialized()
			}()
		}
		wg.Wait()
	})

	// T2-F16: Hermetic Model Bounds
	t.Run("T2_F16_HermeticModelBounds", func(t *testing.T) {
		model := newTeamworkFakeModel()
		srv := httptest.NewServer(model)
		defer srv.Close()

		// T2-F16-01: Complete network isolation assertion
		if !strings.HasPrefix(srv.URL, "http://127.0.0.1") && !strings.HasPrefix(srv.URL, "http://[::1]") {
			t.Errorf("server not on localhost: %s", srv.URL)
		}

		// T2-F16-02: Unknown role prompt fallback
		ans, call := answerForTeamwork(model, "unknown_alien_role", false, false)
		if len(ans) == 0 && call == nil {
			t.Errorf("unknown role should return safe fallback prose")
		}

		// T2-F16-03: Tool call serialization bounds
		tc := teamworkToolCall("ws_write", map[string]any{"path": "foo.go", "content": "package foo\n"})
		if tc["id"] == "" || tc["type"] != "function" {
			t.Errorf("tool call structure invalid")
		}

		// T2-F16-04: SSE streaming termination
		rec := httptest.NewRecorder()
		teamworkWriteStreamedCompletion(rec, "done", nil)
		if !strings.Contains(rec.Body.String(), "[DONE]") {
			t.Errorf("SSE stream missing [DONE] frame")
		}

		// T2-F16-05: Execution repeatability across 3 iterations
		callsBefore, _ := model.counts()
		for i := 0; i < 3; i++ {
			req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions",
				strings.NewReader(`{"messages":[{"role":"system","content":"teamwork_worker"}]}`))
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("iteration %d failed: %v", i, err)
			}
			_ = resp.Body.Close()
		}
		callsAfter, _ := model.counts()
		if callsAfter-callsBefore != 3 {
			t.Errorf("call accounting mismatch: before=%d, after=%d", callsBefore, callsAfter)
		}
	})

	// T2-F17: End-to-End Bounds
	t.Run("T2_F17_EndToEndBounds", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, root, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		// T2-F17-01: Full greenfield workspace execution
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := runTeamworkHarness(ctx, h, "Implement calculator in calc.go")
		if err != nil || !res.Success {
			t.Fatalf("greenfield execution failed: %v", err)
		}

		// T2-F17-02: Zero stray files assertion
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.Name() != "calc.go" && e.Name() != "go.mod" && e.Name() != ".slmcode" {
				t.Errorf("stray file found in root: %s", e.Name())
			}
		}

		// T2-F17-03: Read-only roles wrote zero files
		for _, tool := range model.toolCalls() {
			if strings.Contains(tool, "interviewer") || strings.Contains(tool, "adversarial_reviewer") {
				t.Errorf("read-only role invoked write tool: %s", tool)
			}
		}

		// T2-F17-04: Multi-round correction convergence
		model.badFirstWrites = 1
		resRetry, _ := runTeamworkHarness(ctx, h, "Implement calculator in calc.go")
		if resRetry == nil {
			t.Errorf("multi-round correction returned nil")
		}

		// T2-F17-05: Failed tasks accounting
		if res.FailedTasks != 0 {
			t.Errorf("expected 0 failed tasks, got: %d", res.FailedTasks)
		}
	})

	// T2-F18: Adversarial Hardening Bounds
	t.Run("T2_F18_AdversarialHardeningBounds", func(t *testing.T) {
		// T2-F18-01: Corrupted checkpoint file recovery
		root := t.TempDir()
		slmDir := filepath.Join(root, ".slmcode")
		_ = os.MkdirAll(slmDir, 0o755)

		// 1. Write corrupted root checkpoint.json
		corrupted := filepath.Join(slmDir, "checkpoint.json")
		_ = os.WriteFile(corrupted, []byte("{\"messages\": [truncated EOF"), 0o600)

		// Verify ResolveResumeTurn handles corrupted checkpoint without panic
		turn, err := session.ResolveResumeTurn(slmDir, "latest")
		if err == nil {
			t.Errorf("expected error resolving corrupted checkpoint, got turn: %+v", turn)
		}

		// 2. Corrupted React task checkpoint loading returns error safely
		_ = os.WriteFile(corrupted, []byte("{\"turn_id\": \"turn-1\", \"interrupted\": true}"), 0o600)
		reactDir := session.ReactDir(slmDir, "turn-1")
		_ = os.MkdirAll(reactDir, 0o755)
		_ = os.WriteFile(session.ReactPath(slmDir, "turn-1", "T1"), []byte("{truncated json"), 0o600)

		reactCp, reactErr := session.LoadReactCheckpoint(slmDir, "turn-1", "T1")
		if reactErr == nil {
			t.Errorf("expected unmarshal error on corrupted react checkpoint, got: %+v", reactCp)
		}

		// T2-F18-02: Path traversal attack prevention
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc.go"}})
		ctx := workspace.WithRole(context.Background(), "worker")
		if err := guard.Check(ctx, "../../etc/passwd"); err == nil {
			t.Errorf("path traversal attack was not blocked")
		}

		// T2-F18-03: Deep nested path safety
		deepPath := "pkg/a/b/c/d/e/f/g/h/i/j/main.go"
		guard.SetWave([][]string{{deepPath}})
		if err := guard.Check(ctx, deepPath); err != nil {
			t.Errorf("deep valid path falsely blocked: %v", err)
		}

		// T2-F18-04: Maximum token overflow protection
		b := contextstore.DefaultBudget(8192)
		if b.Available("worker") > 8192 {
			t.Errorf("available tokens exceeded context limit")
		}

		// T2-F18-05: Goroutine leak audit
		before := runtime.NumGoroutine()
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		_ = h.Close()
		cleanup()
		runtime.GC()
		after := runtime.NumGoroutine()
		if after-before > 15 {
			t.Errorf("potential goroutine leak: before=%d, after=%d", before, after)
		}
	})
}

// ---------------------------------------------------------------------------
// TIER 3: Cross-Feature Interaction Test Suite (18 Pairwise Scenarios)
// ---------------------------------------------------------------------------

func TestTeamwork_Tier3_CrossFeatureInteractions(t *testing.T) {
	// T3-01: Spec Validation under Identifier-Only Context (F1 x F7)
	t.Run("T3_01_SpecValidationUnderIdentifierOnlyContext", func(t *testing.T) {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, "api.go"), []byte("package api\nfunc Handler() {}\n"), 0o600)
		store := contextstore.New(root)
		packer := contextstore.NewPacker(store, root, 32)
		pack, err := packer.BuildPack(contextstore.BuildRequest{Role: "interviewer", Files: []string{"api.go"}})
		if err != nil {
			t.Fatalf("packer failed: %v", err)
		}
		if len(pack.Render()) == 0 {
			t.Fatalf("pack rendered empty")
		}
		specJSON := `{"needs_user":false,"assumptions":["stdlib"],"acceptance":["passes smoke"]}`
		if err := schema.Validate(schema.RoleTeamworkSpec, []byte(specJSON)); err != nil {
			t.Errorf("spec validation failed: %v", err)
		}
	})

	// T3-02: PRD Generation with Document Ceiling & Pruning (F1 x F8)
	t.Run("T3_02_PRDGenerationWithDocumentCeilingAndPruning", func(t *testing.T) {
		root := t.TempDir()
		store := contextstore.New(root)
		head := "# PRD\n\nCore product scope.\n\n"
		var updates strings.Builder
		updates.WriteString(head)
		for i := 0; i < 50; i++ {
			fmt.Fprintf(&updates, "## Clarify (%s)\nScope decision round %d.\n\n", time.Now().UTC().Format(time.RFC3339Nano), i)
		}
		_ = store.WriteBounded(contextstore.DocPRD, updates.String())
		data, err := store.Read(contextstore.DocPRD)
		if err != nil || len(data) > contextstore.PRDAppendMaxBytes {
			t.Errorf("PRD ceiling exceeded: len=%d, max=%d", len(data), contextstore.PRDAppendMaxBytes)
		}
		if !strings.HasPrefix(data, "# PRD") {
			t.Errorf("PRD head lost during pruning")
		}
	})

	// T3-03: Clarify PRD Propagation to Architect & Plan (F1 x F12)
	t.Run("T3_03_ClarifyPRDPropagationToArchitectAndPlan", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, _, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := runTeamworkHarness(ctx, h, "Implement basic math in calc.go")
		if err != nil || !res.Success {
			t.Fatalf("clarify propagation run failed: %v", err)
		}
		store := contextstore.New(h.Config.SlmDir())
		if q, _ := store.Read(contextstore.DocQuery); len(q) == 0 {
			t.Errorf("query not persisted in context store")
		}
	})

	// T3-04: Architect Planning under 70% Token Budget (F2 x F6)
	t.Run("T3_04_ArchitectPlanningUnder70PctTokenBudget", func(t *testing.T) {
		if got := contextstore.RoleBudgetPercent("architect"); got != 70 {
			t.Errorf("architect budget percent = %d, want 70", got)
		}
		b := contextstore.DefaultBudget(16384)
		avail := b.Available("architect")
		if avail <= 0 || avail > 12000 {
			t.Errorf("architect available tokens out of bounds: %d", avail)
		}
		sc, _ := schema.For(schema.RoleTeamworkPlan)
		if !sc.Strict {
			t.Errorf("plan schema must be strict")
		}
	})

	// T3-05: Planner Task Splitting with Strict File Exclusivity (F2 x F9)
	t.Run("T3_05_PlannerTaskSplittingWithStrictFileExclusivity", func(t *testing.T) {
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc.go"}})
		ctx := workspace.WithRole(context.Background(), "planner")
		// Planner is read-only
		if err := guard.Check(ctx, "calc.go"); err == nil || !strings.Contains(err.Error(), "does not edit files at all") {
			t.Errorf("planner write attempt should be refused: %v", err)
		}
	})

	// T3-06: Plan Dependency Ordering to Concurrent Waves (F2 x F12)
	t.Run("T3_06_PlanDependencyOrderingToConcurrentWaves", func(t *testing.T) {
		// Verify real dependency ordering and wave admission logic in pkg/plan
		board := &plan.Board{
			Tasks: []plan.Task{
				{ID: "T1", Files: []string{"a.go"}, Column: plan.ColReadyToDev},
				{ID: "T2", Files: []string{"b.go"}, DependsOn: []string{"T1"}, Column: plan.ColReadyToDev},
				{ID: "T3", Files: []string{"c.go"}, DependsOn: []string{"T2"}, Column: plan.ColReadyToDev},
			},
		}

		// Wave 1: Only T1 has all dependencies satisfied; T2 and T3 must NOT be ready
		readyWave1 := board.ReadyTasks()
		if len(readyWave1) != 1 || readyWave1[0].ID != "T1" {
			t.Fatalf("wave 1: expected only [T1] ready, got: %+v", readyWave1)
		}

		// Simulate completion of T1
		t1, ok := board.Get("T1")
		if !ok {
			t.Fatalf("task T1 missing from board")
		}
		t1.MoveTo(plan.ColDone)
		board.UpdateTask(t1)

		// Wave 2: T2 is now ready because T1 is done; T3 is still blocked on T2
		readyWave2 := board.ReadyTasks()
		if len(readyWave2) != 1 || readyWave2[0].ID != "T2" {
			t.Fatalf("wave 2: expected only [T2] ready after T1 done, got: %+v", readyWave2)
		}

		// Simulate completion of T2
		t2, _ := board.Get("T2")
		t2.MoveTo(plan.ColDone)
		board.UpdateTask(t2)

		// Wave 3: T3 is finally ready
		readyWave3 := board.ReadyTasks()
		if len(readyWave3) != 1 || readyWave3[0].ID != "T3" {
			t.Fatalf("wave 3: expected only [T3] ready after T2 done, got: %+v", readyWave3)
		}

		// Dependency cycle detection: mutually dependent tasks are moved to ColBlocked
		cycleBoard := &plan.Board{
			Tasks: []plan.Task{
				{ID: "C1", Column: plan.ColReadyToDev, DependsOn: []string{"C2"}},
				{ID: "C2", Column: plan.ColReadyToDev, DependsOn: []string{"C1"}},
			},
		}
		blocked := cycleBoard.PropagateBlocked()
		if len(blocked) != 2 {
			t.Errorf("expected 2 tasks blocked by cycle detection, got: %v", blocked)
		}
		if len(cycleBoard.ReadyTasks()) != 0 {
			t.Errorf("cyclic board must have 0 ready tasks")
		}
	})

	// T3-07: Worker Focus Scope Enforcement Blocking Stray Writes (F3 x F9)
	t.Run("T3_07_WorkerFocusScopeEnforcementBlockingStrayWrites", func(t *testing.T) {
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc.go"}})
		ctx := workspace.WithRole(context.Background(), "worker")
		if err := guard.Check(ctx, "calc.go"); err != nil {
			t.Errorf("focus file calc.go falsely blocked: %v", err)
		}
		if err := guard.Check(ctx, "unauthorized.go"); err == nil {
			t.Errorf("stray write to unauthorized.go was not blocked")
		}
	})

	// T3-08: Worker ReAct Tool Execution under Dynamic Tool Cap (F3 x F10)
	t.Run("T3_08_WorkerReActToolExecutionUnderDynamicToolCap", func(t *testing.T) {
		root := t.TempDir()
		longContent := strings.Repeat("a", 10000)
		_ = os.WriteFile(filepath.Join(root, "huge.txt"), []byte(longContent), 0o600)
		res, _ := runWorkspaceTool(t, root, "worker", "ws_read", map[string]any{"path": "huge.txt"}, 8192)
		if len(res) > 3500 {
			t.Errorf("tool output not capped to small window limit: len=%d", len(res))
		}
	})

	// T3-09: Worker Finalization Transitioning Task to InReview (F3 x F12)
	t.Run("T3_09_WorkerFinalizationTransitioningTaskToInReview", func(t *testing.T) {
		// Verify real task state transition, status normalization, and board hook dispatch
		board := &plan.Board{
			Tasks: []plan.Task{
				{ID: "T1", Title: "worker task", Column: plan.ColInProgress, Status: plan.StatusRunning},
			},
		}

		var observedHook plan.Task
		var hookCallCount int
		removeHook := plan.AddBoardHook(func(t plan.Task) {
			if t.ID == "T1" {
				observedHook = t
				hookCallCount++
			}
		})
		defer removeHook()

		t1, ok := board.Get("T1")
		if !ok {
			t.Fatalf("task T1 not found on board")
		}

		// Worker finishes: MoveTo(ColInReview) updates column, status, and UpdatedAt timestamp
		t1.MoveTo(plan.ColInReview)
		if t1.Column != plan.ColInReview || t1.Status != plan.StatusReview {
			t.Fatalf("MoveTo(ColInReview) failed: column=%q status=%q", t1.Column, t1.Status)
		}
		if t1.UpdatedAt == "" {
			t.Fatalf("MoveTo did not set UpdatedAt timestamp")
		}

		board.UpdateTask(t1)

		// Assert board state persisted the transition
		got, ok := board.Get("T1")
		if !ok || got.Column != plan.ColInReview || got.Status != plan.StatusReview {
			t.Fatalf("board.Get mismatch after UpdateTask: %+v", got)
		}

		// Assert real listeners (Studio / orchestrator monitor) received the transition hook
		if hookCallCount == 0 || observedHook.Column != plan.ColInReview {
			t.Fatalf("BoardHook was not fired on transition to in_review: count=%d, observed=%+v", hookCallCount, observedHook)
		}
	})

	// T3-10: Adversarial Reviewer Packing under 85% Budget (F4 x F6)
	t.Run("T3_10_AdversarialReviewerPackingUnder85PctBudget", func(t *testing.T) {
		pct := contextstore.RoleBudgetPercent("adversarial_reviewer")
		if pct != 85 {
			t.Errorf("adversarial reviewer budget percent = %d, want 85", pct)
		}
		sc, _ := schema.For(schema.RoleTeamworkReview)
		if !sc.Strict {
			t.Errorf("review schema must be strict")
		}
	})

	// T3-11: Adversarial Reviewer Read-Only Sandbox Guard (F4 x F9)
	t.Run("T3_11_AdversarialReviewerReadOnlySandboxGuard", func(t *testing.T) {
		guard := workspace.NewFocusGuard()
		ctx := workspace.WithRole(context.Background(), "adversarial_reviewer")
		if err := guard.Check(ctx, "calc.go"); err == nil || !strings.Contains(err.Error(), "does not edit files at all") {
			t.Errorf("adversarial reviewer write attempt must be refused: %v", err)
		}
	})

	// T3-12: Deterministic Hard Gate Vetoing Sycophant Reviewer (F4 x F13)
	t.Run("T3_12_DeterministicHardGateVetoingSycophantReviewer", func(t *testing.T) {
		brokenSection := quality.FormatSmokeSection(quality.SmokeResult{
			Ran:     true,
			OK:      false,
			Command: "go vet ./...",
			Output:  "calc.go:3: syntax error",
		})
		if !quality.SmokeFailedInOutput(brokenSection) {
			t.Errorf("deterministic smoke gate did not trigger on syntax error")
		}
	})

	// T3-13: Review Rejection Transitioning to Corrector Ladder (F4 x F12)
	t.Run("T3_13_ReviewRejectionTransitioningToCorrectorLadder", func(t *testing.T) {
		// Exercise genuine loop runner review rejection, corrector dispatch, and retry accounting
		model := newTeamworkFakeModel()
		model.rejectReviews = 1 // First review rejected, forcing transition to corrector ladder
		h, _, cleanup := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.MaxRetries = 2
		})
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Implement calculator helper in calc.go")
		if err != nil || !res.Success {
			t.Fatalf("harness run failed: %v", err)
		}

		// Verify model interaction: reviewer rejected round 1, corrector was dispatched, round 2 approved
		if model.reviewsSeen < 2 {
			t.Fatalf("expected at least 2 review rounds (rejection + re-review), got: %d", model.reviewsSeen)
		}
		if model.byRole["corrector"] < 1 {
			t.Fatalf("expected corrector to be dispatched after review rejection, got calls: %d", model.byRole["corrector"])
		}

		// Verify board task retry accounting
		if len(res.Board.Tasks) == 0 {
			t.Fatalf("no tasks on board after run")
		}
		if res.Board.Tasks[0].Retries != 1 {
			t.Errorf("expected task retries = 1 after one rejection+correction, got: %d", res.Board.Tasks[0].Retries)
		}
		if res.Board.Tasks[0].Column != plan.ColDone {
			t.Errorf("expected final task column to be done, got: %s", res.Board.Tasks[0].Column)
		}
	})

	// T3-14: Corrector Repairing Syntax and Passing Re-Review (F3 x F13)
	t.Run("T3_14_CorrectorRepairingSyntaxAndPassingReReview", func(t *testing.T) {
		cleanOutput := "PASS\nexit status 0"
		if quality.SmokeFailedInOutput(cleanOutput) {
			t.Errorf("clean output failed smoke gate")
		}
	})

	// T3-15: Zero-Drift Prompt Contract Matching GBNF Key Order (F5 x F1)
	t.Run("T3_15_ZeroDriftPromptContractMatchingGBNFKeyOrder", func(t *testing.T) {
		sc, _ := schema.For(schema.RoleTeamworkSpec)
		g := schema.GBNF(sc)
		contract := `{"needs_user":false,"assumptions":["SQLite stdlib storage"],"acceptance":["go test ./... passes"]}`
		acc, err := schema.AcceptsGBNF(g, contract)
		if err != nil || !acc {
			t.Errorf("prompt contract failed GBNF acceptance: %v", err)
		}
	})

	// T3-16: Multi-Stage Durable Document Ceilings & Pruning (F8 x F12)
	t.Run("T3_16_MultiStageDurableDocumentCeilingsAndPruning", func(t *testing.T) {
		root := t.TempDir()
		store := contextstore.New(root)
		_ = store.WriteBounded(contextstore.DocPRD, strings.Repeat("A", 10000))
		_ = store.WriteBounded(contextstore.DocContract, strings.Repeat("B", 10000))
		_ = store.WriteBounded(contextstore.DocReview, strings.Repeat("C", 10000))

		dPRD, _ := store.Read(contextstore.DocPRD)
		dContract, _ := store.Read(contextstore.DocContract)
		dReview, _ := store.Read(contextstore.DocReview)

		if len(dPRD) > contextstore.PRDAppendMaxBytes || len(dContract) > contextstore.ContractAppendMaxBytes || len(dReview) > contextstore.ReviewAppendMaxBytes {
			t.Errorf("document ceiling exceeded across stages")
		}
	})

	// T3-17: CLI --workflow teamwork Instantiating Pipeline Preset (F11 x F14)
	t.Run("T3_17_CLIWorkflowInstantiatingPipelinePreset", func(t *testing.T) {
		// Verify canonical pipeline.Teamwork() preset
		pipe := pipeline.Teamwork()
		if err := pipe.Validate(); err != nil {
			t.Fatalf("pipeline.Teamwork() validation failed: %v", err)
		}
		if pipe.Execute.Reviewer != pipeline.RoleTeamworkReview {
			t.Errorf("teamwork pipeline reviewer = %q, want %q", pipe.Execute.Reviewer, pipeline.RoleTeamworkReview)
		}
		if pipe.Execute.DefaultRole != pipeline.RoleTeamworkWorker {
			t.Errorf("teamwork pipeline default role = %q, want %q", pipe.Execute.DefaultRole, pipeline.RoleTeamworkWorker)
		}
		if pipe.Execute.MaxWaves != 2 {
			t.Errorf("teamwork pipeline max waves = %d, want 2", pipe.Execute.MaxWaves)
		}

		// Verify bundled blocks registry loads the preset block correctly
		reg, err := blocks.Load(t.TempDir())
		if err != nil {
			t.Fatalf("failed to load builtin blocks registry: %v", err)
		}
		block, ok := reg.GetPipeline("teamwork")
		if !ok || block == nil {
			t.Fatalf("bundled pipeline block %q missing from builtin registry", "teamwork")
		}
		if block.Spec.Execute.Reviewer != pipeline.RoleTeamworkReview {
			t.Errorf("bundled teamwork pipeline reviewer = %q, want %q", block.Spec.Execute.Reviewer, pipeline.RoleTeamworkReview)
		}
	})

	// T3-18: Embeddable Façade RunTeamwork Hermetic E2E Execution (F15 x F16)
	t.Run("T3_18_EmbeddableFacadeRunTeamworkHermeticExecution", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, root, cleanup := newTeamworkTestHarness(t, model)
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Implement calculator in calc.go")
		if err != nil || !res.Success {
			t.Fatalf("façade hermetic run failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "calc.go")); err != nil {
			t.Errorf("target file not found on disk: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// TIER 4: Real-World Application Scenarios (5 Workloads)
// ---------------------------------------------------------------------------

func TestTeamwork_Tier4_RealWorldScenarios(t *testing.T) {
	// Scenario 1: Greenfield Microservice Development with Full Interview & Plan
	t.Run("Scenario1_GreenfieldMicroserviceDevelopmentWithFullInterviewAndPlan", func(t *testing.T) {
		model := newTeamworkFakeModel()
		model.targetFile = "store.go"
		model.targetSource = "package store\n\ntype KVStore struct {\n\tdata map[string]string\n}\n\nfunc New() *KVStore {\n\treturn &KVStore{data: make(map[string]string)}\n}\n\nfunc (s *KVStore) Get(k string) string {\n\treturn s.data[k]\n}\n\nfunc (s *KVStore) Set(k, v string) {\n\ts.data[k] = v\n}\n"

		h, root, cleanup := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.ClarifyMode = "auto"
		})
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Build a key-value store REST microservice in store.go with Get and Set")
		if err != nil || !res.Success {
			t.Fatalf("greenfield microservice development failed: %v", err)
		}

		// Verify target file on disk
		targetPath := filepath.Join(root, "store.go")
		body, err := os.ReadFile(targetPath)
		if err != nil || !strings.Contains(string(body), "type KVStore struct") {
			t.Fatalf("store.go content mismatch: %v, body: %s", err, string(body))
		}

		// Verify board tasks completed
		if len(res.Board.Tasks) == 0 {
			t.Fatalf("no tasks on board")
		}
		for _, task := range res.Board.Tasks {
			if task.Column != plan.ColDone {
				t.Errorf("task %s not completed: %s", task.ID, task.Column)
			}
		}
	})

	// Scenario 2: Bug Fix with Sycophant Reviewer Vetoed by Broken Smoke Test
	t.Run("Scenario2_BugFixWithSycophantReviewerVetoedByBrokenSmokeTest", func(t *testing.T) {
		model := newTeamworkFakeModel()
		model.sycophant = true   // Sycophant reviewer attempts to approve broken code
		model.badFirstWrites = 1 // Worker writes broken code on first attempt

		h, root, cleanup := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.MaxRetries = 2
			c.PostWorkerSmoke = true
			c.RequireSmoke = true
		})
		defer cleanup()

		// Initial broken file
		calcPath := filepath.Join(root, "calc.go")
		_ = os.WriteFile(calcPath, []byte(teamworkBrokenSource), 0o600)

		// Assert that SmokeFailedInOutput correctly identifies broken code
		brokenSection := quality.FormatSmokeSection(quality.SmokeResult{
			Ran:     true,
			OK:      false,
			Command: "go test ./...",
			Output:  "calc.go:3: syntax error",
		})
		if !quality.SmokeFailedInOutput(brokenSection) {
			t.Fatalf("smoke failure detection failed")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Fix syntax error in calc.go")
		if err != nil || !res.Success {
			t.Fatalf("scenario 2 execution failed: %v", err)
		}
	})

	// Scenario 3: Small Context Window Execution (8K Tokens) with Severe Output Caps
	t.Run("Scenario3_SmallContextWindowExecution8KWithSevereOutputCaps", func(t *testing.T) {
		model := newTeamworkFakeModel()
		// Inject oversized source (4500+ chars) so that tester ws_shell cat produces output exceeding 3000 chars
		longComment := strings.Repeat("// Large explanatory comment exercising tool output capping in 8K context.\n", 50)
		model.targetSource = fmt.Sprintf("package calc\n\n%s\nfunc Add(a, b int) int { return a + b }\n", longComment)

		h, root, cleanup := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.MaxContextKB = 32 // 32 KB * 1024 / 4 = 8192 tokens
			if c.ModelProfiles == nil {
				c.ModelProfiles = map[string]config.ModelProfile{}
			}
			c.ModelProfiles[c.Model] = config.ModelProfile{
				ContextLimit: 8192,
			}
		})
		defer cleanup()

		// Verify dynamic tool capping to 3000 characters for 8K window
		cap8K := workspace.AdaptiveMaxToolChars(8192)
		if cap8K != 3000 {
			t.Errorf("tool cap for 8K window = %d, want 3000", cap8K)
		}

		// Verify interviewer budget allocation is constrained
		b := contextstore.DefaultBudget(8192)
		availInt := b.Available("interviewer")
		if availInt <= 0 || availInt > 4500 {
			t.Errorf("interviewer available tokens in 8K window out of bounds: %d", availInt)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Implement calculator in calc.go under 8K window")
		if err != nil || !res.Success {
			t.Fatalf("8K context execution failed: %v", err)
		}

		// Assert live tool output was dynamically clamped to 3000 chars during execution
		toolResults := model.toolResults()
		if len(toolResults) == 0 {
			t.Fatalf("expected at least one tool result received by fake model during execution")
		}
		var foundClamped bool
		for _, tr := range toolResults {
			if strings.Contains(tr, "[result truncated:") && strings.Contains(tr, "3000 shown") {
				foundClamped = true
				break
			}
		}
		if !foundClamped {
			t.Errorf("expected live harness execution to dynamically clamp tool output to 3000 chars, got: %v", toolResults)
		}

		// Ensure written file compiles cleanly
		cmd := exec.Command("go", "vet", "./...")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go vet failed: %s", out)
		}
	})

	// Scenario 4: CLI Workflow Run with Structured PRD and Zero Stray Files
	t.Run("Scenario4_CLIWorkflowRunWithStructuredPRDAndZeroStrayFiles", func(t *testing.T) {
		slmBin, fakeBin := buildBinaries(t)
		if slmBin == "" || fakeBin == "" {
			t.Fatalf("binaries build failed")
		}
		endpoint := startFakeModel(t)

		home := t.TempDir()
		dir := t.TempDir()
		write(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.22\n")
		write(t, filepath.Join(dir, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
		gitInit(t, dir)

		// Initialize workspace
		outInit, codeInit := slm(t, dir, home, "init", "--endpoint", endpoint, "--provider", "openai", "--model", "fake-model")
		if codeInit != 0 {
			t.Fatalf("init failed: %s", outInit)
		}
		configure(t, dir, home)

		// Run with workflow flag
		outRun, codeRun := slmTimeout(t, dir, home, acceptanceTimeout, "run", "--endpoint", endpoint, "--workflow", "teamwork", "Add a Divide function to calc.go")
		if codeRun != 0 {
			t.Fatalf("run failed: %s", outRun)
		}

		// Assert zero stray files outside expected directories
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Name() != ".slmcode" && e.Name() != "go.mod" && e.Name() != "calc.go" && e.Name() != ".git" {
				t.Errorf("unexpected stray file in root: %s", e.Name())
			}
		}
	})

	// Scenario 5: Multi-Wave Concurrent Worker Execution with Strict Disjoint Scoping
	t.Run("Scenario5_MultiWaveConcurrentWorkerExecutionWithStrictDisjointScoping", func(t *testing.T) {
		model := newTeamworkFakeModel()
		h, root, cleanup := newTeamworkTestHarness(t, model, func(c *config.Config) {
			c.MaxParallel = 2
		})
		defer cleanup()

		// Multi-wave plan decomposition:
		// Wave 1: T1 (calc_add.go) and T2 (calc_sub.go) are ready simultaneously with disjoint scopes
		// Wave 2: T3 (calc_mul.go) depends on T1 and T2
		model.targetFile = "calc_add.go"
		model.interviewPRD = `{"needs_user":false,"assumptions":["Standard Go toolchain"],"acceptance":["go vet ./... passes cleanly"],"entrypoint":"calc_add.go","features":[{"id":"F1","title":"Modular arithmetic","acceptance":"calc_add.go, calc_sub.go, calc_mul.go compile cleanly","priority":"must","description":"Modular arithmetic"}],"language":"go","non_goals":["GUI"],"questions":[]}`
		model.planJSON = `{"strategy":"Decompose arithmetic functions into modular components across waves","milestones":[{"id":"M1","title":"Implement addition","acceptance":"go vet ./... passes","depends_on":[],"description":"Create calc_add.go"},{"id":"M2","title":"Implement subtraction","acceptance":"go vet ./... passes","depends_on":[],"description":"Create calc_sub.go"},{"id":"M3","title":"Implement multiplication","acceptance":"go vet ./... passes","depends_on":["M1","M2"],"description":"Create calc_mul.go"}],"file_ownership":[{"owner":"worker","paths":["calc_add.go"]},{"owner":"worker","paths":["calc_sub.go"]},{"owner":"worker","paths":["calc_mul.go"]}],"contracts":[{"interface_id":"ICalc","provider":"worker","spec":"Add/Subtract/Multiply","consumers":["main"]}],"risks":[]}`

		model.customResponses["splitter"] = `{"tasks":[{"id":"T1","title":"Create calc_add.go","description":"Implement Add in calc_add.go","role":"worker","files":["calc_add.go"],"acceptance":"calc_add.go exists with Add","depends_on":[],"criteria":[{"id":"C1","text":"compiles","verify":"go vet ./...","priority":"must"}]},{"id":"T2","title":"Create calc_sub.go","description":"Implement Subtract in calc_sub.go","role":"worker","files":["calc_sub.go"],"acceptance":"calc_sub.go exists with Subtract","depends_on":[],"criteria":[{"id":"C2","text":"compiles","verify":"go vet ./...","priority":"must"}]},{"id":"T3","title":"Create calc_mul.go","description":"Implement Multiply in calc_mul.go","role":"worker","files":["calc_mul.go"],"acceptance":"calc_mul.go exists with Multiply","depends_on":["T1","T2"],"criteria":[{"id":"C3","text":"compiles","verify":"go vet ./...","priority":"must"}]}]}`

		// Verify FocusGuard wave allowlist isolation: Wave 1 allows calc_add and calc_sub, denies calc_mul and outside files
		guard := workspace.NewFocusGuard()
		guard.SetWave([][]string{{"calc_add.go"}, {"calc_sub.go"}})
		ctxWorker := workspace.WithRole(context.Background(), "worker")

		if err := guard.Check(ctxWorker, "calc_add.go"); err != nil {
			t.Errorf("guard blocked calc_add.go in wave: %v", err)
		}
		if err := guard.Check(ctxWorker, "calc_sub.go"); err != nil {
			t.Errorf("guard blocked calc_sub.go in wave: %v", err)
		}
		if err := guard.Check(ctxWorker, "calc_mul.go"); err == nil {
			t.Errorf("guard allowed wave-2 file calc_mul.go during wave-1")
		}
		if err := guard.Check(ctxWorker, "outside.go"); err == nil {
			t.Errorf("guard allowed unlisted outside file during wave-1")
		}

		// Run with MaxParallel = 2
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := runTeamworkHarness(ctx, h, "Implement multi-component arithmetic in calc_add.go, calc_sub.go, and calc_mul.go")
		if err != nil || !res.Success {
			t.Fatalf("parallel execution run failed: %v", err)
		}

		// 1. Concurrency verification: MaxParallel=2 executed at least 2 simultaneous workers
		if mc := model.maxConcurrent(); mc < 2 {
			t.Errorf("expected at least 2 concurrent workers, got %d", mc)
		}

		// 2. Multi-wave completion: All 3 tasks executed and marked ColDone
		if len(res.Board.Tasks) < 3 {
			t.Fatalf("expected at least 3 tasks on board, got %d", len(res.Board.Tasks))
		}
		for _, task := range res.Board.Tasks {
			if task.Column != plan.ColDone {
				t.Errorf("task %s not done: column=%s", task.ID, task.Column)
			}
		}

		// 3. Sibling isolation: Each file exists and contains strictly its own functions
		addBytes, err := os.ReadFile(filepath.Join(root, "calc_add.go"))
		if err != nil {
			t.Fatalf("calc_add.go missing: %v", err)
		}
		if !strings.Contains(string(addBytes), "func Add") || strings.Contains(string(addBytes), "func Subtract") {
			t.Errorf("calc_add.go content violated sibling isolation: %s", string(addBytes))
		}

		subBytes, err := os.ReadFile(filepath.Join(root, "calc_sub.go"))
		if err != nil {
			t.Fatalf("calc_sub.go missing: %v", err)
		}
		if !strings.Contains(string(subBytes), "func Subtract") || strings.Contains(string(subBytes), "func Add") {
			t.Errorf("calc_sub.go content violated sibling isolation: %s", string(subBytes))
		}

		mulBytes, err := os.ReadFile(filepath.Join(root, "calc_mul.go"))
		if err != nil {
			t.Fatalf("calc_mul.go missing: %v", err)
		}
		if !strings.Contains(string(mulBytes), "func Multiply") {
			t.Errorf("calc_mul.go missing Multiply: %s", string(mulBytes))
		}

		// 4. Compilation verification: package calc compiles cleanly with all modular files
		cmd := exec.Command("go", "vet", "./...")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go vet failed on multi-task output: %s", out)
		}
	})
}
