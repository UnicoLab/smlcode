// Package retrieval provides semantic (embedding) + lexical ranking over
// project memory: query summaries, MEMORY.md, and learned skills.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Chunk is a retrievable memory unit.
type Chunk struct {
	ID      string
	Source  string // summary|memory|skill|index
	Text    string
	Query   string // originating user query when known
	Heading string // originating "## " section, when chunked
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

	// MinScore overrides the calibrated per-mode threshold (0 = use default).
	MinScore float64
	// CacheDir enables the on-disk embedding cache (typically .slmcode).
	CacheDir string
	// MaxInjectBytes caps the rendered "Retrieved prior knowledge" block.
	MaxInjectBytes int
}

// Calibrated score floors.
//
// The old threshold was >= 0.02, which for signed feature hashing into 384
// dimensions is deep inside the noise band — pure noise was being injected as
// "Retrieved prior knowledge", spending up to 3 KB of a 12.8 KB budget.
//
// These floors are MEASURED, not guessed (see TestNoiseFloorsAreCalibrated).
// Over pairs of unrelated engineering sentences:
//
//	LocalEmbedder (384-dim signed feature hashing): min 0.186, median 0.341,
//	  max 0.486 — character n-grams of any English text overlap heavily, so the
//	  absolute baseline is high and the floor must sit above the median.
//	LexicalEmbedder (TF-IDF bag of words): max 0.116.
const (
	// MinScoreLocal is the floor for the local hashing embedder.
	MinScoreLocal = 0.40
	// MinScoreOpenAI is the floor for real embeddings, which separate related
	// from unrelated text far more cleanly.
	MinScoreOpenAI = 0.25
	// MinScoreLexical is the floor for TF-IDF bag-of-words.
	MinScoreLexical = 0.15
	// NoiseMargin is how far above the corpus's own median similarity a hit
	// must sit. This adapts to a corpus whose baseline is unusually high.
	NoiseMargin = 0.06
	// MinChunksForNoiseFloor is the corpus size below which the relative
	// noise floor is not meaningful (a single chunk IS its own median).
	MinChunksForNoiseFloor = 4
	// DefaultMaxInjectBytes caps the injected retrieval block.
	DefaultMaxInjectBytes = 1800
	// MaxQueryDirs is how many .slmcode/queries/<id> dirs are retained.
	MaxQueryDirs = 50
)

// MinScoreFor returns the calibrated absolute floor for an embedder mode.
func MinScoreFor(mode string) float64 {
	switch mode {
	case "openai":
		return MinScoreOpenAI
	case "lexical":
		return MinScoreLexical
	default:
		return MinScoreLocal
	}
}

// NoiseFloor is the measured per-corpus baseline: the median score plus
// NoiseMargin. It returns 0 for corpora too small to estimate a baseline from.
func NoiseFloor(hits []Scored) float64 {
	if len(hits) < MinChunksForNoiseFloor {
		return 0
	}
	scores := make([]float64, len(hits))
	for i, h := range hits {
		scores[i] = h.Score
	}
	sort.Float64s(scores)
	median := scores[(len(scores)-1)/2]
	return median + NoiseMargin
}

// CalibratedThreshold combines the absolute per-mode floor with the corpus's
// own measured noise floor. override (>0) wins outright.
func CalibratedThreshold(mode string, hits []Scored, override float64) float64 {
	if override > 0 {
		return override
	}
	threshold := MinScoreFor(mode)
	if floor := NoiseFloor(hits); floor > threshold {
		threshold = floor
	}
	return threshold
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
		s, cerr := cosine(q, vecs[i+1])
		if cerr != nil {
			// A dimension mismatch means the vectors came from different
			// spaces. Silently comparing min(len(a),len(b)) dimensions
			// produces a plausible-looking score from unrelated numbers.
			return nil, cerr
		}
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

// FormatHits renders ranked chunks for CONTEXT.md injection, bounded to
// DefaultMaxInjectBytes.
func FormatHits(hits []Scored) string {
	return FormatHitsBudget(hits, DefaultMaxInjectBytes)
}

// FormatHitsBudget renders ranked chunks under an explicit byte budget.
func FormatHitsBudget(hits []Scored, maxBytes int) string {
	if len(hits) == 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxInjectBytes
	}
	var b strings.Builder
	b.WriteString("# Retrieved prior knowledge\n\n")
	perHit := maxBytes / len(hits)
	if perHit < 200 {
		perHit = 200
	}
	for i, h := range hits {
		header := fmt.Sprintf("## Hit %d (%.3f · %s)\n\n", i+1, h.Score, h.Chunk.Source)
		var section strings.Builder
		section.WriteString(header)
		if h.Chunk.Query != "" {
			section.WriteString("**Prior query:** " + firstLine(h.Chunk.Query) + "\n\n")
		}
		section.WriteString(textutil.Truncate(strings.TrimSpace(h.Chunk.Text), perHit, "\n…"))
		section.WriteString("\n\n")
		if b.Len()+section.Len() > maxBytes {
			continue
		}
		b.WriteString(section.String())
	}
	out := strings.TrimSpace(b.String())
	if out == "# Retrieved prior knowledge" {
		return ""
	}
	return textutil.Truncate(out, maxBytes, "\n…")
}

// CollectChunks gathers summaries, MEMORY, and learned skills from .slmcode,
// section-chunked so each embedding describes one topic.
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
		data, err := os.ReadFile(filepath.Join(qdir, id, "summary.md")) //nolint:gosec // id comes from os.ReadDir(qdir), our own queries dir
		if err != nil || len(data) == 0 {
			continue
		}
		q := ""
		if qb, qerr := os.ReadFile(filepath.Join(qdir, id, "QUERY.md")); qerr == nil { //nolint:gosec // id comes from os.ReadDir(qdir), our own queries dir
			q = strings.TrimSpace(strings.TrimPrefix(string(qb), "# Query"))
			q = strings.TrimSpace(q)
		}
		out = append(out, SplitSections("summary:"+id, "summary", string(data), q)...)
	}
	if data, err := os.ReadFile(filepath.Join(slmDir, "summaries", "INDEX.md")); err == nil && len(data) > 0 { //nolint:gosec // slmDir is our own project state dir, not external input
		out = append(out, SplitSections("index", "index", string(data), "")...)
	}
	if data, err := os.ReadFile(filepath.Join(slmDir, "MEMORY.md")); err == nil && len(data) > 80 {
		out = append(out, SplitSections("memory", "memory", string(data), "")...)
	}
	if data, err := os.ReadFile(filepath.Join(slmDir, "skills", "learned", "SKILL.md")); err == nil && len(data) > 40 {
		out = append(out, SplitSections("learned", "skill", string(data), "")...)
	}
	return out
}

// PruneQueryDirs deletes all but the newest keep query directories under
// .slmcode/queries. Nothing ever pruned them, so the retrieval corpus grew
// without bound and every query re-embedded all of it.
func PruneQueryDirs(slmDir string, keep int) (int, error) {
	if keep <= 0 {
		keep = MaxQueryDirs
	}
	qdir := filepath.Join(slmDir, "queries")
	entries, err := os.ReadDir(qdir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	type dirInfo struct {
		name string
		mod  int64
	}
	var dirs []dirInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		dirs = append(dirs, dirInfo{name: e.Name(), mod: info.ModTime().UnixNano()})
	}
	if len(dirs) <= keep {
		return 0, nil
	}
	// Newest last; delete from the front.
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].mod == dirs[j].mod {
			return dirs[i].name < dirs[j].name
		}
		return dirs[i].mod < dirs[j].mod
	})
	removed := 0
	var firstErr error
	for _, d := range dirs[:len(dirs)-keep] {
		if rerr := os.RemoveAll(filepath.Join(qdir, d.name)); rerr != nil {
			if firstErr == nil {
				firstErr = rerr
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// RetrieveForQuery is the high-level CONTEXT enrichment entrypoint.
// Mode cascade: openai → local → lexical. Falls back to a recency-trimmed
// index if ranking yields nothing useful.
func RetrieveForQuery(ctx context.Context, slmDir, query string, cfg Config) (string, string, error) {
	// Keep the corpus bounded before we do anything expensive with it.
	pruneDir := cfg.CacheDir
	if pruneDir == "" {
		pruneDir = slmDir
	}
	_, _ = PruneQueryDirs(pruneDir, MaxQueryDirs)

	chunks := CollectChunks(slmDir)
	if len(chunks) == 0 {
		return "", "none", nil
	}
	emb, mode := ResolveEmbedder(ctx, cfg)
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = slmDir
	}
	cached := NewCachedEmbedder(emb, cacheDir)
	r := &Retriever{Embedder: cached, TopK: cfg.TopK}
	if r.TopK <= 0 {
		r.TopK = 5
	}
	hits, err := r.Search(ctx, query, chunks)
	if err != nil {
		return "", mode, err
	}
	_ = cached.Flush()

	threshold := CalibratedThreshold(mode, hits, cfg.MinScore)
	var kept []Scored
	for _, h := range hits {
		if h.Score >= threshold {
			kept = append(kept, h)
		}
	}
	if len(kept) == 0 {
		return "", mode, nil
	}
	return FormatHitsBudget(kept, cfg.MaxInjectBytes), mode, nil
}

// ErrDimensionMismatch is returned when two vectors are not in the same space.
var ErrDimensionMismatch = errors.New("retrieval: embedding dimension mismatch")

var errEmbeddingCount = errors.New("retrieval: embedder returned the wrong number of vectors")

func cosine(a, b []float64) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, nil
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d", ErrDimensionMismatch, len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
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

func firstLine(s string) string { return textutil.FirstLine(s, 120) }
