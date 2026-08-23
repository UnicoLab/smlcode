package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newTestJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	j, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// End-to-end over a real socket, not httptest.NewRequest: the whole point of
// finding 1 is what `curl http://127.0.0.1:7420/` returns, and only a real
// client exercises Handler() wiring, cookie storage and redirect-free flow.
func TestLiveStudioTokenPolicy(t *testing.T) {
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!DOCTYPE html><html><head></head><body>SPA</body></html>")}}
	s := NewWithOptions(newHarness(t), ui, Options{GenerateToken: true})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	tok := s.Token()

	get := func(c *http.Client, url string) (int, string) {
		resp, err := c.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// 1. bare curl of the shell: no token, no SPA.
	code, body := get(srv.Client(), srv.URL+"/")
	t.Logf("GET / -> %d, token in body: %v", code, strings.Contains(body, tok))
	if code != 401 || strings.Contains(body, tok) || strings.Contains(body, "SPA") {
		t.Fatalf("SHELL LEAK: %d %s", code, body)
	}
	// 2. /api/* without token.
	code, _ = get(srv.Client(), srv.URL+"/api/health")
	if code != 401 {
		t.Fatalf("api open: %d", code)
	}
	// 3. tokenised bootstrap then cookie-only.
	jarClient := srv.Client()
	jarClient.Jar = newTestJar(t)
	code, body = get(jarClient, srv.URL+"/?t="+tok)
	if code != 200 || !strings.Contains(body, "SPA") {
		t.Fatalf("bootstrap failed: %d %s", code, body)
	}
	code, _ = get(jarClient, srv.URL+"/api/health")
	if code != 200 {
		t.Fatalf("cookie auth failed: %d", code)
	}
	t.Log("live policy OK: shell gated, api gated, ?t= mints cookie, cookie authenticates")
}
