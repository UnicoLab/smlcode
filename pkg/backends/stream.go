package backends

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Live token streaming.
//
// The whole delivery path for token-by-token output already existed —
// stream.KindToken, loop.Runner.EmitToken, the orchestrator's event bridge, the
// CLI activity line and Studio's Live view — with nothing at the source. This
// file is the source.
//
// It cannot live in the ReAct loop: ggagent.SubAgentRequest has no per-token
// callback and GoLangGraph builds llm.CompletionRequest itself. But the roles
// are already bound to their own provider registrations (see BindRole), and a
// provider DOES see every chunk. So the tee is a third provider wrapper,
// stacked under the role-bound structured wrapper and over the retry wrapper:
//
//	structuredProvider (role, constrained decoding)
//	  └── streamTeeProvider (role, THIS FILE)
//	        └── retryProvider (policy, deadline, throughput)
//	              └── raw ollama / openai provider
//
// It only ever tees. When no sink is registered for the (role, task) pair it
// delegates verbatim and costs one map lookup, so the non-streaming paths —
// notably structuredProvider's direct constrained-decoding HTTP call, which is
// deliberately `"stream": false` — degrade silently to exactly today's
// behavior.

// TokenSink receives coalesced output deltas for one agent call. tokens is the
// running estimate of completion tokens produced so far by that call.
//
// A sink must not block for long: it is called from a pump goroutine that is
// decoupled from inference, so a slow sink cannot stall decode, but it can make
// the deltas it receives arrive in larger clumps.
type TokenSink func(delta string, tokens int)

// Coalescing cadence. A local 7B decodes at 15–40 tokens/sec; emitting one
// event per token would repaint the terminal 30 times a second for no gain.
// Flushing on whichever of these comes first keeps it legible and still live.
const (
	// TokenFlushInterval is the maximum time a buffered delta waits.
	TokenFlushInterval = 50 * time.Millisecond
	// TokenFlushChars is the buffer size that forces an immediate flush.
	TokenFlushChars = 40
)

// sinkKey addresses one live agent call.
//
// Role comes from the provider registration (BindRole encodes it in the
// registry key), task from the context tag the loop sets with
// workspace.WithTaskID before dispatching each request.
type sinkKey struct {
	role string
	task string
}

var tokenSinks = struct {
	mu sync.RWMutex
	m  map[sinkKey]TokenSink
}{m: map[sinkKey]TokenSink{}}

func normalizeSinkKey(role, task string) sinkKey {
	return sinkKey{role: strings.TrimSpace(role), task: strings.TrimSpace(task)}
}

// RegisterTokenSink installs fn as the live-token consumer for one role/task
// pair and returns the function that removes it again. The returned function is
// idempotent and must always be deferred: an orphaned sink would keep emitting
// deltas attributed to a task that finished.
//
// A nil fn, or an empty role, registers nothing and returns a no-op.
func RegisterTokenSink(role, task string, fn TokenSink) func() {
	k := normalizeSinkKey(role, task)
	if fn == nil || k.role == "" {
		return func() {}
	}
	tokenSinks.mu.Lock()
	tokenSinks.m[k] = fn
	tokenSinks.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			tokenSinks.mu.Lock()
			delete(tokenSinks.m, k)
			tokenSinks.mu.Unlock()
		})
	}
}

// lookupTokenSink resolves the sink for a role/task pair.
//
// Task attribution is best-effort by design. The exact task id is used when the
// context carries one; a sink registered for the role with an empty task id is
// the fallback, which is what the sequential (non-wave) call sites get. A sink
// is never shared across roles, because that is the attribution the terminal
// actually needs at max_parallel > 1.
func lookupTokenSink(role, task string) TokenSink {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil
	}
	tokenSinks.mu.RLock()
	defer tokenSinks.mu.RUnlock()
	if len(tokenSinks.m) == 0 {
		return nil
	}
	if fn, ok := tokenSinks.m[sinkKey{role: role, task: strings.TrimSpace(task)}]; ok {
		return fn
	}
	if fn, ok := tokenSinks.m[sinkKey{role: role}]; ok {
		return fn
	}
	return nil
}

// TokenSinkCount reports how many sinks are currently registered. Tests use it
// to prove registration is cleaned up; diagnostics use it to prove the CLI is
// actually attached.
func TokenSinkCount() int {
	tokenSinks.mu.RLock()
	defer tokenSinks.mu.RUnlock()
	return len(tokenSinks.m)
}

// ResetTokenSinks drops every registration (tests).
func ResetTokenSinks() {
	tokenSinks.mu.Lock()
	tokenSinks.m = map[sinkKey]TokenSink{}
	tokenSinks.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Coalescing pump
// ---------------------------------------------------------------------------

// tokenPump decouples the inference goroutine from the sink.
//
// push() only appends to a buffer under a mutex and pokes a one-slot channel,
// so it can never block on the consumer. A dedicated goroutine drains the whole
// buffer and calls the sink outside the lock. A slow consumer therefore
// coalesces — it receives fewer, larger deltas — and never drops text and never
// stalls decode.
type tokenPump struct {
	sink   TokenSink
	every  time.Duration
	atChar int

	mu     sync.Mutex
	buf    strings.Builder
	tokens int

	notify  chan struct{}
	done    chan struct{}
	stopped chan struct{}
}

func newTokenPump(sink TokenSink) *tokenPump {
	p := &tokenPump{
		sink:    sink,
		every:   TokenFlushInterval,
		atChar:  TokenFlushChars,
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go p.run()
	return p
}

func (p *tokenPump) run() {
	t := time.NewTicker(p.every)
	defer t.Stop()
	defer close(p.stopped)
	for {
		select {
		case <-p.done:
			p.flush()
			return
		case <-p.notify:
			p.flush()
		case <-t.C:
			p.flush()
		}
	}
}

// push buffers one raw delta. Never blocks.
func (p *tokenPump) push(delta string) {
	if p == nil || delta == "" {
		return
	}
	p.mu.Lock()
	p.buf.WriteString(delta)
	n := p.buf.Len()
	p.mu.Unlock()
	if n >= p.atChar {
		select {
		case p.notify <- struct{}{}:
		default: // a flush is already pending; this delta rides along with it
		}
	}
}

// flush drains the buffer and hands it to the sink. The sink is called with the
// lock released so a slow consumer cannot block push().
func (p *tokenPump) flush() {
	p.mu.Lock()
	s := p.buf.String()
	p.buf.Reset()
	if s == "" {
		p.mu.Unlock()
		return
	}
	// Token accounting reuses GoLangGraph's tiktoken-backed estimator — the
	// same one pkg/orchestrator/usage.go and pkg/context/tokens.go use, so the
	// live count and the final usage number come from one counter.
	//
	// APPROXIMATION: the estimate is summed per flushed chunk rather than
	// recomputed over the whole stream, which costs O(n) instead of O(n²) and
	// differs from a single whole-text encode by at most a token per flush
	// (a chunk boundary can split one BPE token in two).
	p.tokens += llm.EstimateTokens(s)
	n := p.tokens
	p.mu.Unlock()
	p.sink(s, n)
}

// close flushes what is left and stops the pump goroutine.
func (p *tokenPump) close() {
	if p == nil {
		return
	}
	close(p.done)
	<-p.stopped
}

// ---------------------------------------------------------------------------
// streamTeeProvider
// ---------------------------------------------------------------------------

// streamTeeProvider tees streaming deltas for one agent role to whatever sink
// is registered for the task the call is running under. It never transforms the
// response the ReAct loop sees.
type streamTeeProvider struct {
	inner llm.Provider
	role  string
}

// newStreamTee wraps p so that its streaming completions are teed. Returns p
// unchanged when there is nothing to attribute deltas to.
func newStreamTee(p llm.Provider, role string) llm.Provider {
	if p == nil || strings.TrimSpace(role) == "" {
		return p
	}
	return &streamTeeProvider{inner: p, role: strings.TrimSpace(role)}
}

// sinkFor resolves the sink for this call, or nil when nothing is listening.
func (p *streamTeeProvider) sinkFor(ctx context.Context) TokenSink {
	return lookupTokenSink(p.role, workspace.TaskIDFrom(ctx))
}

// streams reports whether the requested mode will actually produce chunks.
func streams(req llm.CompletionRequest, mode llm.StreamMode) bool {
	switch mode {
	case llm.StreamModeForced:
		return true
	case llm.StreamModeAuto:
		return req.Stream
	case llm.StreamModeNone:
		return false
	default:
		return req.Stream
	}
}

func (p *streamTeeProvider) GetName() string { return p.inner.GetName() }

func (p *streamTeeProvider) GetModels(ctx context.Context) ([]string, error) {
	return p.inner.GetModels(ctx)
}

// Complete is the non-streaming path: nothing to tee, so it is a pass-through.
func (p *streamTeeProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return p.inner.Complete(ctx, req)
}

// CompleteWithMode is the path the ReAct loop actually takes: pkg/agents sets
// EnableStreaming with StreamModeForced so a completed tool call can cancel the
// rest of the decode (llm.DefaultEarlyExit).
//
// When a sink is listening, this drives the stream itself so the deltas are
// visible, reassembling the response with llm.CollectStream — the exact
// function the underlying provider would have used — so early exit, tool-call
// accumulation and usage estimation all behave identically.
func (p *streamTeeProvider) CompleteWithMode(
	ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode,
) (*llm.CompletionResponse, error) {
	sink := p.sinkFor(ctx)
	if sink == nil || !streams(req, mode) || !p.inner.SupportsStreaming() {
		return p.inner.CompleteWithMode(ctx, req, mode)
	}
	pump := newTokenPump(sink)
	defer pump.close()

	start := time.Now()
	resp, err := llm.CollectStream(ctx, func(
		c context.Context, r llm.CompletionRequest, cb llm.StreamCallback,
	) error {
		return p.inner.CompleteStream(c, r, p.tee(pump, cb))
	}, req)
	// The retry wrapper observes throughput on Complete/CompleteWithMode only,
	// and this call took over from it — so fold the sample in here, otherwise
	// enabling live streaming would silently blind EstimateTimeout.
	p.observe(req, resp, start)
	return resp, err
}

func (p *streamTeeProvider) CompleteStream(
	ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback,
) error {
	sink := p.sinkFor(ctx)
	if sink == nil {
		return p.inner.CompleteStream(ctx, req, cb)
	}
	pump := newTokenPump(sink)
	defer pump.close()
	return p.inner.CompleteStream(ctx, req, p.tee(pump, cb))
}

func (p *streamTeeProvider) CompleteStreamWithMode(
	ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, mode llm.StreamMode,
) error {
	sink := p.sinkFor(ctx)
	if sink == nil || !streams(req, mode) {
		return p.inner.CompleteStreamWithMode(ctx, req, cb, mode)
	}
	pump := newTokenPump(sink)
	defer pump.close()
	return p.inner.CompleteStreamWithMode(ctx, req, p.tee(pump, cb), mode)
}

// tee returns cb with a side effect: every content delta is pushed to the pump
// first, then the original callback runs unchanged and its error (including
// llm.ErrStreamEarlyExit) is returned verbatim.
func (p *streamTeeProvider) tee(pump *tokenPump, cb llm.StreamCallback) llm.StreamCallback {
	return func(chunk llm.CompletionResponse) error {
		pump.push(chunkDelta(chunk))
		if cb == nil {
			return nil
		}
		return cb(chunk)
	}
}

// chunkDelta extracts the incremental assistant text from one stream chunk.
// Tool-call argument fragments are deliberately NOT teed: they are JSON the
// user did not ask to read, and the activity line renders prose.
func chunkDelta(chunk llm.CompletionResponse) string {
	if len(chunk.Choices) == 0 {
		return ""
	}
	if d := chunk.Choices[0].Delta.Content; d != "" {
		return d
	}
	return ""
}

func (p *streamTeeProvider) observe(req llm.CompletionRequest, resp *llm.CompletionResponse, start time.Time) {
	if resp == nil {
		return
	}
	model := resp.Model
	if strings.TrimSpace(model) == "" {
		model = req.Model
	}
	observeAndPersist(model, resp.Usage.CompletionTokens, time.Since(start))
}

func (p *streamTeeProvider) IsHealthy(ctx context.Context) error { return p.inner.IsHealthy(ctx) }

func (p *streamTeeProvider) GetConfig() map[string]interface{} {
	c := p.inner.GetConfig()
	if c == nil {
		c = map[string]interface{}{}
	}
	c["slmcode_stream_role"] = p.role
	return c
}

func (p *streamTeeProvider) SetConfig(c map[string]interface{}) error { return p.inner.SetConfig(c) }
func (p *streamTeeProvider) SupportsStreaming() bool                  { return p.inner.SupportsStreaming() }
func (p *streamTeeProvider) GetStreamingConfig() *llm.StreamingConfig {
	return p.inner.GetStreamingConfig()
}
func (p *streamTeeProvider) SetStreamingConfig(c *llm.StreamingConfig) error {
	return p.inner.SetStreamingConfig(c)
}
func (p *streamTeeProvider) Close() error { return p.inner.Close() }
