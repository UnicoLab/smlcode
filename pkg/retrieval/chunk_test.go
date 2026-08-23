package retrieval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSplitSections(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantChunks  int
		wantHeading string
		wantNoChunk bool
	}{
		{name: "empty", text: "", wantNoChunk: true},
		{name: "whitespace", text: "   \n\n ", wantNoChunk: true},
		{name: "tiny", text: "ok", wantNoChunk: true},
		{
			name:        "single section",
			text:        "# Doc\n\n## Outcome\n\n- Renamed Hello to Greet in pkg/greet/greet.go\n",
			wantChunks:  1,
			wantHeading: "Outcome",
		},
		{
			name: "multiple sections become multiple chunks",
			text: "# Doc\n\n## Alpha\n\n" + strings.Repeat("alpha detail line\n", 5) +
				"\n## Beta\n\n" + strings.Repeat("beta detail line\n", 5),
			wantChunks: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitSections("id", "summary", tc.text, "q")
			if tc.wantNoChunk {
				if len(got) != 0 {
					t.Fatalf("expected no chunks, got %d: %+v", len(got), got)
				}
				return
			}
			if len(got) != tc.wantChunks {
				t.Fatalf("got %d chunks want %d: %+v", len(got), tc.wantChunks, got)
			}
			seen := map[string]bool{}
			for _, c := range got {
				if len(c.Text) > MaxChunkBytes {
					t.Fatalf("chunk %s is %d bytes, over %d", c.ID, len(c.Text), MaxChunkBytes)
				}
				if !utf8.ValidString(c.Text) {
					t.Fatalf("chunk %s is not valid UTF-8", c.ID)
				}
				if seen[c.ID] {
					t.Fatalf("duplicate chunk id %s", c.ID)
				}
				seen[c.ID] = true
				if c.Query != "q" || c.Source != "summary" {
					t.Fatalf("metadata lost: %+v", c)
				}
			}
			if tc.wantHeading != "" && got[0].Heading != tc.wantHeading {
				t.Fatalf("heading=%q want %q", got[0].Heading, tc.wantHeading)
			}
		})
	}
}

func TestSplitSectionsBreaksUpAFatIndex(t *testing.T) {
	// A 24 KB INDEX.md used to be ONE chunk: its vector is the average of 40
	// unrelated summaries and matches nothing in particular.
	var b strings.Builder
	b.WriteString("# Rolling index\n\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "## Run %d\n\n", i)
		fmt.Fprintf(&b, "%s\n\n", strings.Repeat(fmt.Sprintf("run-%d detail sentence. ", i), 20))
	}
	chunks := SplitSections("index", "index", b.String(), "")
	if len(chunks) < 40 {
		t.Fatalf("fat index produced only %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Text) > MaxChunkBytes {
			t.Fatalf("chunk over cap: %d", len(c.Text))
		}
	}
	// Each chunk should be about one run.
	if !strings.Contains(chunks[0].Text, "run-0") {
		t.Fatalf("first chunk lost its topic: %q", chunks[0].Text)
	}
}

func TestSplitSectionsHandlesMonsterLines(t *testing.T) {
	line := strings.Repeat("é", 5000) // multi-byte, no newlines
	chunks := SplitSections("x", "memory", "## Blob\n\n"+line, "")
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	for _, c := range chunks {
		if len(c.Text) > MaxChunkBytes {
			t.Fatalf("chunk over cap: %d", len(c.Text))
		}
		if !utf8.ValidString(c.Text) {
			t.Fatal("split produced invalid UTF-8")
		}
	}
}

func TestCosineRejectsDimensionMismatch(t *testing.T) {
	tests := []struct {
		name    string
		a, b    []float64
		wantErr bool
		want    float64
	}{
		{"identical", []float64{1, 0}, []float64{1, 0}, false, 1},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, false, 0},
		{"mismatch", []float64{1, 0, 0}, []float64{1, 0}, true, 0},
		{"empty a", nil, []float64{1}, false, 0},
		{"empty b", []float64{1}, nil, false, 0},
		{"zero vector", []float64{0, 0}, []float64{1, 1}, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cosine(tc.a, tc.b)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// mismatchEmbedder returns vectors of differing dimension.
type mismatchEmbedder struct{}

func (mismatchEmbedder) Name() string { return "mismatch" }
func (mismatchEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = make([]float64, 3+i) // deliberately inconsistent
		for j := range out[i] {
			out[i][j] = float64(j + 1)
		}
	}
	return out, nil
}

func TestSearchSurfacesDimensionMismatch(t *testing.T) {
	r := &Retriever{Embedder: mismatchEmbedder{}, TopK: 3}
	_, err := r.Search(context.Background(), "anything", []Chunk{{ID: "a", Text: "some text"}})
	if err == nil {
		t.Fatal("mismatched dimensions must be an error, not a silently truncated score")
	}
	if !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMinScoreFor(t *testing.T) {
	tests := []struct {
		mode string
		want float64
	}{
		{"openai", MinScoreOpenAI},
		{"local", MinScoreLocal},
		{"lexical", MinScoreLexical},
		{"unknown", MinScoreLocal},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			if got := MinScoreFor(tc.mode); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	// The old 0.02 floor must be well below every calibrated floor.
	for _, m := range []string{"openai", "local", "lexical"} {
		if MinScoreFor(m) <= 0.02 {
			t.Fatalf("%s floor still in the noise band", m)
		}
	}
}

func TestThresholdRejectsUnrelatedMemory(t *testing.T) {
	slm := t.TempDir()
	mkQuery(t, slm, "run-1", "configure nginx TLS termination",
		"# Query summary\n\n## Outcome\n\n- Configured nginx with cert-manager for TLS on staging cluster\n")
	body, mode, err := RetrieveForQuery(context.Background(), slm,
		"rewrite the payment rounding logic in the billing service", Config{TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local" {
		t.Fatalf("mode=%s", mode)
	}
	if body != "" {
		t.Fatalf("unrelated memory injected as prior knowledge:\n%s", body)
	}
}

func mkQuery(t *testing.T, slm, id, query, summary string) {
	t.Helper()
	dir := filepath.Join(slm, "queries", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "QUERY.md"), []byte("# Query\n\n"+query+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPruneQueryDirs(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		keep      int
		wantAfter int
	}{
		{"under limit", 10, 50, 10},
		{"at limit", 50, 50, 50},
		{"over limit", 80, 50, 50},
		{"custom keep", 20, 5, 5},
		{"keep zero uses default", 60, 0, MaxQueryDirs},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slm := t.TempDir()
			base := time.Now().Add(-time.Duration(tc.total) * time.Hour)
			for i := 0; i < tc.total; i++ {
				id := fmt.Sprintf("run-%03d", i)
				mkQuery(t, slm, id, "q", "# S\n\n## Outcome\n\n- did something useful here\n")
				stamp := base.Add(time.Duration(i) * time.Hour)
				if err := os.Chtimes(filepath.Join(slm, "queries", id), stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := PruneQueryDirs(slm, tc.keep); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(filepath.Join(slm, "queries"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != tc.wantAfter {
				t.Fatalf("kept %d dirs want %d", len(entries), tc.wantAfter)
			}
			// The NEWEST must survive.
			newest := fmt.Sprintf("run-%03d", tc.total-1)
			if _, err := os.Stat(filepath.Join(slm, "queries", newest)); err != nil {
				t.Fatalf("newest dir %s was pruned", newest)
			}
		})
	}
	// Missing dir is not an error.
	if n, err := PruneQueryDirs(t.TempDir(), 5); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// countingEmbedder records how many texts it was asked to embed.
type countingEmbedder struct {
	name  string
	calls int
	texts int
}

func (c *countingEmbedder) Name() string { return c.name }
func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	c.calls++
	c.texts += len(texts)
	return NewLocalEmbedder().Embed(ctx, texts)
}

func TestCachedEmbedderAvoidsReEmbedding(t *testing.T) {
	dir := t.TempDir()
	inner := &countingEmbedder{name: "local"}
	c := NewCachedEmbedder(inner, dir)

	corpus := []string{"alpha text", "beta text", "gamma text"}
	if _, err := c.Embed(context.Background(), corpus); err != nil {
		t.Fatal(err)
	}
	if inner.texts != 3 {
		t.Fatalf("first pass embedded %d texts", inner.texts)
	}
	// Second query over the same corpus plus one new text.
	if _, err := c.Embed(context.Background(), append([]string{"new query"}, corpus...)); err != nil {
		t.Fatal(err)
	}
	if inner.texts != 4 {
		t.Fatalf("cache miss: embedded %d texts total, want 4", inner.texts)
	}
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}

	// A fresh cache over the same dir must reuse the persisted vectors.
	inner2 := &countingEmbedder{name: "local"}
	c2 := NewCachedEmbedder(inner2, dir)
	if _, err := c2.Embed(context.Background(), corpus); err != nil {
		t.Fatal(err)
	}
	if inner2.texts != 0 {
		t.Fatalf("disk cache not reused: embedded %d texts", inner2.texts)
	}
}

func TestCachedEmbedderVectorsMatchInner(t *testing.T) {
	dir := t.TempDir()
	inner := NewLocalEmbedder()
	c := NewCachedEmbedder(inner, dir)
	texts := []string{"one", "two", "three"}
	want, err := inner.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 3; pass++ {
		got, err := c.Embed(context.Background(), texts)
		if err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if len(got[i]) != len(want[i]) {
				t.Fatalf("pass %d: dim %d vs %d", pass, len(got[i]), len(want[i]))
			}
			for j := range want[i] {
				if got[i][j] != want[i][j] {
					t.Fatalf("pass %d: vector %d differs at %d", pass, i, j)
				}
			}
		}
	}
}

func TestCachedEmbedderSkipsLexical(t *testing.T) {
	// Lexical vectors are only comparable within one batch (shared vocab), so
	// caching them across calls would compare incompatible spaces.
	dir := t.TempDir()
	inner := &countingEmbedder{name: "lexical"}
	c := NewCachedEmbedder(inner, dir)
	for i := 0; i < 3; i++ {
		if _, err := c.Embed(context.Background(), []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 3 {
		t.Fatalf("lexical embedder should never be cached, calls=%d", inner.calls)
	}
}

func TestCachedEmbedderKeyIncludesEmbedderName(t *testing.T) {
	if CacheKey("local", "x") == CacheKey("openai-embed:m", "x") {
		t.Fatal("cache key must be namespaced by embedder")
	}
	if CacheKey("local", "x") == CacheKey("local", "y") {
		t.Fatal("cache key must depend on the text")
	}
	if len(CacheKey("local", "x")) != len("local:")+64 {
		t.Fatalf("key should embed a sha256 hex digest: %q", CacheKey("local", "x"))
	}
}

func TestCachedEmbedderNoPersistenceDir(t *testing.T) {
	c := NewCachedEmbedder(NewLocalEmbedder(), "")
	if _, err := c.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestFormatHitsBudget(t *testing.T) {
	hits := []Scored{
		{Chunk: Chunk{ID: "a", Source: "summary", Query: "prior q", Text: strings.Repeat("alpha ", 2000)}, Score: 0.9},
		{Chunk: Chunk{ID: "b", Source: "memory", Text: strings.Repeat("beta ", 2000)}, Score: 0.5},
		{Chunk: Chunk{ID: "c", Source: "skill", Text: strings.Repeat("gamma ", 2000)}, Score: 0.3},
	}
	for _, budget := range []int{0, 300, 900, 1800, 8000} {
		limit := budget
		if limit <= 0 {
			limit = DefaultMaxInjectBytes
		}
		out := FormatHitsBudget(hits, budget)
		if len(out) > limit {
			t.Fatalf("budget %d produced %d bytes", budget, len(out))
		}
		if !utf8.ValidString(out) {
			t.Fatalf("budget %d produced invalid UTF-8", budget)
		}
	}
	if FormatHitsBudget(nil, 1000) != "" {
		t.Fatal("no hits should render empty")
	}
	// The default must be far below the old effectively-3KB injection.
	if DefaultMaxInjectBytes > 2048 {
		t.Fatalf("default injection budget too large: %d", DefaultMaxInjectBytes)
	}
}

func TestResolveEmbedderProbeAlwaysBounded(t *testing.T) {
	// A parent context WITH a long deadline must not disable the probe timeout.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	start := time.Now()
	_, mode := ResolveEmbedder(ctx, Config{
		Enabled: true, Endpoint: "http://127.0.0.1:1", Model: "x",
	})
	elapsed := time.Since(start)
	if mode != "local" {
		t.Fatalf("unreachable endpoint should fall back to local, got %s", mode)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("probe took %v — the 2s timeout did not apply", elapsed)
	}
}
