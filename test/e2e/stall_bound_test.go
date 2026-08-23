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
)

// The convergence regression net.
//
// A task that can NEVER satisfy its gate must terminate in a bounded, small
// number of model calls. Before the bounds existed, the same board (one task,
// a reviewer that always rejects, an escalate arbitrator that always answers
// "retry") ran until RunBoard's fixed 200-round safety guard tripped:
// 9,006 model calls in 2m18s against this exact rig, ending in
//
//	loop: gave up before the board was finished: wave loop exceeded
//	safety guard (rounds=201, tasks still open=1)
//
// which names neither what stalled nor what to do about it. Three bounds now
// fire first — the escalate gate's retry cap, the per-task attempt ceiling,
// and the no-progress detector.

// rejectingModel is the smoke test's fake with two changes: the reviewer
// refuses every attempt, and the escalate-timeout arbitrator always answers
// "retry" — the answer that used to make the board spin forever.
type rejectingModel struct {
	mu    sync.Mutex
	calls int
}

func (f *rejectingModel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *rejectingModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": fakeModelID, "object": "model"}},
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

	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	// The escalate arbitrator states its contract in the USER prompt, not the
	// system one, so it is matched against the whole conversation.
	var content string
	var call map[string]any
	switch {
	case strings.Contains(all.String(), `"action":"retry|re_scope|abort|mark_done"`):
		content = `{"action":"retry","reason":"try again","confidence":0.9}`
	case strings.Contains(system, `{"approved"`), strings.Contains(system, "never approved"):
		content = `{"approved":false,"score":10,"summary":"not acceptable","issues":["not done"]}`
	default:
		content, call = answerFor(roleOf(system, all.String()), len(req.Tools) > 0, sawToolResult)
	}
	if req.Stream {
		writeStreamedCompletion(w, content, call)
		return
	}
	writeCompletion(w, content, call)
}

func TestPermanentlyFailingBoardTerminatesInBoundedModelCalls(t *testing.T) {
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

	model := &rejectingModel{}
	server := httptest.NewServer(model)
	defer server.Close()

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = fakeModelID
	cfg.APIKey = "test-key"
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	cfg.ClarifyMode = "off"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	// The failure mode under test: an unattended run answering "retry" at the
	// escalate gate, forever.
	cfg.EscalateAsk = "auto"
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 1
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

	// The timeout is the backstop, not the assertion: a run that only stops
	// because the test's context expired has not converged.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	res, err := h.Run(ctx, "Create "+targetFile+" with a Hello function")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the run only ended because the test context expired — it did not converge")
	}
	calls := model.count()
	t.Logf("terminated after %d model calls in %s", calls, elapsed.Round(time.Millisecond))

	// The pre-fix number on this rig was 9,006. The bound is generous enough
	// not to be brittle and small enough that 9,006 could never pass it.
	const ceiling = 600
	if calls > ceiling {
		t.Fatalf("a permanently-failing board spent %d model calls (ceiling %d) — the ladder does not converge",
			calls, ceiling)
	}

	// Every task must be in a TERMINAL state a human can act on, and the gate
	// must not have granted more retries than its cap.
	if len(res.Board.Tasks) == 0 {
		t.Fatal("the board has no tasks")
	}
	for _, task := range res.Board.Tasks {
		switch task.Column {
		case plan.ColToScope, plan.ColScoped, plan.ColBlocked, plan.ColDone:
		default:
			t.Errorf("task %s ended mid-flight in %q — not a state a human can act on", task.ID, task.Column)
		}
		if task.GateRetries > plan.DefaultMaxGateRetries {
			t.Errorf("task %s was granted %d gate retries, cap is %d",
				task.ID, task.GateRetries, plan.DefaultMaxGateRetries)
		}
		if task.Column == plan.ColToScope && strings.TrimSpace(task.Error) == "" {
			t.Errorf("task %s was parked with no reason an operator can read", task.ID)
		}
	}
}
