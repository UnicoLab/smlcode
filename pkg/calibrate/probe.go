package calibrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// The HTTP prober speaks plain OpenAI-compatible Chat Completions, exactly as
// pkg/backends does for capability negotiation. It is deliberately NOT built on
// the provider registry: a calibration must be able to run before the
// orchestrator exists, must not inherit retry/timeout policy that would distort
// the very latency it is measuring, and must be able to count tokens from the
// raw `usage` block.

// maxProbeBody bounds how much of a response is read. A calibration only needs
// the `usage` object; a runaway body must not become the slow path.
const maxProbeBody = 64 << 10

// HTTPProber issues calibration calls against one endpoint.
type HTTPProber struct {
	client   *http.Client
	chatURL  string
	modelURL string
	model    string
	apiKey   string
	// prompt is fixed so every call is the same work. temperature 0 keeps the
	// completion length stable across samples.
	prompt    string
	maxTokens int
}

// NewHTTPProber builds a prober for a provider/endpoint/model triple.
//
// callTimeout bounds ONE call. It must be generous enough for a cold 30B on
// the warm-up and tight enough that a hung server cannot outlast the
// calibration budget.
func NewHTTPProber(provider, endpoint, model, apiKey string, callTimeout time.Duration) *HTTPProber {
	if callTimeout <= 0 {
		callTimeout = 30 * time.Second
	}
	base := strings.TrimSpace(endpoint)
	if base == "" {
		base = config.DefaultEndpointFor(provider)
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/chat/completions")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &HTTPProber{
		client:    &http.Client{Timeout: callTimeout},
		chatURL:   base + "/chat/completions",
		modelURL:  base + "/models",
		model:     strings.TrimSpace(model),
		apiKey:    strings.TrimSpace(apiKey),
		prompt:    "Count from 1 to 8, separated by spaces. Nothing else.",
		maxTokens: DefaultMaxTokens,
	}
}

// Complete issues one tiny completion and times it.
func (p *HTTPProber) Complete(ctx context.Context) (Sample, error) {
	body, err := json.Marshal(map[string]any{
		"model":       p.model,
		"messages":    []any{map[string]any{"role": "user", "content": p.prompt}},
		"max_tokens":  p.maxTokens,
		"temperature": 0,
		"stream":      false,
	})
	if err != nil {
		return Sample{}, err
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatURL, bytes.NewReader(body))
	if err != nil {
		return Sample{}, err
	}
	p.auth(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return Sample{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	elapsed := time.Since(start)
	if err != nil {
		return Sample{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Sample{}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, p.chatURL)
	}
	var out struct {
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	// A server that answers 200 with no parseable usage is still a valid
	// latency sample; only throughput is left unmeasured.
	_ = json.Unmarshal(raw, &out)
	return Sample{Elapsed: elapsed, CompletionTokens: out.Usage.CompletionTokens}, nil
}

// Metadata asks GET /v1/models what the server says about this model.
//
// This is why the context window is not probed by binary-searching real
// generations: nearly every OpenAI-compatible server reports it, and a probe
// that spends minutes of GPU time on a number the server will simply hand over
// is not a probe, it is a waste.
func (p *HTTPProber) Metadata(ctx context.Context) (Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelURL, nil)
	if err != nil {
		return Metadata{}, err
	}
	p.auth(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return Metadata{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return Metadata{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Metadata{}, fmt.Errorf("HTTP %d from %s", resp.StatusCode, p.modelURL)
	}
	return parseModelsMetadata(raw, p.model)
}

func (p *HTTPProber) auth(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" && p.apiKey != "local" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// modelEntry covers the context-window spellings the common servers use.
// oMLX and vLLM report max_model_len; LM Studio and llama.cpp lean on
// context_length / max_context_length; some gateways nest it under a
// "context_window". Nothing here invents a value.
type modelEntry struct {
	ID               string `json:"id"`
	MaxModelLen      int    `json:"max_model_len"`
	ContextLength    int    `json:"context_length"`
	MaxContextLength int    `json:"max_context_length"`
	ContextWindow    int    `json:"context_window"`
}

func (m modelEntry) contextLimit() (int, string) {
	switch {
	case m.MaxModelLen > 0:
		return m.MaxModelLen, "max_model_len"
	case m.MaxContextLength > 0:
		return m.MaxContextLength, "max_context_length"
	case m.ContextLength > 0:
		return m.ContextLength, "context_length"
	case m.ContextWindow > 0:
		return m.ContextWindow, "context_window"
	}
	return 0, ""
}

// parseModelsMetadata finds the requested model in a /v1/models listing.
// A model the server does not list yields a zero Metadata and no error: not
// knowing is a normal outcome, and it must not look like a failure.
func parseModelsMetadata(raw []byte, model string) (Metadata, error) {
	var list struct {
		Data []modelEntry `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return Metadata{}, err
	}
	want := strings.ToLower(strings.TrimSpace(model))
	for _, e := range list.Data {
		if !strings.EqualFold(strings.TrimSpace(e.ID), want) {
			continue
		}
		if n, src := e.contextLimit(); n > 0 {
			return Metadata{ContextLimit: n, Source: "GET /v1/models " + src}, nil
		}
		return Metadata{}, nil
	}
	return Metadata{}, nil
}
