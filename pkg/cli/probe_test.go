package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemediationMapsTransportErrors(t *testing.T) {
	cases := []struct {
		err       string
		status    int
		wantCause string
		wantIn    string
	}{
		{err: "dial tcp 127.0.0.1:1234: connect: connection refused", wantCause: "connection refused", wantIn: "start your"},
		{err: `dial tcp: lookup nope.invalid: no such host`, wantCause: "host not found", wantIn: "typo"},
		{err: "context deadline exceeded", wantCause: "timed out", wantIn: "still loading"},
		{err: "x509: certificate signed by unknown authority", wantCause: "TLS handshake failed", wantIn: "http://"},
		{status: 401, wantCause: "HTTP 401 unauthorized", wantIn: "API key"},
		{status: 404, wantCause: "HTTP 404 — model not found", wantIn: "not served by this endpoint"},
		{status: 429, wantCause: "HTTP 429 rate limited", wantIn: "max_parallel"},
		{status: 503, wantCause: "HTTP 503 from the provider", wantIn: "check its logs"},
	}
	for _, c := range cases {
		cause, remedy := Remediation("ollama", "http://127.0.0.1:1234/v1", "qwen:7b", c.status, c.err)
		if cause != c.wantCause {
			t.Errorf("cause for %q/%d = %q want %q", c.err, c.status, cause, c.wantCause)
		}
		if !strings.Contains(remedy, c.wantIn) {
			t.Errorf("remedy for %q/%d = %q want it to mention %q", c.err, c.status, remedy, c.wantIn)
		}
	}
}

func TestRemediation404WithoutModel(t *testing.T) {
	cause, remedy := Remediation("openai", "https://x/v1", "", 404, "")
	if cause != "HTTP 404" || !strings.Contains(remedy, "/v1") {
		t.Fatalf("cause=%q remedy=%q", cause, remedy)
	}
}

func TestProbeEndpointOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("probed %q, expected the /models path", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	res := ProbeEndpoint(context.Background(), "ollama", srv.URL, "m", "", 2*time.Second)
	if res.State != ProbeOK {
		t.Fatalf("state=%v cause=%q", res.State, res.Cause)
	}
	if res.CheckedAt.IsZero() {
		t.Fatal("CheckedAt not stamped")
	}
}

func TestProbeEndpointUnauthorizedIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	res := ProbeEndpoint(context.Background(), "openai", srv.URL, "m", "", 2*time.Second)
	if res.State != ProbeDown {
		t.Fatalf("state=%v", res.State)
	}
	if !strings.Contains(res.Remedy, "API key") {
		t.Fatalf("remedy=%q", res.Remedy)
	}
}

func TestProbeEndpoint404IsDegradedNotDown(t *testing.T) {
	// Some OpenAI-compatible servers do not implement /models yet still serve
	// completions — that is degraded, not dead.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	res := ProbeEndpoint(context.Background(), "lmstudio", srv.URL, "m", "", 2*time.Second)
	if res.State != ProbeDegrade {
		t.Fatalf("state=%v", res.State)
	}
}

func TestProbeEndpointRefusedIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	res := ProbeEndpoint(context.Background(), "ollama", url, "m", "", time.Second)
	if res.State != ProbeDown {
		t.Fatalf("state=%v err=%q", res.State, res.Err)
	}
	if res.Cause == "" || res.Remedy == "" {
		t.Fatalf("expected a cause+remedy, got %+v", res)
	}
}

func TestProbeEndpointEmptyEndpoint(t *testing.T) {
	res := ProbeEndpoint(context.Background(), "omlx", "  ", "m", "", time.Second)
	if res.State != ProbeDegrade || !strings.Contains(res.Cause, "no endpoint") {
		t.Fatalf("%+v", res)
	}
}

func TestProbeBlockIsDoctorQuality(t *testing.T) {
	SetColorMode(ColorNever)
	res := ProbeResult{
		State: ProbeDown, Provider: "ollama", Endpoint: "http://127.0.0.1:11434/v1",
		Model: "qwen:7b", Cause: "connection refused", Remedy: "start ollama",
	}
	block := res.Block()
	for _, want := range []string{"cause:", "endpoint:", "provider:", "tip:", "fix:", "slmcode doctor"} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
	// The endpoint must appear in full, not truncated mid-URL.
	if !strings.Contains(block, "http://127.0.0.1:11434/v1") {
		t.Fatalf("endpoint truncated:\n%s", block)
	}
}

func TestProbeDotColorsByState(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	if !strings.Contains(ProbeResult{State: ProbeOK}.Dot(), "32") {
		t.Fatal("ok should be green")
	}
	if !strings.Contains(ProbeResult{State: ProbeDegrade}.Dot(), "33") {
		t.Fatal("degraded should be amber")
	}
	if !strings.Contains(ProbeResult{State: ProbeDown}.Dot(), "31") {
		t.Fatal("down should be red")
	}
}

func TestProbeCacheReusesFreshResults(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewProbeCache(time.Minute)
	for i := 0; i < 3; i++ {
		if res := ProbeCached(context.Background(), c, "p", srv.URL, "m", "", time.Second); res.State != ProbeOK {
			t.Fatalf("state=%v", res.State)
		}
	}
	if hits != 1 {
		t.Fatalf("endpoint hit %d times, want 1 (cache miss)", hits)
	}
}

func TestProbeCacheExpires(t *testing.T) {
	c := NewProbeCache(time.Nanosecond)
	c.Put("k", ProbeResult{State: ProbeOK, CheckedAt: time.Now().Add(-time.Hour)})
	if _, ok := c.Get("k"); ok {
		t.Fatal("stale entry must not be reused")
	}
	if last := c.Last("k"); last.State != ProbeOK {
		t.Fatal("Last should return the stale value for the dot")
	}
}

func TestProbeCacheUnknownKey(t *testing.T) {
	c := NewProbeCache(time.Minute)
	if got := c.Last("nope"); got.State != ProbeUnknown {
		t.Fatalf("state=%v", got.State)
	}
}
