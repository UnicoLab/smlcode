package backends

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// rawServer answers every POST with a caller-supplied status and body, and
// counts the requests. It is deliberately not the well-behaved fakeServer.
type rawServer struct {
	*httptest.Server
	status int
	body   string
	hits   int
}

func newRawServer(t *testing.T, status int, body string) *rawServer {
	t.Helper()
	s := &rawServer{status: status, body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.hits++
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *rawServer) endpoint() string { return s.URL + "/v1" }

// bindReviewer wires a JSON-only reviewer role against endpoint and returns the
// registry key plus the manager.
func bindReviewer(t *testing.T, provider, endpoint string) (*llm.ProviderManager, string) {
	t.Helper()
	m, _ := newManagerFor(t, provider, endpoint)
	key := BindRole(m, config.NormalizeProvider(provider), Directives{
		Role: "reviewer", SchemaRole: schema.RoleReview, JSONOnly: true,
	})
	return m, key
}

// Constrained decoding must never be the reason a run fails. Every one of these
// is a server behaving badly on the direct structured path.
func TestStructuredPathSurvivesHostileServers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"404 on chat/completions", http.StatusNotFound, `{"error":{"message":"not found"}}`},
		{"200 with no choices", http.StatusOK, `{"id":"x","object":"chat.completion","choices":[]}`},
		{"200 with no usage", http.StatusOK,
			`{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"approved\":true}"},"finish_reason":"stop"}]}`},
		{"200 with an SSE stream anyway", http.StatusOK,
			"data: {\"choices\":[{\"delta\":{\"content\":\"{\"}}]}\n\ndata: [DONE]\n\n"},
		{"200 with a valid-but-empty object", http.StatusOK,
			`{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`},
		{"200 error envelope", http.StatusOK, `{"error":{"message":"model not loaded","type":"server"}}`},
		{"empty body", http.StatusOK, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ResetCapabilityCache()
			ResetTelemetry()
			srv := newRawServer(t, tc.status, tc.body)
			SetCapabilities("openai", srv.endpoint(), "fake-model",
				Capabilities{JSONSchema: true, JSONObject: true, Probed: time.Now()})
			m, key := bindReviewer(t, "openai", srv.endpoint())

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				// Either a completion or an error is fine. A panic, a hang, or a
				// nil-deref is not.
				_, _ = m.Complete(ctx, key, reviewRequest())
			}()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("structured path hung")
			}
			if srv.hits == 0 {
				t.Fatal("no request reached the server")
			}
		})
	}
}

// A capability record that says "json_schema" against a server that has since
// been swapped for one which rejects it must self-heal, not wedge the run.
func TestStaleCapabilityRecordSelfHeals(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t, "json_object") // only JSON mode, no schema
	SetCapabilities("openai", srv.endpoint(), "fake-model", Capabilities{
		JSONSchema: true, JSONObject: true, Probed: time.Now(), Source: "cache",
	})
	m, key := bindReviewer(t, "openai", srv.endpoint())

	resp, err := m.Complete(context.Background(), key, reviewRequest())
	if err != nil {
		t.Fatalf("stale capability record wedged the run: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("no completion produced")
	}
	c, _ := CachedCapabilities("openai", srv.endpoint(), "fake-model")
	if c.JSONSchema {
		t.Error("json_schema was not demoted after the server rejected it")
	}
	if !c.JSONObject {
		t.Error("demotion took json_object down with it")
	}
	// And the next call must not re-try the demoted rung.
	srv.reset()
	if _, err := m.Complete(context.Background(), key, reviewRequest()); err != nil {
		t.Fatal(err)
	}
	for _, body := range srv.seen() {
		if rf, ok := body["response_format"].(map[string]any); ok && rf["type"] == "json_schema" {
			t.Fatal("a demoted mechanism was attempted again")
		}
	}
}

// An unreachable endpoint must not make the probe (and therefore the first
// call of a run) fail or hang.
func TestProbeOnUnreachableEndpointIsHarmless(t *testing.T) {
	ResetCapabilityCache()
	done := make(chan Capabilities, 1)
	go func() {
		// Port 1 on loopback: connection refused, immediately.
		done <- Probe(context.Background(), "openai", "http://127.0.0.1:1/v1", "m", "local")
	}()
	select {
	case c := <-done:
		if c.Any() {
			t.Fatalf("unreachable endpoint reported capabilities: %v", c)
		}
	case <-time.After(ProbeTimeout + 15*time.Second):
		t.Fatal("probe hung on an unreachable endpoint")
	}
}

// A provider that supports NOTHING must still complete end to end through the
// ordinary (prompt-only) path.
func TestProviderWithNoCapabilitiesStillCompletes(t *testing.T) {
	ResetCapabilityCache()
	ResetTelemetry()
	srv := newFakeServer(t) // supports nothing
	SetCapabilities("openai", srv.endpoint(), "fake-model", Capabilities{Probed: time.Now()})
	m, key := bindReviewer(t, "openai", srv.endpoint())

	resp, err := m.Complete(context.Background(), key, reviewRequest())
	if err != nil {
		t.Fatalf("prompt-only provider failed: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || !strings.Contains(resp.Choices[0].Message.Content, "approved") {
		t.Fatalf("unexpected completion: %+v", resp)
	}
	if got := RoleMechanisms()["reviewer"]; got != "" && got != MechPromptOnly {
		t.Errorf("mechanism = %q, want prompt_only or unset", got)
	}
}
