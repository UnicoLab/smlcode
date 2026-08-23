package backends

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// ---------------------------------------------------------------------------
// A provider that really streams, so the tee is exercised end to end.
// ---------------------------------------------------------------------------

type fakeStreamProvider struct {
	name   string
	model  string
	chunks []string
	// gap is the pause between chunks, i.e. how fast the "model" decodes.
	gap time.Duration

	mu           sync.Mutex
	streamCalls  int
	completeMode int
	plainCalls   int
}

func (p *fakeStreamProvider) GetName() string { return p.name }
func (p *fakeStreamProvider) GetModels(context.Context) ([]string, error) {
	return []string{p.model}, nil
}

func (p *fakeStreamProvider) text() string { return strings.Join(p.chunks, "") }

func (p *fakeStreamProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.mu.Lock()
	p.plainCalls++
	p.mu.Unlock()
	return &llm.CompletionResponse{
		Model:   p.model,
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: p.text()}, FinishReason: "stop"}},
		Usage:   llm.Usage{CompletionTokens: len(p.chunks)},
	}, nil
}

func (p *fakeStreamProvider) CompleteWithMode(
	ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode,
) (*llm.CompletionResponse, error) {
	p.mu.Lock()
	p.completeMode++
	p.mu.Unlock()
	if mode == llm.StreamModeNone {
		return p.Complete(ctx, req)
	}
	return llm.CollectStream(ctx, p.CompleteStream, req)
}

func (p *fakeStreamProvider) CompleteStream(
	ctx context.Context, _ llm.CompletionRequest, cb llm.StreamCallback,
) error {
	p.mu.Lock()
	p.streamCalls++
	p.mu.Unlock()
	for _, c := range p.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.gap > 0 {
			time.Sleep(p.gap)
		}
		chunk := llm.CompletionResponse{
			Model:   p.model,
			Choices: []llm.Choice{{Delta: llm.Message{Role: "assistant", Content: c}}},
		}
		if err := cb(chunk); err != nil {
			return err
		}
	}
	return cb(llm.CompletionResponse{
		Model:   p.model,
		Choices: []llm.Choice{{Delta: llm.Message{Role: "assistant"}, FinishReason: "stop"}},
		Usage:   llm.Usage{CompletionTokens: len(p.chunks)},
	})
}

func (p *fakeStreamProvider) CompleteStreamWithMode(
	ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, _ llm.StreamMode,
) error {
	return p.CompleteStream(ctx, req, cb)
}

func (p *fakeStreamProvider) IsHealthy(context.Context) error        { return nil }
func (p *fakeStreamProvider) GetConfig() map[string]interface{}      { return map[string]interface{}{} }
func (p *fakeStreamProvider) SetConfig(map[string]interface{}) error { return nil }
func (p *fakeStreamProvider) SupportsStreaming() bool                { return true }
func (p *fakeStreamProvider) GetStreamingConfig() *llm.StreamingConfig {
	return &llm.StreamingConfig{Enabled: true, Mode: llm.StreamModeForced}
}
func (p *fakeStreamProvider) SetStreamingConfig(*llm.StreamingConfig) error { return nil }
func (p *fakeStreamProvider) Close() error                                  { return nil }

func (p *fakeStreamProvider) counts() (stream, withMode, plain int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamCalls, p.completeMode, p.plainCalls
}

// recorder collects what a sink saw.
type recorder struct {
	mu     sync.Mutex
	deltas []string
	tokens []int
}

func (r *recorder) sink(delta string, tokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deltas = append(r.deltas, delta)
	r.tokens = append(r.tokens, tokens)
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.deltas, "")
}

func (r *recorder) snapshot() ([]string, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.deltas...), append([]int(nil), r.tokens...)
}

// wordChunks is a plausible token stream: one word-ish fragment per chunk.
func wordChunks(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "tok")
	}
	return out
}

func bindFake(t *testing.T, role string, p llm.Provider) (*llm.ProviderManager, llm.Provider) {
	t.Helper()
	m := llm.NewProviderManager()
	if err := m.RegisterProvider("fake", p); err != nil {
		t.Fatal(err)
	}
	key := BindRole(m, "fake", Directives{Role: role, SerialTools: true, ToolChoice: "auto"})
	if !strings.Contains(key, RoleKeySeparator+role) {
		t.Fatalf("role not bound: key = %q", key)
	}
	bound, err := m.GetProvider(key)
	if err != nil {
		t.Fatal(err)
	}
	return m, bound
}

// ---------------------------------------------------------------------------

// The headline test: a streaming provider drives a registered sink, the deltas
// are attributed to the right agent+task, they are coalesced rather than one
// event per token, the running count is real, and nothing leaks afterwards.
func TestStreamTeeDeliversCoalescedDeltasToTheRegisteredSink(t *testing.T) {
	ResetTokenSinks()
	fake := &fakeStreamProvider{name: "fake", model: "qwen2.5-coder:7b", chunks: wordChunks(60)}
	_, bound := bindFake(t, "worker", fake)

	var rec recorder
	stop := RegisterTokenSink("worker", "T1", rec.sink)
	if TokenSinkCount() != 1 {
		t.Fatalf("sink not registered (count %d)", TokenSinkCount())
	}

	ctx := workspace.WithTaskID(context.Background(), "T1")
	resp, err := bound.CompleteWithMode(ctx, llm.CompletionRequest{
		Model: fake.model, Stream: true,
	}, llm.StreamModeForced)
	if err != nil {
		t.Fatalf("CompleteWithMode: %v", err)
	}

	// The tee must not TRANSFORM: the loop sees exactly what it would have.
	if got := resp.Choices[0].Message.Content; got != fake.text() {
		t.Fatalf("content changed by the tee:\n got %q\nwant %q", got, fake.text())
	}
	if s, _, plain := fake.counts(); s != 1 || plain != 0 {
		t.Fatalf("expected exactly one streamed call, got stream=%d plain=%d", s, plain)
	}

	deltas, tokens := rec.snapshot()
	if len(deltas) == 0 {
		t.Fatal("nothing reached the sink — the token stream still has no producer")
	}
	// Nothing is dropped: the concatenated deltas are the full completion.
	if rec.text() != fake.text() {
		t.Errorf("sink text != completion:\n got %q\nwant %q", rec.text(), fake.text())
	}
	// Coalesced: a 60-chunk stream must not become 60 repaints.
	if len(deltas) >= len(fake.chunks) {
		t.Errorf("no coalescing: %d sink calls for %d chunks", len(deltas), len(fake.chunks))
	}
	// The running count is monotonic and ends at a real token estimate of the
	// whole completion (per-chunk estimates, so allow a small drift).
	for i := 1; i < len(tokens); i++ {
		if tokens[i] < tokens[i-1] {
			t.Fatalf("running token count went backwards: %v", tokens)
		}
	}
	want := llm.EstimateTokens(fake.text())
	got := tokens[len(tokens)-1]
	if got <= 0 {
		t.Fatalf("running token count is %d", got)
	}
	if got < want/2 || got > want*2+8 {
		t.Errorf("running token count %d is not a plausible estimate of %d", got, want)
	}

	// Cleanup: unregister leaves nothing behind, and a later call is silent.
	stop()
	stop() // idempotent
	if TokenSinkCount() != 0 {
		t.Fatalf("sink leaked: %d still registered", TokenSinkCount())
	}
	before := len(deltas)
	if _, err := bound.CompleteWithMode(ctx, llm.CompletionRequest{Model: fake.model, Stream: true},
		llm.StreamModeForced); err != nil {
		t.Fatal(err)
	}
	if after, _ := rec.snapshot(); len(after) != before {
		t.Errorf("an unregistered sink still received %d deltas", len(after)-before)
	}
}

// Attribution is the whole reason the sink is keyed by role AND task: with
// max_parallel > 1 several agents stream at once and the terminal must not
// interleave them into one another's lines.
func TestStreamTeeAttributesConcurrentAgentsSeparately(t *testing.T) {
	ResetTokenSinks()
	workerP := &fakeStreamProvider{name: "fake", model: "m", chunks: []string{"worker-output "}, gap: time.Millisecond}
	reviewP := &fakeStreamProvider{name: "fake", model: "m", chunks: []string{"reviewer-output "}, gap: time.Millisecond}
	for i := 0; i < 40; i++ {
		workerP.chunks = append(workerP.chunks, "W")
		reviewP.chunks = append(reviewP.chunks, "R")
	}
	_, wBound := bindFake(t, "worker", workerP)
	_, rBound := bindFake(t, "reviewer", reviewP)

	var wRec, rRec recorder
	defer RegisterTokenSink("worker", "T1", wRec.sink)()
	defer RegisterTokenSink("reviewer", "T2", rRec.sink)()
	if TokenSinkCount() != 2 {
		t.Fatalf("registered %d sinks, want 2", TokenSinkCount())
	}

	var wg sync.WaitGroup
	run := func(p llm.Provider, task string) {
		defer wg.Done()
		ctx := workspace.WithTaskID(context.Background(), task)
		if _, err := p.CompleteWithMode(ctx,
			llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced); err != nil {
			t.Errorf("%s: %v", task, err)
		}
	}
	wg.Add(2)
	go run(wBound, "T1")
	go run(rBound, "T2")
	wg.Wait()

	if got := wRec.text(); got != workerP.text() {
		t.Errorf("worker sink got %q", got)
	}
	if got := rRec.text(); got != reviewP.text() {
		t.Errorf("reviewer sink got %q", got)
	}
	if strings.Contains(wRec.text(), "reviewer-output") || strings.Contains(rRec.text(), "worker-output") {
		t.Error("agents' deltas crossed sinks — attribution is broken")
	}
}

// A sink registered for a role with no task id is the fallback for the
// sequential call sites, which do not always carry a task tag.
func TestStreamTeeFallsBackToTheRoleWideSink(t *testing.T) {
	ResetTokenSinks()
	fake := &fakeStreamProvider{name: "fake", model: "m", chunks: wordChunks(20)}
	_, bound := bindFake(t, "planner", fake)

	var rec recorder
	defer RegisterTokenSink("planner", "", rec.sink)()

	// No task id on the context at all.
	if _, err := bound.CompleteWithMode(context.Background(),
		llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced); err != nil {
		t.Fatal(err)
	}
	if rec.text() != fake.text() {
		t.Errorf("role-wide sink got %q, want %q", rec.text(), fake.text())
	}

	// A different role must not be served by it.
	var other recorder
	_, otherBound := bindFake(t, "reviewer", &fakeStreamProvider{name: "fake", model: "m", chunks: wordChunks(5)})
	stop := RegisterTokenSink("planner", "", other.sink)
	defer stop()
	if _, err := otherBound.CompleteWithMode(context.Background(),
		llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced); err != nil {
		t.Fatal(err)
	}
	if other.text() != "" {
		t.Errorf("a reviewer's deltas reached the planner's sink: %q", other.text())
	}
}

// With nothing registered the wrapper must be transparent — same call shape on
// the inner provider, no goroutine, no cost.
func TestStreamTeeIsTransparentWithNoSink(t *testing.T) {
	ResetTokenSinks()
	fake := &fakeStreamProvider{name: "fake", model: "m", chunks: wordChunks(10)}
	_, bound := bindFake(t, "worker", fake)

	resp, err := bound.CompleteWithMode(context.Background(),
		llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != fake.text() {
		t.Error("content changed with no sink registered")
	}
	// Delegated, not driven by the tee: the inner provider's own
	// CompleteWithMode ran, which is what preserves retryProvider's deadline
	// and throughput accounting on the no-sink path.
	if _, withMode, _ := fake.counts(); withMode != 1 {
		t.Errorf("inner CompleteWithMode called %d times, want 1", withMode)
	}
}

// The constrained-decoding path is deliberately non-streaming, and so is any
// caller that asks for StreamModeNone. Both must degrade silently.
func TestStreamTeeDegradesOnNonStreamingCalls(t *testing.T) {
	ResetTokenSinks()
	fake := &fakeStreamProvider{name: "fake", model: "m", chunks: wordChunks(10)}
	_, bound := bindFake(t, "worker", fake)

	var rec recorder
	defer RegisterTokenSink("worker", "T1", rec.sink)()
	ctx := workspace.WithTaskID(context.Background(), "T1")

	if _, err := bound.CompleteWithMode(ctx, llm.CompletionRequest{Model: "m"}, llm.StreamModeNone); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Complete(ctx, llm.CompletionRequest{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if got := rec.text(); got != "" {
		t.Errorf("a non-streaming call produced deltas: %q", got)
	}
	if _, _, plain := fake.counts(); plain != 2 {
		t.Errorf("non-streaming calls = %d, want 2", plain)
	}
}

// A sink that blocks must never stall inference: the pump is the only thing
// that waits on it, and the producer keeps decoding.
func TestSlowSinkNeverStallsInference(t *testing.T) {
	ResetTokenSinks()
	fake := &fakeStreamProvider{name: "fake", model: "m", chunks: wordChunks(200)}
	_, bound := bindFake(t, "worker", fake)

	var seen int
	var mu sync.Mutex
	release := make(chan struct{})
	var once sync.Once
	defer RegisterTokenSink("worker", "T1", func(string, int) {
		mu.Lock()
		seen++
		mu.Unlock()
		// The very first sink call blocks for far longer than the whole stream
		// takes to produce.
		once.Do(func() { <-release })
	})()

	ctx := workspace.WithTaskID(context.Background(), "T1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = bound.CompleteWithMode(ctx, llm.CompletionRequest{Model: "m", Stream: true}, llm.StreamModeForced)
	}()

	// Decode finishes while the consumer is still stuck on its first delta.
	time.Sleep(150 * time.Millisecond)
	if s, _, _ := fake.counts(); s != 1 {
		t.Fatalf("stream not started: %d", s)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a slow sink stalled the completion")
	}
	mu.Lock()
	defer mu.Unlock()
	if seen == 0 {
		t.Error("the slow sink received nothing")
	}
}

// Streaming must keep feeding EstimateTimeout: the tee takes the call over from
// retryProvider, so it has to record the sample itself.
func TestStreamTeeStillObservesThroughput(t *testing.T) {
	ResetTokenSinks()
	GlobalThroughput.Reset()
	t.Cleanup(GlobalThroughput.Reset)

	fake := &fakeStreamProvider{name: "fake", model: "tput-model", chunks: wordChunks(40)}
	_, bound := bindFake(t, "worker", fake)
	var rec recorder
	defer RegisterTokenSink("worker", "T1", rec.sink)()

	ctx := workspace.WithTaskID(context.Background(), "T1")
	if _, err := bound.CompleteWithMode(ctx,
		llm.CompletionRequest{Model: "tput-model", Stream: true}, llm.StreamModeForced); err != nil {
		t.Fatal(err)
	}
	tps, samples, ok := ObservedThroughput("tput-model")
	if !ok || samples != 1 || tps <= 0 {
		t.Fatalf("throughput not observed on the teed path: tps=%v samples=%d ok=%v", tps, samples, ok)
	}
	snap := ThroughputSnapshot()
	if len(snap) != 1 || snap[0].Model != "tput-model" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestThroughputSnapshotIsSortedAndReadOnly(t *testing.T) {
	tp := &Throughput{}
	tp.Observe("zebra", 100, time.Second)
	tp.Observe("alpha", 50, time.Second)
	tp.Observe("alpha", 50, time.Second)
	snap := tp.Snapshot()
	if len(snap) != 2 || snap[0].Model != "alpha" || snap[1].Model != "zebra" {
		t.Fatalf("snapshot not sorted: %+v", snap)
	}
	if snap[0].Samples != 2 || snap[1].Samples != 1 {
		t.Errorf("sample counts = %+v", snap)
	}
	// Mutating the copy must not touch the tracker.
	snap[0].TokensPerSec = -1
	if tps, _ := tp.TokensPerSec("alpha"); tps <= 0 {
		t.Error("Snapshot handed out a live reference")
	}
	// An unobserved model is absent rather than zero.
	if _, _, ok := ObservedThroughput("never-seen-model"); ok {
		t.Error("an unobserved model reported as measured")
	}
	if (&Throughput{}).Snapshot() != nil && len((&Throughput{}).Snapshot()) != 0 {
		t.Error("empty tracker should snapshot empty")
	}
}

func TestRegisterTokenSinkIgnoresJunk(t *testing.T) {
	ResetTokenSinks()
	if stop := RegisterTokenSink("", "T1", func(string, int) {}); stop == nil {
		t.Fatal("nil cleanup")
	} else {
		stop()
	}
	if stop := RegisterTokenSink("worker", "T1", nil); stop == nil {
		t.Fatal("nil cleanup")
	} else {
		stop()
	}
	if TokenSinkCount() != 0 {
		t.Errorf("junk registrations landed: %d", TokenSinkCount())
	}
	if lookupTokenSink("", "") != nil {
		t.Error("empty role resolved a sink")
	}
}

// Two overlapping registrations for one (role, task) pair: the first
// unregister must not take the second one's sink with it.
func TestOverlappingTokenSinkRegistrationsUnregisterIndependently(t *testing.T) {
	ResetTokenSinks()
	defer ResetTokenSinks()

	var firstHits, secondHits int
	stop1 := RegisterTokenSink("reviewer", "T1", func(string, int) { firstHits++ })
	stop2 := RegisterTokenSink("reviewer", "T1", func(string, int) { secondHits++ })

	if fn := lookupTokenSink("reviewer", "T1"); fn != nil {
		fn("x", 1)
	}
	stop1() // the OUTER call finishing must not deregister the inner one
	fn := lookupTokenSink("reviewer", "T1")
	if fn == nil {
		t.Fatal("the surviving registration was deleted by the other one's cleanup")
	}
	fn("y", 1)
	stop2()
	if lookupTokenSink("reviewer", "T1") != nil {
		t.Fatal("sink leaked after its own unregister")
	}
	if secondHits != 2 {
		t.Fatalf("second sink received %d deltas, want 2", secondHits)
	}
	if firstHits != 0 {
		t.Fatalf("first sink received %d deltas after being replaced", firstHits)
	}
}
