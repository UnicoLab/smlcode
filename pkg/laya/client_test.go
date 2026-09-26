package laya

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbabilityProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			State     string
			Model     string
			Questions map[string]map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.State != "task and code" || req.Model != "multilingual" || req.Questions["relevant"]["type"] != "noul" {
			t.Errorf("bad body: %+v", req)
		}
		_, _ = w.Write([]byte(`{"answers":{"relevant":{"type":"noul","noul":0.0}}}`))
	}))
	defer srv.Close()
	c, err := New(Options{Endpoint: srv.URL + "/", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Probability(context.Background(), "task and code", "useful?")
	if err != nil || p != 0 {
		t.Fatalf("p=%v err=%v", p, err)
	}
}

func TestProbabilityRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"answers":{"relevant":{"type":"noul"}}}`, `{"answers":{"relevant":{"type":"noul","noul":null}}}`,
		`{"answers":{"relevant":{"type":"noul","noul":1.1}}}`, `{"answers":{"relevant":{"type":"score","noul":0.8}}}`,
		`{"answers":{"relevant":{"type":"noul","noul":-0.1}}}`, `not json`, `{} {}`, strings.Repeat(" ", MaxResponseBytes+1),
	} {
		t.Run(body[:min(len(body), 70)], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			c, err := New(Options{Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Probability(context.Background(), "s", "q"); err == nil {
				t.Fatal("accepted invalid answer")
			}
		})
	}
}

func TestServiceFailuresAndRedirects(t *testing.T) {
	for _, status := range []int{302, 307, 401, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://invalid.invalid/private")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("secret-response"))
		}))
		c, err := New(Options{Endpoint: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Probability(context.Background(), "s", "q")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("bad error %v", err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer srv.Close()
	c, err := New(Options{Endpoint: srv.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Probability(context.Background(), "s", "q"); err == nil {
		t.Fatal("timeout missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Probability(ctx, "s", "q"); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestOptions(t *testing.T) {
	for _, ep := range []string{"", "file:///tmp/laya", "http://user:secret@host", "http://host?key=secret", "http://host/#secret", "%"} {
		if _, err := New(Options{Endpoint: ep}); err == nil {
			t.Errorf("accepted %q", ep)
		}
	}
	if _, err := New(Options{Endpoint: "http://localhost", Model: "typo"}); err == nil {
		t.Fatal("unknown model accepted")
	}
}
