package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestRunTeamworkNilReceiver(t *testing.T) {
	var h *Harness
	res, err := h.RunTeamwork(context.Background(), "Implement calculator")
	if res != nil {
		t.Errorf("expected nil result on nil harness, got %v", res)
	}
	if err == nil {
		t.Fatal("expected error for nil harness receiver, got nil")
	}
	if !strings.Contains(err.Error(), "nil harness") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunTeamworkNilConfig(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunTeamwork panicked on nil Config: %v", r)
		}
	}()

	h := &Harness{Config: nil, Orchestrator: nil}
	res, err := h.RunTeamwork(context.Background(), "Implement calculator")
	if res != nil {
		t.Errorf("expected nil result, got %v", res)
	}
	if err == nil {
		t.Fatal("expected error on harness with nil config, got nil")
	}
	if !strings.Contains(err.Error(), "nil config") && !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunTeamworkNilContext(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	var nilCtx context.Context
	res, err := h.RunTeamwork(nilCtx, "Implement calculator") //nolint:staticcheck // SA1012: deliberately testing defensive nil context handling
	if res != nil {
		t.Errorf("expected nil result on nil context, got %v", res)
	}
	if err == nil {
		t.Fatal("expected error for nil context, got nil")
	}
	if !strings.Contains(err.Error(), "nil context") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunTeamworkPreCanceledContext(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := h.RunTeamwork(ctx, "Implement calculator")
	if res != nil {
		t.Errorf("expected nil result on canceled context, got %v", res)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestRunTeamworkEmptyQuery(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	for _, empty := range []string{"", "   ", "\t\n", "\r\n\t  ", "\n \t \r "} {
		res, err := h.RunTeamwork(context.Background(), empty)
		if res != nil {
			t.Errorf("expected nil result for empty query %q, got %v", empty, res)
		}
		if err == nil {
			t.Errorf("expected error for empty query %q, got nil", empty)
		}
		if !strings.Contains(err.Error(), "nothing to run") && !strings.Contains(err.Error(), "empty query") {
			t.Errorf("unexpected error message for %q: %v", empty, err)
		}
	}
}

func TestRunTeamworkClosedHarness(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	res, err := h.RunTeamwork(context.Background(), "Implement calculator")
	if res != nil {
		t.Errorf("expected nil result after close, got %v", res)
	}
	if err == nil {
		t.Fatal("expected error running on closed harness, got nil")
	}
	if !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("unexpected error message on closed harness: %v", err)
	}
}

func TestRunTeamworkNonDestructiveWorkflowRestoration(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	origWorkflow := config.WorkflowDiscover
	origDynamic := true
	h.Config.Workflow = origWorkflow
	h.Config.DynamicPipeline = origDynamic
	h.Config.Calibrate = config.CalibrateOff

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel fast to verify restoration on error
	_, _ = h.RunTeamwork(ctx, "Test query")

	if h.Config.Workflow != origWorkflow {
		t.Errorf("expected Workflow restored to %q, got %q", origWorkflow, h.Config.Workflow)
	}
	if h.Config.DynamicPipeline != origDynamic {
		t.Errorf("expected DynamicPipeline restored to %v, got %v", origDynamic, h.Config.DynamicPipeline)
	}
}

func TestRunTeamworkPipelineBindings(t *testing.T) {
	tw := pipeline.Teamwork()
	tw.Normalize()

	if tw.Execute.DefaultRole != pipeline.RoleTeamworkWorker {
		t.Errorf("expected DefaultRole %q, got %q", pipeline.RoleTeamworkWorker, tw.Execute.DefaultRole)
	}
	if tw.Execute.Reviewer != pipeline.RoleTeamworkReview {
		t.Errorf("expected Reviewer %q, got %q", pipeline.RoleTeamworkReview, tw.Execute.Reviewer)
	}

	clarifyPhase, ok := tw.Phases["clarify"]
	if !ok {
		t.Fatal("missing clarify phase in pipeline.Teamwork()")
	}
	if clarifyPhase.Agent != pipeline.RoleTeamworkSpec {
		t.Errorf("expected clarify agent %q, got %q", pipeline.RoleTeamworkSpec, clarifyPhase.Agent)
	}
	if clarifyPhase.When != pipeline.WhenAlways {
		t.Errorf("expected clarify When=WhenAlways, got %q", clarifyPhase.When)
	}

	planPhase, ok := tw.Phases["plan"]
	if !ok {
		t.Fatal("missing plan phase in pipeline.Teamwork()")
	}
	if planPhase.Agent != pipeline.RoleTeamworkPlan {
		t.Errorf("expected plan agent %q, got %q", pipeline.RoleTeamworkPlan, planPhase.Agent)
	}
	if planPhase.When != pipeline.WhenAlways {
		t.Errorf("expected plan When=WhenAlways, got %q", planPhase.When)
	}

	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	h.Config.Workflow = config.WorkflowTeamwork
	orchPipe := h.Orchestrator.Pipeline()
	if orchPipe == nil {
		t.Fatal("expected non-nil pipeline from Orchestrator.Pipeline()")
	}
	if orchPipe.Execute.Reviewer != pipeline.RoleTeamworkReview {
		t.Errorf("expected Orchestrator.Pipeline() Reviewer %q, got %q", pipeline.RoleTeamworkReview, orchPipe.Execute.Reviewer)
	}
	if orchPipe.PhaseWhen("clarify") != pipeline.WhenAlways {
		t.Errorf("expected PhaseWhen(clarify) WhenAlways, got %q", orchPipe.PhaseWhen("clarify"))
	}
}

func TestRunTeamworkConcurrentSafety(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	origWorkflow := config.WorkflowDiscover
	origDynamic := true
	h.Config.Workflow = origWorkflow
	h.Config.DynamicPipeline = origDynamic

	const concurrency = 6
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_, _ = h.RunTeamwork(ctx, "concurrent query")
		}()
	}

	wg.Wait()

	if h.Config.Workflow != origWorkflow {
		t.Errorf("CONCURRENCY BUG: h.Config.Workflow corrupted! got %q, want %q", h.Config.Workflow, origWorkflow)
	}
	if h.Config.DynamicPipeline != origDynamic {
		t.Errorf("CONCURRENCY BUG: h.Config.DynamicPipeline corrupted! got %v, want %v", h.Config.DynamicPipeline, origDynamic)
	}
}

// TestRunTeamworkHermeticExecution verifies genuine, end-to-end execution of RunTeamwork
// against an in-process mock model server producing valid structured outputs for all 4 stages.
func TestRunTeamworkHermeticExecution(t *testing.T) {
	const (
		modelID      = "test-teamwork-model"
		targetFile   = "calc.go"
		targetSource = "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Divide(a, b float64) (float64, error) {\n\tif b == 0 {\n\t\treturn 0, nil\n\t}\n\treturn a / b, nil\n}\n"
	)

	callsByRole := make(map[string]int)
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": modelID, "object": "model"}},
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
		for _, m := range req.Messages {
			all.WriteString(m.Content)
			all.WriteByte('\n')
			if m.Role == "tool" {
				sawToolResult = true
			}
		}
		system := ""
		if len(req.Messages) > 0 {
			system = req.Messages[0].Content
		}

		// Detect role
		role := "prose"
		switch {
		case strings.Contains(system, "teamwork_review") || strings.Contains(system, "teamwork-review") || strings.Contains(system, "adversarial_reviewer") || strings.Contains(system, "challenge_scenarios") || strings.Contains(system, "Second reviewer"):
			role = "reviewer"
		case strings.Contains(system, `{"approved"`), strings.Contains(system, "never approved"):
			role = "reviewer"
		case strings.Contains(system, "teamwork_spec") || strings.Contains(system, "teamwork-spec") || strings.Contains(system, "ScopePRD") || strings.Contains(system, "needs_user") || strings.Contains(system, "Teamwork specification and requirements"):
			role = "spec"
		case strings.Contains(system, "Teamwork architect and planner") || strings.Contains(system, "teamwork_plan") || strings.Contains(system, "teamwork-plan") || strings.Contains(system, "CONTRACT.md"):
			role = "plan"
		case strings.Contains(system, `"tasks":[{"id":"T1"`), strings.Contains(system, "splitter"):
			role = "splitter"
		case strings.Contains(system, `"steps":["step one"`), strings.Contains(system, "SLM planner"):
			role = "planner"
		case strings.Contains(system, "Verify the task by actually running") || strings.Contains(system, `"passed"`):
			role = "tester"
		case strings.Contains(system, `"relevant_files"`):
			role = "explorer"
		case strings.Contains(system, "teamwork_worker") || strings.Contains(system, "teamwork-worker") || strings.Contains(system, `"files_changed"`) || strings.Contains(system, "Implement ONE atomic task for Teamwork"):
			role = "worker"
		}
		if strings.Contains(all.String(), "needs_user") || strings.Contains(all.String(), "teamwork-spec") {
			role = "spec"
		}

		mu.Lock()
		callsByRole[role]++
		mu.Unlock()

		var content string
		var call map[string]any
		switch role {
		case "spec":
			content = `{"needs_user":false,"assumptions":["Go module exists"],"acceptance":["Divide function implemented"],"entrypoint":"calc.go","features":[{"id":"F1","title":"Divide function","acceptance":"Divide function implemented","priority":"must","description":"Add Divide to calc.go"}],"language":"go","non_goals":[]}`
		case "plan":
			content = `{"strategy":"Implement Divide in calc.go","milestones":[{"id":"M1","title":"Divide arithmetic","acceptance":"Divide implemented","depends_on":[],"description":"Implement Divide"}],"file_ownership":[{"owner":"worker","paths":["calc.go"]}],"contracts":[{"interface_id":"ICalc","provider":"worker","spec":"Divide(float64, float64) (float64, error)","consumers":["calc"]}],"risks":[]}`
		case "planner":
			content = `{"summary":"Implement Divide","steps":["Add Divide to calc.go"],"goals":[],"assumptions":[],"risks":[]}`
		case "splitter":
			content = `{"tasks":[{"id":"T1","title":"Implement Divide","description":"Add Divide to calc.go","role":"teamwork-worker","files":["calc.go"],"acceptance":"Divide exists in calc.go"}]}`
		case "tester":
			content = `{"passed":true,"summary":"all tests passed","failures":[]}`
		case "worker":
			if len(req.Tools) > 0 && !sawToolResult {
				b, _ := json.Marshal(map[string]any{"path": targetFile, "content": targetSource})
				call = map[string]any{
					"id": "call_write_1", "type": "function",
					"function": map[string]any{"name": "ws_write", "arguments": string(b)},
				}
			} else {
				content = `{"status":"done","summary":"Added Divide function to calc.go","files_changed":["calc.go"],"action_plan":["ws_write calc.go"],"diff_summary":"Added Divide function","notes":""}`
			}
		case "reviewer":
			content = `{"approved":true,"score":95,"verdict":"approve","summary":"Divide function verified on disk","challenge_scenarios":["Divide by zero handled"],"edge_cases":[],"issues":[]}`
		case "explorer":
			content = `{"summary":"calc package","relevant_files":["calc.go"],"key_symbols":["Add"],"risks":[],"notes":""}`
		default:
			content = "# Working Context\n\n## Active focus\nAdd divide function to calc.go\n"
		}

		if req.Stream {
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
			emit(map[string]any{"id": "cmpl-test", "object": "chat.completion.chunk", "model": modelID,
				"choices": []map[string]any{{"index": 0, "delta": delta}}})
			emit(map[string]any{"id": "cmpl-test", "object": "chat.completion.chunk", "model": modelID,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
				"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		msg := map[string]any{"role": "assistant", "content": content}
		finish := "stop"
		if call != nil {
			msg["content"] = ""
			msg["tool_calls"] = []any{call}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-test", "object": "chat.completion", "model": modelID,
			"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": msg}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40},
		})
	}))
	defer server.Close()

	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module testdemo\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, targetFile), []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)

	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	h.Config.Provider = "openai"
	h.Config.Endpoint = server.URL + "/v1"
	h.Config.Calibrate = config.CalibrateOff
	h.Config.FastModel = modelID
	h.Config.Model = modelID
	h.Config.StructuredDecoding = "off"
	h.Config.ClarifyMode = "auto"
	h.Config.PlanApprove = "auto"
	h.Config.ContinueAsk = "off"
	h.Config.EscalateAsk = "off"
	h.Config.QAGate = false
	h.Config.PostWorkerSmoke = false
	h.Config.RequireSmoke = false
	h.Config.ScopeJudge = false
	h.Config.PlaceholderPass = false
	h.Config.ThinkPasses = 1
	h.Config.MaxParallel = 1
	h.Config.MaxRetries = 1
	h.Config.TaskTimeout = 30 * time.Second
	h.Config.WriteGuard = false

	if err := h.RebuildOrchestrator(); err != nil {
		t.Fatalf("RebuildOrchestrator failed: %v", err)
	}
	if err := h.EnsureInitialized(); err != nil {
		t.Fatalf("EnsureInitialized failed: %v", err)
	}

	// Verify initial workflow before RunTeamwork is auto
	if h.Config.Workflow != config.WorkflowAuto {
		t.Errorf("initial workflow = %q, want %q", h.Config.Workflow, config.WorkflowAuto)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Genuinely execute RunTeamwork
	res, err := h.RunTeamwork(ctx, "Add a divide function")
	if err != nil {
		t.Fatalf("RunTeamwork failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil orchestrator.Result, got nil")
	}

	// 1. Verify Workflow was restored to auto
	if h.Config.Workflow != config.WorkflowAuto {
		t.Errorf("Workflow after RunTeamwork = %q, want %q", h.Config.Workflow, config.WorkflowAuto)
	}

	// 2. Verify board tasks executed and completed
	if len(res.Board.Tasks) == 0 {
		t.Fatalf("expected tasks on board, got: %+v", res.Board)
	}
	for _, task := range res.Board.Tasks {
		if task.Column != plan.ColDone && task.Status != plan.StatusDone {
			t.Errorf("task %s column = %q, status = %q, want done", task.ID, task.Column, task.Status)
		}
	}

	// 3. Verify target file was mutated on disk by worker tool execution
	content, err := os.ReadFile(filepath.Join(tmp, targetFile))
	if err != nil {
		t.Fatalf("failed to read target file %s: %v", targetFile, err)
	}
	if !strings.Contains(string(content), "func Divide") {
		t.Errorf("expected calc.go to contain func Divide, got:\n%s", string(content))
	}

	// 4. Verify all 4 Teamwork stages were actually executed by the mock model
	mu.Lock()
	defer mu.Unlock()
	for _, expectedRole := range []string{"spec", "plan", "worker", "reviewer"} {
		if callsByRole[expectedRole] == 0 {
			t.Errorf("expected at least 1 call for role %q, got 0 (all calls: %v)", expectedRole, callsByRole)
		}
	}
}
