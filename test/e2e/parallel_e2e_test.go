package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/server"
)

// TestE2EParallelizationAndAPI verifies the full Studio API surface,
// parallel execution config, blocks/pipelines, file browser, and HITL.
func TestE2EParallelizationAndAPI(t *testing.T) {
	root := t.TempDir()

	// Seed a simple Go project
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "internal", "calc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "internal", "calc", "math.go"), []byte("package calc\n\nfunc Mul(a, b int) int { return a * b }\n"), 0o644)

	cfg := config.Default(root)
	cfg.MaxParallel = 4
	cfg.DryRun = true // no real LLM calls
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg

	srv := server.New(h, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) *http.Response {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}
	post := func(path string, body interface{}) *http.Response {
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	put := func(path string, body interface{}) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("PUT", ts.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		return resp
	}
	readJSON := func(resp *http.Response, dest interface{}) {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 {
			t.Fatalf("%s %d: %s", resp.Request.URL.Path, resp.StatusCode, string(body))
		}
		if err := json.Unmarshal(body, dest); err != nil {
			t.Fatalf("json decode %s: %v\n%s", resp.Request.URL.Path, err, string(body))
		}
	}

	// ── 1. Health ──
	t.Run("health", func(t *testing.T) {
		resp := get("/api/health")
		defer func() { _ = resp.Body.Close() }()
		var h map[string]interface{}
		readJSON(resp, &h)
		if v, _ := h["ok"].(bool); !v {
			t.Fatal("health not ok")
		}
		if v, _ := h["version"].(string); v == "" {
			t.Fatal("missing version")
		}
	})

	// ── 2. Config: verify parallelization fields ──
	t.Run("config", func(t *testing.T) {
		resp := get("/api/config")
		defer func() { _ = resp.Body.Close() }()
		var c map[string]interface{}
		readJSON(resp, &c)
		// Verify parallelization config fields exist
		for _, key := range []string{"max_parallel", "plan_approve", "clarify_mode", "continue_ask", "escalate_ask", "auto_approve"} {
			if _, ok := c[key]; !ok {
				t.Errorf("config missing field: %s", key)
			}
		}
		// Verify max_parallel is 4 (what we set)
		if mp, _ := c["max_parallel"].(float64); int(mp) != 4 {
			t.Errorf("max_parallel = %v, want 4", c["max_parallel"])
		}
	})

	// ── 3. Agents ──
	t.Run("agents", func(t *testing.T) {
		resp := get("/api/agents")
		defer func() { _ = resp.Body.Close() }()
		var agents []map[string]interface{}
		readJSON(resp, &agents)
		if len(agents) < 10 {
			t.Fatalf("expected >=10 agents, got %d", len(agents))
		}
		// Check agent detail includes system_prompt
		resp2 := get("/api/agents/worker")
		defer func() { _ = resp2.Body.Close() }()
		var agent map[string]interface{}
		readJSON(resp2, &agent)
		if sp, _ := agent["system_prompt"].(string); sp == "" {
			t.Error("agent detail missing system_prompt")
		}
	})

	// ── 4. Pipeline ──
	t.Run("pipeline", func(t *testing.T) {
		resp := get("/api/pipeline")
		defer func() { _ = resp.Body.Close() }()
		var pv map[string]interface{}
		readJSON(resp, &pv)
		if _, ok := pv["config"]; !ok {
			t.Fatal("pipeline missing config")
		}
	})

	// ── 5. Blocks catalog ──
	t.Run("blocks", func(t *testing.T) {
		resp := get("/api/blocks")
		defer func() { _ = resp.Body.Close() }()
		var bl map[string]interface{}
		readJSON(resp, &bl)
		if blocks, _ := bl["blocks"].([]interface{}); len(blocks) == 0 {
			t.Error("zero blocks returned")
		}
	})

	// ── 6. Workspace file tree ──
	t.Run("workspace_tree", func(t *testing.T) {
		// Root
		resp := get("/api/workspace/tree")
		defer func() { _ = resp.Body.Close() }()
		var tree map[string]interface{}
		readJSON(resp, &tree)
		entries, _ := tree["entries"].([]interface{})
		if len(entries) == 0 {
			t.Fatal("workspace tree empty")
		}
		// Should contain main.go and go.mod (not hidden dirs)
		found := false
		for _, e := range entries {
			entry := e.(map[string]interface{})
			if name, _ := entry["name"].(string); name == "main.go" {
				found = true
				if isDir, _ := entry["is_dir"].(bool); isDir {
					t.Error("main.go should not be a directory")
				}
			}
		}
		if !found {
			t.Error("main.go not found in workspace tree")
		}

		// Subdirectory
		resp2 := get("/api/workspace/tree?path=internal")
		defer func() { _ = resp2.Body.Close() }()
		var tree2 map[string]interface{}
		readJSON(resp2, &tree2)
		entries2, _ := tree2["entries"].([]interface{})
		if len(entries2) == 0 {
			t.Error("internal dir should have entries")
		}
	})

	// ── 7. Workspace file content ──
	t.Run("workspace_file", func(t *testing.T) {
		resp := get("/api/workspace/file?path=main.go")
		defer func() { _ = resp.Body.Close() }()
		var f map[string]interface{}
		readJSON(resp, &f)
		content, _ := f["content"].(string)
		if !strings.Contains(content, "package main") {
			t.Error("file content missing package main")
		}
		if size, _ := f["size"].(float64); size < 10 {
			t.Error("file size too small")
		}

		// Path traversal blocked
		resp2 := get("/api/workspace/file?path=../../../etc/passwd")
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != 403 && resp2.StatusCode != 404 {
			t.Errorf("path traversal not blocked: %d", resp2.StatusCode)
		}
	})

	// ── 8. Docs ──
	t.Run("docs", func(t *testing.T) {
		resp := get("/api/docs")
		defer func() { _ = resp.Body.Close() }()
		var docs []string
		readJSON(resp, &docs)
		if len(docs) == 0 {
			t.Error("no docs returned")
		}
	})

	// ── 9. Stacks ──
	t.Run("stacks", func(t *testing.T) {
		resp := get("/api/stacks")
		defer func() { _ = resp.Body.Close() }()
		var stacks map[string]interface{}
		readJSON(resp, &stacks)
		t.Logf("stacks: %v", stacks)
	})

	// ── 10. Config update (HITL fields) ──
	t.Run("config_update", func(t *testing.T) {
		resp := put("/api/config", map[string]interface{}{
			"plan_approve": "ask",
			"clarify_mode": "ask",
			"max_parallel": 6,
		})
		defer func() { _ = resp.Body.Close() }()
		var c map[string]interface{}
		readJSON(resp, &c)
		if pa, _ := c["plan_approve"].(string); pa != "ask" {
			t.Errorf("plan_approve = %s, want ask", pa)
		}
		if cm, _ := c["clarify_mode"].(string); cm != "ask" {
			t.Errorf("clarify_mode = %s, want ask", cm)
		}
	})

	// ── 11. HITL endpoints ──
	t.Run("hitl_endpoints", func(t *testing.T) {
		endpoints := []string{
			"/api/clarify/pending",
			"/api/plan/pending",
			"/api/continue/pending",
			"/api/escalate/pending",
			"/api/shell/pending",
		}
		for _, ep := range endpoints {
			resp := get(ep)
			defer func() { _ = resp.Body.Close() }()
			var pending map[string]interface{}
			readJSON(resp, &pending)
			if p, _ := pending["pending"].(bool); p {
				t.Logf("%s has pending ask (ok if test ran before)", ep)
			}
		}
	})

	// ── 12. Board / Tasks ──
	t.Run("board", func(t *testing.T) {
		resp := get("/api/board")
		defer func() { _ = resp.Body.Close() }()
		var board map[string]interface{}
		readJSON(resp, &board)
		if _, ok := board["tasks"]; !ok {
			t.Error("board missing tasks")
		}
	})

	// ── 13. Skills ──
	t.Run("skills", func(t *testing.T) {
		resp := get("/api/skills")
		defer func() { _ = resp.Body.Close() }()
		var skills []interface{}
		readJSON(resp, &skills)
		t.Logf("%d skills loaded", len(skills))
	})

	// ── 14. Verify blocks work with parallel config ──
	t.Run("blocks_with_parallel", func(t *testing.T) {
		// Apply go pack using correct endpoint
		resp := post("/api/packs/go/apply", map[string]interface{}{
			"materialize_agents": false,
		})
		defer func() { _ = resp.Body.Close() }()
		var result map[string]interface{}
		readJSON(resp, &result)
		// pipeline_id is nested under "result" key
		if r, ok := result["result"].(map[string]interface{}); ok {
			if pid, _ := r["pipeline_id"].(string); pid != "" {
				t.Logf("pack applied, pipeline: %s", pid)
			} else {
				t.Error("pack apply returned no pipeline_id")
			}
		} else {
			t.Error("pack apply result missing 'result' key")
		}
	})

	// ── 15. Status ──
	t.Run("status", func(t *testing.T) {
		resp := get("/api/status")
		defer func() { _ = resp.Body.Close() }()
		var status map[string]interface{}
		readJSON(resp, &status)
		if text, _ := status["text"].(string); text == "" {
			t.Error("status text empty")
		}
	})

	// ── 16. Archives ──
	t.Run("archives", func(t *testing.T) {
		resp := get("/api/archives")
		defer func() { _ = resp.Body.Close() }()
		var archives []interface{}
		readJSON(resp, &archives)
		t.Logf("%d archives", len(archives))
	})
}
