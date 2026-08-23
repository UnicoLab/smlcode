package memory

import (
	"math"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Recall tuning. These are BM25 parameters plus the field weights of a
// BM25F-style multi-field score.
//
// Why lexical BM25F and not embeddings: recall runs on every task start, must
// be deterministic under CI, must work with zero LLM/embedding calls, and
// scores *structured* fields (paths, tool names, tags) where exact token
// overlap is the signal — a path token like "runner.go" is worth far more than
// its cosine similarity to anything. Embeddings would also need a cache keyed
// on a model that can change between runs, which is precisely the kind of
// silent-staleness this package is supposed to avoid. pkg/retrieval remains
// the right tool for prose-heavy code chunks; episodes are not prose.
const (
	bm25K1 = 1.2
	bm25B  = 0.75

	weightQuery   = 3
	weightFiles   = 3
	weightSummary = 2
	weightTags    = 2
	weightTools   = 1

	// DefaultMinCoverage is the precision gate: the fraction of distinct query
	// terms an episode must actually contain. A raw BM25 threshold cannot do
	// this job because its scale depends on corpus size — on a fresh project
	// every term is "common" and every score collapses. Coverage is
	// corpus-independent, which is what makes it a safe floor on run one and
	// on run one thousand.
	//
	// It matters because for a 7B model an irrelevant-but-plausible memory is
	// worse than no memory at all: precision beats recall here, always.
	DefaultMinCoverage = 0.34

	// relativeFloor drops matches far weaker than the best one, so a query
	// with one strong match does not drag in three weak ones for company.
	relativeFloor = 0.45

	// minIDF keeps a term from contributing nothing just because a small
	// corpus makes every term look common.
	minIDF = 0.25

	// recallHalfLifeDays is the recency decay applied to episode scores.
	recallHalfLifeDays = 45
)

// Query selects episodes to recall.
type Query struct {
	// Text is the new task description. Usually the user query.
	Text string
	// Files, Tools and Tags narrow by structured overlap.
	Files []string
	Tools []string
	Tags  []string
	// Language and Model, when set, restrict the candidate set.
	Language string
	Model    string
	// SuccessOnly keeps only episodes that ended in success.
	SuccessOnly bool
	// FailuresOnly keeps only episodes that recorded a failure — used to warn
	// a model about ground it has already tripped over.
	FailuresOnly bool
	// Since drops episodes older than this instant.
	Since time.Time
	// MinScore is an optional absolute BM25 floor (0 = off).
	MinScore float64
	// MinCoverage overrides DefaultMinCoverage. Negative disables the gate.
	MinCoverage float64
}

// Scored pairs an episode with its recall score.
type Scored struct {
	Episode Episode
	Score   float64
}

// document is a token bag with per-token frequency.
type document struct {
	tf     map[string]int
	length int
}

func buildDoc(en indexEntry) *document {
	d := &document{tf: make(map[string]int, 32)}
	add := func(text string, weight int) {
		for _, tok := range tokenize(text) {
			d.tf[tok] += weight
			d.length += weight
		}
	}
	add(en.Query, weightQuery)
	add(en.Summary, weightSummary)
	for _, f := range en.Files {
		add(pathTokens(f), weightFiles)
	}
	for _, t := range en.Tools {
		add(t, weightTools)
	}
	for _, t := range en.Tags {
		add(t, weightTags)
	}
	add(en.Language, weightTags)
	return d
}

// RecallEpisodes returns up to n episodes most similar to q, best first.
func (s *Episodes) RecallEpisodes(q Query, n int) []Episode {
	scored := s.RecallScored(q, n)
	out := make([]Episode, 0, len(scored))
	for _, sc := range scored {
		out = append(out, sc.Episode)
	}
	return out
}

// RecallScored is RecallEpisodes with the scores attached.
func (s *Episodes) RecallScored(q Query, n int) []Scored {
	if n <= 0 {
		n = 3
	}
	s.mu.Lock()
	candidates := make([]*indexEntry, 0, len(s.entries))
	for i := range s.entries {
		en := &s.entries[i]
		if !q.matches(*en) {
			continue
		}
		if en.doc == nil {
			en.doc = buildDoc(*en)
		}
		candidates = append(candidates, en)
	}
	now := s.now()
	s.mu.Unlock()

	if len(candidates) == 0 {
		return nil
	}
	terms := queryTerms(q)
	if len(terms) == 0 {
		return nil
	}

	// Document frequency over the candidate set only: relevance is relative to
	// what this project has actually done.
	df := make(map[string]int, len(terms))
	total := 0
	for _, c := range candidates {
		total += c.doc.length
		for _, t := range terms {
			if c.doc.tf[t] > 0 {
				df[t]++
			}
		}
	}
	avgLen := float64(total) / float64(len(candidates))
	if avgLen <= 0 {
		avgLen = 1
	}
	nDocs := float64(len(candidates))

	minCoverage := q.MinCoverage
	if minCoverage == 0 {
		minCoverage = DefaultMinCoverage
	}

	scored := make([]Scored, 0, len(candidates))
	best := 0.0
	for _, c := range candidates {
		score, matched := 0.0, 0
		for _, t := range terms {
			tf := float64(c.doc.tf[t])
			if tf == 0 {
				continue
			}
			matched++
			idf := math.Max(minIDF, math.Log(1+(nDocs-float64(df[t])+0.5)/(float64(df[t])+0.5)))
			norm := tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*float64(c.doc.length)/avgLen))
			score += idf * norm
		}
		if score <= 0 {
			continue
		}
		if coverage := float64(matched) / float64(len(terms)); coverage < minCoverage {
			continue
		}
		if q.MinScore > 0 && score < q.MinScore {
			continue
		}
		score *= recency(c.At, now, recallHalfLifeDays)
		if c.Success {
			// A remembered success is more actionable than a remembered mess.
			score *= 1.15
		}
		if score > best {
			best = score
		}
		scored = append(scored, Scored{Score: score, Episode: Episode{ID: c.ID}})
	}
	if len(scored) == 0 {
		return nil
	}

	floor := best * relativeFloor

	kept := scored[:0]
	for _, sc := range scored {
		if sc.Score >= floor {
			kept = append(kept, sc)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Score > kept[j].Score })
	if len(kept) > n {
		kept = kept[:n]
	}
	out := make([]Scored, 0, len(kept))
	for _, sc := range kept {
		full, ok := s.Get(sc.Episode.ID)
		if !ok {
			continue
		}
		out = append(out, Scored{Episode: full, Score: sc.Score})
	}
	return out
}

func (q Query) matches(en indexEntry) bool {
	if q.Language != "" && en.Language != "" && !strings.EqualFold(q.Language, en.Language) {
		return false
	}
	if q.Model != "" && en.Model != "" && !strings.EqualFold(q.Model, en.Model) {
		return false
	}
	if q.SuccessOnly && !en.Success {
		return false
	}
	if q.FailuresOnly && en.Failures == 0 {
		return false
	}
	if !q.Since.IsZero() && en.At.Before(q.Since) {
		return false
	}
	return true
}

func queryTerms(q Query) []string {
	var raw []string
	raw = append(raw, tokenize(q.Text)...)
	for _, f := range q.Files {
		raw = append(raw, tokenize(pathTokens(f))...)
	}
	raw = append(raw, tokenize(strings.Join(q.Tools, " "))...)
	raw = append(raw, tokenize(strings.Join(q.Tags, " "))...)
	seen := make(map[string]bool, len(raw))
	out := raw[:0]
	for _, t := range raw {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// pathTokens explodes a path into searchable pieces: every directory segment,
// the base name, and the base name without its extension.
func pathTokens(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	base := parts[len(parts)-1]
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	out := append([]string{}, parts...)
	out = append(out, stem)
	if ext != "" {
		out = append(out, strings.TrimPrefix(ext, "."))
	}
	return strings.Join(out, " ")
}

// stopwords are dropped from recall terms: they add length without signal and,
// worse, they let an unrelated episode match on "the file in a function".
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "you": true, "your": true, "are": true, "was": true,
	"but": true, "not": true, "all": true, "can": true, "use": true, "using": true,
	"add": true, "make": true, "please": true, "should": true, "would": true,
	"its": true, "it": true, "is": true, "to": true, "of": true, "in": true,
	"on": true, "an": true, "a": true, "be": true, "do": true, "we": true,
}

// tokenize lowercases, splits on non-alphanumerics, splits camelCase, and drops
// stopwords and single characters.
func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		for _, piece := range splitCamel(string(cur)) {
			piece = strings.ToLower(piece)
			if len(piece) < 2 || stopwords[piece] {
				continue
			}
			out = append(out, piece)
		}
		cur = cur[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	runes := []rune(s)
	for i := 1; i < len(runes); i++ {
		if !unicode.IsUpper(runes[i]) {
			continue
		}
		// lower→Upper ("httpClient") or the tail of an acronym ("HTTPClient").
		acronymTail := unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if !unicode.IsUpper(runes[i-1]) || acronymTail {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	out = append(out, string(runes[start:]))
	if len(out) > 1 {
		out = append(out, s) // keep the whole identifier too
	}
	return out
}
