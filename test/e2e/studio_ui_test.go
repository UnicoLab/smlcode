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

	// --- Fetch index.html and discover asset paths ---
	htmlPath, jsPath, cssPath := discoverAssets(t, ts.URL)

	// --- Static UI assets used by Live ---
	for _, entry := range []struct {
		path    string
		markers []string
	}{
		{"/", []string{"SLMCode Studio", "id=\"root\"", "<title>"}},
		{htmlPath, []string{"SLMCode Studio", "id=\"root\"", "<title>"}},
		{jsPath, []string{
			"/api/events", "/runs", "/runs/stop", "/runs/latest",
			"/agents", "/board", "/config", "/skills",
			"/pipeline", "/tasks", "/stacks",
			"/health", "/auth", "/mcp",
			"pipeline", "autoScroll",
		}},
		{cssPath, []string{
			"--color-surface",
		}},
	} {
		resp, err := http.Get(ts.URL + entry.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s → %d", entry.path, resp.StatusCode)
		}
		if len(entry.markers) > 0 {
			src := string(body)
			for _, marker := range entry.markers {
				if !strings.Contains(src, marker) {
					t.Fatalf("%s missing %q", entry.path, marker)
				}
			}
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

// discoverAssets fetches the index page and extracts paths to JS and CSS bundles.
func discoverAssets(t *testing.T, baseURL string) (htmlPath, jsPath, cssPath string) {
	t.Helper()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)

	// Extract JS bundle path from <script type="module" ... src="...">
	idx := strings.Index(html, `<script type="module"`)
	if idx >= 0 {
		rest := html[idx:]
		start := strings.Index(rest, `src="`)
		if start >= 0 {
			start += 5
			end := strings.Index(rest[start:], `"`)
			if end >= 0 {
				jsPath = rest[start : start+end]
			}
		}
	}

	// Extract local CSS path: find <link rel="stylesheet" ... href="/assets/...">
	// Skip the Google Fonts stylesheet (which uses href before rel).
	search := html
	for {
		idx := strings.Index(search, `rel="stylesheet"`)
		if idx < 0 {
			break
		}
		// Look for href= within this link element's scope
		seg := search[max(0, idx-200):min(len(search), idx+200)]
		hrefIdx := strings.Index(seg, `href="`)
		if hrefIdx >= 0 {
			hrefStart := hrefIdx + 6
			hrefEnd := strings.Index(seg[hrefStart:], `"`)
			if hrefEnd >= 0 {
				candidate := seg[hrefStart : hrefStart+hrefEnd]
				if strings.HasPrefix(candidate, "/assets/") {
					cssPath = candidate
					break
				}
			}
		}
		search = search[idx+len(`rel="stylesheet"`):]
	}

	// Try /index.html as well
	htmlPath = "/index.html"
	if jsPath == "" {
		t.Fatal("could not discover JS bundle path from index.html")
	}
	if cssPath == "" {
		t.Fatal("could not discover CSS bundle path from index.html")
	}

	return htmlPath, jsPath, cssPath
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
