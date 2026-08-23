package backends

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class ErrorClass
	}{
		{"nil", nil, ClassCanceled},
		{"canceled", context.Canceled, ClassCanceled},
		{"deadline", context.DeadlineExceeded, ClassCanceled},
		{"stream early exit", llm.ErrStreamEarlyExit, ClassCanceled},
		{"connection refused", errors.New("dial tcp 127.0.0.1:9000: connect: connection refused"), ClassTransient},
		{"reset", errors.New("read: connection reset by peer"), ClassTransient},
		{"net.Error", &net.DNSError{Err: "no such host", IsTimeout: true}, ClassTransient},
		{"500", errors.New("OpenAI completion failed: error, status code: 500, message: boom"), ClassTransient},
		{"502", errors.New("error, status code: 502, message: bad gateway"), ClassTransient},
		{"503", errors.New("error, status code: 503"), ClassTransient},
		{"429", errors.New("error, status code: 429, message: rate limit"), ClassRateLimited},
		{"408", errors.New("error, status code: 408"), ClassTransient},
		{"400", errors.New("error, status code: 400, message: bad request"), ClassPermanent},
		{"401", errors.New("error, status code: 401, message: unauthorized"), ClassPermanent},
		{"404", errors.New("error, status code: 404, message: model not found"), ClassPermanent},
		{"422", errors.New("error, status code: 422, message: unprocessable"), ClassPermanent},
		{
			"context overflow",
			errors.New("error, status code: 400, message: This model's maximum context length is 8192 tokens"),
			ClassContextOverflow,
		},
		{
			"context_length_exceeded code",
			errors.New(`error, status code: 400, message: {"code":"context_length_exceeded"}`),
			ClassContextOverflow,
		},
		{"unknown", errors.New("something odd happened"), ClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got.Class != tc.class {
				t.Errorf("Classify(%v).Class = %v, want %v", tc.err, got.Class, tc.class)
			}
		})
	}
}

func TestClassifyRetryableSet(t *testing.T) {
	// The whole point: 400-class failures must never cost a second prefill.
	for _, msg := range []string{
		"error, status code: 400", "error, status code: 401",
		"error, status code: 403", "error, status code: 404",
		"error, status code: 422",
	} {
		if Classify(errors.New(msg)).Retryable() {
			t.Errorf("%q must not be retryable", msg)
		}
	}
	for _, msg := range []string{
		"error, status code: 500", "error, status code: 429",
		"connection refused",
	} {
		if !Classify(errors.New(msg)).Retryable() {
			t.Errorf("%q must be retryable", msg)
		}
	}
	// An unknown failure is treated as permanent, not replayed blindly.
	if Classify(errors.New("mystery")).Retryable() {
		t.Error("unknown errors must not be retried")
	}
}

func TestClassifyHTTPErrorAndRetryAfter(t *testing.T) {
	e := &HTTPError{Status: 429, Body: "slow down", RetryAfter: 3 * time.Second}
	c := Classify(fmt.Errorf("wrapped: %w", e))
	if c.Class != ClassRateLimited || c.RetryAfter != 3*time.Second {
		t.Fatalf("got %+v", c)
	}
	if !IsContextOverflow(&HTTPError{Status: 400, Body: "maximum context length exceeded"}) {
		t.Error("context overflow not detected on HTTPError")
	}
}

func TestBackoffJitterAndCeiling(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second, Jitter: true, Rand: func() float64 { return 1.0 }}
	// Full jitter at rand=1.0 gives the full exponential value.
	if got := p.Backoff(1, 0); got != 100*time.Millisecond {
		t.Errorf("attempt 1 = %v", got)
	}
	if got := p.Backoff(2, 0); got != 200*time.Millisecond {
		t.Errorf("attempt 2 = %v", got)
	}
	if got := p.Backoff(10, 0); got != time.Second {
		t.Errorf("ceiling not applied: %v", got)
	}
	// rand=0 must be able to return ~0 — that is what spreads workers apart.
	p.Rand = func() float64 { return 0 }
	if got := p.Backoff(3, 0); got != 0 {
		t.Errorf("full jitter floor = %v, want 0", got)
	}
	// A Retry-After hint always wins, clamped to MaxDelay.
	if got := p.Backoff(1, 500*time.Millisecond); got != 500*time.Millisecond {
		t.Errorf("hint ignored: %v", got)
	}
	if got := p.Backoff(1, time.Hour); got != time.Second {
		t.Errorf("hint not clamped: %v", got)
	}
	// Without jitter the backoff is deterministic.
	p2 := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}
	if got := p2.Backoff(3, 0); got != 4*time.Second {
		t.Errorf("no-jitter backoff = %v", got)
	}
}

func TestRetryDoOnlyRetriesRetryableFailures(t *testing.T) {
	fast := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	cases := []struct {
		name     string
		err      error
		attempts int
	}{
		{"permanent 400 tried once", errors.New("error, status code: 400"), 1},
		{"unknown tried once", errors.New("weird"), 1},
		{"500 exhausts attempts", errors.New("error, status code: 500"), 3},
		{"429 exhausts attempts", errors.New("error, status code: 429"), 3},
		{"connection refused exhausts", errors.New("connection refused"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			_, err := retryDo(context.Background(), fast, func(context.Context, int) (int, error) {
				n++
				return 0, tc.err
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if n != tc.attempts {
				t.Errorf("attempts = %d, want %d", n, tc.attempts)
			}
		})
	}
}

func TestRetryDoSucceedsAfterTransientFailure(t *testing.T) {
	fast := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	n := 0
	got, err := retryDo(context.Background(), fast, func(context.Context, int) (string, error) {
		n++
		if n < 3 {
			return "", errors.New("error, status code: 503")
		}
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if n != 3 {
		t.Errorf("attempts = %d", n)
	}
}

func TestRetryDoStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Hour, MaxDelay: time.Hour}
	n := 0
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := retryDo(ctx, p, func(context.Context, int) (int, error) {
		n++
		return 0, errors.New("error, status code: 500")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if n > 2 {
		t.Errorf("kept retrying after cancel: %d attempts", n)
	}
}

func TestEstimateTimeoutScalesWithTokensAndThroughput(t *testing.T) {
	GlobalThroughput.Reset()
	// Cold: pessimistic prior, but bounded.
	small := EstimateTimeout("m", 256)
	big := EstimateTimeout("m", 8192)
	if small < MinCallTimeout {
		t.Errorf("small = %v, below floor", small)
	}
	if big <= small {
		t.Errorf("timeout did not scale with max_tokens: %v vs %v", small, big)
	}
	if big > MaxCallTimeout {
		t.Errorf("big = %v, above ceiling", big)
	}
	// A fast model observed several times should shorten the estimate.
	for i := 0; i < 10; i++ {
		GlobalThroughput.Observe("fast", 400, time.Second) // 400 tok/s
	}
	fast := EstimateTimeout("fast", 8192)
	if fast >= big {
		t.Errorf("observed throughput did not shorten the deadline: fast=%v slow=%v", fast, big)
	}
	// The old behaviour floored every call at 3 minutes; a small request must
	// now be far below that.
	if EstimateTimeout("fast", 256) >= 3*time.Minute {
		t.Error("small request still holds a slot for 3 minutes")
	}
}

func TestThroughputIgnoresPrefillDominatedSamples(t *testing.T) {
	GlobalThroughput.Reset()
	GlobalThroughput.Observe("m", 3, time.Second) // too few tokens
	if _, n := GlobalThroughput.TokensPerSec("m"); n != 0 {
		t.Errorf("tiny sample recorded: %d", n)
	}
	GlobalThroughput.Observe("m", 100, 0) // no elapsed time
	if _, n := GlobalThroughput.TokensPerSec("m"); n != 0 {
		t.Errorf("zero-duration sample recorded: %d", n)
	}
	GlobalThroughput.Observe("m", 100, time.Second)
	tps, n := GlobalThroughput.TokensPerSec("m")
	if n != 1 || tps < 99 || tps > 101 {
		t.Errorf("tps=%v n=%d", tps, n)
	}
	// EWMA moves toward the newer observation.
	GlobalThroughput.Observe("m", 400, time.Second)
	tps2, _ := GlobalThroughput.TokensPerSec("m")
	if tps2 <= tps {
		t.Errorf("EWMA did not move: %v → %v", tps, tps2)
	}
}

func TestRegisteredProviderRetriesTransientThenSucceeds(t *testing.T) {
	ResetCapabilityCache()
	srv := newFakeServer(t, "json_object")
	srv.failures = 2 // two 503s, then success
	m, _ := newManagerFor(t, "omlx", srv.endpoint())
	start := time.Now()
	resp, err := m.Complete(context.Background(), "omlx", reviewRequest())
	if err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if resp.Choices[0].Message.Content == "" {
		t.Fatal("empty content")
	}
	if time.Since(start) > 30*time.Second {
		t.Error("retry took implausibly long")
	}
}

func TestRegisteredProviderDoesNotRetry400(t *testing.T) {
	ResetCapabilityCache()
	srv := newFakeServer(t)
	srv.failures = 99
	srv.failStatus = 400
	srv.failBody = `{"error":{"message":"context_length_exceeded"}}`
	m, _ := newManagerFor(t, "omlx", srv.endpoint())
	srv.reset()
	_, err := m.Complete(context.Background(), "omlx", reviewRequest())
	if err == nil {
		t.Fatal("expected error")
	}
	if n := len(srv.seen()); n != 1 {
		t.Errorf("400 was retried: %d requests reached the server", n)
	}
	if !IsContextOverflow(err) {
		t.Errorf("context overflow not surfaced distinctly: %v", err)
	}
}

func TestRetryPolicyFromConfig(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.LLMRetryCount = 5
	cfg.LLMRetryDelayMS = 250
	p := retryPolicy(cfg)
	if p.MaxAttempts != 6 {
		t.Errorf("MaxAttempts = %d, want retries+1", p.MaxAttempts)
	}
	if p.BaseDelay != 250*time.Millisecond {
		t.Errorf("BaseDelay = %v", p.BaseDelay)
	}
	if !p.Jitter {
		t.Error("jitter must stay on so parallel workers do not retry in lockstep")
	}
	if got := retryPolicy(nil); got.MaxAttempts != DefaultRetryPolicy().MaxAttempts {
		t.Errorf("nil config = %+v", got)
	}
}

func TestLLMTimeoutNoLongerFloorsAtThreeMinutes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.TaskTimeout = 20 * time.Second
	if got := llmTimeout(cfg); got != MinCallTimeout {
		t.Errorf("llmTimeout = %v, want the %v floor", got, MinCallTimeout)
	}
	cfg.TaskTimeout = 2 * time.Minute
	if got := llmTimeout(cfg); got != 2*time.Minute {
		t.Errorf("llmTimeout = %v, want it to follow task_timeout", got)
	}
	cfg.TaskTimeout = 30 * time.Minute
	if got := llmTimeout(cfg); got != MaxCallTimeout {
		t.Errorf("llmTimeout = %v, want the %v ceiling", got, MaxCallTimeout)
	}
}
