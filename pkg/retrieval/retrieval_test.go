package retrieval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLexicalRanksRelevantSummaryAboveIrrelevant(t *testing.T) {
	chunks := []Chunk{
		{
			ID: "a", Source: "summary", Query: "rename Hello to Greet in greet.go",
			Text: "## Outcome\n- Renamed Hello → Greet in pkg/greet/greet.go\n- Updated return string\n",
		},
		{
			ID: "b", Source: "summary", Query: "add docker compose for postgres",
			Text: "## Outcome\n- Added docker-compose.yml with postgres:16\n- Exposed port 5432\n",
		},
		{
			ID: "c", Source: "summary", Query: "fix CSS theme colors",
			Text: "## Outcome\n- Updated styles.css accent tokens for light/dark themes\n",
		},
	}
	r := &Retriever{Embedder: &FakeEmbedder{}, TopK: 3}
	hits, err := r.Search(context.Background(), "continue the greet rename / symbol refactor", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Chunk.ID != "a" {
		t.Fatalf("expected rename summary first, got %s (%s)", hits[0].Chunk.ID, hitScores(hits))
	}
	if len(hits) > 1 && hits[0].Score <= hits[1].Score {
		t.Fatalf("relevant should outrank: %s", hitScores(hits))
	}
}

func TestLexicalFallbackWhenEmbedderFails(t *testing.T) {
	chunks := []Chunk{
		{ID: "mem", Source: "memory", Text: "Prefer ws_edit for tiny SLM patches. Never invent main.go."},
		{ID: "css", Source: "summary", Text: "Changed button border-radius in styles.css"},
	}
	r := &Retriever{Embedder: &FakeEmbedder{Fail: true}, TopK: 2}
	hits, err := r.Search(context.Background(), "how should workers edit files for SLMs?", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Chunk.ID != "mem" {
		t.Fatalf("expected memory hit via lexical fallback, got %s", hitScores(hits))
	}
}

func TestCollectAndRetrieveForQuery(t *testing.T) {
	slm := t.TempDir()
	_ = os.MkdirAll(filepath.Join(slm, "queries", "run-1"), 0o755)
	_ = os.MkdirAll(filepath.Join(slm, "queries", "run-2"), 0o755)
	_ = os.MkdirAll(filepath.Join(slm, "summaries"), 0o755)
	_ = os.WriteFile(filepath.Join(slm, "queries", "run-1", "QUERY.md"),
		[]byte("# Query\n\nrename Hello to Greet\n"), 0o644)
	_ = os.WriteFile(filepath.Join(slm, "queries", "run-1", "summary.md"),
		[]byte("# Query summary\n\n## Outcome\n- Renamed Hello to Greet in greet.go\n"), 0o644)
	_ = os.WriteFile(filepath.Join(slm, "queries", "run-2", "QUERY.md"),
		[]byte("# Query\n\nsetup nginx reverse proxy\n"), 0o644)
	_ = os.WriteFile(filepath.Join(slm, "queries", "run-2", "summary.md"),
		[]byte("# Query summary\n\n## Outcome\n- Configured nginx for TLS termination\n"), 0o644)
	_ = os.WriteFile(filepath.Join(slm, "MEMORY.md"),
		[]byte("# Memory\n\n- Project uses Go modules\n"), 0o644)

	body, mode, err := RetrieveForQuery(context.Background(), slm, "finish the Greet rename work", Config{
		Enabled: false, TopK: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local" {
		t.Fatalf("mode=%s want local", mode)
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "greet") && !strings.Contains(body, "Hello") {
		t.Fatalf("expected rename knowledge in retrieval:\n%s", body)
	}
}

func TestLocalEmbedderRanksSemanticallyRelated(t *testing.T) {
	chunks := []Chunk{
		{
			ID: "related", Source: "summary", Query: "symbol refactor greeting",
			Text: "Completed renaming the Hello helper to Greet across the greet package and call sites.",
		},
		{
			ID: "unrelated", Source: "summary", Query: "kubernetes ingress tls",
			Text: "Configured nginx ingress with cert-manager for TLS termination on staging.",
		},
		{
			ID: "noise", Source: "summary", Query: "css theme tokens",
			Text: "Updated button border-radius and slate palette variables in styles.css.",
		},
	}
	r := &Retriever{Embedder: NewLocalEmbedder(), TopK: 3}
	// Paraphrase with little exact token overlap vs "Hello"/"Greet" titles —
	// n-gram hashing should still prefer the rename summary.
	hits, err := r.Search(context.Background(), "continue the greeting function rename refactor", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Chunk.ID != "related" {
		t.Fatalf("expected related first, got %s", hitScores(hits))
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("related should outrank: %s", hitScores(hits))
	}
}

func TestResolveEmbedderModes(t *testing.T) {
	emb, mode := ResolveEmbedder(context.Background(), Config{ForceLexical: true})
	if mode != "lexical" || emb.Name() != "lexical" {
		t.Fatalf("force lexical: %s %s", mode, emb.Name())
	}
	emb, mode = ResolveEmbedder(context.Background(), Config{})
	if mode != "local" {
		t.Fatalf("default offline=%s", mode)
	}
	// Unreachable openai endpoint → local fallback.
	emb, mode = ResolveEmbedder(context.Background(), Config{
		Enabled: true, Endpoint: "http://127.0.0.1:1", Model: "x",
	})
	if mode != "local" {
		t.Fatalf("unreachable openai should fall back to local, got %s (%s)", mode, emb.Name())
	}
}

func hitScores(hits []Scored) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s=%.3f", h.Chunk.ID, h.Score))
	}
	return strings.Join(parts, ",")
}
