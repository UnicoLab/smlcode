package backends

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// ---------------------------------------------------------------------------
// Error classification
// ---------------------------------------------------------------------------

// ErrorClass buckets an LLM call failure by what the caller should do next.
type ErrorClass int

const (
	// ClassUnknown could not be classified — treated as permanent so a broken
	// request is surfaced instead of replayed three times against a local model.
	ClassUnknown ErrorClass = iota
	// ClassTransient is a connection-level failure or 5xx: worth retrying.
	ClassTransient
	// ClassRateLimited is 429 (or an explicit Retry-After): retry, honouring the hint.
	ClassRateLimited
	// ClassPermanent is 400/401/403/404/413/422: retrying burns a full prefill
	// for nothing. Surface immediately.
	ClassPermanent
	// ClassContextOverflow is a context_length_exceeded 400. Permanent for retry
	// purposes, but distinct because the fix is "shrink the pack / raise the
	// window", not "try again".
	ClassContextOverflow
	// ClassCanceled is ctx cancellation/deadline or a deliberate stream early exit.
	ClassCanceled
)

func (c ErrorClass) String() string {
	switch c {
	case ClassTransient:
		return "transient"
	case ClassRateLimited:
		return "rate_limited"
	case ClassPermanent:
		return "permanent"
	case ClassContextOverflow:
		return "context_overflow"
	case ClassCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

// Classification is the verdict on one failed LLM call.
type Classification struct {
	Class      ErrorClass
	Status     int           // HTTP status when one could be recovered, else 0
	RetryAfter time.Duration // server hint, else 0
}

// Retryable reports whether another attempt is worth a full prefill.
func (c Classification) Retryable() bool {
	return c.Class == ClassTransient || c.Class == ClassRateLimited
}

var (
	statusRe     = regexp.MustCompile(`status code: (\d{3})`)
	altStatusRe  = regexp.MustCompile(`\b(?:HTTP )?(\d{3})\b`)
	retryAfterRe = regexp.MustCompile(`(?i)retry[- ]after[:= ]+\s*(\d+)`)
)

// contextOverflowMarkers are the phrasings the target servers actually use.
var contextOverflowMarkers = []string{
	"context_length_exceeded",
	"maximum context length",
	"context length exceeded",
	"reduce the length of the messages",
	"too many tokens",
	"prompt is too long",
	"exceeds the maximum",
	"kv cache",
	"n_ctx",
}

// Classify inspects an error from a provider call.
//
// Classification is textual on purpose: the concrete API error types live in
// GoLangGraph's vendored go-openai dependency, which this module must not
// import directly. Errors raised by the direct structured HTTP path carry a
// typed *HTTPError and short-circuit the text matching.
func Classify(err error) Classification {
	if err == nil {
		return Classification{Class: ClassCanceled}
	}
	if llm.IsStreamEarlyExit(err) {
		return Classification{Class: ClassCanceled}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Classification{Class: ClassCanceled}
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return classifyStatus(he.Status, he.Body, he.RetryAfter)
	}
	// Transport-level failures are always worth one more attempt: a local
	// inference server that just finished loading a model refuses connections
	// for a few seconds.
	var ne net.Error
	if errors.As(err, &ne) {
		return Classification{Class: ClassTransient}
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return Classification{Class: ClassTransient}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	for _, m := range []string{
		"connection refused", "connection reset", "no such host", "eof",
		"broken pipe", "server closed", "i/o timeout", "tls handshake",
		"dial tcp", "network is unreachable", "unexpected eof",
	} {
		if strings.Contains(lower, m) {
			return Classification{Class: ClassTransient}
		}
	}
	status := 0
	if m := statusRe.FindStringSubmatch(msg); len(m) == 2 {
		status, _ = strconv.Atoi(m[1])
	} else if m := altStatusRe.FindStringSubmatch(msg); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 400 && n < 600 {
			status = n
		}
	}
	var after time.Duration
	if m := retryAfterRe.FindStringSubmatch(msg); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			after = time.Duration(n) * time.Second
		}
	}
	return classifyStatus(status, msg, after)
}

func classifyStatus(status int, body string, after time.Duration) Classification {
	lower := strings.ToLower(body)
	overflow := false
	for _, m := range contextOverflowMarkers {
		if strings.Contains(lower, m) {
			overflow = true
			break
		}
	}
	switch {
	case status == 429:
		return Classification{Class: ClassRateLimited, Status: status, RetryAfter: after}
	case status == 408 || status == 409 || status == 425:
		return Classification{Class: ClassTransient, Status: status, RetryAfter: after}
	case status >= 500 && status < 600:
		return Classification{Class: ClassTransient, Status: status, RetryAfter: after}
	case status == 400 && overflow:
		return Classification{Class: ClassContextOverflow, Status: status}
	case status >= 400 && status < 500:
		return Classification{Class: ClassPermanent, Status: status}
	case overflow:
		return Classification{Class: ClassContextOverflow, Status: status}
	case status == 0:
		return Classification{Class: ClassUnknown}
	}
	return Classification{Class: ClassUnknown, Status: status}
}

// HTTPError is the typed failure the direct structured path returns.
type HTTPError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
	URL        string
}

func (e *HTTPError) Error() string {
	body := e.Body
	if len(body) > 400 {
		body = body[:400] + "…"
	}
	return fmt.Sprintf("llm http %d: %s", e.Status, body)
}

// IsContextOverflow reports whether err means "the prompt did not fit".
// Callers should shrink the context pack or raise max_tokens rather than retry.
func IsContextOverflow(err error) bool {
	return Classify(err).Class == ClassContextOverflow
}

// ---------------------------------------------------------------------------
// Retry policy
// ---------------------------------------------------------------------------

// RetryPolicy is exponential backoff with full jitter over a bounded number of
// attempts. It replaces the provider's own fixed-delay retry, which is
// registered with RetryCount 0 so a request is never retried twice over.
type RetryPolicy struct {
	MaxAttempts int           // total attempts including the first (default 3)
	BaseDelay   time.Duration // first backoff (default 500ms)
	MaxDelay    time.Duration // backoff ceiling (default 20s)
	// Jitter spreads concurrent workers. With max_parallel=4 against one local
	// server, lockstep retries are the difference between a recovery and a
	// thundering herd on a backend that serialises inference anyway.
	Jitter bool
	// Rand is injectable for deterministic tests. Nil uses a shared source.
	Rand func() float64
}

// DefaultRetryPolicy is the policy applied to every registered provider.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, MaxDelay: 20 * time.Second, Jitter: true}
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 500 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 20 * time.Second
	}
	return p
}

var jitterRand = struct {
	mu sync.Mutex
	r  *rand.Rand
}{r: rand.New(rand.NewSource(time.Now().UnixNano()))} // #nosec G404 -- jitter, not crypto

func (p RetryPolicy) random() float64 {
	if p.Rand != nil {
		return p.Rand()
	}
	jitterRand.mu.Lock()
	defer jitterRand.mu.Unlock()
	return jitterRand.r.Float64()
}

// Backoff returns the delay before attempt n (1-based: Backoff(1) is the pause
// after the first failure). A server Retry-After hint always wins.
func (p RetryPolicy) Backoff(attempt int, hint time.Duration) time.Duration {
	p = p.normalized()
	if hint > 0 {
		if hint > p.MaxDelay {
			return p.MaxDelay
		}
		return hint
	}
	exp := float64(p.BaseDelay) * math.Pow(2, float64(attempt-1))
	if exp > float64(p.MaxDelay) {
		exp = float64(p.MaxDelay)
	}
	if !p.Jitter {
		return time.Duration(exp)
	}
	return time.Duration(p.random() * exp) // full jitter
}

// retryDo runs fn under the policy, retrying only transient / rate-limited
// failures. attemptFn receives the 1-based attempt number.
func retryDo[T any](ctx context.Context, p RetryPolicy, fn func(ctx context.Context, attempt int) (T, error)) (T, error) {
	p = p.normalized()
	var zero T
	var lastErr error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return zero, lastErr
			}
			return zero, err
		}
		out, err := fn(ctx, attempt)
		if err == nil {
			return out, nil
		}
		lastErr = err
		c := Classify(err)
		if !c.Retryable() || attempt == p.MaxAttempts {
			return zero, err
		}
		delay := p.Backoff(attempt, c.RetryAfter)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return zero, err
		case <-t.C:
		}
	}
	return zero, lastErr
}

// ---------------------------------------------------------------------------
// Observed throughput
// ---------------------------------------------------------------------------

// Throughput records observed decode speed per model so the request deadline
// can be derived from "this model produces N tokens/sec" instead of from the
// whole-task budget. The estimate improves within a session.
type Throughput struct {
	mu sync.RWMutex
	m  map[string]*tpEntry
}

type tpEntry struct {
	tps     float64 // EWMA tokens/sec
	samples int
}

// GlobalThroughput is the process-wide tracker consulted by EstimateTimeout.
var GlobalThroughput = &Throughput{m: map[string]*tpEntry{}}

// DefaultTokensPerSec is the conservative prior used before any observation.
// It is deliberately pessimistic: a 30B 4-bit model on a laptop.
const DefaultTokensPerSec = 12.0

// Observe folds one completed call into the model's decode-rate estimate.
// Calls that produced fewer than 8 tokens are ignored — they are dominated by
// prefill and would drag the estimate down.
func (t *Throughput) Observe(model string, completionTokens int, elapsed time.Duration) {
	if t == nil || completionTokens < 8 || elapsed <= 0 {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	rate := float64(completionTokens) / elapsed.Seconds()
	if rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]*tpEntry{}
	}
	e, ok := t.m[model]
	if !ok {
		t.m[model] = &tpEntry{tps: rate, samples: 1}
		return
	}
	// EWMA, α=0.3 — responsive enough to notice a model swap mid-session.
	e.tps = 0.7*e.tps + 0.3*rate
	e.samples++
}

// TokensPerSec returns the observed decode rate and how many samples back it.
func (t *Throughput) TokensPerSec(model string) (float64, int) {
	if t == nil {
		return 0, 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.m[strings.TrimSpace(model)]
	if !ok {
		return 0, 0
	}
	return e.tps, e.samples
}

// Reset clears observations (tests).
func (t *Throughput) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m = map[string]*tpEntry{}
}

// Timeout bounds for a single completion.
const (
	MinCallTimeout = 45 * time.Second
	MaxCallTimeout = 10 * time.Minute
	// PrefillAllowance covers model load + prompt evaluation before the first
	// token. Cold local models are the reason this is not smaller.
	PrefillAllowance = 40 * time.Second
	// DecodeSafetyFactor multiplies the pure decode estimate.
	DecodeSafetyFactor = 2.5
)

// EstimateTimeout derives a per-call deadline from the role's max_tokens and
// the model's observed decode rate, replacing the old "floor at 3 minutes"
// rule that let a hung 1.5B hold a worker slot for three minutes.
func EstimateTimeout(model string, maxTokens int) time.Duration {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	tps, samples := GlobalThroughput.TokensPerSec(model)
	if samples == 0 || tps <= 0 {
		tps = DefaultTokensPerSec
	}
	// Until a few samples are in, stay closer to the pessimistic prior.
	if samples > 0 && samples < 3 && tps > DefaultTokensPerSec {
		tps = (tps + DefaultTokensPerSec) / 2
	}
	decode := time.Duration(float64(maxTokens) / tps * DecodeSafetyFactor * float64(time.Second))
	total := PrefillAllowance + decode
	if total < MinCallTimeout {
		total = MinCallTimeout
	}
	if total > MaxCallTimeout {
		total = MaxCallTimeout
	}
	return total
}

// ---------------------------------------------------------------------------
// retryProvider
// ---------------------------------------------------------------------------

// retryProvider owns the retry policy and the per-call deadline for one
// backend, so the underlying provider can be registered with RetryCount 0.
type retryProvider struct {
	inner  llm.Provider
	policy RetryPolicy
	model  string
	name   string
}

// NewRetryProvider wraps p with slmcode's retry policy and token-derived
// deadlines. Exported so a caller assembling its own ProviderManager gets the
// same behaviour as RegisterLLM.
func NewRetryProvider(p llm.Provider, name, model string, policy RetryPolicy) llm.Provider {
	if p == nil {
		return nil
	}
	return &retryProvider{inner: p, policy: policy.normalized(), model: model, name: name}
}

func (p *retryProvider) GetName() string { return p.inner.GetName() }

func (p *retryProvider) GetModels(ctx context.Context) ([]string, error) {
	return p.inner.GetModels(ctx)
}

// callCtx applies the token-derived deadline. It only ever shortens the
// caller's context, never extends it.
func (p *retryProvider) callCtx(ctx context.Context, req llm.CompletionRequest) (context.Context, context.CancelFunc) {
	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.model
	}
	return context.WithTimeout(ctx, EstimateTimeout(model, req.MaxTokens))
}

func (p *retryProvider) observe(req llm.CompletionRequest, resp *llm.CompletionResponse, start time.Time) {
	if resp == nil {
		return
	}
	model := resp.Model
	if strings.TrimSpace(model) == "" {
		model = req.Model
	}
	if strings.TrimSpace(model) == "" {
		model = p.model
	}
	GlobalThroughput.Observe(model, resp.Usage.CompletionTokens, time.Since(start))
}

func (p *retryProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return retryDo(ctx, p.policy, func(ctx context.Context, _ int) (*llm.CompletionResponse, error) {
		cctx, cancel := p.callCtx(ctx, req)
		defer cancel()
		start := time.Now()
		resp, err := p.inner.Complete(cctx, req)
		p.observe(req, resp, start)
		return resp, err
	})
}

func (p *retryProvider) CompleteWithMode(ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode) (*llm.CompletionResponse, error) {
	return retryDo(ctx, p.policy, func(ctx context.Context, _ int) (*llm.CompletionResponse, error) {
		cctx, cancel := p.callCtx(ctx, req)
		defer cancel()
		start := time.Now()
		resp, err := p.inner.CompleteWithMode(cctx, req, mode)
		p.observe(req, resp, start)
		return resp, err
	})
}

// CompleteStream is not retried: a partially delivered stream cannot be
// replayed without the callback seeing the prefix twice.
func (p *retryProvider) CompleteStream(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback) error {
	cctx, cancel := p.callCtx(ctx, req)
	defer cancel()
	return p.inner.CompleteStream(cctx, req, cb)
}

func (p *retryProvider) CompleteStreamWithMode(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, mode llm.StreamMode) error {
	cctx, cancel := p.callCtx(ctx, req)
	defer cancel()
	return p.inner.CompleteStreamWithMode(cctx, req, cb, mode)
}

func (p *retryProvider) IsHealthy(ctx context.Context) error      { return p.inner.IsHealthy(ctx) }
func (p *retryProvider) GetConfig() map[string]interface{}        { return p.inner.GetConfig() }
func (p *retryProvider) SetConfig(c map[string]interface{}) error { return p.inner.SetConfig(c) }
func (p *retryProvider) SupportsStreaming() bool                  { return p.inner.SupportsStreaming() }
func (p *retryProvider) GetStreamingConfig() *llm.StreamingConfig {
	return p.inner.GetStreamingConfig()
}
func (p *retryProvider) SetStreamingConfig(c *llm.StreamingConfig) error {
	return p.inner.SetStreamingConfig(c)
}
func (p *retryProvider) Close() error { return p.inner.Close() }
