package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// fakeReactExec interrupts mid-execute on first call (with messages), then
// continues from restored history on resume — never requires a replan.
type fakeReactExec struct {
	mu        sync.Mutex
	calls     int
	sawResume bool
	sawMsgs   int
	coldOnly  bool
	root      string
}

func (f *fakeReactExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(reqs) == 0 {
		return nil, fmt.Errorf("empty reqs")
	}
	req := reqs[0]
	role := req.AgentID
	if role == plan.RoleReviewer {
		return []ggagent.SubAgentResult{{
			AgentID: role,
			Output:  `{"approved":true,"score":90,"summary":"ok"}`,
		}}, nil
	}
	if f.calls == 1 {
		msgs := []llm.Message{
			{Role: "user", Content: req.Input},
			{Role: "assistant", Content: "I'll move the file", ToolCalls: []llm.ToolCall{{
				ID: "call_1", Type: "function",
				Function: llm.FunctionCall{Name: "ws_mv", Arguments: `{"from":"a.go","to":"b.go"}`},
			}}},
		}
		return []ggagent.SubAgentResult{{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Messages: msgs, Iteration: 2,
			PendingToolCalls: msgs[1].ToolCalls,
			Provider:         "fake", Model: "test",
			Error: context.Canceled,
		}}, context.Canceled
	}
	// Resume path — must receive restored history (not a cold restart).
	if req.Resume && len(req.Messages) >= 2 {
		f.sawResume = true
		f.sawMsgs = len(req.Messages)
		if f.root != "" {
			_ = os.Remove(filepath.Join(f.root, "a.go"))
			_ = os.WriteFile(filepath.Join(f.root, "b.go"), []byte("package main\n"), 0o644)
		}
		return []ggagent.SubAgentResult{{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: "Observation: moved a.go → b.go\n\n" +
				`{"status":"done","summary":"continued from checkpoint","files_changed":["b.go"]}`,
			Messages:  append(req.Messages, llm.Message{Role: "tool", ToolCallID: "call_1", Content: "moved a.go → b.go"}),
			Iteration: req.Iteration + 1,
			Provider:  "fake", Model: "test",
		}}, nil
	}
	f.coldOnly = true
	return []ggagent.SubAgentResult{{
		AgentID: req.AgentID,
		Output:  `{"status":"done","summary":"cold restart — unexpected"}`,
	}}, nil
}

func TestReactMidExecuteInterruptResume(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644)

	turn, err := session.BeginTurn(slm, "run-react-1", "rename a.go to b.go")
	if err != nil {
		t.Fatal(err)
	}

	fake := &fakeReactExec{root: root}
	r := NewRunner(fake, ggagent.NewSharedState())
	r.Root = root
	r.SlmDir = slm
	r.TurnID = turn.ID
	r.MaxRetries = 0
	r.MaxParallel = 1
	r.Timeout = time.Second
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	board := &plan.Board{
		QueryID: turn.ID, Query: turn.Query,
		Tasks: []plan.Task{{
			ID: "T1", Title: "rename a.go to b.go", Role: plan.RoleWorker,
			Column: plan.ColReadyToDev, Files: []string{"a.go", "b.go"},
			Acceptance: "RENAME_FILE a.go -> b.go",
		}},
	}

	err = r.RunBoard(context.Background(), board)
	if err == nil {
		t.Fatal("expected cancel error from mid-execute interrupt")
	}

	cp, err := session.LoadReactCheckpoint(slm, turn.ID, "T1")
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil || len(cp.Messages) < 2 {
		t.Fatalf("expected react checkpoint with messages, got %+v logs=%v", cp, logs)
	}
	if cp.Iteration != 2 || cp.Provider != "fake" {
		t.Fatalf("checkpoint meta: %+v", cp)
	}
	if !session.HasReactHistory(slm, turn.ID) {
		t.Fatal("HasReactHistory")
	}

	// /resume: normalize board columns; keep react history — no cold replan.
	board2 := session.NormalizeForResume(*board)
	r2 := NewRunner(fake, ggagent.NewSharedState())
	r2.Root = root
	r2.SlmDir = slm
	r2.TurnID = turn.ID
	r2.MaxRetries = 1
	r2.MaxParallel = 1
	r2.Timeout = time.Second
	r2.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	if err := r2.RunBoard(context.Background(), &board2); err != nil {
		t.Fatalf("resume RunBoard: %v", err)
	}
	if !r2.ResumedReact {
		t.Fatalf("expected ResumedReact; logs=%s", strings.Join(logs, "\n"))
	}
	if !fake.sawResume || fake.sawMsgs < 2 {
		t.Fatalf("fake did not see resume messages: sawResume=%v msgs=%d cold=%v calls=%d",
			fake.sawResume, fake.sawMsgs, fake.coldOnly, fake.calls)
	}
	if fake.coldOnly {
		t.Fatal("cold restart path taken — not allowed when checkpoint has messages")
	}
	got, ok := board2.Get("T1")
	if !ok || got.Column != plan.ColDone {
		t.Fatalf("expected T1 done after resume, got %+v", got)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "no cold replan") && !strings.Contains(joined, "resuming ReAct") {
		t.Fatalf("expected resume log, got: %s", joined)
	}
}

func TestApplyResumeInjectsFinalizeSteer(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	turn, err := session.BeginTurn(slm, "run-steer", "finish task")
	if err != nil {
		t.Fatal(err)
	}
	cp := session.ReactCheckpoint{
		TurnID: turn.ID, TaskID: "T9", AgentID: plan.RoleWorker,
		Iteration: 13, MaxIterations: 16,
		Messages: []session.ReactMessage{
			{Role: "user", Content: "do it"},
			{Role: "assistant", Content: "working"},
		},
	}
	if err := session.SaveReactCheckpoint(slm, cp); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(nil, ggagent.NewSharedState())
	r.SlmDir = slm
	r.TurnID = turn.ID
	r.FinalizeWarn = true
	req := &ggagent.SubAgentRequest{AgentID: plan.RoleWorker, Input: "continue"}
	if !r.applyResumeRequest(req, "T9") {
		t.Fatal("expected resume")
	}
	if !strings.Contains(req.Input, "TURN BUDGET") {
		t.Fatalf("expected finalize steer in input: %q", req.Input)
	}
	found := false
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "TURN BUDGET") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected steer user message in history")
	}
}

func TestSaveLoadReactCheckpoint(t *testing.T) {
	slm := t.TempDir()
	cp := session.ReactCheckpoint{
		TurnID: "run-x", TaskID: "T2", AgentID: "worker",
		Provider: "omlx", Model: "qwen", Iteration: 3,
		Messages: []session.ReactMessage{
			{Role: "user", Content: "do work"},
			{Role: "assistant", Content: "tool", ToolCalls: []session.ReactToolCall{
				{ID: "c1", Type: "function", Name: "ws_edit", Arguments: `{}`},
			}},
		},
		PendingToolCalls: []session.ReactToolCall{{ID: "c1", Name: "ws_edit"}},
	}
	if err := session.SaveReactCheckpoint(slm, cp); err != nil {
		t.Fatal(err)
	}
	got, err := session.LoadReactCheckpoint(slm, "run-x", "T2")
	if err != nil || got == nil {
		t.Fatalf("%v %+v", err, got)
	}
	if len(got.Messages) != 2 || got.Iteration != 3 {
		t.Fatalf("%+v", got)
	}
}
