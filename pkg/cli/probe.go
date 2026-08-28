package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Endpoint pre-flight.
//
// Nothing used to check the model server before a run: with the endpoint down
// the pipeline marched through every phase emitting per-agent failures, each
// rendered with a green marker, and the raw dependency error surfaced truncated
// mid-URL. This probes first and refuses to start with a doctor-quality block.

// ProbeState is the connection health used to drive the dashboard dot.
type ProbeState string

const (
	ProbeOK      ProbeState = "ok"      // green
	ProbeDegrade ProbeState = "degrade" // amber — reachable but not healthy
	ProbeDown    ProbeState = "down"    // red
	ProbeUnknown ProbeState = "unknown" // never probed
)

// ProbeResult is one endpoint check.
type ProbeResult struct {
	State     ProbeState
	Endpoint  string
	Provider  string
	Model     string
	Status    int
	Latency   time.Duration
	Err       string
	Cause     string // one-line human cause
	Remedy    string // one-line remediation
	CheckedAt time.Time
}

// Age reports how long ago the probe ran.
func (p ProbeResult) Age() time.Duration {
	if p.CheckedAt.IsZero() {
		return 0
	}
	return time.Since(p.CheckedAt)
}

// Dot renders the connection indicator, colored by state, with the probe age.
func (p ProbeResult) Dot() string {
	switch p.State {
	case ProbeOK:
		return Green("●")
	case ProbeDegrade:
		return Yellow("●")
	case ProbeDown:
		return Red("●")
	default:
		return Dim("○")
	}
}

// StatusLine renders "● provider  model  endpoint  12ms (3s ago)".
func (p ProbeResult) StatusLine() string {
	age := ""
	if !p.CheckedAt.IsZero() {
		age = Dim(fmt.Sprintf(" (%s ago)", roundDur(p.Age())))
	}
	lat := ""
	if p.Latency > 0 {
		lat = Dim(fmt.Sprintf("  %dms", p.Latency.Milliseconds()))
	}
	return p.Dot() + " " + White(p.Provider) + lat + age
}

// Block renders the doctor-quality failure block shown when a run is refused.
func (p ProbeResult) Block() string {
	var b strings.Builder
	b.WriteString(Error("cannot reach the model server — run not started"))
	b.WriteString("\n")
	if p.Cause != "" {
		b.WriteString(Dim("  cause:    ") + p.Cause + "\n")
	}
	if p.Endpoint != "" {
		b.WriteString(Dim("  endpoint: ") + p.Endpoint + "\n")
	}
	if p.Provider != "" {
		b.WriteString(Dim("  provider: ") + p.Provider + "  " + Dim("model: ") + p.Model + "\n")
	}
	if p.Remedy != "" {
		b.WriteString(Dim("  tip:      ") + p.Remedy + "\n")
	}
	b.WriteString(Dim("  fix:      slmcode doctor   ·   slmcode run --endpoint <url> --provider <name>\n"))
	return b.String()
}

func roundDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// Remediation maps a raw transport/HTTP failure onto a one-line fix.
func Remediation(provider, endpoint, model string, status int, err string) (cause, remedy string) {
	le := strings.ToLower(err)
	switch {
	case strings.Contains(le, "connection refused"):
		// `configure` leads because it is the only remedy here that does not
		// require knowing the answer: if a server IS running somewhere else,
		// it finds it, and if none is, it says which of the three problems
		// this actually is.
		return "connection refused", "nothing is listening on " + endpoint +
			" — run `slmcode configure` to find your model server, or start one (`ollama serve`, LM Studio, oMLX)"
	case strings.Contains(le, "no such host"), strings.Contains(le, "dns"):
		return "host not found", "the endpoint hostname does not resolve — check --endpoint / SLMCODE_ENDPOINT for a typo"
	case strings.Contains(le, "timeout"), strings.Contains(le, "deadline exceeded"):
		return "timed out", "the endpoint accepted the connection but did not answer in time — is the model still loading?"
	case strings.Contains(le, "certificate"), strings.Contains(le, "tls"):
		return "TLS handshake failed", "the endpoint's certificate was rejected — use http:// for a local server, or fix the CA bundle"
	}
	switch status {
	case 401, 403:
		return fmt.Sprintf("HTTP %d unauthorized", status), "the provider rejected the API key — set one with `slmcode auth set <key>` or SLMCODE_API_KEY"
	case 404:
		if model != "" {
			return "HTTP 404 — model not found", fmt.Sprintf(
				"%q is not served by this endpoint — run `slmcode configure` to pick one it does serve, "+
					"or set it yourself with `slmcode config set model <id>`", model)
		}
		return "HTTP 404", "the endpoint path is wrong — most OpenAI-compatible servers need the /v1 suffix"
	case 429:
		return "HTTP 429 rate limited", "the provider is throttling — retry shortly or lower `max_parallel`"
	case 500, 502, 503, 504:
		return fmt.Sprintf("HTTP %d from the provider", status), "the model server is up but failing — check its logs"
	}
	if err != "" {
		return Clip(err, 120), "check the endpoint with `slmcode doctor`"
	}
	return "", ""
}

// ProbeCache memoizes probe results so a fast REPL does not hammer the
// endpoint. Results are reused for TTL (default 30s).
type ProbeCache struct {
	mu   sync.Mutex
	ttl  time.Duration
	last map[string]ProbeResult
}

// NewProbeCache builds a cache with the given TTL (30s when <= 0).
func NewProbeCache(ttl time.Duration) *ProbeCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &ProbeCache{ttl: ttl, last: map[string]ProbeResult{}}
}

// Get returns a cached result when it is still fresh.
func (c *ProbeCache) Get(key string) (ProbeResult, bool) {
	if c == nil {
		return ProbeResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.last[key]
	if !ok || time.Since(r.CheckedAt) > c.ttl {
		return r, false
	}
	return r, true
}

// Put stores a result.
func (c *ProbeCache) Put(key string, r ProbeResult) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.last[key] = r
	c.mu.Unlock()
}

// Last returns the most recent result regardless of freshness (for the dot).
func (c *ProbeCache) Last(key string) ProbeResult {
	if c == nil {
		return ProbeResult{State: ProbeUnknown}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.last[key]
	if !ok {
		return ProbeResult{State: ProbeUnknown, Endpoint: key}
	}
	return r
}

// ProbeEndpoint checks that the OpenAI-compatible endpoint is answering.
// It is deliberately cheap: a GET on <endpoint>/models with a short timeout.
func ProbeEndpoint(ctx context.Context, provider, endpoint, model, apiKey string, timeout time.Duration) ProbeResult {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	res := ProbeResult{
		Provider: provider, Endpoint: endpoint, Model: model,
		CheckedAt: time.Now(), State: ProbeUnknown,
	}
	if strings.TrimSpace(endpoint) == "" {
		res.State = ProbeDegrade
		res.Cause = "no endpoint configured"
		res.Remedy = "set one with `slmcode config set endpoint <url>`"
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Scheme-less spellings ("127.0.0.1:1234/v1") are a shape config files
	// carry; net/url refuses to build a request from one.
	url := strings.TrimRight(config.NormalizeEndpoint(endpoint), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.State = ProbeDown
		res.Err = err.Error()
		res.Cause, res.Remedy = Remediation(provider, endpoint, model, 0, err.Error())
		return res
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: timeout}).DialContext,
			TLSHandshakeTimeout: timeout,
		},
	}
	start := time.Now()
	resp, err := client.Do(req)
	res.Latency = time.Since(start)
	if err != nil {
		res.State = ProbeDown
		res.Err = err.Error()
		res.Cause, res.Remedy = Remediation(provider, endpoint, model, 0, err.Error())
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	res.Status = resp.StatusCode
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.State = ProbeOK
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		res.State = ProbeDown
		res.Cause, res.Remedy = Remediation(provider, endpoint, model, resp.StatusCode, "")
	case resp.StatusCode == 404:
		// Some servers do not implement /models but serve completions fine.
		res.State = ProbeDegrade
		res.Cause = "HTTP 404 on /models"
		res.Remedy = "the server answered but does not list models — this is fine for some backends; `slmcode doctor` runs the deeper check"
	default:
		res.State = ProbeDegrade
		res.Cause, res.Remedy = Remediation(provider, endpoint, model, resp.StatusCode, "")
	}
	return res
}

// ProbeCached runs ProbeEndpoint through a cache keyed by endpoint+model.
func ProbeCached(ctx context.Context, c *ProbeCache, provider, endpoint, model, apiKey string, timeout time.Duration) ProbeResult {
	key := endpoint + "|" + model
	if r, ok := c.Get(key); ok {
		return r
	}
	r := ProbeEndpoint(ctx, provider, endpoint, model, apiKey, timeout)
	c.Put(key, r)
	return r
}
