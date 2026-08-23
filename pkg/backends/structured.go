package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Directives describe how one agent role's completions must be shaped. They
// are attached to a provider registration rather than to the request, because
// GoLangGraph's ReAct loop builds llm.CompletionRequest itself and offers no
// hook to add fields — but it does look the provider up by name, and the name
// is chosen by slmcode's own factory.
type Directives struct {
	// Role is the agent id, used for logs and telemetry.
	Role string
	// SchemaRole is the pkg/schema contract this role normally emits. It is a
	// hint: the actual contract is re-detected per request from the prompt,
	// because several agents are re-tasked with a different contract (the
	// planner agent also runs the clarify interview).
	SchemaRole string
	// JSONOnly marks a role whose entire output is one JSON document, making it
	// eligible for constrained decoding.
	JSONOnly bool
	// SerialTools caps the assistant message at one tool call per turn.
	SerialTools bool
	// StopSequences are passed through as `stop`, so the trailing prose tail
	// that pkg/repair currently strips is never generated in the first place.
	StopSequences []string
	// ToolChoice is passed through as `tool_choice` (normally "auto").
	ToolChoice string
}

// backendMeta is what the direct structured path needs to talk to a server on
// its own: registerOpenAICompat / registerOllamaNamed record it per registry key.
type backendMeta struct {
	Provider string
	Endpoint string
	Model    string
	APIKey   string
}

var backendReg = struct {
	mu sync.RWMutex
	m  map[string]backendMeta
}{m: map[string]backendMeta{}}

func rememberBackend(key string, meta backendMeta) {
	backendReg.mu.Lock()
	defer backendReg.mu.Unlock()
	backendReg.m[key] = meta
}

func lookupBackend(key string) (backendMeta, bool) {
	backendReg.mu.RLock()
	defer backendReg.mu.RUnlock()
	m, ok := backendReg.m[key]
	return m, ok
}

// BackendEndpoint returns the endpoint/model recorded for a provider registry
// key. Used by diagnostics and by the agents factory when reporting which
// mechanism a role will use.
func BackendEndpoint(key string) (provider, endpoint, model string, ok bool) {
	m, found := lookupBackend(key)
	return m.Provider, m.Endpoint, m.Model, found
}

// RoleKeySeparator marks a role-bound provider registration.
const RoleKeySeparator = "#role="

// shapes reports whether these directives change the shape of a request. When
// they do not, the role registration exists purely so live token deltas can be
// attributed to a role.
func (d Directives) shapes() bool {
	return d.JSONOnly || d.SerialTools || len(d.StopSequences) > 0 ||
		strings.TrimSpace(d.ToolChoice) != ""
}

// BindRole registers (once) a role-scoped provider that wraps the provider at
// baseKey and applies d to every completion, returning the registry key the
// agent should use. It is a no-op returning baseKey when the manager, the base
// provider, or the role is missing, so callers need no error handling.
//
// The registration is made for EVERY named role, even one with nothing to
// shape. It used to be skipped in that case to avoid a redundant wrapper, but
// the role key is also the only address live token streaming has: the deltas
// of four concurrent workers are told apart by the role encoded here plus the
// task id on the context (see stream.go). A role that shared the base
// registration had no way to attribute its output.
func BindRole(m *llm.ProviderManager, baseKey string, d Directives) string {
	if m == nil || strings.TrimSpace(baseKey) == "" || strings.TrimSpace(d.Role) == "" {
		return baseKey
	}
	key := baseKey + RoleKeySeparator + d.Role
	if _, err := m.GetProvider(key); err == nil {
		return key
	}
	inner, err := m.GetProvider(baseKey)
	if err != nil || inner == nil {
		return baseKey
	}
	meta, _ := lookupBackend(baseKey)
	// Stack order matters: the tee sits UNDER the structured wrapper, so the
	// constrained-decoding direct HTTP call (deliberately non-streaming) is
	// still reached and simply produces no deltas, rather than being bypassed.
	p := newStreamTee(inner, d.Role)
	if d.shapes() {
		p = &structuredProvider{
			inner:      p,
			directives: d,
			meta:       meta,
			policy:     DefaultRetryPolicy(),
			client:     &http.Client{},
		}
	}
	if err := m.RegisterProvider(key, p); err != nil {
		// A concurrent Create won the race — reuse whatever landed.
		if _, gerr := m.GetProvider(key); gerr == nil {
			return key
		}
		return baseKey
	}
	// Diagnostics resolve endpoint/model by registry key; without this a
	// role-bound key reported "unknown backend".
	rememberBackend(key, meta)
	return key
}

// structuredProvider shapes requests for one agent role.
type structuredProvider struct {
	inner      llm.Provider
	directives Directives
	meta       backendMeta
	policy     RetryPolicy
	client     *http.Client
}

// ---------------------------------------------------------------------------
// Request shaping
// ---------------------------------------------------------------------------

// shape injects the fields GoLangGraph's agent never sets. StopSequences and
// ToolChoice are honored by the underlying OpenAI provider, so these reach the
// wire even on the delegated path.
func (p *structuredProvider) shape(req llm.CompletionRequest) llm.CompletionRequest {
	if len(p.directives.StopSequences) > 0 && len(req.StopSequences) == 0 {
		req.StopSequences = append([]string(nil), p.directives.StopSequences...)
	}
	if req.ToolChoice == nil && strings.TrimSpace(p.directives.ToolChoice) != "" && len(req.Tools) > 0 {
		req.ToolChoice = p.directives.ToolChoice
	}
	return req
}

// promptText concatenates the system prompt and the first user message — the
// two places an output contract is ever stated — for schema detection.
func promptText(req llm.CompletionRequest) string {
	var b strings.Builder
	b.WriteString(req.SystemPrompt)
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			b.WriteString("\n")
			b.WriteString(m.Content)
		case "user":
			b.WriteString("\n")
			b.WriteString(m.Content)
		}
	}
	return b.String()
}

// truncateToolCalls enforces one tool call per turn. GoLangGraph's ReAct loop
// executes every ToolCall in an assistant message with nothing capping it, so a
// 7B emitting three malformed ws_edit calls has all three executed.
func (p *structuredProvider) truncateToolCalls(resp *llm.CompletionResponse) {
	if resp == nil || !p.directives.SerialTools {
		return
	}
	for i := range resp.Choices {
		if len(resp.Choices[i].Message.ToolCalls) > 1 {
			recordDropped(p.directives.Role, len(resp.Choices[i].Message.ToolCalls)-1)
			resp.Choices[i].Message.ToolCalls = resp.Choices[i].Message.ToolCalls[:1]
		}
		if len(resp.Choices[i].Delta.ToolCalls) > 1 {
			resp.Choices[i].Delta.ToolCalls = resp.Choices[i].Delta.ToolCalls[:1]
		}
	}
}

// ---------------------------------------------------------------------------
// llm.Provider
// ---------------------------------------------------------------------------

func (p *structuredProvider) GetName() string { return p.inner.GetName() }

func (p *structuredProvider) GetModels(ctx context.Context) ([]string, error) {
	return p.inner.GetModels(ctx)
}

func (p *structuredProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return p.complete(ctx, req, func(ctx context.Context, r llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return p.inner.Complete(ctx, r)
	})
}

func (p *structuredProvider) CompleteWithMode(ctx context.Context, req llm.CompletionRequest, mode llm.StreamMode) (*llm.CompletionResponse, error) {
	return p.complete(ctx, req, func(ctx context.Context, r llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return p.inner.CompleteWithMode(ctx, r, mode)
	})
}

func (p *structuredProvider) complete(
	ctx context.Context,
	req llm.CompletionRequest,
	delegate func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error),
) (*llm.CompletionResponse, error) {
	req = p.shape(req)
	if spec, mech, ok := p.plan(ctx, req); ok {
		resp, err := p.structuredCall(ctx, req, spec, mech)
		if err == nil {
			p.truncateToolCalls(resp)
			return resp, nil
		}
		// Only retry through the ordinary path when the failure was about the
		// REQUEST — a rejected field or an unrecognized response. A transient
		// failure has already exhausted its retries, a cancellation is the
		// caller's, and a context overflow will not fit on the second try
		// either; replaying any of those would double the attempts against a
		// local server that serializes inference.
		switch Classify(err).Class {
		case ClassTransient, ClassRateLimited, ClassCanceled, ClassContextOverflow:
			return nil, err
		}
		// Constrained decoding must never be the reason a run fails: fall back
		// to the ordinary path and let pkg/repair handle the output.
		recordMechanism(p.directives.Role, MechPromptOnly)
	}
	resp, err := delegate(ctx, req)
	if err != nil {
		return nil, err
	}
	p.truncateToolCalls(resp)
	return resp, nil
}

// plan decides whether this request should go through constrained decoding and
// with which mechanism.
func (p *structuredProvider) plan(ctx context.Context, req llm.CompletionRequest) (schema.Spec, string, bool) {
	if !p.directives.JSONOnly || len(req.Tools) > 0 {
		return schema.Spec{}, "", false
	}
	if strings.TrimSpace(p.meta.Endpoint) == "" && strings.TrimSpace(p.meta.Provider) == "" {
		return schema.Spec{}, "", false
	}
	spec, ok := schema.DetectRole(promptText(req), p.directives.SchemaRole)
	if !ok {
		return schema.Spec{}, "", false
	}
	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.meta.Model
	}
	c := Probe(ctx, p.meta.Provider, p.meta.Endpoint, model, p.meta.APIKey)
	mech := c.SelectMechanism(spec, nil)
	if mech == MechPromptOnly {
		return schema.Spec{}, "", false
	}
	return spec, mech, true
}

func (p *structuredProvider) CompleteStream(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback) error {
	return p.inner.CompleteStream(ctx, p.shape(req), cb)
}

func (p *structuredProvider) CompleteStreamWithMode(ctx context.Context, req llm.CompletionRequest, cb llm.StreamCallback, mode llm.StreamMode) error {
	return p.inner.CompleteStreamWithMode(ctx, p.shape(req), cb, mode)
}

func (p *structuredProvider) IsHealthy(ctx context.Context) error { return p.inner.IsHealthy(ctx) }
func (p *structuredProvider) GetConfig() map[string]interface{} {
	c := p.inner.GetConfig()
	if c == nil {
		c = map[string]interface{}{}
	}
	c["slmcode_role"] = p.directives.Role
	c["slmcode_schema_role"] = p.directives.SchemaRole
	c["slmcode_json_only"] = p.directives.JSONOnly
	c["slmcode_serial_tools"] = p.directives.SerialTools
	return c
}
func (p *structuredProvider) SetConfig(c map[string]interface{}) error { return p.inner.SetConfig(c) }
func (p *structuredProvider) SupportsStreaming() bool                  { return p.inner.SupportsStreaming() }
func (p *structuredProvider) GetStreamingConfig() *llm.StreamingConfig {
	return p.inner.GetStreamingConfig()
}
func (p *structuredProvider) SetStreamingConfig(c *llm.StreamingConfig) error {
	return p.inner.SetStreamingConfig(c)
}
func (p *structuredProvider) Close() error { return p.inner.Close() }

// ---------------------------------------------------------------------------
// Direct structured HTTP
// ---------------------------------------------------------------------------

// structuredCall issues the completion itself so the constrained-decoding
// fields — which llm.CompletionRequest has no room for — reach the wire.
// It walks the mechanism ladder downwards whenever the server rejects a field,
// and permanently records the rejection so the next call skips that rung.
func (p *structuredProvider) structuredCall(ctx context.Context, req llm.CompletionRequest, spec schema.Spec, mech string) (*llm.CompletionResponse, error) {
	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.meta.Model
	}
	url := chatCompletionsURL(p.meta.Provider, p.meta.Endpoint)
	if url == "" {
		return nil, fmt.Errorf("structured: no endpoint for provider %q", p.meta.Provider)
	}
	excluded := map[string]bool{}
	var lastErr error
	for mech != MechPromptOnly {
		body := p.buildBody(req, spec, mech, model)
		resp, err := retryDo(ctx, p.policy, func(ctx context.Context, _ int) (*llm.CompletionResponse, error) {
			cctx, cancel := context.WithTimeout(ctx, EstimateTimeout(model, req.MaxTokens))
			defer cancel()
			start := time.Now()
			r, err := p.post(cctx, url, body)
			if r != nil {
				observeAndPersist(model, r.Usage.CompletionTokens, time.Since(start))
			}
			return r, err
		})
		if err == nil {
			recordMechanism(p.directives.Role, mech)
			return resp, nil
		}
		lastErr = err
		c := Classify(err)
		// A permanent rejection of a request that only differs from a plain one
		// by the constrained-decoding field means this server does not support
		// that field. Demote it for good and try the next rung.
		if c.Class != ClassPermanent {
			return nil, err
		}
		demoteCapability(p.meta.Provider, p.meta.Endpoint, model, mech)
		excluded[mech] = true
		cp := Probe(ctx, p.meta.Provider, p.meta.Endpoint, model, p.meta.APIKey)
		mech = cp.SelectMechanism(spec, excluded)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("structured: no mechanism available")
	}
	return nil, lastErr
}

// buildBody renders the OpenAI-compatible chat completions payload plus the
// mechanism-specific field.
func (p *structuredProvider) buildBody(req llm.CompletionRequest, spec schema.Spec, mech, model string) map[string]any {
	msgs := make([]any, 0, len(req.Messages)+1)
	if s := strings.TrimSpace(req.SystemPrompt); s != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}
	for _, m := range req.Messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.Name != "" {
			msg["name"] = m.Name
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		msgs = append(msgs, msg)
	}
	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   false,
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	switch mech {
	case MechJSONSchema:
		doc := spec.Schema
		if spec.Strict {
			doc = schema.StrictSchema(spec)
		}
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "slmcode_" + spec.Name,
				"schema": doc,
				"strict": spec.Strict,
			},
		}
	case MechGuidedJSON:
		body["guided_json"] = spec.Schema
	case MechGrammar:
		body["grammar"] = schema.GBNF(spec)
	case MechJSONObject:
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	return body
}

func (p *structuredProvider) post(ctx context.Context, url string, body map[string]any) (*llm.CompletionResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(p.meta.APIKey); k != "" && k != "local" {
		httpReq.Header.Set("Authorization", "Bearer "+k)
	}
	client := p.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{
			Status:     resp.StatusCode,
			Body:       string(raw),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			URL:        url,
		}
	}
	return decodeChatCompletion(raw)
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v + "s"); err == nil && d > 0 {
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// wireCompletion mirrors the OpenAI chat completions response shape.
type wireCompletion struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Index    int    `json:"index"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	SystemFingerprint string `json:"system_fingerprint"`
	Error             *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func decodeChatCompletion(raw []byte) (*llm.CompletionResponse, error) {
	var w wireCompletion
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("structured: decode response: %w", err)
	}
	if w.Error != nil && strings.TrimSpace(w.Error.Message) != "" {
		// Some OpenAI-compatible servers answer 200 with an error envelope.
		return nil, &HTTPError{Status: 400, Body: w.Error.Message}
	}
	out := &llm.CompletionResponse{
		ID:                w.ID,
		Object:            w.Object,
		Created:           w.Created,
		Model:             w.Model,
		SystemFingerprint: w.SystemFingerprint,
		Usage: llm.Usage{
			PromptTokens:     w.Usage.PromptTokens,
			CompletionTokens: w.Usage.CompletionTokens,
			TotalTokens:      w.Usage.TotalTokens,
		},
	}
	for _, c := range w.Choices {
		msg := llm.Message{Role: c.Message.Role, Content: c.Message.Content}
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		for _, tc := range c.Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:    tc.ID,
				Type:  tc.Type,
				Index: tc.Index,
				Function: llm.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out.Choices = append(out.Choices, llm.Choice{
			Index:        c.Index,
			Message:      msg,
			FinishReason: c.FinishReason,
		})
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("structured: response carried no choices")
	}
	return out, nil
}

// demoteCapability records that a server rejected one mechanism.
func demoteCapability(provider, endpoint, model, mech string) {
	key := CapabilityKey(provider, endpoint, model)
	c, ok := caps.get(key)
	if !ok {
		// No record to demote. Writing a wholesale-false one here would disable
		// structured decoding for the whole TTL on the strength of a single
		// rejection, so leave it for the next Probe to determine properly.
		return
	}
	switch mech {
	case MechJSONSchema:
		c.JSONSchema = false
	case MechGuidedJSON:
		c.GuidedJSON = false
	case MechGrammar:
		c.GBNFGrammar = false
	case MechJSONObject:
		c.JSONObject = false
	}
	c.Probed = time.Now()
	c.Source = "demoted"
	caps.put(key, c)
}

// ---------------------------------------------------------------------------
// Telemetry
// ---------------------------------------------------------------------------

var mechStats = struct {
	mu      sync.Mutex
	counts  map[string]int
	byRole  map[string]string
	dropped map[string]int
}{counts: map[string]int{}, byRole: map[string]string{}, dropped: map[string]int{}}

func recordMechanism(role, mech string) {
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	mechStats.counts[mech]++
	if role != "" {
		mechStats.byRole[role] = mech
	}
}

func recordDropped(role string, n int) {
	if n <= 0 {
		return
	}
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	mechStats.dropped[role] += n
}

// MechanismStats returns how many completions each decoding mechanism served.
func MechanismStats() map[string]int {
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	out := make(map[string]int, len(mechStats.counts))
	for k, v := range mechStats.counts {
		out[k] = v
	}
	return out
}

// RoleMechanisms returns the mechanism most recently used for each role.
func RoleMechanisms() map[string]string {
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	out := make(map[string]string, len(mechStats.byRole))
	for k, v := range mechStats.byRole {
		out[k] = v
	}
	return out
}

// DroppedToolCalls returns how many extra tool calls SerialTools truncated,
// per role. A high count means the prompt's one-call-per-turn rule is not
// landing for that model.
func DroppedToolCalls() map[string]int {
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	out := make(map[string]int, len(mechStats.dropped))
	for k, v := range mechStats.dropped {
		out[k] = v
	}
	return out
}

// ResetTelemetry clears counters (tests).
func ResetTelemetry() {
	mechStats.mu.Lock()
	defer mechStats.mu.Unlock()
	mechStats.counts = map[string]int{}
	mechStats.byRole = map[string]string{}
	mechStats.dropped = map[string]int{}
}

// TelemetryReport renders a stable, sorted summary for logs.
func TelemetryReport() []string {
	stats := MechanismStats()
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%d", k, stats[k]))
	}
	return out
}
