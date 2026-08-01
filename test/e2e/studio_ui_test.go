package e2e_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/server"
)

// TestStudioUIInteraction exercises Studio Live flows at HTTP + asset level
// (settings, docs markdown editor markers, board/deps, stop/status, SSE).
func TestStudioUIInteraction(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	cfg := config.Default(root)
	cfg.DryRun = true
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
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

	uiRoot := filepath.Join(findRepoRoot(t), "cmd", "slmcode", "ui")
	ui := os.DirFS(uiRoot)
	srv := server.New(h, ui)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// --- Static UI assets used by Live ---
	for _, path := range []string{"/", "/app.jsx", "/styles.css", "/index.html"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s → %d", path, resp.StatusCode)
		}
		if path == "/app.jsx" {
			src := string(body)
			for _, marker := range []string{
				"renderMarkdown", "DepGraph", "PROVIDER_PRESETS", "/api/runs",
				"/api/runs/stop", "/api/events", "file_change", "autoScroll",
				`id: "queries"`, "/api/queries", "openQuery", "queryDocTab",
				"openAgent", "showDebugEvents", "/api/agents/",
			} {
				if !strings.Contains(src, marker) {
					t.Fatalf("app.jsx missing %q", marker)
				}
			}
		}
		if path == "/styles.css" && !strings.Contains(string(body), "--accent") {
			t.Fatal("styles.css missing design tokens")
		}
	}

	// --- Settings: switch provider/model ---
	patch := []byte(`{"provider":"ollama","model":"qwen2.5-coder:14b","endpoint":"http://127.0.0.1:11434"}`)
	resp, err := http.NewRequest(http.MethodPut, ts.URL+"/api/config", bytes.NewReader(patch))
	if err != nil {
		t.Fatal(err)
	}
	resp.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatal(err)
	}
	cfgBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("put config %d %s", res.StatusCode, cfgBody)
	}
	var pub map[string]interface{}
	_ = json.Unmarshal(cfgBody, &pub)
	if pub["provider"] != "ollama" {
		t.Fatalf("provider=%v", pub["provider"])
	}

	// --- Docs (markdown editor path) ---
	docPut := []byte(`{"content":"# Focus\n\n**bold** and ` + "`code`" + `\n"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/docs/CONTEXT.md", bytes.NewReader(docPut))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("put doc %d", res.StatusCode)
	}
	res, err = http.Get(ts.URL + "/api/docs/CONTEXT.md")
	if err != nil {
		t.Fatal(err)
	}
	docBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(docBody), "Focus") {
		t.Fatalf("doc body=%s", docBody)
	}

	// --- Board / tasks / deps graph data ---
	taskPayload := []byte(`{"title":"Edit hello","description":"add comment","column":"ready_to_dev","role":"worker","files":["hello.go"],"depends_on":[]}`)
	res, err = http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewReader(taskPayload))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("add task %d", res.StatusCode)
	}
	task2 := []byte(`{"title":"Verify","description":"test","column":"ready_to_dev","role":"tester","files":["hello.go"],"depends_on":["T1"]}`)
	res, err = http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewReader(task2))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res, err = http.Get(ts.URL + "/api/board")
	if err != nil {
		t.Fatal(err)
	}
	boardBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(boardBody), "hello.go") {
		t.Fatalf("board missing focus files: %s", boardBody)
	}

	res, err = http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}

	// --- Queries API (Studio Queries panel) ---
	res, err = http.Get(ts.URL + "/api/queries")
	if err != nil {
		t.Fatal(err)
	}
	qBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("queries %d %s", res.StatusCode, qBody)
	}
	if strings.TrimSpace(string(qBody)) == "" || string(qBody)[0] != '[' {
		t.Fatalf("queries should return JSON array: %s", qBody)
	}

	res, err = http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// --- SSE stream briefly (Live feed) ---
	sseCtxDone := make(chan struct{})
	go func() {
		defer close(sseCtxDone)
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		_ = sc.Scan() // may timeout; presence of endpoint is enough
	}()

	// --- Stop endpoint (Live stop button) ---
	res, err = http.Post(ts.URL+"/api/runs/stop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("stop %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/api/runs/latest")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("latest %d", res.StatusCode)
	}

	select {
	case <-sseCtxDone:
	case <-time.After(3 * time.Second):
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}
