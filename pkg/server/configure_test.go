package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/harness"
)

// A user whose endpoint is not answering is looking at a Studio that cannot run
// anything. Telling them to go and find a terminal is the point at which they
// stop, which is why this exists on both surfaces.

func configureServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	return New(h, nil), root
}

func fakeModelServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	body := `{"object":"list","data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `"}`
	}
	body += `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func decodeConfigure(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	return out
}

// GET looks and reports. A scan that wrote would mean the only way to see what
// auto-configuration would do is to let it happen.
func TestConfigureScanChangesNothing(t *testing.T) {
	s, _ := configureServer(t)
	model := fakeModelServer(t, "nomic-embed-text", "Qwen2.5-Coder-14B-Instruct")
	before := s.cfg().Endpoint

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet,
		"/api/configure?endpoint="+model.URL+"/v1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeConfigure(t, rec)
	if out["ok"] != true {
		t.Fatalf("ok=%v body=%s", out["ok"], rec.Body.String())
	}
	if out["applied"] != false {
		t.Error("a scan reported itself as applied")
	}
	choice, _ := out["choice"].(map[string]any)
	if choice["model"] != "Qwen2.5-Coder-14B-Instruct" {
		t.Errorf("choice = %v, want the coder rather than the embedding model", choice)
	}
	if s.cfg().Endpoint != before {
		t.Errorf("the scan rewrote the endpoint: %q", s.cfg().Endpoint)
	}
}

func TestConfigureApplyWritesAndRebuilds(t *testing.T) {
	s, _ := configureServer(t)
	model := fakeModelServer(t, "nomic-embed-text", "Qwen2.5-Coder-14B-Instruct")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost,
		"/api/configure?endpoint="+model.URL+"/v1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if out := decodeConfigure(t, rec); out["applied"] != true {
		t.Error("apply did not report itself as applied")
	}
	cfg := s.cfg()
	if cfg.Endpoint != model.URL+"/v1" {
		t.Errorf("endpoint = %q, want the server that answered", cfg.Endpoint)
	}
	if cfg.Model != "Qwen2.5-Coder-14B-Instruct" {
		t.Errorf("model = %q", cfg.Model)
	}
	// The orchestrator holds the endpoint it was built with; a Studio reporting
	// a configuration the harness is not using is worse than not configuring.
	if s.orch() == nil {
		t.Error("the orchestrator was not rebuilt")
	}
}

// The three failure modes are different problems with different fixes.
func TestConfigureReportsWhyItFoundNothing(t *testing.T) {
	s, _ := configureServer(t)
	model := fakeModelServer(t, "nomic-embed-text", "whisper-large-v3")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost,
		"/api/configure?endpoint="+model.URL+"/v1", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422 for a server with nothing usable", rec.Code)
	}
	out := decodeConfigure(t, rec)
	reason, _ := out["reason"].(string)
	if !strings.Contains(reason, "coder-tuned") {
		t.Errorf("reason = %q, want the real problem named", reason)
	}
}

// An explicit endpoint is an instruction: without narrowing, a probe of a dead
// address falls through to whatever else is running.
func TestAnExplicitEndpointIsNotSecondGuessed(t *testing.T) {
	s, _ := configureServer(t)
	live := fakeModelServer(t, "Qwen2.5-Coder-14B-Instruct")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/configure?endpoint=http://127.0.0.1:9/v1", nil))
	out := decodeConfigure(t, rec)
	if out["ok"] == true {
		t.Errorf("a dead endpoint the caller named must not fall through: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), live.URL) {
		t.Errorf("the scan reached an endpoint nobody named: %s", rec.Body.String())
	}
}
