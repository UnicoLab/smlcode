package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/harness"
)

func TestStacksListAndApply(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.Listen = "127.0.0.1:9999"
	h.Config.SkillsDirs = []string{"/keep/skills"}
	if err := h.Config.Save(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/stacks", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	stackList, ok := listed["stacks"].([]interface{})
	if !ok || len(stackList) < 5 {
		t.Fatalf("expected stacks: %+v", listed)
	}

	body := []byte(`{"apply_agent_defaults":true}`)
	req = httptest.NewRequest(http.MethodPost, "/api/stacks/openai/apply", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	var applied map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied["ok"] != true {
		t.Fatalf("apply: %+v", applied)
	}
	if h.Config.Provider != "openai" || h.Config.Model != "gpt-4o" {
		t.Fatalf("config not updated: %s %s", h.Config.Provider, h.Config.Model)
	}
	if h.Config.ActiveStack != "openai" {
		t.Fatalf("active_stack=%s", h.Config.ActiveStack)
	}
	if h.Config.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen wiped: %s", h.Config.Listen)
	}
	if len(h.Config.SkillsDirs) != 1 || h.Config.SkillsDirs[0] != "/keep/skills" {
		t.Fatalf("skills wiped: %v", h.Config.SkillsDirs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("agents status=%d", rec.Code)
	}
	var agentsList []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &agentsList); err != nil {
		t.Fatal(err)
	}
	foundWorker := false
	for _, a := range agentsList {
		if a["id"] == "worker" {
			foundWorker = true
			if a["effective_model"] == nil || a["effective_model"] == "" {
				t.Fatalf("worker missing effective_model: %+v", a)
			}
			// openai stack agents: worker model gpt-4o-mini when apply_agent_defaults
			if a["model"] != "gpt-4o-mini" && a["effective_model"] != "gpt-4o" && a["effective_model"] != "gpt-4o-mini" {
				t.Fatalf("worker llm unexpected: %+v", a)
			}
		}
	}
	if !foundWorker {
		t.Fatal("worker missing from agents list")
	}
}

func TestModelsAuthAndSearch(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.Provider = "openai"
	h.Config.Endpoint = "https://api.openai.com/v1"
	h.Config.Model = "gpt-4o"
	h.Config.APIKey = ""
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SLMCODE_API_KEY", "")

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/models?q=gpt&limit=5", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("models status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cat map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	auth, _ := cat["auth"].(map[string]interface{})
	if auth == nil {
		t.Fatalf("missing auth: %+v", cat)
	}
	if auth["required"] != true {
		t.Fatalf("openai should require auth: %+v", auth)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("auth status=%d", rec.Code)
	}
}

func TestGetAgentIncludesEffective(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.Provider = "ollama"
	h.Config.Model = "qwen2.5-coder:7b"
	h.Config.ActiveStack = "ollama-local"

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/agents/worker", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var a map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a["effective_model"] != "qwen2.5-coder:7b" {
		t.Fatalf("effective_model=%v", a["effective_model"])
	}
	if a["inherits_model"] != true {
		t.Fatalf("inherits_model=%v", a["inherits_model"])
	}
	if a["active_stack"] != "ollama-local" {
		t.Fatalf("active_stack=%v", a["active_stack"])
	}
}
