package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/permissions"
)

func TestPutConfigPartialPreservesDryRun(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.DryRun = true
	h.Config.Permission = permissions.ModeDryRun
	if err := h.Config.Save(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := []byte(`{"model":"patched-model"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "patched-model" {
		t.Fatalf("model=%v", out["model"])
	}
	if out["dry_run"] != true {
		t.Fatalf("dry_run cleared: %v", out["dry_run"])
	}
	if out["permission"] != permissions.ModeDryRun {
		t.Fatalf("permission=%v", out["permission"])
	}
}

func TestPutConfigSetsPermission(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := []byte(`{"permission":"review"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.Permission != permissions.ModeReview {
		t.Fatalf("permission=%s", h.Config.Permission)
	}
	if h.Config.DryRun {
		t.Fatal("dry_run should be false for review")
	}

	// Clear via dry_run false after dry-run mode
	body = []byte(`{"permission":"dry-run"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if !h.Config.DryRun {
		t.Fatal("expected dry_run")
	}

	body = []byte(`{"dry_run":false}`)
	req = httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.DryRun || h.Config.Permission != permissions.ModeAuto {
		t.Fatalf("clear: dry=%v perm=%s", h.Config.DryRun, h.Config.Permission)
	}
}

func TestHealth(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestSPAContentTypes(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html":                  &fstest.MapFile{Data: []byte("<html>ok</html>")},
		"app.jsx":                     &fstest.MapFile{Data: []byte("const x = 1")},
		"styles.css":                  &fstest.MapFile{Data: []byte("body{}")},
		"vendor/react.production.min.js": &fstest.MapFile{Data: []byte("/*react*/")},
	}
	s := New(h, ui)
	cases := []struct{ path, want string }{
		{"/app.jsx", "text/javascript"},
		{"/styles.css", "text/css"},
		{"/vendor/react.production.min.js", "application/javascript"},
		{"/", "text/html"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s status=%d", tc.path, rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, tc.want) {
			t.Fatalf("%s Content-Type=%q want substring %q", tc.path, ct, tc.want)
		}
	}
}
