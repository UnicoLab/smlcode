package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func advServer(t *testing.T, opts Options) *Server {
	t.Helper()
	return NewWithOptions(newHarness(t), nil, opts)
}

// Hosts that must NOT be accepted.
func TestAdvHostHeaderBattery(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	bad := []string{
		"127.0.0.1.evil.com", "127.0.0.1.evil.com:7420",
		"evil.com", "0.0.0.0:7420", "127.1:7420", "2130706433",
		"0x7f000001", "[::ffff:127.0.0.1]@evil.com", "localhost.evil.com",
		"evil.localhost", "sub.evil.localhost:7420",
		"", "127.0.0.1\tevil.com",
	}
	for _, h := range bad {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		r.Host = h
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("HOST ACCEPTED %q -> %d", h, rec.Code)
		}
	}
	good := []string{"127.0.0.1:7420", "localhost:7420", "[::1]:7420", "127.0.0.1", "::1"}
	for _, h := range good {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		r.Host = h
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		if rec.Code == http.StatusForbidden {
			t.Errorf("HOST OVER-BLOCKED %q", h)
		}
	}
}

// A cross-site browser request must never reach a handler.
func TestAdvCrossSiteBattery(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	type c struct{ origin, site string }
	bad := []c{
		{"http://evil.com", "cross-site"},
		{"http://evil.com", "same-site"},
		{"", "cross-site"},
		{"", "same-site"},
		{"null", ""},
		{"http://127.0.0.1:7420.evil.com", "cross-site"},
		{"http://evil.com", ""},
		{"http://localhost:5173", ""}, // dev origin without --dev-cors
	}
	for _, cc := range bad {
		r := withOrigin(newAPIRequest(http.MethodPost, "/api/run", strings.NewReader("{}")), cc.origin, cc.site)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("CROSS-SITE ACCEPTED origin=%q site=%q -> %d %s", cc.origin, cc.site, rec.Code, rec.Body.String())
		}
	}
}

// Token must be required everywhere under /api, in every method.
func TestAdvTokenBattery(t *testing.T) {
	s := advServer(t, Options{Token: "sekrit"})
	paths := []string{
		"/api/health", "/api/status", "/api/config", "/api/board", "/api/events",
		"/api/review/pending", "/api/workspace/tree", "/api/workspace/file?path=go.mod",
		"/api/queries", "/api/archives", "/api/blocks", "/api/readiness",
		// /api/calibration reports the measured endpoint, the model's context
		// window and the budgets in force — an inventory of the local model
		// setup, and no more public than the config it derives from.
		"/api/calibration",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("NO-TOKEN ACCEPTED %s -> %d", p, rec.Code)
		}
	}
	// wrong places / wrong values
	for _, h := range [][2]string{
		{"X-SLMCode-Token", "wrong"},
		{"Authorization", "Bearer wrong"},
		{"Cookie", "t=sekrit"},
		{"X-Forwarded-Token", "sekrit"},
	} {
		r := newAPIRequest(http.MethodGet, "/api/health", nil)
		r.Header.Set(h[0], h[1])
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("BAD TOKEN ACCEPTED via %s -> %d", h[0], rec.Code)
		}
	}
}

// Unauthenticated requests must not reveal whether a path exists.
func TestAdvNoUnauthPathOracle(t *testing.T) {
	s := advServer(t, Options{Token: "sekrit"})
	root := s.rootDir()
	_ = os.WriteFile(filepath.Join(root, "exists.txt"), []byte("x"), 0o600)
	a := httptest.NewRecorder()
	s.Handler().ServeHTTP(a, newAPIRequest(http.MethodGet, "/api/workspace/file?path=exists.txt", nil))
	b := httptest.NewRecorder()
	s.Handler().ServeHTTP(b, newAPIRequest(http.MethodGet, "/api/workspace/file?path=nope.txt", nil))
	if a.Code != b.Code || a.Body.String() != b.Body.String() {
		t.Errorf("UNAUTH PATH ORACLE: %d/%q vs %d/%q", a.Code, a.Body.String(), b.Code, b.Body.String())
	}
}

// Path traversal on every path-taking endpoint.
func TestAdvPathTraversalBattery(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0o600)
	_ = os.Symlink(outside, filepath.Join(s.rootDir(), "sym"))

	bad := []string{
		"../../etc/passwd", "..%2f..%2fetc%2fpasswd", "....//....//etc/passwd",
		"/etc/passwd", "sym/secret.txt", "./../" + filepath.Base(outside) + "/secret.txt",
		"a/../../../etc/passwd", "\\..\\..\\etc\\passwd",
	}
	for _, p := range bad {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/workspace/file?path="+p, nil))
		if strings.Contains(rec.Body.String(), "TOPSECRET") || strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("TRAVERSAL %q -> %d %s", p, rec.Code, rec.Body.String())
		}
	}
	// review API ids
	for _, id := range []string{"../../../etc/passwd", "..%2fx.patch.json", "a/b.patch.json", "../x.patch.json"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/review/pending/"+id, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("REVIEW ID TRAVERSAL %q -> %d %s", id, rec.Code, rec.Body.String())
		}
	}
	// archives
	for _, n := range []string{"../../../etc/passwd", "..%2f..%2fgo.mod"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/archives/"+n, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("ARCHIVE TRAVERSAL %q -> %d", n, rec.Code)
		}
	}
}

// Shutdown must actually stop in-flight work, for BOTH run and resume.
func TestAdvShutdownWaitsForInFlightRun(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	started := make(chan struct{})
	finished := make(chan struct{})
	s.runWG.Add(1)
	go func() {
		defer s.runWG.Done()
		close(started)
		<-s.runContext().Done() // a run that unwinds when canceled
		time.Sleep(50 * time.Millisecond)
		close(finished)
	}()
	<-started
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Shutdown returned while the run goroutine was still working")
	}
}

// Resume must be cancellable by Shutdown (it used to use context.Background).
func TestAdvResumeUsesCancellableContext(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "ctx := context.Background()\n\t\tres, err := s.h.Resume") {
		t.Fatal("resume run goroutine uses context.Background(); Shutdown cannot cancel it")
	}
}
