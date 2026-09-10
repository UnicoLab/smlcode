package backends

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// flakyStreamProvider fails its first failBefore stream calls with failErr
// BEFORE delivering anything, and (when failAfterDelta is set) fails every
// call with failErr right AFTER delivering one delta.
type flakyStreamProvider struct {
	fakeStreamProvider
	failBefore     int
	failAfterDelta bool
	failErr        error
	mu2            sync.Mutex
	calls          int
}

func (p *flakyStreamProvider) CompleteStream(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback) error {
	p.mu2.Lock()
	p.calls++
	n := p.calls
	p.mu2.Unlock()
	if n <= p.failBefore {
		return p.failErr
	}
	if p.failAfterDelta {
		_ = cb(llm.CompletionResponse{Model: p.model,
			Choices: []llm.Choice{{Delta: llm.Message{Role: "assistant", Content: "partial"}}}})
		return p.failErr
	}
	return p.fakeStreamProvider.CompleteStream(ctx, req, cb)
}

func (p *flakyStreamProvider) CompleteStreamWithMode(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, _ llm.StreamMode) error {
	return p.CompleteStream(ctx, req, cb)
}

func (p *flakyStreamProvider) CompleteWithMode(ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode) (*llm.CompletionResponse, error) {
	if mode == llm.StreamModeNone {
		return p.Complete(ctx, req)
	}
	return llm.CollectStream(ctx, p.CompleteStream, req)
}

func (p *flakyStreamProvider) streamCallCount() int {
	p.mu2.Lock()
	defer p.mu2.Unlock()
	return p.calls
}

func fastPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}
}

// A transient failure BEFORE any delta is retried on the streaming path, the
// one the ReAct loop takes whenever a token sink (TUI/Studio) is attached.
func TestStreamRetriesTransientFailureBeforeFirstDelta(t *testing.T) {
	inner := &flakyStreamProvider{
		fakeStreamProvider: fakeStreamProvider{name: "fake", model: "m", chunks: []string{"hel", "lo"}},
		failBefore:         2,
		failErr:            errors.New("error, status code: 429, message: rate limit"),
	}
	p := NewRetryProvider(inner, "fake", "m", fastPolicy())

	var got strings.Builder
	err := p.CompleteStream(context.Background(), llm.CompletionRequest{Model: "m"}, func(c llm.CompletionResponse) error {
		for _, ch := range c.Choices {
			got.WriteString(ch.Delta.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream failed after retries: %v", err)
	}
	if inner.streamCallCount() != 3 {
		t.Fatalf("stream attempts = %d, want 3 (two 429s then success)", inner.streamCallCount())
	}
	if got.String() != "hello" {
		t.Fatalf("callback saw %q — deltas must come from the successful attempt only", got.String())
	}

	// Through the tee (the real stack: tee over retry), with a sink attached.
	inner2 := &flakyStreamProvider{
		fakeStreamProvider: fakeStreamProvider{name: "fake", model: "m", chunks: []string{"a", "b"}},
		failBefore:         1,
		failErr:            errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"),
	}
	stack := newStreamTee(NewRetryProvider(inner2, "fake", "m", fastPolicy()), "worker")
	rec := &recorder{}
	unregister := RegisterTokenSink("worker", "T1", rec.sink)
	defer unregister()
	ctx := workspace.WithTaskID(context.Background(), "T1")
	resp, err := stack.CompleteWithMode(ctx, llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced)
	if err != nil {
		t.Fatalf("teed stream failed after retry: %v", err)
	}
	if inner2.streamCallCount() != 2 {
		t.Fatalf("teed stream attempts = %d, want 2", inner2.streamCallCount())
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "ab" {
		t.Fatalf("teed response: %+v", resp)
	}
	if joined := rec.text(); joined != "ab" {
		t.Fatalf("sink saw %q, want the successful attempt's deltas only", joined)
	}
}

// Once a delta has been delivered the stream cannot be replayed: the error is
// surfaced as-is, with no second attempt.
func TestStreamDoesNotRetryAfterFirstDelta(t *testing.T) {
	failErr := errors.New("error, status code: 503, message: upstream reset")
	inner := &flakyStreamProvider{
		fakeStreamProvider: fakeStreamProvider{name: "fake", model: "m", chunks: []string{"x"}},
		failAfterDelta:     true,
		failErr:            failErr,
	}
	p := NewRetryProvider(inner, "fake", "m", fastPolicy())
	deltas := 0
	err := p.CompleteStream(context.Background(), llm.CompletionRequest{Model: "m"}, func(llm.CompletionResponse) error {
		deltas++
		return nil
	})
	if err == nil || !errors.Is(err, failErr) {
		t.Fatalf("error after partial stream: %v (want the original)", err)
	}
	if inner.streamCallCount() != 1 {
		t.Fatalf("stream attempts = %d, want 1 (no replay after a delivered delta)", inner.streamCallCount())
	}
	if deltas != 1 {
		t.Fatalf("callback saw %d deltas, want exactly the one delivered", deltas)
	}
	// Permanent failures before any delta are still tried once.
	inner3 := &flakyStreamProvider{
		fakeStreamProvider: fakeStreamProvider{name: "fake", model: "m", chunks: []string{"x"}},
		failBefore:         5,
		failErr:            errors.New("error, status code: 400, message: bad request"),
	}
	_ = NewRetryProvider(inner3, "fake", "m", fastPolicy()).CompleteStream(context.Background(), llm.CompletionRequest{Model: "m"}, nil)
	if inner3.streamCallCount() != 1 {
		t.Fatalf("permanent failure attempts = %d, want 1", inner3.streamCallCount())
	}
}
