package backends

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// recordingProvider captures every request the wrapper hands down.
type recordingProvider struct {
	*fakeStreamProvider
	mu   sync.Mutex
	reqs []llm.CompletionRequest
}

func newRecordingProvider() *recordingProvider {
	return &recordingProvider{fakeStreamProvider: &fakeStreamProvider{
		name: "rec", model: "m", chunks: []string{"ok"},
	}}
}

func (p *recordingProvider) record(req llm.CompletionRequest) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
}

func (p *recordingProvider) last() llm.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs[len(p.reqs)-1]
}

func (p *recordingProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.record(req)
	return p.fakeStreamProvider.Complete(ctx, req)
}

func (p *recordingProvider) CompleteWithMode(ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode) (*llm.CompletionResponse, error) {
	p.record(req)
	return p.fakeStreamProvider.CompleteWithMode(ctx, req, mode)
}

func (p *recordingProvider) CompleteStream(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback) error {
	p.record(req)
	return p.fakeStreamProvider.CompleteStream(ctx, req, cb)
}

func (p *recordingProvider) CompleteStreamWithMode(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, mode llm.StreamMode) error {
	p.record(req)
	return p.fakeStreamProvider.CompleteStreamWithMode(ctx, req, cb, mode)
}

// liveTranscript is a ReAct transcript: system prompt, the task, then n tool
// pairs whose results are `size` bytes each.
func liveTranscript(n, size int) []llm.Message {
	msgs := []llm.Message{
		{Role: "system", Content: "You are the worker. Tools: ws_read, ws_edit."},
		{Role: "user", Content: "implement the thing"},
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
				ID: id, Type: "function",
				Function: llm.FunctionCall{Name: "ws_read", Arguments: fmt.Sprintf(`{"path":"f%d.go"}`, i)},
			}}},
			llm.Message{Role: "tool", ToolCallID: id, Name: "ws_read", Content: strings.Repeat("x", size)},
		)
	}
	return msgs
}

func toolResults(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, m)
		}
	}
	return out
}

// TestLiveElideCollapsesOldToolResultsOverThreshold: over the threshold, every
// tool result but the last five is replaced with the placeholder — and nothing
// else in the transcript moves. Under it, the request goes through untouched.
func TestLiveElideCollapsesOldToolResultsOverThreshold(t *testing.T) {
	ResetLiveElideStats()
	inner := newRecordingProvider()
	// 2000-token window, elide at 50% → 1000 tokens ≈ 4000 chars.
	p := newLiveElide(inner, "worker", LiveElide{WindowTokens: 2000, AtPercent: 50})

	big := liveTranscript(8, 1000)
	if _, err := p.CompleteWithMode(context.Background(), llm.CompletionRequest{
		Messages: big, Model: "m",
	}, llm.StreamModeNone); err != nil {
		t.Fatal(err)
	}
	got := inner.last().Messages
	if len(got) != len(big) {
		t.Fatalf("elision changed the message count: %d → %d", len(big), len(got))
	}
	if got[0].Role != "system" || got[0].Content != big[0].Content {
		t.Fatalf("the leading system message was touched: %+v", got[0])
	}
	if got[1].Role != "user" || got[1].Content != big[1].Content {
		t.Fatalf("the user turn was touched: %+v", got[1])
	}
	results := toolResults(got)
	if len(results) != 8 {
		t.Fatalf("tool results = %d, want 8", len(results))
	}
	for i, m := range results {
		wantID := fmt.Sprintf("call_%d", i)
		if m.ToolCallID != wantID {
			t.Errorf("result %d lost its tool_call_id: %q", i, m.ToolCallID)
		}
		elided := m.Content == compact.DefaultElidedPlaceholder
		if i < 3 && !elided {
			t.Errorf("old result %d was kept verbatim (%d bytes)", i, len(m.Content))
		}
		if i >= 3 && elided {
			t.Errorf("one of the last five results (%d) was elided", i)
		}
	}
	calls := 0
	for i, m := range got {
		if m.Role == "assistant" {
			calls += len(m.ToolCalls)
			if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != big[i].ToolCalls[0].ID {
				t.Errorf("assistant tool call at %d was changed: %+v", i, m)
			}
		}
	}
	if calls != 8 {
		t.Fatalf("tool calls = %d, want 8 (elision must never drop a call)", calls)
	}
	if n := LiveElideStats()["worker"]; n != 3 {
		t.Fatalf("elision telemetry = %d, want 3", n)
	}
	// The agent's own slice is not mutated: the next iteration re-sends the
	// full transcript and is elided the same way.
	if big[2*1+2].Content == compact.DefaultElidedPlaceholder {
		t.Fatal("the caller's transcript was mutated in place")
	}

	small := liveTranscript(2, 100)
	if _, err := p.Complete(context.Background(), llm.CompletionRequest{Messages: small}); err != nil {
		t.Fatal(err)
	}
	for i, m := range inner.last().Messages {
		if m.Role != small[i].Role || m.Content != small[i].Content || m.ToolCallID != small[i].ToolCallID {
			t.Fatalf("an under-threshold transcript was rewritten at %d: %+v", i, m)
		}
	}
	if n := LiveElideStats()["worker"]; n != 3 {
		t.Fatalf("an under-threshold request was counted: %d", n)
	}
}

// TestLiveElideIsAPassThroughWhenDisabled: the zero policy installs nothing.
func TestLiveElideIsAPassThroughWhenDisabled(t *testing.T) {
	inner := newRecordingProvider()
	for _, cfg := range []LiveElide{{}, {WindowTokens: 8192}, {AtPercent: 80}, {WindowTokens: 8192, AtPercent: 100}} {
		if got := newLiveElide(inner, "worker", cfg); got != llm.Provider(inner) {
			t.Errorf("%+v installed a wrapper", cfg)
		}
	}
}

// TestBindRoleInstallsLiveElideOutermost: a role bound with a LiveElide policy
// gets the wrapper on top of its stack; a role without one does not.
func TestBindRoleInstallsLiveElideOutermost(t *testing.T) {
	m := llm.NewProviderManager()
	if err := m.RegisterProvider("base", newRecordingProvider()); err != nil {
		t.Fatal(err)
	}
	key := BindRole(m, "base", Directives{
		Role: "worker", SerialTools: true, ToolChoice: "auto",
		LiveElide: LiveElide{WindowTokens: 8192, AtPercent: 80},
	})
	p, err := m.GetProvider(key)
	if err != nil {
		t.Fatalf("role not registered: %v", err)
	}
	if p.GetConfig()["slmcode_live_elide"] != true {
		t.Fatalf("live elision is not on the role's stack: %v", p.GetConfig())
	}
	if _, outer := p.(*liveElideProvider); !outer {
		t.Fatalf("the elision wrapper is not outermost: %T", p)
	}
	plainKey := BindRole(m, "base", Directives{Role: "planner", JSONOnly: true})
	plain, _ := m.GetProvider(plainKey)
	if _, has := plain.GetConfig()["slmcode_live_elide"]; has {
		t.Fatal("a role with no policy was given the wrapper")
	}
}
