package autoconfig

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Finding is what one candidate turned out to be.
type Finding struct {
	Candidate
	// Models is what the server said it serves, in its own order.
	Models []string
	// Latency is how long the listing took.
	Latency time.Duration
	// Err explains why this candidate is not usable, "" when it answered.
	Err string
}

// Live reports whether this endpoint answered with at least one model.
func (f Finding) Live() bool { return f.Err == "" && len(f.Models) > 0 }

// Prober lists the models an endpoint serves.
//
// An interface so discovery is testable without a network and without a model
// server: everything interesting here is which candidates get tried, in what
// order, and what is done with the answers.
type Prober func(ctx context.Context, c Candidate, apiKey string) ([]string, error)

// Result is the outcome of a full discovery pass.
type Result struct {
	// Findings is every candidate tried, in candidate order.
	Findings []Finding
	// Choice is the recommended configuration; Found is false when nothing on
	// this machine can serve the harness.
	Choice Choice
	Found  bool
}

// Choice is a configuration that would work.
type Choice struct {
	Provider string   `json:"provider"`
	Endpoint string   `json:"endpoint"`
	Model    string   `json:"model"`
	Why      string   `json:"why"`
	Others   []string `json:"others,omitempty"`
}

// Discover probes the candidates and picks a configuration.
//
// Candidates are probed CONCURRENTLY. Serially, a laptop with nothing running
// pays the timeout for every well-known address in turn, and a first-run
// experience that appears to hang for half a minute is one people quit.
func Discover(ctx context.Context, cfg *config.Config, envKey func(string) string, probe Prober) Result {
	cands := Candidates(cfg, envKey)
	if len(cands) == 0 || probe == nil {
		return Result{}
	}
	configured := ""
	key := ""
	if cfg != nil {
		configured = cfg.Endpoint
		cp := *cfg
		cp.ResolveAPIKey()
		key = cp.APIKey
	}

	findings := make([]Finding, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c Candidate) {
			defer wg.Done()
			sendKey := ""
			if c.WantsKey(configured) {
				sendKey = key
			}
			start := time.Now()
			names, err := probe(ctx, c, sendKey)
			f := Finding{Candidate: c, Models: names, Latency: time.Since(start)}
			if err != nil {
				f.Err = err.Error()
			} else if len(names) == 0 {
				f.Err = "answered, but serves no models"
			}
			findings[i] = f
		}(i, c)
	}
	wg.Wait()

	out := Result{Findings: findings}
	out.Choice, out.Found = Choose(findings)
	return out
}

// Choose picks the configuration to write from what was found.
//
// Candidate order decides between live endpoints, and candidate order puts the
// user's own configuration first — so a working setup is confirmed rather than
// replaced. Only when the configured endpoint is dead does anything move.
func Choose(findings []Finding) (Choice, bool) {
	for _, f := range findings {
		if !f.Live() {
			continue
		}
		best, ok := Best(f.Models)
		if !ok {
			continue
		}
		c := Choice{
			Provider: f.Provider,
			Endpoint: f.Endpoint,
			Model:    best.Name,
			Why:      best.Why,
		}
		for _, alt := range Rank(f.Models) {
			if alt.Usable && alt.Name != best.Name {
				c.Others = append(c.Others, alt.Name)
			}
		}
		if len(c.Others) > 5 {
			c.Others = c.Others[:5]
		}
		return c, true
	}
	return Choice{}, false
}

// Explain renders what discovery found, for a CLI that has to justify itself.
func (r Result) Explain() string {
	var b strings.Builder
	for _, f := range r.Findings {
		switch {
		case f.Live():
			fmt.Fprintf(&b, "  ✓ %s  %s — %d model(s) in %s (%s)\n",
				f.Provider, f.Endpoint, len(f.Models), roundMS(f.Latency), f.Reason)
		default:
			fmt.Fprintf(&b, "  ✗ %s  %s — %s\n", f.Provider, f.Endpoint, shorten(f.Err))
		}
	}
	return b.String()
}

// NothingFound explains a failed pass in the terms a user can act on.
//
// The three reasons are different problems with different fixes, and collapsing
// them into "no endpoint found" is what sends somebody to restart a server that
// is already running.
func (r Result) NothingFound() string {
	answered, servedModels := 0, 0
	for _, f := range r.Findings {
		if f.Err == "" || strings.Contains(f.Err, "serves no models") {
			answered++
		}
		if len(f.Models) > 0 {
			servedModels++
		}
	}
	switch {
	case answered == 0:
		return "No model server answered. Start one (LM Studio, Ollama, oMLX, vLLM) " +
			"or set a hosted provider's API key, then run this again."
	case servedModels == 0:
		return "A server answered but serves no models. Load a model into it, then run this again."
	default:
		return "A server answered, but none of the models it serves can write code — " +
			"they look like embedding, speech or vision models. Load a coder-tuned " +
			"instruct model (for example a Qwen Coder or Codestral build)."
	}
}

func roundMS(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

func shorten(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 120 {
		return s[:117] + "..."
	}
	if s == "" {
		return "no answer"
	}
	return s
}
