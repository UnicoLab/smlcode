package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/authstore"
)

// No Studio endpoint may echo a stored provider key back over HTTP.
func TestAdvNoAPIKeyLeakOverHTTP(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	slm := s.slmDir()
	if err := os.MkdirAll(slm, 0o750); err != nil {
		t.Fatal(err)
	}
	const secret = "sk-LEAKCANARY-0123456789"
	if err := authstore.Set(slm, "openai", secret); err != nil {
		t.Fatal(err)
	}
	// auth.json must be 0600.
	st, err := os.Stat(filepath.Join(slm, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("auth.json mode is %v, want 0600", st.Mode().Perm())
	}

	for _, p := range []string{
		"/api/health", "/api/status", "/api/config", "/api/readiness",
		"/api/models", "/api/board", "/api/queries", "/api/providers",
		"/api/workspace/tree", "/api/workspace/file?path=.slmcode/auth.json",
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, p, nil))
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("API KEY LEAKED via %s: %s", p, rec.Body.String())
		}
	}
	// The credential file must not appear in a directory listing either.
	rec0 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec0, newAPIRequest(http.MethodGet, "/api/workspace/tree?path=.slmcode", nil))
	if strings.Contains(rec0.Body.String(), "auth.json") {
		t.Errorf("auth.json advertised in the workspace tree: %s", rec0.Body.String())
	}

	// A queued review patch must not be able to land harness control state.
	pend := filepath.Join(slm, "pending")
	_ = os.MkdirAll(pend, 0o750)
	_ = os.WriteFile(filepath.Join(pend, "evil.patch.json"),
		[]byte(`{"path":".slmcode/hooks.json","kind":"write","content":"{}"}`), 0o600)
	if _, err := s.applyPending("evil.patch.json"); err == nil {
		t.Error("review queue applied a write into .slmcode/")
	}
	if _, err := os.Stat(filepath.Join(slm, "hooks.json")); err == nil {
		t.Error(".slmcode/hooks.json was created via the review queue")
	}

	// Explicitly: the config endpoint must redact, not omit-and-round-trip.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	var cfg map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	if v, ok := cfg["api_key"].(string); ok && v != "" && v != "***" {
		t.Errorf("config endpoint exposed api_key=%q", v)
	}
}
