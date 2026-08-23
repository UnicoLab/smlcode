package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// fakeServer is a configurable OpenAI-compatible chat-completions endpoint. It
// is the only way to verify capability negotiation and request shaping actually
// reach the wire in the right shape.
type fakeServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []map[string]any

	// supports gates which extra body fields are accepted; anything not listed
	// is answered 400, exactly like a server that does not implement it.
	supports map[string]bool
	// content is the assistant message returned on success.
	content string
	// toolCalls, when non-empty, is returned as the assistant's tool calls.
	toolCalls []map[string]any
	// failures counts down: while >0 the server answers with failStatus.
	failures   int
	failStatus int
	failBody   string
	retryAfter string
}

func newFakeServer(t *testing.T, supports ...string) *fakeServer {
	t.Helper()
	f := &fakeServer{
		supports:   map[string]bool{},
		content:    `{"approved":true,"score":90,"summary":"ok"}`,
		failStatus: 503,
	}
	for _, s := range supports {
		f.supports[s] = true
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	f.mu.Lock()
	f.requests = append(f.requests, body)
	fail := f.failures
	if fail > 0 {
		f.failures--
	}
	supports := map[string]bool{}
	for k, v := range f.supports {
		supports[k] = v
	}
	content, toolCalls := f.content, f.toolCalls
	failStatus, failBody, retryAfter := f.failStatus, f.failBody, f.retryAfter
	f.mu.Unlock()

	if fail > 0 {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(failStatus)
		if failBody == "" {
			failBody = `{"error":{"message":"temporarily unavailable"}}`
		}
		_, _ = io.WriteString(w, failBody)
		return
	}

	reject := func(field string) bool {
		_, present := body[field]
		return present && !supports[field]
	}
	if reject("guided_json") || reject("grammar") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Unrecognized request argument"}}`)
		return
	}
	if rf, ok := body["response_format"].(map[string]any); ok {
		typ, _ := rf["type"].(string)
		if !supports[typ] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"response_format `+typ+` not supported"}}`)
			return
		}
	}
	if _, ok := body["tools"]; ok && !supports["tools"] {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"tools not supported"}}`)
		return
	}

	msg := map[string]any{"role": "assistant", "content": content}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "cmpl-fake",
		"object":  "chat.completion",
		"model":   "fake-model",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 30, "total_tokens": 40},
	})
}

func (f *fakeServer) seen() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeServer) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

func (f *fakeServer) endpoint() string { return f.URL + "/v1" }

// ---------------------------------------------------------------------------

func TestProbeNegotiatesPerEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		supports []string
		want     Capabilities
	}{
		{
			name: "openai style json_schema", provider: "openai",
			supports: []string{"json_schema", "json_object", "tools"},
			want:     Capabilities{JSONSchema: true, JSONObject: true, NativeTools: true, Streaming: true},
		},
		{
			name: "vllm guided_json only", provider: "vllm",
			supports: []string{"guided_json", "json_object", "tools"},
			want:     Capabilities{GuidedJSON: true, JSONObject: true, NativeTools: true, Streaming: true},
		},
		{
			name: "llama.cpp grammar", provider: "llamacpp",
			supports: []string{"grammar", "json_object"},
			want:     Capabilities{GBNFGrammar: true, JSONObject: true, Streaming: true},
		},
		{
			name: "json mode only", provider: "deepseek",
			supports: []string{"json_object", "tools"},
			want:     Capabilities{JSONObject: true, NativeTools: true, Streaming: true},
		},
		{
			name: "nothing structured", provider: "groq",
			supports: []string{},
			want:     Capabilities{Streaming: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ResetCapabilityCache()
			srv := newFakeServer(t, tc.supports...)
			got := Probe(context.Background(), tc.provider, srv.endpoint(), "fake-model", "")
			got.Probed = time.Time{}
			got.Source = ""
			if got != tc.want {
				t.Errorf("Probe = %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestProbeUnreachableIsWeakestAndNotFatal(t *testing.T) {
	ResetCapabilityCache()
	// Port 1 is reserved; nothing listens there.
	got := Probe(context.Background(), "omlx", "http://127.0.0.1:1/v1", "m", "")
	if got.Any() || got.NativeTools {
		t.Errorf("unreachable endpoint must yield the weakest capabilities, got %+v", got)
	}
	if got.Source != "unreachable" {
		t.Errorf("source = %q, want unreachable", got.Source)
	}
	// Must not be cached as a fresh probe (so a later run re-negotiates).
	if _, ok := CachedCapabilities("omlx", "http://127.0.0.1:1/v1", "m"); ok {
		if c, _ := CachedCapabilities("omlx", "http://127.0.0.1:1/v1", "m"); !c.Probed.IsZero() {
			t.Error("unreachable result must not be cached as probed")
		}
	}
}

func TestProbeCachesAndCollapsesConcurrentCallers(t *testing.T) {
	ResetCapabilityCache()
	srv := newFakeServer(t, "json_schema", "json_object", "tools")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Probe(context.Background(), "openai", srv.endpoint(), "fake-model", "")
		}()
	}
	wg.Wait()
	first := len(srv.seen())
	if first == 0 {
		t.Fatal("no probe requests issued")
	}
	srv.reset()
	Probe(context.Background(), "openai", srv.endpoint(), "fake-model", "")
	if n := len(srv.seen()); n != 0 {
		t.Errorf("cached probe still issued %d requests", n)
	}
	if _, ok := CachedCapabilities("openai", srv.endpoint(), "fake-model"); !ok {
		t.Error("probe result not cached")
	}
}

func TestCapabilityCachePersistsToDisk(t *testing.T) {
	ResetCapabilityCache()
	dir := t.TempDir()
	SetCapabilityCacheDir(dir)
	srv := newFakeServer(t, "json_object")
	Probe(context.Background(), "omlx", srv.endpoint(), "fake-model", "")

	// New process: memory cleared, disk cache must still answer.
	caps.mu.Lock()
	caps.mem = map[string]Capabilities{}
	caps.single = map[string]*sync.Once{}
	caps.loaded = false
	caps.mu.Unlock()

	got, ok := CachedCapabilities("omlx", srv.endpoint(), "fake-model")
	if !ok {
		t.Fatal("capabilities not restored from disk")
	}
	if !got.JSONObject || got.Source != "cache" {
		t.Errorf("restored = %+v", got)
	}
	SetCapabilityCacheDir("")
}

func TestSelectMechanismLadder(t *testing.T) {
	spec, _ := schema.For(schema.RoleReview)
	cases := []struct {
		name string
		caps Capabilities
		want string
	}{
		{"strongest wins", Capabilities{JSONSchema: true, GuidedJSON: true, GBNFGrammar: true, JSONObject: true}, MechJSONSchema},
		{"guided next", Capabilities{GuidedJSON: true, GBNFGrammar: true, JSONObject: true}, MechGuidedJSON},
		{"grammar next", Capabilities{GBNFGrammar: true, JSONObject: true}, MechGrammar},
		{"json mode floor", Capabilities{JSONObject: true}, MechJSONObject},
		{"nothing", Capabilities{}, MechPromptOnly},
	}
	for _, tc := range cases {
		if got := tc.caps.SelectMechanism(spec, nil); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
	full := Capabilities{JSONSchema: true, GuidedJSON: true, JSONObject: true}
	if got := full.SelectMechanism(spec, map[string]bool{MechJSONSchema: true}); got != MechGuidedJSON {
		t.Errorf("exclusion ignored: %q", got)
	}
}

func TestPresetCapabilitiesGateProbes(t *testing.T) {
	// OpenAI must never be probed for vLLM/llama.cpp-only fields.
	ResetCapabilityCache()
	srv := newFakeServer(t, "json_schema", "json_object", "tools")
	Probe(context.Background(), "openai", srv.endpoint(), "fake-model", "")
	for _, req := range srv.seen() {
		if _, ok := req["guided_json"]; ok {
			t.Error("guided_json probed against an openai preset")
		}
		if _, ok := req["grammar"]; ok {
			t.Error("grammar probed against an openai preset")
		}
	}
	// And every probe must be a one-token request.
	for _, req := range srv.seen() {
		if mt, ok := req["max_tokens"].(float64); !ok || mt != 1 {
			if _, isJSONMode := req["response_format"]; !isJSONMode {
				t.Errorf("probe was not 1 token: %v", req["max_tokens"])
			}
		}
	}
}

func TestChatCompletionsURL(t *testing.T) {
	cases := []struct{ provider, in, want string }{
		{"openai", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"omlx", "http://127.0.0.1:9000", "http://127.0.0.1:9000/v1/chat/completions"},
		{"ollama", "http://127.0.0.1:11434", "http://127.0.0.1:11434/v1/chat/completions"},
		{"vllm", "http://h/v1/chat/completions", "http://h/v1/chat/completions"},
		{"openai", "https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := chatCompletionsURL(c.provider, c.in); got != c.want {
			t.Errorf("chatCompletionsURL(%q,%q) = %q want %q", c.provider, c.in, got, c.want)
		}
	}
}

func TestCapabilityReportIsStable(t *testing.T) {
	ResetCapabilityCache()
	SetCapabilities("omlx", "http://a/v1", "m1", Capabilities{JSONObject: true})
	SetCapabilities("vllm", "http://b/v1", "m2", Capabilities{GuidedJSON: true})
	rep := CapabilityReport()
	if len(rep) != 2 {
		t.Fatalf("report = %v", rep)
	}
	if !strings.Contains(rep[0], "omlx") || !strings.Contains(rep[1], "vllm") {
		t.Errorf("report not sorted: %v", rep)
	}
}
