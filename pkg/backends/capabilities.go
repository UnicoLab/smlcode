package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/schema"
)

// Capabilities records which constrained-decoding mechanisms one
// provider+endpoint+model actually accepts.
//
// Zero value is the weakest possible backend: prompt-only JSON with post-hoc
// repair. Anything we could not confirm stays false — never assume support.
type Capabilities struct {
	// JSONObject: response_format {"type":"json_object"} (OpenAI JSON mode).
	JSONObject bool `json:"json_object"`
	// JSONSchema: response_format {"type":"json_schema","json_schema":{…,"strict":true}}.
	JSONSchema bool `json:"json_schema"`
	// GuidedJSON: vLLM `guided_json` / `guided_grammar` extra body fields.
	GuidedJSON bool `json:"guided_json"`
	// GBNFGrammar: llama.cpp server `grammar` body field.
	GBNFGrammar bool `json:"gbnf_grammar"`
	// NativeTools: `tools` + `tool_choice`.
	NativeTools bool `json:"native_tools"`
	// Streaming: SSE `stream: true`.
	Streaming bool `json:"streaming"`
	// Probed is when this record was written. Zero means "never confirmed".
	Probed time.Time `json:"probed"`
	// Source is "probe", "cache", "prior" or "unreachable" — for diagnostics.
	Source string `json:"source,omitempty"`
}

// Mechanism names returned by SelectMechanism, in strength order.
const (
	MechJSONSchema = "json_schema"
	MechGuidedJSON = "guided_json"
	MechGrammar    = "gbnf_grammar"
	MechJSONObject = "json_object"
	MechPromptOnly = "prompt_only"
)

// Any reports whether any structured mechanism is available.
func (c Capabilities) Any() bool {
	return c.JSONSchema || c.GuidedJSON || c.GBNFGrammar || c.JSONObject
}

// SelectMechanism picks the strongest mechanism these capabilities support for
// a given spec, honoring an exclusion set of mechanisms already rejected by
// the server during this call.
func (c Capabilities) SelectMechanism(spec schema.Spec, exclude map[string]bool) string {
	try := func(name string, ok bool) string {
		if ok && !exclude[name] {
			return name
		}
		return ""
	}
	if m := try(MechJSONSchema, c.JSONSchema); m != "" {
		return m
	}
	if m := try(MechGuidedJSON, c.GuidedJSON); m != "" {
		return m
	}
	if m := try(MechGrammar, c.GBNFGrammar); m != "" {
		return m
	}
	if m := try(MechJSONObject, c.JSONObject); m != "" {
		return m
	}
	return MechPromptOnly
}

// String renders a compact one-line summary for logs.
func (c Capabilities) String() string {
	var on []string
	for _, p := range []struct {
		n string
		v bool
	}{
		{"json_schema", c.JSONSchema}, {"guided_json", c.GuidedJSON},
		{"grammar", c.GBNFGrammar}, {"json_object", c.JSONObject},
		{"tools", c.NativeTools}, {"stream", c.Streaming},
	} {
		if p.v {
			on = append(on, p.n)
		}
	}
	if len(on) == 0 {
		return "none (prompt-only)"
	}
	return strings.Join(on, "+")
}

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

// CapabilityTTL is how long a probed record stays fresh.
const CapabilityTTL = 7 * 24 * time.Hour

type capCache struct {
	mu     sync.Mutex
	mem    map[string]Capabilities
	dir    string
	loaded bool
	single map[string]*sync.Once
}

var caps = &capCache{mem: map[string]Capabilities{}, single: map[string]*sync.Once{}}

// CapabilityKey is the cache key for one backend.
func CapabilityKey(provider, endpoint, model string) string {
	return strings.Join([]string{
		config.NormalizeProvider(provider),
		canonicalEndpoint(provider, endpoint),
		strings.TrimSpace(model),
	}, "|")
}

// SetCapabilityCacheDir points the on-disk capability cache at dir (normally
// `.slmcode`). Passing "" disables disk persistence. RegisterLLM calls this
// automatically from Config.Root, so no caller wiring is required.
func SetCapabilityCacheDir(dir string) {
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.dir == dir {
		return
	}
	caps.dir = dir
	caps.loaded = false
}

func (c *capCache) path() string {
	if c.dir == "" {
		return ""
	}
	return filepath.Join(c.dir, "capabilities.json")
}

// loadLocked merges the on-disk cache into memory once per cache dir.
func (c *capCache) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	p := c.path()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p) // #nosec G304 -- path derived from project root
	if err != nil {
		return
	}
	var disk map[string]Capabilities
	if err := json.Unmarshal(b, &disk); err != nil {
		return
	}
	for k, v := range disk {
		if _, ok := c.mem[k]; ok {
			continue
		}
		if time.Since(v.Probed) > CapabilityTTL {
			continue
		}
		v.Source = "cache"
		c.mem[k] = v
	}
}

func (c *capCache) get(key string) (Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	v, ok := c.mem[key]
	if !ok {
		return Capabilities{}, false
	}
	if !v.Probed.IsZero() && time.Since(v.Probed) > CapabilityTTL {
		delete(c.mem, key)
		return Capabilities{}, false
	}
	return v, true
}

func (c *capCache) put(key string, v Capabilities) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	c.mem[key] = v
	p := c.path()
	if p == "" || v.Probed.IsZero() {
		return
	}
	snapshot := make(map[string]Capabilities, len(c.mem))
	for k, e := range c.mem {
		if e.Probed.IsZero() {
			continue
		}
		snapshot[k] = e
	}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o600)
}

// once returns a per-key sync.Once so N parallel workers hitting a cold
// endpoint issue exactly one probe between them.
func (c *capCache) once(key string) *sync.Once {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.single[key]
	if !ok {
		o = &sync.Once{}
		c.single[key] = o
	}
	return o
}

// CachedCapabilities returns a previously probed record without issuing HTTP.
func CachedCapabilities(provider, endpoint, model string) (Capabilities, bool) {
	return caps.get(CapabilityKey(provider, endpoint, model))
}

// SetCapabilities seeds the cache directly. Used by tests and by callers that
// know their backend (e.g. an air-gapped deployment pinning llama.cpp).
func SetCapabilities(provider, endpoint, model string, c Capabilities) {
	if c.Probed.IsZero() {
		c.Probed = time.Now()
	}
	if c.Source == "" {
		c.Source = "manual"
	}
	caps.put(CapabilityKey(provider, endpoint, model), c)
}

// ResetCapabilityCache clears in-memory state (tests).
func ResetCapabilityCache() {
	caps.mu.Lock()
	defer caps.mu.Unlock()
	caps.mem = map[string]Capabilities{}
	caps.single = map[string]*sync.Once{}
	caps.loaded = false
	caps.dir = ""
}

// ---------------------------------------------------------------------------
// Priors
// ---------------------------------------------------------------------------

// PresetCapabilities is the documented prior for a provider preset. It decides
// which probes are worth issuing (never issue a guided_json probe at OpenAI)
// and what to fall back to when a probe is inconclusive. A prior is a hint —
// only a successful probe sets Probed and is trusted for decoding.
func PresetCapabilities(provider string) Capabilities {
	switch config.NormalizeProvider(provider) {
	case "openai", "azure":
		return Capabilities{JSONObject: true, JSONSchema: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "vllm":
		return Capabilities{JSONObject: true, JSONSchema: true, GuidedJSON: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "llamacpp", "llama-cpp", "llama_cpp":
		return Capabilities{JSONObject: true, JSONSchema: true, GBNFGrammar: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "ollama":
		return Capabilities{JSONObject: true, JSONSchema: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "lmstudio":
		return Capabilities{JSONObject: true, JSONSchema: true, GBNFGrammar: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "omlx", "mlx":
		return Capabilities{JSONObject: true, JSONSchema: true, NativeTools: true, Streaming: true, Source: "prior"}
	case "groq", "deepseek", "mistral", "together", "openrouter", "qwen", "google", "litellm":
		return Capabilities{JSONObject: true, NativeTools: true, Streaming: true, Source: "prior"}
	default:
		return Capabilities{JSONObject: true, NativeTools: true, Streaming: true, Source: "prior"}
	}
}

// ---------------------------------------------------------------------------
// Probe
// ---------------------------------------------------------------------------

// ProbeTimeout bounds the whole negotiation. A cold local model can take a
// while to load; a probe is never allowed to become the slow path.
var ProbeTimeout = 20 * time.Second

// probeSpec is the trivial contract used for negotiation: one required boolean.
var probeSchema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
	"required":             []any{"ok"},
	"additionalProperties": false,
}

// Probe determines what a provider+endpoint+model actually supports and caches
// the answer (memory, plus `<cacheDir>/capabilities.json` when set).
//
// It is safe to call on every request: the result is memoised per key and
// concurrent callers collapse onto one probe. It never returns an error and
// never blocks longer than ProbeTimeout — an unreachable or hostile endpoint
// yields the zero value, which routes everything through prompt-only + repair.
func Probe(ctx context.Context, provider, endpoint, model, apiKey string) Capabilities {
	key := CapabilityKey(provider, endpoint, model)
	if c, ok := caps.get(key); ok {
		return c
	}
	caps.once(key).Do(func() {
		c := runProbe(ctx, provider, endpoint, model, apiKey)
		caps.put(key, c)
	})
	if c, ok := caps.get(key); ok {
		return c
	}
	return Capabilities{}
}

func runProbe(ctx context.Context, provider, endpoint, model, apiKey string) Capabilities {
	prior := PresetCapabilities(provider)
	url := chatCompletionsURL(provider, endpoint)
	if url == "" {
		return Capabilities{Source: "unreachable"}
	}
	// Detach from the caller's cancellation so one canceled request does not
	// leave the cache empty for everyone else, but never outlive the caller's
	// deadline: the first structured call of a run waits on this.
	budget := ProbeTimeout
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < budget {
			budget = remaining
		}
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()

	client := &http.Client{Timeout: ProbeTimeout}
	base := map[string]any{
		"model":      model,
		"messages":   []any{map[string]any{"role": "user", "content": "ok"}},
		"max_tokens": 1,
		"stream":     false,
	}
	reachable := false
	// cutShort records that the SHARED negotiation deadline expired part way
	// through. Without it a timeout is indistinguishable from "the server
	// rejected this field" — see the note above the return.
	cutShort := false
	attempt := func(extra map[string]any) bool {
		body := make(map[string]any, len(base)+len(extra))
		for k, v := range base {
			body[k] = v
		}
		for k, v := range extra {
			body[k] = v
		}
		status, err := probeOnce(pctx, client, url, apiKey, body)
		if err != nil {
			if pctx.Err() != nil {
				cutShort = true
			}
			return false
		}
		reachable = true
		return status >= 200 && status < 300
	}

	out := Capabilities{}
	// Plain call first: proves the endpoint answers at all, and gives us a
	// baseline for "does this server 400 on anything unusual".
	if !attempt(nil) && !reachable {
		return Capabilities{Source: "unreachable"}
	}
	out.Streaming = prior.Streaming

	if prior.JSONSchema {
		out.JSONSchema = attempt(map[string]any{"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "slmcode_probe",
				"schema": probeSchema,
				"strict": true,
			},
		}})
	}
	if prior.GuidedJSON {
		out.GuidedJSON = attempt(map[string]any{"guided_json": probeSchema})
	}
	if prior.GBNFGrammar {
		out.GBNFGrammar = attempt(map[string]any{"grammar": `root ::= "{" "}"`})
	}
	// json_object is the universal floor — always worth confirming, and cheap.
	out.JSONObject = attempt(map[string]any{
		"response_format": map[string]any{"type": "json_object"},
		"messages": []any{map[string]any{
			"role": "user", "content": "reply with the JSON object {\"ok\":true}",
		}},
	})
	if prior.NativeTools {
		out.NativeTools = attempt(map[string]any{
			"tools": []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "noop",
					"description": "probe",
					"parameters":  probeSchema,
				},
			}},
			"tool_choice": "auto",
		})
	}
	if !reachable {
		return Capabilities{Source: "unreachable"}
	}
	if cutShort {
		// ONE deadline covers up to six sequential requests, and a cold local
		// model — the exact case this budget was widened for — can spend most of
		// it loading weights for the first. When it expires part way through,
		// every field not yet asked about is false because it was NEVER ASKED,
		// not because the server refused it. Stamping that as `Source: "probe"`
		// used to publish an all-false record and, because Probed was set, cache
		// it for CapabilityTTL — seven days in which every structured role on
		// this endpoint silently degraded to prompt-only + repair, with no path
		// back: nothing re-probes a record that is still fresh.
		//
		// So fall back to the family preset and leave Probed ZERO, which is what
		// keeps this out of the on-disk cache (capCache.put skips zero-Probed
		// records) so the next process negotiates again from scratch.
		//
		// Preferring the preset over all-false is deliberate, and the asymmetry
		// is the argument: an over-claimed mechanism costs ONE 400 on first use
		// and is then recorded by demoteCapability, while an under-claimed one
		// costs a week of degraded decoding that nothing can detect. Guessing in
		// the recoverable direction is the whole point.
		prior.Source = "prior-probe-timeout"
		return prior
	}
	out.Probed = time.Now()
	out.Source = "probe"
	return out
}

// probeOnce issues one probe request and returns the HTTP status. A transport
// error (server down, DNS, TLS) returns err so the caller can tell "rejected
// the field" apart from "cannot reach the server".
func probeOnce(ctx context.Context, client *http.Client, url, apiKey string, body map[string]any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(apiKey); k != "" && k != "local" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a bounded amount so the connection can be reused.
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	return resp.StatusCode, nil
}

// chatCompletionsURL derives the OpenAI-compatible chat completions URL. Even
// for the native-Ollama provider we negotiate and constrain over Ollama's
// OpenAI-compatible `/v1` surface, so one code path covers every backend.
func chatCompletionsURL(provider, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		ep = config.DefaultEndpointFor(config.NormalizeProvider(provider))
	}
	if ep == "" {
		return ""
	}
	ep = strings.TrimRight(ep, "/")
	if strings.HasSuffix(ep, "/chat/completions") {
		return ep
	}
	if !strings.HasSuffix(ep, "/v1") {
		ep += "/v1"
	}
	return ep + "/chat/completions"
}

// CapabilityReport renders every cached record, newest first (diagnostics).
func CapabilityReport() []string {
	caps.mu.Lock()
	defer caps.mu.Unlock()
	caps.loadLocked()
	keys := make([]string, 0, len(caps.mem))
	for k := range caps.mem {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		c := caps.mem[k]
		out = append(out, fmt.Sprintf("%s → %s (%s)", k, c.String(), c.Source))
	}
	return out
}
