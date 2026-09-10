package backends

import (
	"context"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Live ReAct compaction: deterministic elision of old tool results.
//
// A ReAct iteration is exactly one llm.Provider completion, and the roles are
// already bound to their own provider registrations (BindRole). So the one
// place in the process that sees iteration N of a 16-iteration worker — with
// its whole transcript — is a provider wrapper, the same route live token
// streaming took (stream.go). This file is that wrapper. It sits OUTERMOST in
// the role's stack, above the structured wrapper, so the elided transcript is
// what both the delegate and the direct constrained-decoding call send:
//
//	liveElideProvider   (role, THIS FILE)
//	  └── structuredProvider (role, constrained decoding)
//	        └── streamTeeProvider (role, live tokens)
//	              └── retryProvider → raw provider
//
// It does ONE thing, and only when the estimated transcript is over the
// threshold: the content of every tool RESULT but the last KeepLast is
// replaced with a placeholder. Every tool CALL stays, every tool_call_id pair
// stays, the leading system message and every user/assistant turn stay — a
// transcript that was legal before is legal after. Nothing is summarized: the
// checkpoint/resume path (pkg/loop.maybeCompactReact) may fold the head into a
// system digest, but on a LIVE request the head is the role's tool contract
// and dropping it mid-call would break the agent more reliably than a long
// context does. Deterministic elision costs no inference and is measured to
// beat summarization, which is also why the checkpoint path tries it first.
//
// The rewrite is per request and never touches the agent's own conversation,
// so it is repeatable: iteration N+1 sends the full transcript again and is
// elided the same way, and the elided prefix is byte-identical between
// iterations until the keep window slides — KV-cache reuse survives.

// LiveElide configures the wrapper for one role. The zero value disables it.
type LiveElide struct {
	// WindowTokens is the model's context window in tokens; <=0 disables.
	WindowTokens int
	// AtPercent triggers elision when the estimated transcript reaches this
	// percentage of WindowTokens; <=0 or >=100 disables.
	AtPercent int
	// KeepLast is how many trailing tool results stay verbatim; <=0 means
	// compact.DefaultElideKeepLast.
	KeepLast int
}

// Enabled reports whether this configuration elides anything at all.
func (e LiveElide) Enabled() bool {
	return e.WindowTokens > 0 && e.AtPercent > 0 && e.AtPercent < 100
}

func (e LiveElide) keep() int {
	if e.KeepLast > 0 {
		return e.KeepLast
	}
	return compact.DefaultElideKeepLast
}

type liveElideProvider struct {
	inner llm.Provider
	role  string
	cfg   LiveElide
}

// newLiveElide wraps p, or returns it unchanged when there is nothing to do.
func newLiveElide(p llm.Provider, role string, cfg LiveElide) llm.Provider {
	if p == nil || !cfg.Enabled() {
		return p
	}
	return &liveElideProvider{inner: p, role: strings.TrimSpace(role), cfg: cfg}
}

// requestBytes approximates the prompt the request will render: the system
// prompt plus every message's text and tool-call payload.
func requestBytes(req llm.CompletionRequest) int {
	n := len(req.SystemPrompt)
	for _, m := range req.Messages {
		n += len(m.Role) + len(m.Content) + len(m.Name) + len(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

// elide returns req with old tool results collapsed when the transcript is
// over the threshold, and req unchanged otherwise.
func (p *liveElideProvider) elide(req llm.CompletionRequest) llm.CompletionRequest {
	tokens := compact.EstimateTokens(requestBytes(req))
	if compact.UsagePercent(tokens, p.cfg.WindowTokens) < float64(p.cfg.AtPercent) {
		return req
	}
	out, n := compact.ElideOldToolResultsFunc(req.Messages, p.cfg.keep(), compact.DefaultElidedPlaceholder,
		func(m llm.Message) bool { return strings.EqualFold(m.Role, compact.RoleTool) },
		func(m llm.Message, placeholder string) llm.Message { m.Content = placeholder; return m })
	if n == 0 {
		return req
	}
	recordElided(p.role, n)
	req.Messages = out
	return req
}

func (p *liveElideProvider) GetName() string { return p.inner.GetName() }

func (p *liveElideProvider) GetModels(ctx context.Context) ([]string, error) {
	return p.inner.GetModels(ctx)
}

func (p *liveElideProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return p.inner.Complete(ctx, p.elide(req))
}

func (p *liveElideProvider) CompleteWithMode(
	ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode,
) (*llm.CompletionResponse, error) {
	return p.inner.CompleteWithMode(ctx, p.elide(req), mode)
}

func (p *liveElideProvider) CompleteStream(
	ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback,
) error {
	return p.inner.CompleteStream(ctx, p.elide(req), cb)
}

func (p *liveElideProvider) CompleteStreamWithMode(
	ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, mode llm.StreamMode,
) error {
	return p.inner.CompleteStreamWithMode(ctx, p.elide(req), cb, mode)
}

func (p *liveElideProvider) IsHealthy(ctx context.Context) error { return p.inner.IsHealthy(ctx) }

func (p *liveElideProvider) GetConfig() map[string]interface{} {
	c := p.inner.GetConfig()
	if c == nil {
		c = map[string]interface{}{}
	}
	c["slmcode_live_elide"] = true
	c["slmcode_live_elide_window_tokens"] = p.cfg.WindowTokens
	c["slmcode_live_elide_at_percent"] = p.cfg.AtPercent
	return c
}

func (p *liveElideProvider) SetConfig(c map[string]interface{}) error { return p.inner.SetConfig(c) }
func (p *liveElideProvider) SupportsStreaming() bool                  { return p.inner.SupportsStreaming() }
func (p *liveElideProvider) GetStreamingConfig() *llm.StreamingConfig {
	return p.inner.GetStreamingConfig()
}

func (p *liveElideProvider) SetStreamingConfig(c *llm.StreamingConfig) error {
	return p.inner.SetStreamingConfig(c)
}
func (p *liveElideProvider) Close() error { return p.inner.Close() }

// Elision telemetry: how many tool results each role has had collapsed on live
// requests. Diagnostics read it the way they read MechanismStats.
var elideStats = struct {
	mu     sync.Mutex
	byRole map[string]int
}{byRole: map[string]int{}}

func recordElided(role string, n int) {
	if n <= 0 {
		return
	}
	elideStats.mu.Lock()
	defer elideStats.mu.Unlock()
	elideStats.byRole[role] += n
}

// LiveElideStats returns, per role, how many tool results live elision has
// collapsed so far in this process.
func LiveElideStats() map[string]int {
	elideStats.mu.Lock()
	defer elideStats.mu.Unlock()
	out := make(map[string]int, len(elideStats.byRole))
	for k, v := range elideStats.byRole {
		out[k] = v
	}
	return out
}

// ResetLiveElideStats clears the elision counters (tests).
func ResetLiveElideStats() {
	elideStats.mu.Lock()
	defer elideStats.mu.Unlock()
	elideStats.byRole = map[string]int{}
}
