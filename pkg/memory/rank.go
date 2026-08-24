package memory

import (
	"math"
	"sort"
	"time"
)

// This file holds the ONE lexical ranking pipeline in the package. Episode
// recall (recall.go) and every other lexical lookup — semantic facts ranked
// against the current task, prose lessons ranked before they enter a prompt —
// call rankDocs, so the coverage gate, the relative floor, the recency decay
// and the success boost are implemented once and tuned once. A second copy of
// this arithmetic would silently drift away from the tested one.

// rankable is one scoring candidate: a token bag plus the metadata the pipeline
// decays and boosts on. A zero At disables recency decay for that candidate.
type rankable struct {
	doc     *document
	at      time.Time
	success bool
}

// rankHit is a surviving candidate: its index in the input slice and its score.
type rankHit struct {
	index int
	score float64
}

// rankDocs scores cands against terms with BM25F and applies, in order:
//
//   - the coverage gate — a candidate must contain at least minCoverage of the
//     distinct query terms. 0 means DefaultMinCoverage; negative disables it.
//   - an optional absolute floor (minScore, 0 = off).
//   - recency decay (recallHalfLifeDays) and a 15 % boost for successes.
//   - the relative floor — anything below relativeFloor × best is dropped, so
//     one strong match does not drag in three weak ones for company.
//
// Results are sorted best first and capped at n (n <= 0 means "all"). Document
// frequency is computed over the candidate set only: relevance is relative to
// what this project has actually done, not to an imaginary global corpus.
func rankDocs(terms []string, cands []rankable, now time.Time, minCoverage, minScore float64, n int) []rankHit {
	if len(terms) == 0 || len(cands) == 0 {
		return nil
	}
	df := make(map[string]int, len(terms))
	total := 0
	for _, c := range cands {
		if c.doc == nil {
			continue
		}
		total += c.doc.length
		for _, t := range terms {
			if c.doc.tf[t] > 0 {
				df[t]++
			}
		}
	}
	avgLen := float64(total) / float64(len(cands))
	if avgLen <= 0 {
		avgLen = 1
	}
	nDocs := float64(len(cands))

	if minCoverage == 0 {
		minCoverage = DefaultMinCoverage
	}

	hits := make([]rankHit, 0, len(cands))
	best := 0.0
	for i, c := range cands {
		if c.doc == nil {
			continue
		}
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
		if minScore > 0 && score < minScore {
			continue
		}
		score *= recency(c.at, now, recallHalfLifeDays)
		if c.success {
			// A remembered success is more actionable than a remembered mess.
			score *= 1.15
		}
		if score > best {
			best = score
		}
		hits = append(hits, rankHit{index: i, score: score})
	}
	if len(hits) == 0 {
		return nil
	}

	floor := best * relativeFloor
	kept := hits[:0]
	for _, h := range hits {
		if h.score >= floor {
			kept = append(kept, h)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].score > kept[j].score })
	if n > 0 && len(kept) > n {
		kept = kept[:n]
	}
	return kept
}

// buildTextDoc is the single-field form of buildDoc, for candidates that are
// plain prose rather than a structured episode.
func buildTextDoc(text string) *document {
	d := &document{tf: make(map[string]int, 16)}
	for _, tok := range tokenize(text) {
		d.tf[tok]++
		d.length++
	}
	return d
}

// TextCandidate is a free-text item to rank with RankText: a lesson bullet, a
// distilled fact, anything prose. A zero At disables recency decay for it.
type TextCandidate struct {
	Text    string
	At      time.Time
	Success bool
}

// TextMatch is one ranked candidate: its index in the slice handed to RankText
// and the score it earned.
type TextMatch struct {
	Index int
	Score float64
}

// RankText ranks free-text candidates against q with exactly the pipeline that
// RecallScored applies to episodes — BM25F, the corpus-independent coverage
// gate, the relative floor, recency decay and the success boost.
//
// It exists so callers outside this package (prose lessons on their way into a
// prompt, for instance) get real task-conditioned relevance instead of a
// hand-rolled keyword list, without a second copy of the scorer. Candidates
// that do not clear the gates are simply absent from the result: the caller
// decides what, if anything, to do with the rest.
//
// n <= 0 returns every survivor.
func RankText(q Query, cands []TextCandidate, n int) []TextMatch {
	terms := queryTerms(q)
	if len(terms) == 0 || len(cands) == 0 {
		return nil
	}
	items := make([]rankable, len(cands))
	for i, c := range cands {
		items[i] = rankable{doc: buildTextDoc(c.Text), at: c.At, success: c.Success}
	}
	hits := rankDocs(terms, items, time.Now(), q.MinCoverage, q.MinScore, n)
	if len(hits) == 0 {
		return nil
	}
	out := make([]TextMatch, 0, len(hits))
	for _, h := range hits {
		out = append(out, TextMatch{Index: h.index, Score: h.score})
	}
	return out
}
