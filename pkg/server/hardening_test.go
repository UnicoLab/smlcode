package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/permissions"
)

func newHarness(t *testing.T) *harness.Harness {
	t.Helper()
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	return h
}

// ── Defect 1: CORS / origin / host / token ──

func TestNoWildcardCORS(t *testing.T) {
	s := New(newHarness(t), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/health", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("same-origin response must not carry CORS headers, got %q", got)
	}
}

func TestCrossOriginRequestRejected(t *testing.T) {
	s := New(newHarness(t), nil)
	for _, tc := range []struct {
		name, origin, fetchSite, method, path string
	}{
		{"post-run", "https://evil.example", "cross-site", http.MethodPost, "/api/runs"},
		{"put-auth", "https://evil.example", "cross-site", http.MethodPut, "/api/auth"},
		{"put-config", "https://evil.example", "cross-site", http.MethodPut, "/api/config"},
		{"read-file", "https://evil.example", "cross-site", http.MethodGet, "/api/workspace/file?path=go.mod"},
		{"no-origin-cross-site", "", "cross-site", http.MethodGet, "/api/workspace/file?path=go.mod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withOrigin(newAPIRequest(tc.method, tc.path, strings.NewReader("{}")), tc.origin, tc.fetchSite)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNonLoopbackHostRejected(t *testing.T) {
	s := New(newHarness(t), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil) // Host: example.com
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DNS-rebinding host accepted: status=%d", rec.Code)
	}
}

func TestSameOriginAllowed(t *testing.T) {
	s := New(newHarness(t), nil)
	req := withOrigin(newAPIRequest(http.MethodGet, "/api/health", nil), "http://"+loopbackHost, "same-origin")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDevCORSAllowsViteOriginOnly(t *testing.T) {
	s := NewWithOptions(newHarness(t), nil, Options{DevCORS: true})
	req := withOrigin(newAPIRequest(http.MethodGet, "/api/health", nil), "http://127.0.0.1:5173", "cross-site")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("vite origin rejected: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("ACAO=%q", got)
	}

	req = withOrigin(newAPIRequest(http.MethodGet, "/api/health", nil), "http://127.0.0.1:9999", "cross-site")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other loopback port must not be allowed: %d", rec.Code)
	}
}

func TestSessionTokenRequired(t *testing.T) {
	s := NewWithOptions(newHarness(t), nil, Options{GenerateToken: true})
	if !s.AuthEnabled() || s.Token() == "" {
		t.Fatal("token not generated")
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless request accepted: %d", rec.Code)
	}

	for _, apply := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set(TokenHeader, s.Token()) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.Token()) },
	} {
		req := newAPIRequest(http.MethodGet, "/api/health", nil)
		apply(req)
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("valid token rejected: %d %s", rec.Code, rec.Body.String())
		}
	}

	// EventSource cannot set headers — the query parameter must work.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/health?t="+s.Token(), nil))
	if rec.Code != 200 {
		t.Fatalf("query token rejected: %d", rec.Code)
	}

	// A wrong token must not pass.
	req := newAPIRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set(TokenHeader, "deadbeef")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token accepted: %d", rec.Code)
	}
}

func TestNoAuthEscapeHatch(t *testing.T) {
	s := NewWithOptions(newHarness(t), nil, Options{GenerateToken: true, NoAuth: true})
	if s.AuthEnabled() {
		t.Fatal("NoAuth did not disable the token")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestTokenInjectedIntoIndexHTML(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!DOCTYPE html><html><head><title>t</title></head><body></body></html>")},
	}
	s := NewWithOptions(newHarness(t), ui, Options{GenerateToken: true})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="`+TokenMetaName+`"`) || !strings.Contains(body, s.Token()) {
		t.Fatalf("token meta not injected: %s", body)
	}
	// The rest of the document must survive the rewrite, and Content-Length must
	// be dropped — it described the pre-injection body.
	if !strings.Contains(body, "</html>") || !strings.Contains(body, "<title>t</title>") {
		t.Fatalf("document truncated by injection: %s", body)
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("stale Content-Length %q after injection", cl)
	}

	// With auth off the document is served untouched.
	plain := New(newHarness(t), ui)
	rec = httptest.NewRecorder()
	plain.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), TokenMetaName) {
		t.Fatal("meta injected with auth disabled")
	}
}

func TestServerURLCarriesToken(t *testing.T) {
	s := NewWithOptions(newHarness(t), nil, Options{GenerateToken: true})
	if got := s.URL("127.0.0.1:7420"); got != "http://127.0.0.1:7420/?t="+s.Token() {
		t.Fatalf("url=%s", got)
	}
	plain := New(newHarness(t), nil)
	if got := plain.URL(":7420"); got != "http://127.0.0.1:7420/" {
		t.Fatalf("url=%s", got)
	}
}

// ── Defect 3: path traversal + symlink escape ──

func TestWorkspacePathPrefixSibling(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "proj-secrets")
	mustMkdir(t, root)
	mustMkdir(t, sibling)
	secret := filepath.Join(sibling, "creds.env")
	mustWrite(t, secret, "API_KEY=hunter2")

	// The old check was strings.HasPrefix(full, root) with no separator.
	if _, err := resolveWorkspacePath(root, "../proj-secrets/creds.env"); err == nil {
		t.Fatal("sibling directory sharing the root prefix was accepted")
	}
	if _, err := resolveWorkspacePath(root, "../../etc/passwd"); err == nil {
		t.Fatal("parent traversal accepted")
	}
	if _, err := resolveWorkspacePath(root, "/etc/passwd"); err == nil {
		t.Fatal("absolute path accepted")
	}
}

func TestWorkspacePathSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, root)
	mustMkdir(t, outside)
	mustWrite(t, filepath.Join(outside, "secret.txt"), "nope")

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveWorkspacePath(root, "escape/secret.txt"); err == nil {
		t.Fatal("symlink escape accepted")
	}

	// In-tree paths still resolve.
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	if _, err := resolveWorkspacePath(root, "main.go"); err != nil {
		t.Fatalf("in-tree read rejected: %v", err)
	}
	// Not-yet-existing in-tree paths (review-queue writes) resolve too.
	if _, err := resolveWorkspacePath(root, "new/dir/file.go"); err != nil {
		t.Fatalf("new in-tree path rejected: %v", err)
	}
}

func TestWorkspaceFileEndpointRejectsEscape(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	outside := filepath.Join(filepath.Dir(h.Config.Root), "outside.txt")
	mustWrite(t, outside, "secret")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/workspace/file?path=../outside.txt", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/workspace/tree?path=../", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tree status=%d", rec.Code)
	}
}

// ── Defect 8: dot-entries in the file tree ──

func TestWorkspaceTreeShowsDotEntries(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	mustMkdir(t, filepath.Join(h.Config.Root, ".github"))
	mustMkdir(t, filepath.Join(h.Config.Root, ".git"))

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/workspace/tree", nil))
	var out struct {
		Entries []struct {
			Name   string `json:"name"`
			Hidden bool   `json:"hidden"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range out.Entries {
		names[e.Name] = true
	}
	if !names[".slmcode"] {
		t.Fatalf("review queue dir hidden; entries=%v", names)
	}
	if !names[".github"] {
		t.Fatalf(".github hidden; entries=%v", names)
	}
	if names[".git"] {
		t.Fatal(".git must stay hidden")
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/workspace/tree?hidden=false", nil))
	out.Entries = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, e := range out.Entries {
		if strings.HasPrefix(e.Name, ".") {
			t.Fatalf("hidden=false still returned %s", e.Name)
		}
	}
}

// ── Defect 5: SSE ids, Last-Event-ID replay, gap markers ──

func TestSSEAssignsIDsAndResumes(t *testing.T) {
	s := New(newHarness(t), nil)
	for i := 1; i <= 5; i++ {
		s.emit(orchestrator.Event{Phase: "execute", Kind: "output", Message: fmt.Sprintf("e%d", i), Time: time.Now()})
	}

	body := readSSE(t, s, "")
	if !strings.Contains(body, "event: connected") {
		t.Fatal("no connected frame")
	}
	for i := 1; i <= 5; i++ {
		if !strings.Contains(body, fmt.Sprintf("id: %d\n", i)) {
			t.Fatalf("missing id %d in:\n%s", i, body)
		}
	}

	// Reconnect from id 3 — only 4 and 5 should come back.
	body = readSSE(t, s, "3")
	if strings.Contains(body, `"e1"`) || strings.Contains(body, `"e3"`) {
		t.Fatalf("replayed already-seen events:\n%s", body)
	}
	if !strings.Contains(body, "id: 4\n") || !strings.Contains(body, "id: 5\n") {
		t.Fatalf("missing resumed events:\n%s", body)
	}
	if strings.Contains(body, "event: gap") {
		t.Fatalf("unexpected gap:\n%s", body)
	}
}

func TestSSEEmitsGapWhenBufferRolled(t *testing.T) {
	s := New(newHarness(t), nil)
	for i := 0; i < 3; i++ {
		s.emit(orchestrator.Event{Phase: "execute", Message: "x", Time: time.Now()})
	}
	// Simulate a buffer that has rolled past the client's resume point.
	s.mu.Lock()
	s.events = s.events[2:]
	s.mu.Unlock()

	body := readSSE(t, s, "0")
	// Resuming from id 0 means "give me everything", so no gap is claimed.
	if strings.Contains(body, "event: gap") {
		t.Fatalf("gap claimed for a full replay:\n%s", body)
	}
	body = readSSE(t, s, "1")
	if !strings.Contains(body, "event: gap") {
		t.Fatalf("no gap marker after buffer roll:\n%s", body)
	}
}

func TestEmitEvictsTokenEventsFirst(t *testing.T) {
	buf := []seqEvent{
		{Seq: 1, Event: orchestrator.Event{Kind: "phase"}},
		{Seq: 2, Event: orchestrator.Event{Kind: tokenKind}},
		{Seq: 3, Event: orchestrator.Event{Kind: "phase"}},
	}
	out := evictOldest(buf)
	if len(out) != 2 || out[0].Seq != 1 || out[1].Seq != 3 {
		t.Fatalf("wrong eviction: %+v", out)
	}
	// With no token events it falls back to oldest-first.
	out = evictOldest([]seqEvent{{Seq: 7, Event: orchestrator.Event{Kind: "phase"}}, {Seq: 8}})
	if len(out) != 1 || out[0].Seq != 8 {
		t.Fatalf("wrong fallback eviction: %+v", out)
	}
}

// readSSE runs one SSE request with a cancelled-after-write context and returns
// the emitted frames.
func readSSE(t *testing.T, s *Server, lastEventID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := newAPIRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sse handler did not return")
	}
	return rec.Body.String()
}

// ── Defect 4: timeouts, shutdown, data races ──

func TestHTTPServerHasTimeouts(t *testing.T) {
	s := New(newHarness(t), nil)
	srv := s.httpServer("127.0.0.1:0")
	if srv.ReadHeaderTimeout == 0 {
		t.Fatal("ReadHeaderTimeout unset (gosec G114 / Slowloris)")
	}
	if srv.IdleTimeout == 0 {
		t.Fatal("IdleTimeout unset")
	}
	if srv.WriteTimeout != 0 {
		t.Fatal("WriteTimeout must stay 0 so SSE streams are not cut off")
	}
}

func TestShutdownClosesStreamsAndCancelsRunContext(t *testing.T) {
	s := New(newHarness(t), nil)
	runCtx := s.runContext()
	if runCtx.Err() != nil {
		t.Fatal("run context already cancelled")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runCtx.Err() == nil {
		t.Fatal("Shutdown did not cancel the run context")
	}
	// A stream started after shutdown returns immediately instead of hanging.
	_ = readSSE(t, s, "")
}

// TestConfigReadDuringRunStartIsRaceFree hammers GET /api/config while a run is
// starting. Run with -race: the per-run Mode/Specialist/PinnedSkills override
// used to be an unsynchronised write against every config reader.
func TestConfigReadDuringRunStartIsRaceFree(t *testing.T) {
	h := newHarness(t)
	// Keep the run from touching the network/filesystem for long.
	h.Config.DryRun = true
	h.Config.Permission = permissions.ModeDryRun
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, path := range []string{"/api/config", "/api/health", "/api/status"} {
					rec := httptest.NewRecorder()
					s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, path, nil))
				}
			}
		}()
	}

	// Exercise the override apply/restore path directly and through the API.
	for i := 0; i < 50; i++ {
		q, saved := s.applyRunOptions(runOptions{
			Mode: "specialist", Specialist: "worker", Skills: []string{"atomic-coding"},
		}, "do a thing")
		if !strings.Contains(q, "@skill:atomic-coding") {
			t.Fatalf("skill pin not appended: %q", q)
		}
		s.restoreRunOptions(saved)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"query":"noop","specialist":"worker"}`)))
	if rec.Code != 200 && rec.Code != http.StatusConflict {
		t.Fatalf("start run status=%d body=%s", rec.Code, rec.Body.String())
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Overrides must not leak into the persisted config once restored.
	var got map[string]any
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/config", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] == nil {
		t.Fatalf("config unreadable: %s", rec.Body.String())
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
