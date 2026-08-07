package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func TestMCPSchemaAuthEventsAPIs(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)

	// GET /api/mcp
	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("mcp status=%d body=%s", rec.Code, rec.Body.String())
	}
	var mcp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &mcp); err != nil {
		t.Fatal(err)
	}
	if mcp["meta_tool"] != "mcp_call" {
		t.Fatalf("mcp: %+v", mcp)
	}

	// GET /api/config/schema
	req = httptest.NewRequest(http.MethodGet, "/api/config/schema", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("schema status=%d", rec.Code)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	fields, _ := schema["fields"].([]interface{})
	if len(fields) < 5 {
		t.Fatalf("schema fields: %+v", schema)
	}

	// PUT /api/auth
	body := []byte(`{"provider":"openai","api_key":"sk-test-prime"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/auth", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("put auth status=%d body=%s", rec.Code, rec.Body.String())
	}
	key, ok := authstore.Get(h.Config.SlmDir(), "openai")
	if !ok || key != "sk-test-prime" {
		t.Fatalf("auth.json missing key: ok=%v key=%q path=%s", ok, key, filepath.Join(h.Config.SlmDir(), "auth.json"))
	}

	// GET /api/queries/{id}/events
	if err := session.AppendEvent(h.Config.SlmDir(), "run-test", session.EventRecord{
		Phase: "init", Kind: "phase", Message: "hello-events",
	}); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/queries/run-test/events", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("events status=%d body=%s", rec.Code, rec.Body.String())
	}
	var ev map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	events, _ := ev["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("events: %+v", ev)
	}
}
