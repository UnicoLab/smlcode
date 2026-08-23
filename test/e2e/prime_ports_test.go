package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/refine"
	"github.com/UnicoLab/slmcode/pkg/server"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/stacks"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// TestPrimePortsEndToEnd exercises the full stacks→agents→models→auth→mcp→
// compact→events→refine surface without a live LLM (offline e2e).
func TestPrimePortsEndToEnd(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n"), 0o644)

	cfg := config.Default(root)
	cfg.Provider = "ollama"
	cfg.Endpoint = "http://127.0.0.1:1" // unreachable on purpose
	cfg.Model = "qwen2.5-coder:7b"
	cfg.SessionEventLog = true
	cfg.ContextCompact = true
	cfg.ContextCompactEngine = "heuristic"
	cfg.AutoRefine = true
	cfg.EnabledModels = []string{"qwen2.5-coder:7b", "keep-me"}
	cfg.LLMRetryCount = 2
	cfg.LLMRetryDelayMS = 100
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	// --- Stacks: load from repo + apply non-destructively ---
	t.Setenv("SLMCODE_STACKS", filepath.Join(findRepoRoot(t), "stacks"))
	list, err := stacks.List()
	if err != nil || len(list) < 3 {
		t.Fatalf("stacks.List: n=%d err=%v dir=%s", len(list), err, stacks.FindDir())
	}
	st, err := stacks.Load("openai")
	if err != nil {
		t.Fatal(err)
	}
	listenKeep := "127.0.0.1:7777"
	cfg.Listen = listenKeep
	cfg.SkillsDirs = []string{"/keep/skills"}
	agentsDir := cfg.AgentsDir()
	applyRes, err := stacks.Apply(cfg, st, agentsDir, stacks.ApplyOptions{ClearAgentLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if applyRes.StackID != "openai" || cfg.ActiveStack != "openai" {
		t.Fatalf("apply: %+v active=%s", applyRes, cfg.ActiveStack)
	}
	if cfg.Listen != listenKeep || len(cfg.SkillsDirs) != 1 {
		t.Fatalf("destructive apply: listen=%s skills=%v", cfg.Listen, cfg.SkillsDirs)
	}
	if cfg.Provider != "openai" {
		t.Fatalf("provider=%s", cfg.Provider)
	}

	// --- Auth store ---
	if err := authstore.Set(cfg.SlmDir(), "openai", "sk-e2e-test"); err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = ""
	cfg.ResolveAPIKey()
	if cfg.APIKey != "sk-e2e-test" {
		t.Fatalf("ResolveAPIKey from auth.json: %q", cfg.APIKey)
	}
	auth := models.ResolveAuth(cfg)
	if !auth.Configured {
		t.Fatalf("auth: %+v", auth)
	}

	// --- Models catalog: fail-closed + enabled filter ---
	cfg.APIKey = ""
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SLMCODE_API_KEY", "")
	// Clear auth.json for fail-closed check
	_ = authstore.Set(cfg.SlmDir(), "openai", "")
	cfg2 := *cfg
	cfg2.APIKey = ""
	cfg2.EnabledModels = []string{"gpt-4o"}
	cfg2.Model = "gpt-4o"
	cat := models.Find(context.Background(), &cfg2, "gpt", 8)
	if cat.Error == "" {
		t.Fatal("expected auth fail-closed error")
	}
	if len(cat.Matches) != 1 || cat.Matches[0].ID != "gpt-4o" {
		t.Fatalf("fail-closed matches: %+v", cat.Matches)
	}

	// Restore key for orchestrator
	_ = authstore.Set(cfg.SlmDir(), "openai", "sk-e2e-test")
	cfg.APIKey = "sk-e2e-test"
	cfg.EnabledModels = nil
	cfg.MaxContextKB = 2 // force CONTEXT compaction in this e2e

	// --- Coding allowlists include specialist tools ---
	have := map[string]bool{}
	for _, name := range append(workspace.ToolNames(), workspace.SpecialistToolNames()...) {
		have[name] = true
	}
	if !have["find_models"] || !have["mcp_call"] {
		t.Fatal("specialist tools missing from allowlist helpers")
	}
	workerOK := false
	for _, s := range agents.Specs() {
		if s.ID != "worker" {
			continue
		}
		tools := map[string]bool{}
		for _, tool := range s.Tools {
			tools[tool] = true
		}
		if !tools["find_models"] || !tools["mcp_call"] {
			t.Fatalf("worker tools: %v", s.Tools)
		}
		workerOK = true
	}
	if !workerOK {
		t.Fatal("worker missing")
	}

	// --- Orchestrator wires find_models + MCP status ---
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.Orchestrator = orch
	mcpSt := orch.MCPStatus()
	if mcpSt.MetaTool != "mcp_call" {
		t.Fatalf("mcp status: %+v", mcpSt)
	}

	// Compact CONTEXT (heuristic) — body must exceed MaxContextKB
	_ = os.WriteFile(filepath.Join(cfg.SlmDir(), "CONTEXT.md"),
		[]byte("# CONTEXT\n\n"+strings.Repeat("## Wave update\n"+strings.Repeat("noise ", 40)+"\n\n", 80)), 0o644)
	cres, err := orch.CompactContextNow()
	if err != nil {
		t.Fatal(err)
	}
	if !cres.Compacted {
		t.Fatalf("expected compaction: %+v", cres)
	}
	if !compact.IsContextOverflow(errString("maximum context length exceeded")) {
		t.Fatal("overflow detector")
	}

	// Refine builder
	_ = refine.Build(refine.Input{Query: "e2e", Round: 1}) // empty lessons → skip is OK, nothing to assert

	// Session events
	if err := session.AppendEvent(cfg.SlmDir(), "run-e2e", session.EventRecord{
		Phase: "init", Kind: "phase", Message: "e2e-start", Model: cfg.Model,
	}); err != nil {
		t.Fatal(err)
	}

	// --- HTTP API surface ---
	srv := server.New(h, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	mustGET := func(path string) map[string]interface{} {
		t.Helper()
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s → %d %s", path, res.StatusCode, body)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(body, &m); err != nil {
			// arrays (agents) —
			var arr []interface{}
			if json.Unmarshal(body, &arr) == nil {
				return map[string]interface{}{"_array": arr}
			}
			t.Fatalf("%s json: %v body=%s", path, err, body)
		}
		return m
	}

	stacksResp := mustGET("/api/stacks")
	if _, ok := stacksResp["stacks"]; !ok {
		t.Fatalf("stacks: %+v", stacksResp)
	}
	modelsResp := mustGET("/api/models?q=gpt&limit=8")
	if modelsResp["provider"] == nil {
		t.Fatalf("models: %+v", modelsResp)
	}
	authResp := mustGET("/api/auth")
	if authResp["provider"] == nil {
		t.Fatalf("auth: %+v", authResp)
	}
	mcpResp := mustGET("/api/mcp")
	if mcpResp["meta_tool"] != "mcp_call" {
		t.Fatalf("mcp: %+v", mcpResp)
	}
	schemaResp := mustGET("/api/config/schema")
	if fields, _ := schemaResp["fields"].([]interface{}); len(fields) < 5 {
		t.Fatalf("schema: %+v", schemaResp)
	}
	evResp := mustGET("/api/queries/run-e2e/events")
	if events, _ := evResp["events"].([]interface{}); len(events) < 1 {
		t.Fatalf("events: %+v", evResp)
	}
	agentsResp := mustGET("/api/agents")
	arr, _ := agentsResp["_array"].([]interface{})
	if len(arr) < 5 {
		t.Fatalf("agents: %+v", agentsResp)
	}
	foundEff := false
	for _, raw := range arr {
		m, _ := raw.(map[string]interface{})
		if m["id"] == "worker" && m["effective_model"] != nil {
			foundEff = true
		}
	}
	if !foundEff {
		t.Fatal("worker missing effective_model")
	}

	// PUT auth
	putBody := []byte(`{"provider":"openai","api_key":"sk-e2e-put"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/auth", bytes.NewReader(putBody))
	req.Header.Set("Content-Type", "application/json")
	httpRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = httpRes.Body.Close()
	if httpRes.StatusCode != 200 {
		t.Fatalf("put auth %d", httpRes.StatusCode)
	}
	key, ok := authstore.Get(cfg.SlmDir(), "openai")
	if !ok || key != "sk-e2e-put" {
		t.Fatalf("auth.json after PUT: %q ok=%v", key, ok)
	}

	// PATCH config new fields
	patch := []byte(`{"context_compact_engine":"auto","auto_refine":true,"session_event_log":true,"llm_retry_count":4}`)
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/config", bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	httpRes, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(httpRes.Body)
	_ = httpRes.Body.Close()
	if httpRes.StatusCode != 200 {
		t.Fatalf("put config %d %s", httpRes.StatusCode, body)
	}
	var pub map[string]interface{}
	_ = json.Unmarshal(body, &pub)
	if pub["context_compact_engine"] != "auto" {
		t.Fatalf("config patch: %+v", pub)
	}

	// POST compact
	httpRes, err = http.Post(ts.URL+"/api/compact", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = httpRes.Body.Close()
	if httpRes.StatusCode != 200 {
		t.Fatalf("compact %d", httpRes.StatusCode)
	}

	// Stack apply via API
	applyBody := []byte(`{"clear_agent_llm":true}`)
	httpRes, err = http.Post(ts.URL+"/api/stacks/omlx-local/apply", "application/json", bytes.NewReader(applyBody))
	if err != nil {
		t.Fatal(err)
	}
	applyResp, _ := io.ReadAll(httpRes.Body)
	_ = httpRes.Body.Close()
	if httpRes.StatusCode != 200 {
		t.Fatalf("stack apply %d %s", httpRes.StatusCode, applyResp)
	}
	var applied map[string]interface{}
	_ = json.Unmarshal(applyResp, &applied)
	if applied["ok"] != true {
		t.Fatalf("apply: %s", applyResp)
	}
}

func errString(s string) error { return &strErr{s} }

type strErr struct{ s string }

func (e *strErr) Error() string { return e.s }
