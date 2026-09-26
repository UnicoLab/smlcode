package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/laya"
)

func TestLayaRerankBoundsStableOrderAndScores(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ State string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		p := 0.5
		if strings.Contains(req.State, "best candidate") {
			p = 0.9
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"relevant": map[string]any{"type": "noul", "noul": p}}})
	}))
	defer srv.Close()
	hits := make([]Scored, 12)
	for i := range hits {
		hits[i] = Scored{Chunk: Chunk{ID: string(rune('a' + i)), Text: "candidate"}, Score: 1 - float64(i)/20}
	}
	hits[3].Chunk.Text = "best candidate"
	before := append([]Scored(nil), hits...)
	// This verifies ordering, not latency. Allow race instrumentation and a busy
	// CI host; the separate shared-deadline test enforces timeout behavior.
	ranked, err := rerankLaya(context.Background(), "task", hits, laya.Options{Endpoint: srv.URL, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != LayaCandidateLimit || ranked[0] != hits[3] || ranked[1] != hits[0] || !reflect.DeepEqual(ranked[8:], hits[8:]) || !reflect.DeepEqual(hits, before) {
		t.Fatalf("bad rerank: calls=%d ranked=%+v", calls.Load(), ranked)
	}
}

func TestLayaRetrievalFallbackAndOptIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("## first\nworker editing Go code with ws_edit: original candidate\n\n## second\nworker editing Go code with ws_edit: preferred candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ForceLexical: true, TopK: 1, MinScore: 0.01}
	baseline, mode, err := RetrieveForQuery(context.Background(), dir, "worker editing Go code", cfg)
	if err != nil || mode != "lexical" {
		t.Fatalf("%s %v", mode, err)
	}
	var calls atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() && calls.Load() == 2 {
			w.WriteHeader(503)
			return
		}
		var req struct{ State string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		p := 0.1
		if strings.Contains(req.State, "preferred") {
			p = 0.9
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"relevant": map[string]any{"type": "noul", "noul": p}}})
	}))
	defer srv.Close()
	cfg.Laya = laya.Options{Endpoint: srv.URL, Timeout: 30 * time.Second}
	body, mode, err := RetrieveForQuery(context.Background(), dir, "worker editing Go code", cfg)
	if err != nil || mode != "lexical+laya" || !strings.Contains(body, "preferred") || strings.Contains(body, "original") {
		t.Fatalf("%s %s %v", body, mode, err)
	}
	calls.Store(0)
	fail.Store(true)
	body, mode, err = RetrieveForQuery(context.Background(), dir, "worker editing Go code", cfg)
	if err == nil || mode != "lexical" || body != baseline {
		t.Fatalf("fallback differs: %s %s %v", body, mode, err)
	}
	cfg.MinScore = 1
	calls.Store(0)
	_, _, _ = RetrieveForQuery(context.Background(), dir, "unrelated", cfg)
	if calls.Load() != 0 {
		t.Fatal("scored candidates below similarity floor")
	}
}

func TestLayaRerankRejectsLongInputsAndSharesDeadline(t *testing.T) {
	hits := []Scored{{Chunk: Chunk{Text: "a"}}, {Chunk: Chunk{Text: "b"}}}
	if _, err := rerankLaya(context.Background(), strings.Repeat("long task ", 1000), hits, laya.Options{Endpoint: "http://127.0.0.1:1"}); err == nil || !strings.Contains(err.Error(), "tokens") {
		t.Fatalf("long query: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(40 * time.Millisecond):
			_, _ = w.Write([]byte(`{"answers":{"relevant":{"type":"noul","noul":0.8}}}`))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := rerankLaya(context.Background(), "task", hits, laya.Options{Endpoint: srv.URL, Timeout: 60 * time.Millisecond})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("deadline not shared: %v", err)
	}
}
