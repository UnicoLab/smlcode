package backends

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// reviewPrompt states the review contract exactly as PromptReviewer does, so
// schema detection has something to lock onto.
const reviewPrompt = `Review ONE task. No tools.
STRICT JSON:
{"approved":true|false,"score":0-100,"issues":[],"summary":"…"}`

func newManagerFor(t *testing.T, provider, endpoint string) (*llm.ProviderManager, *config.Config) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Provider = provider
	cfg.Model = "fake-model"
	cfg.Endpoint = endpoint
	cfg.APIKey = "local"
	m := llm.NewProviderManager()
	if err := RegisterLLM(m, cfg); err != nil {
		t.Fatal(err)
	}
	return m, cfg
}

func reviewRequest() llm.CompletionRequest {
	return llm.CompletionRequest{
		Model:     "fake-model",
		MaxTokens: 256,
		Messages: []llm.Message{
			{Role: "system", Content: reviewPrompt},
			{Role: "user", Content: "Task T1: add a function."},
		},
	}
}

// lastBodyWith returns the most recent request carrying key.
func lastBodyWith(reqs []map[string]any, key string) (map[string]any, bool) {
	for i := len(reqs) - 1; i >= 0; i-- {
		if _, ok := reqs[i][key]; ok {
			return reqs[i], true
		}
	}
	return nil, false
}

func TestStructuredDecodingMechanismPerBackend(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		supports []string
		wantMech string
		// assert inspects the final (non-probe) request body.
		assert func(t *testing.T, body map[string]any)
	}{
		{
			name: "json_schema strict", provider: "openai",
			supports: []string{"json_schema", "json_object"},
			wantMech: MechJSONSchema,
			assert: func(t *testing.T, body map[string]any) {
				rf, _ := body["response_format"].(map[string]any)
				if rf["type"] != "json_schema" {
					t.Fatalf("response_format = %v", rf)
				}
				js, _ := rf["json_schema"].(map[string]any)
				if js["strict"] != true {
					t.Errorf("strict not set: %v", js["strict"])
				}
				if name, _ := js["name"].(string); name != "slmcode_review" {
					t.Errorf("schema name = %q", name)
				}
				doc, _ := js["schema"].(map[string]any)
				if doc["additionalProperties"] != false {
					t.Error("strict schema must forbid additional properties")
				}
				req, _ := doc["required"].([]any)
				if len(req) != 4 {
					t.Errorf("strict required = %v, want all four keys", req)
				}
			},
		},
		{
			name: "vllm guided_json", provider: "vllm",
			supports: []string{"guided_json", "json_object"},
			wantMech: MechGuidedJSON,
			assert: func(t *testing.T, body map[string]any) {
				g, ok := body["guided_json"].(map[string]any)
				if !ok {
					t.Fatalf("guided_json missing: %v", body)
				}
				if g["type"] != "object" {
					t.Errorf("guided_json is not the schema: %v", g)
				}
			},
		},
		{
			name: "llama.cpp grammar", provider: "llamacpp",
			supports: []string{"grammar", "json_object"},
			wantMech: MechGrammar,
			assert: func(t *testing.T, body map[string]any) {
				g, _ := body["grammar"].(string)
				if !strings.Contains(g, "root ::=") {
					t.Fatalf("grammar not a GBNF document: %q", g)
				}
				if !strings.Contains(g, `"approved"`) {
					t.Errorf("grammar does not pin the approved key: %q", g)
				}
			},
		},
		{
			name: "json mode floor", provider: "deepseek",
			supports: []string{"json_object"},
			wantMech: MechJSONObject,
			assert: func(t *testing.T, body map[string]any) {
				rf, _ := body["response_format"].(map[string]any)
				if rf["type"] != "json_object" {
					t.Fatalf("response_format = %v", rf)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ResetCapabilityCache()
			ResetTelemetry()
			srv := newFakeServer(t, tc.supports...)
			m, _ := newManagerFor(t, tc.provider, srv.endpoint())

			key := BindRole(m, config.NormalizeProvider(tc.provider), Directives{
				Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true,
				StopSequences: []string{"\n## "},
			})
			if key == config.NormalizeProvider(tc.provider) {
				t.Fatal("BindRole did not create a role-scoped provider")
			}
			srv.reset()
			resp, err := m.Complete(context.Background(), key, reviewRequest())
			if err != nil {
				t.Fatal(err)
			}
			if resp.Choices[0].Message.Content == "" {
				t.Fatal("empty completion")
			}
			if got := RoleMechanisms()["reviewer"]; got != tc.wantMech {
				t.Fatalf("mechanism = %q, want %q", got, tc.wantMech)
			}
			reqs := srv.seen()
			body := reqs[len(reqs)-1]
			tc.assert(t, body)
			// StopSequences must reach the wire on the structured path too.
			stop, _ := body["stop"].([]any)
			if len(stop) != 1 || stop[0] != "\n## " {
				t.Errorf("stop = %v, want the configured stop sequence", body["stop"])
			}
		})
	}
}

func TestStructuredDegradesSilentlyWhenServerRejects(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	// Prior says json_schema, but this server actually only does json_object.
	// Seed capabilities so the probe does not correct it — this simulates a
	// server whose behaviour changed after the cache was written.
	srv := newFakeServer(t, "json_object")
	SetCapabilities("openai", srv.endpoint(), "fake-model", Capabilities{
		JSONSchema: true, JSONObject: true, NativeTools: true, Streaming: true,
	})
	m, _ := newManagerFor(t, "openai", srv.endpoint())
	key := BindRole(m, "openai", Directives{
		Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true,
	})
	srv.reset()
	resp, err := m.Complete(context.Background(), key, reviewRequest())
	if err != nil {
		t.Fatalf("degradation must be silent, got error: %v", err)
	}
	if resp.Choices[0].Message.Content == "" {
		t.Fatal("empty completion")
	}
	if got := RoleMechanisms()["reviewer"]; got != MechJSONObject {
		t.Errorf("mechanism = %q, want json_object after demotion", got)
	}
	// The rejection must be remembered, so the next call skips json_schema.
	c, _ := CachedCapabilities("openai", srv.endpoint(), "fake-model")
	if c.JSONSchema {
		t.Error("rejected mechanism was not demoted in the cache")
	}
	srv.reset()
	if _, err := m.Complete(context.Background(), key, reviewRequest()); err != nil {
		t.Fatal(err)
	}
	for _, b := range srv.seen() {
		if rf, ok := b["response_format"].(map[string]any); ok && rf["type"] == "json_schema" {
			t.Error("json_schema retried after demotion")
		}
	}
}

func TestStructuredFallsBackToDelegateWhenNothingSupported(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t) // supports nothing
	m, _ := newManagerFor(t, "groq", srv.endpoint())
	key := BindRole(m, "groq", Directives{
		Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true,
	})
	resp, err := m.Complete(context.Background(), key, reviewRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content == "" {
		t.Fatal("empty completion")
	}
	for _, b := range srv.seen() {
		if _, ok := b["response_format"]; ok {
			// probe requests carry it; the real call must not
			if mt, _ := b["max_tokens"].(float64); mt != 1 {
				t.Errorf("unsupported response_format sent on a real call: %v", b)
			}
		}
	}
}

func TestSchemaDetectionOverridesTheBoundRole(t *testing.T) {
	// The planner agent also runs the clarify interview. Binding "plan" to the
	// planner must not force the plan schema onto a clarify prompt.
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t, "json_schema", "json_object")
	srv.content = `{"needs_user":false,"assumptions":["a"],"acceptance":["b"]}`
	m, _ := newManagerFor(t, "openai", srv.endpoint())
	key := BindRole(m, "openai", Directives{
		Role: "planner", SchemaRole: schema.RolePlan, JSONOnly: true,
	})
	srv.reset()
	req := reviewRequest()
	req.Messages = []llm.Message{
		{Role: "system", Content: `Interviewer. STRICT JSON only:
{"needs_user":false,"questions":[],"assumptions":[],"acceptance":[],"non_goals":[],"language":"","entrypoint":"","prd":{}}`},
		{Role: "user", Content: "build me a thing"},
	}
	if _, err := m.Complete(context.Background(), key, req); err != nil {
		t.Fatal(err)
	}
	body, ok := lastBodyWith(srv.seen(), "response_format")
	if !ok {
		t.Fatal("no structured request issued")
	}
	rf := body["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if name, _ := js["name"].(string); name != "slmcode_clarify" {
		t.Errorf("schema name = %q, want slmcode_clarify (detected from the prompt)", name)
	}
}

func TestSerialToolsTruncatesToFirstCall(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t, "tools", "json_object")
	srv.content = ""
	srv.toolCalls = []map[string]any{
		{"id": "c1", "type": "function", "function": map[string]any{"name": "ws_edit", "arguments": `{"path":"a.go"}`}},
		{"id": "c2", "type": "function", "function": map[string]any{"name": "ws_edit", "arguments": `{"path":"b.go"}`}},
		{"id": "c3", "type": "function", "function": map[string]any{"name": "ws_edit", "arguments": `{"path":"c.go"}`}},
	}
	m, _ := newManagerFor(t, "openai", srv.endpoint())
	key := BindRole(m, "openai", Directives{
		Role: "worker", SchemaRole: schema.RoleWorker, SerialTools: true, ToolChoice: "auto",
	})
	req := reviewRequest()
	req.Tools = []llm.ToolDefinition{{
		Type: "function",
		Function: llm.Function{
			Name: "ws_edit", Description: "edit",
			Parameters: map[string]interface{}{"type": "object"},
		},
	}}
	resp, err := m.Complete(context.Background(), key, req)
	if err != nil {
		t.Fatal(err)
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want exactly 1", len(calls))
	}
	if calls[0].ID != "c1" {
		t.Errorf("kept %q, want the first call", calls[0].ID)
	}
	if DroppedToolCalls()["worker"] != 2 {
		t.Errorf("dropped counter = %v", DroppedToolCalls())
	}
	// tool_choice must reach the wire.
	body, ok := lastBodyWith(srv.seen(), "tool_choice")
	if !ok {
		t.Fatal("tool_choice not sent")
	}
	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v", body["tool_choice"])
	}
}

func TestBindRoleIsIdempotentAndSafe(t *testing.T) {
	ResetCapabilityCache()
	srv := newFakeServer(t, "json_object")
	m, _ := newManagerFor(t, "omlx", srv.endpoint())
	d := Directives{Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true}
	k1 := BindRole(m, "omlx", d)
	k2 := BindRole(m, "omlx", d)
	if k1 != k2 {
		t.Fatalf("BindRole not idempotent: %q vs %q", k1, k2)
	}
	if !strings.Contains(k1, RoleKeySeparator) {
		t.Errorf("key %q has no role marker", k1)
	}
	// Unknown base provider must degrade to the base key, never panic.
	if got := BindRole(m, "does-not-exist", d); got != "does-not-exist" {
		t.Errorf("unknown base = %q", got)
	}
	// Nil manager.
	if got := BindRole(nil, "omlx", d); got != "omlx" {
		t.Errorf("nil manager = %q", got)
	}
	// A role with nothing to shape reuses the shared registration.
	if got := BindRole(m, "omlx", Directives{Role: "context"}); got != "omlx" {
		t.Errorf("no-op directives created a provider: %q", got)
	}
}

func TestDecodeChatCompletionHandles200ErrorEnvelope(t *testing.T) {
	_, err := decodeChatCompletion([]byte(`{"error":{"message":"model not loaded","type":"invalid_request_error"}}`))
	if err == nil {
		t.Fatal("200-with-error envelope must surface as an error")
	}
	if Classify(err).Class != ClassPermanent {
		t.Errorf("class = %v", Classify(err).Class)
	}
}

func TestBuildBodyOmitsEmptyFields(t *testing.T) {
	p := &structuredProvider{meta: backendMeta{Provider: "openai", Endpoint: "http://x/v1"}}
	spec, _ := schema.For(schema.RoleReview)
	body := p.buildBody(llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, spec, MechJSONObject, "m")
	for _, k := range []string{"temperature", "max_tokens", "stop"} {
		if _, ok := body[k]; ok {
			t.Errorf("zero-valued %q should be omitted", k)
		}
	}
	if body["stream"] != false {
		t.Error("structured path must not stream")
	}
	b, err := json.Marshal(body)
	if err != nil || !strings.Contains(string(b), `"json_object"`) {
		t.Errorf("body = %s err=%v", b, err)
	}
}

func TestStructuredPathObservesThroughput(t *testing.T) {
	ResetCapabilityCache()
	GlobalThroughput.Reset()
	srv := newFakeServer(t, "json_object")
	m, _ := newManagerFor(t, "omlx", srv.endpoint())
	key := BindRole(m, "omlx", Directives{Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true})
	if _, err := m.Complete(context.Background(), key, reviewRequest()); err != nil {
		t.Fatal(err)
	}
	// The fake server reports 30 completion tokens.
	if _, samples := GlobalThroughput.TokensPerSec("fake-model"); samples == 0 {
		t.Error("structured path did not record decode throughput")
	}
	_ = time.Now
}

func TestStructuredTransientFailureIsNotReplayedThroughTheDelegate(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t, "json_object")
	// Probe first (while the server is healthy), then make everything 503 so the
	// structured path exhausts its retries.
	m, _ := newManagerFor(t, "omlx", srv.endpoint())
	key := BindRole(m, "omlx", Directives{
		Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true,
	})
	if _, err := m.Complete(context.Background(), key, reviewRequest()); err != nil {
		t.Fatal(err)
	}
	srv.reset()
	srv.mu.Lock()
	srv.failures = 99
	srv.failStatus = 503
	srv.mu.Unlock()

	if _, err := m.Complete(context.Background(), key, reviewRequest()); err == nil {
		t.Fatal("expected the call to fail")
	}
	// 3 structured attempts and no delegate replay — not 3 + 3.
	if n := len(srv.seen()); n > 3 {
		t.Errorf("%d requests reached the server; a transient failure was replayed through the delegate", n)
	}
}

func TestDemoteDoesNotBlankAnUnknownEndpoint(t *testing.T) {
	ResetCapabilityCache()
	demoteCapability("openai", "http://never-probed/v1", "m", MechJSONSchema)
	if _, ok := CachedCapabilities("openai", "http://never-probed/v1", "m"); ok {
		t.Error("demoting an unprobed endpoint wrote a wholesale-false record")
	}
}
