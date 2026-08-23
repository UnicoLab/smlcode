package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// ---------------------------------------------------------------------------
// A minimal streaming provider, so this test exercises the REAL path:
//   loop wave → backends.RegisterTokenSink → role-bound tee → sink →
//   Runner.EmitToken → OnEventFull → stream.KindToken with a typed payload.
// ---------------------------------------------------------------------------

type streamingLLM struct {
	chunks []string
}

func (p *streamingLLM) GetName() string { return "fake" }
func (p *streamingLLM) GetModels(context.Context) ([]string, error) {
	return []string{"fake"}, nil
}
func (p *streamingLLM) text() string { return strings.Join(p.chunks, "") }

func (p *streamingLLM) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: p.text()}}},
	}, nil
}

func (p *streamingLLM) CompleteWithMode(ctx context.Context, req llm.CompletionRequest,
	_ llm.StreamMode) (*llm.CompletionResponse, error) {
	return llm.CollectStream(ctx, p.CompleteStream, req)
}

func (p *streamingLLM) CompleteStream(ctx context.Context, _ llm.CompletionRequest,
	cb llm.StreamCallback) error {
	for _, c := range p.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := cb(llm.CompletionResponse{
			Choices: []llm.Choice{{Delta: llm.Message{Role: "assistant", Content: c}}},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *streamingLLM) CompleteStreamWithMode(ctx context.Context, req llm.CompletionRequest,
	cb llm.StreamCallback, _ llm.StreamMode) error {
	return p.CompleteStream(ctx, req, cb)
}

func (p *streamingLLM) IsHealthy(context.Context) error        { return nil }
func (p *streamingLLM) GetConfig() map[string]interface{}      { return map[string]interface{}{} }
func (p *streamingLLM) SetConfig(map[string]interface{}) error { return nil }
func (p *streamingLLM) SupportsStreaming() bool                { return true }
func (p *streamingLLM) GetStreamingConfig() *llm.StreamingConfig {
	return &llm.StreamingConfig{Enabled: true, Mode: llm.StreamModeForced}
}
func (p *streamingLLM) SetStreamingConfig(*llm.StreamingConfig) error { return nil }
func (p *streamingLLM) Close() error                                  { return nil }

// llmBackedExec is a SubAgentRunner that really calls a provider, the way
// GoLangGraph's ReAct loop does — same ctx, same streaming mode.
type llmBackedExec struct {
	mu        sync.Mutex
	m         *llm.ProviderManager
	answer    func(req ggagent.SubAgentRequest) string
	failFor   map[string]error
	sinksSeen []int
}

func newLLMBackedExec(t *testing.T, roles map[string][]string) *llmBackedExec {
	t.Helper()
	m := llm.NewProviderManager()
	if err := m.RegisterProvider("fake", &streamingLLM{chunks: []string{"unused"}}); err != nil {
		t.Fatal(err)
	}
	e := &llmBackedExec{m: m, failFor: map[string]error{}}
	for role, chunks := range roles {
		// Register the role's own base provider, then bind the role exactly as
		// pkg/agents' factory does.
		base := "fake-" + role
		if err := m.RegisterProvider(base, &streamingLLM{chunks: chunks}); err != nil {
			t.Fatal(err)
		}
		if key := backends.BindRole(m, base, backends.Directives{Role: role, SerialTools: true, ToolChoice: "auto"}); key == base {
			t.Fatalf("role %q was not bound", role)
		}
	}
	return e
}

func (e *llmBackedExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	var firstErr error
	for i, req := range reqs {
		e.mu.Lock()
		e.sinksSeen = append(e.sinksSeen, backends.TokenSinkCount())
		e.mu.Unlock()

		if err, ok := e.failFor[req.AgentID]; ok {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: err}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		key := "fake-" + req.AgentID + backends.RoleKeySeparator + req.AgentID
		p, err := e.m.GetProvider(key)
		if err != nil {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: err}
			continue
		}
		// StreamModeForced is what pkg/agents/factory.go sets on every agent.
		if _, err := p.CompleteWithMode(ctx,
			llm.CompletionRequest{Model: "fake", Stream: true}, llm.StreamModeForced); err != nil {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: err}
			continue
		}
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID, Output: e.answer(req),
		}
	}
	return out, firstErr
}

func (e *llmBackedExec) maxSinks() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	max := 0
	for _, n := range e.sinksSeen {
		if n > max {
			max = n
		}
	}
	return max
}

// tokenLog collects the token events one runner emitted, keyed by agent+task.
type tokenLog struct {
	mu sync.Mutex
	by map[string][]stream.Token
}

func newTokenLog() *tokenLog { return &tokenLog{by: map[string][]stream.Token{}} }

func (l *tokenLog) observe(ev LoopEvent) {
	if ev.Kind != stream.KindToken {
		return
	}
	tok, ok := ev.Data.(stream.Token)
	if !ok {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.by[ev.Agent+"/"+ev.TaskID] = append(l.by[ev.Agent+"/"+ev.TaskID], tok)
}

func (l *tokenLog) textFor(key string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, t := range l.by[key] {
		b.WriteString(t.Delta)
	}
	return b.String()
}

func (l *tokenLog) keys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.by))
	for k := range l.by {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (l *tokenLog) countFor(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.by[key])
}

func (l *tokenLog) lastTokensFor(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	ev := l.by[key]
	if len(ev) == 0 {
		return 0
	}
	return ev[len(ev)-1].Tokens
}

// bigChunks is a long stream of one-token-ish deltas.
func bigChunks(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s%d ", prefix, i))
	}
	return out
}

// ---------------------------------------------------------------------------

// The end-to-end proof: two concurrent worker tasks stream, both sets of deltas
// arrive attributed to the right agent AND task, coalesced, with a running
// token count, and every registration is gone when the wave ends.
func TestWaveStreamsTokensPerAgentAndTask(t *testing.T) {
	backends.ResetTokenSinks()
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workerChunks := bigChunks("w", 60)
	exec := newLLMBackedExec(t, map[string][]string{
		plan.RoleWorker:   workerChunks,
		plan.RoleReviewer: bigChunks("r", 30),
	})
	exec.answer = func(req ggagent.SubAgentRequest) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":true,"score":90,"summary":"ok"}`
		}
		return `{"status":"done","summary":"ws_edit applied","files_changed":["` + req.TaskID + `"]}`
	}

	r := defaultRunner(t, root, exec)
	log := newTokenLog()
	r.OnEventFull = log.observe

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "edit a", Role: plan.RoleWorker,
			Files: []string{"a.go"}, Column: plan.ColReadyToDev},
		{ID: "T2", Title: "edit b", Role: plan.RoleWorker,
			Files: []string{"b.go"}, Column: plan.ColReadyToDev},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	// Attribution: worker deltas land under their own task, never merged.
	wantText := strings.Join(workerChunks, "")
	for _, key := range []string{plan.RoleWorker + "/T1", plan.RoleWorker + "/T2"} {
		if got := log.textFor(key); got != wantText {
			t.Errorf("%s streamed %q\nwant %q", key, got, wantText)
		}
		// Coalesced: 60 chunks must not become 60 events.
		if n := log.countFor(key); n == 0 || n >= len(workerChunks) {
			t.Errorf("%s produced %d token events for %d chunks — not coalesced", key, n, len(workerChunks))
		}
		// The running count is real, not a chunk index.
		if got, want := log.lastTokensFor(key), llm.EstimateTokens(wantText); got < want/2 {
			t.Errorf("%s running token count = %d, want ≈%d", key, got, want)
		}
	}
	if got := log.keys(); len(got) < 2 {
		t.Fatalf("token events were not split per agent/task: %v", got)
	}
	for _, k := range log.keys() {
		if !strings.Contains(k, "/T") {
			t.Errorf("token event %q carries no task id — the terminal cannot attribute it", k)
		}
	}
	// While the wave ran, a sink was registered per concurrent agent.
	if exec.maxSinks() == 0 {
		t.Error("no sink was registered while agents were executing")
	}
	// And nothing leaked once the run finished.
	if n := backends.TokenSinkCount(); n != 0 {
		t.Fatalf("%d token sinks leaked after the run", n)
	}
}

// Registration must be released on the failure and cancellation paths too — a
// leaked sink keeps attributing a later agent's output to a dead task.
func TestTokenSinkIsReleasedOnErrorAndCancel(t *testing.T) {
	backends.ResetTokenSinks()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("provider exploded")
	exec := newLLMBackedExec(t, map[string][]string{plan.RoleWorker: bigChunks("w", 4)})
	exec.failFor[plan.RoleWorker] = boom
	exec.answer = func(ggagent.SubAgentRequest) string { return "" }

	r := defaultRunner(t, root, exec)
	r.MaxRetries = 1
	r.OnEventFull = func(LoopEvent) {}

	board := &plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "edit", Role: plan.RoleWorker,
		Files: []string{"a.go"}, Column: plan.ColReadyToDev}}}
	_ = r.RunBoard(context.Background(), board)
	if n := backends.TokenSinkCount(); n != 0 {
		t.Fatalf("%d sinks leaked after a failed wave", n)
	}

	// Canceled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	board2 := &plan.Board{Tasks: []plan.Task{{ID: "T2", Title: "edit", Role: plan.RoleWorker,
		Files: []string{"a.go"}, Column: plan.ColReadyToDev}}}
	_ = r.RunBoard(ctx, board2)
	if n := backends.TokenSinkCount(); n != 0 {
		t.Fatalf("%d sinks leaked after cancellation", n)
	}
}

// A runner with no event consumer must not register anything at all: the tee
// then costs one map lookup per call and nothing else.
func TestNoEventSinkRegistersNothing(t *testing.T) {
	backends.ResetTokenSinks()
	r := &Runner{}
	stop := r.streamTokens("worker", "T1")
	if backends.TokenSinkCount() != 0 {
		t.Error("a runner with no event consumer registered a sink")
	}
	stop()

	r.OnEvent = func(string, string, string, string, string, string) {}
	stop = r.streamTokens("worker", "T1")
	if backends.TokenSinkCount() != 1 {
		t.Error("the legacy OnEvent sink should still receive token events")
	}
	stop()
	if backends.TokenSinkCount() != 0 {
		t.Error("cleanup did not run")
	}

	// Nil runner and empty agent are no-ops, not panics.
	var nilRunner *Runner
	nilRunner.streamTokens("worker", "T1")()
	r.streamTokens("", "T1")()
	r.streamTokensCtx(context.Background(), "")()
	if backends.TokenSinkCount() != 0 {
		t.Error("a junk registration landed")
	}
}
