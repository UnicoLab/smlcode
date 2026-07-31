// Package retrieval provides semantic (embedding) + lexical ranking over
// project memory: query summaries, MEMORY.md, and learned skills.
package retrieval

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Chunk is a retrievable memory unit.
type Chunk struct {
	ID     string
	Source string // summary|memory|skill|index
	Text   string
	Query  string // originating user query when known
}

// Scored is a ranked retrieval hit.
type Scored struct {
	Chunk Chunk
	Score float64
}

// Embedder turns text into a dense vector. Implementations may call a remote
// OpenAI-compatible /v1/embeddings endpoint or a local lexical fallback.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Name() string
}

// Config selects embedding backend + ranking knobs.
type Config struct {
	Enabled      bool
	Endpoint     string // OpenAI-compat base, e.g. http://127.0.0.1:8000/v1
	Model        string
	APIKey       string
	TopK         int
	ForceLexical bool // tests / explicit last-resort TF-IDF only
}

// Retriever ranks memory chunks for CONTEXT injection.
type Retriever struct {
	Embedder Embedder
	TopK     int
}

// New builds a retriever. Tries OpenAI-compat embeddings when configured,
// else local hashing embedder, else lexical TF-IDF (ForceLexical / failures).
func New(cfg Config) *Retriever {
	k := cfg.TopK
	if k <= 0 {
		k = 5
	}
	emb, _ := ResolveEmbedder(context.Background(), cfg)
	return &Retriever{Embedder: emb, TopK: k}
}

// ModeName returns the active embedder mode (openai / local / lexical).
func ModeName(emb Embedder) string {
	if emb == nil {
		return "lexical"
	}
	name := emb.Name()
	switch {
	case strings.HasPrefix(name, "openai"):
		return "openai"
	case name == "local":
		return "local"
	case name == "lexical":
		return "lexical"
	default:
		return name
	}
}

// NewLexical always uses the bag-of-words / TF-IDF fallback (tests + offline).
func NewLexical() *Retriever {
	return &Retriever{Embedder: NewLexicalEmbedder(), TopK: 5}
}

// Search ranks chunks for query and returns the top-k.
func (r *Retriever) Search(ctx context.Context, query string, chunks []Chunk) ([]Scored, error) {
	if r == nil {
		r = NewLexical()
	}
	query = strings.TrimSpace(query)
	if query == "" || len(chunks) == 0 {
		return nil, nil
	}
	k := r.TopK
	if k <= 0 {
		k = 5
	}
	texts := make([]string, 0, len(chunks)+1)
	texts = append(texts, query)
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	vecs, err := r.Embedder.Embed(ctx, texts)
	if err != nil || len(vecs) != len(texts) {
		// Prefer local hashing before last-resort TF-IDF.
		if ModeName(r.Embedder) != "local" {
			loc := NewLocalEmbedder()
			vecs, err = loc.Embed(ctx, texts)
		}
		if err != nil || len(vecs) != len(texts) {
			lex := NewLexicalEmbedder()
			vecs, err = lex.Embed(ctx, texts)
			if err != nil {
				return nil, err
			}
		}
	}
	q := vecs[0]
	var scored []Scored
	for i, c := range chunks {
		s := cosine(q, vecs[i+1])
		// Light boost when the chunk's original query overlaps.
		if c.Query != "" {
			s += 0.05 * jaccardTokens(query, c.Query)
		}
		scored = append(scored, Scored{Chunk: c, Score: s})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

// FormatHits renders ranked chunks for CONTEXT.md injection.
func FormatHits(hits []Scored) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Retrieved prior knowledge\n\n")
	for i, h := range hits {
		b.WriteString(fmt.Sprintf("## Hit %d (%.3f · %s)\n\n", i+1, h.Score, h.Chunk.Source))
		if h.Chunk.Query != "" {
			b.WriteString("**Prior query:** " + firstLine(h.Chunk.Query) + "\n\n")
		}
		body := strings.TrimSpace(h.Chunk.Text)
		if len(body) > 1200 {
			body = body[:1200] + "\n…"
		}
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// CollectChunks gathers summaries, MEMORY, and learned skills from .slmcode.
func CollectChunks(slmDir string) []Chunk {
	var out []Chunk
	// Per-query summaries (richer than INDEX alone).
	qdir := filepath.Join(slmDir, "queries")
	entries, _ := os.ReadDir(qdir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		sumPath := filepath.Join(qdir, id, "summary.md")
		data, err := os.ReadFile(sumPath)
		if err != nil || len(data) == 0 {
			continue
		}
		q := ""
		if qb, err := os.ReadFile(filepath.Join(qdir, id, "QUERY.md")); err == nil {
			q = strings.TrimSpace(strings.TrimPrefix(string(qb), "# Query"))
			q = strings.TrimSpace(q)
		}
		out = append(out, Chunk{
			ID: "summary:" + id, Source: "summary",
			Text: string(data), Query: q,
		})
	}
	// Rolling index as a single chunk (recency signal).
	if data, err := os.ReadFile(filepath.Join(slmDir, "summaries", "INDEX.md")); err == nil && len(data) > 0 {
		out = append(out, Chunk{ID: "index", Source: "index", Text: string(data)})
	}
	if data, err := os.ReadFile(filepath.Join(slmDir, "MEMORY.md")); err == nil && len(data) > 80 {
		out = append(out, Chunk{ID: "memory", Source: "memory", Text: string(data)})
	}
	if data, err := os.ReadFile(filepath.Join(slmDir, "skills", "learned", "SKILL.md")); err == nil && len(data) > 40 {
		out = append(out, Chunk{ID: "learned", Source: "skill", Text: string(data)})
	}
	return out
}

// RetrieveForQuery is the high-level CONTEXT enrichment entrypoint.
// Mode cascade: openai → local → lexical. Falls back to a recency-trimmed
// index if ranking yields nothing useful.
func RetrieveForQuery(ctx context.Context, slmDir, query string, cfg Config) (string, string, error) {
	chunks := CollectChunks(slmDir)
	if len(chunks) == 0 {
		return "", "none", nil
	}
	emb, mode := ResolveEmbedder(ctx, cfg)
	r := &Retriever{Embedder: emb, TopK: cfg.TopK}
	if r.TopK <= 0 {
		r.TopK = 5
	}
	hits, err := r.Search(ctx, query, chunks)
	if err != nil {
		return "", mode, err
	}
	// Drop near-zero noise.
	var kept []Scored
	for _, h := range hits {
		if h.Score >= 0.02 {
			kept = append(kept, h)
		}
	}
	if len(kept) == 0 {
		return "", mode, nil
	}
	return FormatHits(kept), mode, nil
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var b strings.Builder
	var out []string
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tok := b.String()
		b.Reset()
		if len(tok) < 2 {
			return
		}
		if stopWord(tok) {
			return
		}
		out = append(out, tok)
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func jaccardTokens(a, b string) float64 {
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	inter := 0
	ub := map[string]bool{}
	for _, t := range ta {
		ub[t] = true
	}
	for _, t := range tb {
		ub[t] = true
		if set[t] {
			inter++
		}
	}
	if len(ub) == 0 {
		return 0
	}
	return float64(inter) / float64(len(ub))
}

func stopWord(t string) bool {
	switch t {
	case "the", "and", "for", "with", "that", "this", "from", "into", "are", "was",
		"were", "have", "has", "had", "not", "but", "you", "your", "our", "all",
		"any", "can", "will", "just", "than", "then", "when", "what", "how", "why":
		return true
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
