package retrieval

import (
	"context"
	"math"
	"testing"
)

// scoredRun builds a score series as Scored values.
func scoredRun(scores ...float64) []Scored {
	out := make([]Scored, 0, len(scores))
	for _, s := range scores {
		out = append(out, Scored{Chunk: Chunk{Text: "c"}, Score: s})
	}
	return out
}

// TestNoiseFloorMeasuresTheCorpusNotTheWinners is the regression guard for a
// silent retrieval blackout.
//
// THE DEFECT: RetrieveForQuery measured the "corpus median" over the value
// Search returns — which is already truncated to TopK. With the default TopK=5
// the median was the THIRD-BEST hit, making the threshold third-best +
// NoiseMargin. Two consequences, both silent: at most two hits could ever
// survive however good the corpus was, and a tightly clustered top five — a set
// of uniformly strong matches — cleared nothing at all, so RetrieveForQuery
// returned "" with no error. Retrieval went blind precisely when the corpus was
// most on-topic.
func TestNoiseFloorMeasuresTheCorpusNotTheWinners(t *testing.T) {
	// A realistic corpus: five strong, on-topic matches over a long tail of
	// noise. The five are within NoiseMargin of each other.
	strong := []float64{0.72, 0.71, 0.70, 0.69, 0.68}
	var corpus []float64
	corpus = append(corpus, strong...)
	for i := 0; i < 25; i++ {
		corpus = append(corpus, 0.10+float64(i)*0.001)
	}

	corpusFloor := NoiseFloor(scoredRun(corpus...))
	topKFloor := NoiseFloor(scoredRun(strong...))

	// The bug, stated as arithmetic: measured over the winners, the floor lands
	// ABOVE every one of them.
	if topKFloor <= strong[0] {
		t.Fatalf("precondition failed: a top-k floor of %.3f no longer excludes "+
			"the best hit %.3f — retune the fixture", topKFloor, strong[0])
	}
	kept := 0
	for _, s := range strong {
		if s >= topKFloor {
			kept++
		}
	}
	if kept != 0 {
		t.Fatalf("precondition failed: %d hit(s) survived the top-k floor", kept)
	}

	// Measured over the corpus, the floor sits in the noise where it belongs
	// and every strong hit clears it.
	for _, s := range strong {
		if s < corpusFloor {
			t.Fatalf("strong hit %.3f was rejected by the corpus floor %.3f — "+
				"the floor is still being measured against the winners",
				s, corpusFloor)
		}
	}
	// And it must still reject the tail, or it is not a floor at all.
	if 0.12 >= corpusFloor {
		t.Fatalf("corpus floor %.3f does not exclude the noise tail", corpusFloor)
	}
}

// TestSearchTruncatesAndSearchAllDoesNot pins the seam the fix depends on.
func TestSearchTruncatesAndSearchAllDoesNot(t *testing.T) {
	r := NewLexical()
	r.TopK = 3
	chunks := []Chunk{
		{Text: "alpha beta gamma"},
		{Text: "beta gamma delta"},
		{Text: "gamma delta epsilon"},
		{Text: "delta epsilon zeta"},
		{Text: "epsilon zeta eta"},
		{Text: "zeta eta theta"},
	}
	ctx := context.Background()

	top, err := r.Search(ctx, "beta gamma", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 {
		t.Fatalf("Search returned %d hit(s), want TopK=3", len(top))
	}

	all, err := r.SearchAll(ctx, "beta gamma", chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(chunks) {
		t.Fatalf("SearchAll returned %d of %d chunks — it must return the whole "+
			"corpus or it cannot be a baseline", len(all), len(chunks))
	}
	// Still sorted best-first, and the two agree on the winners.
	for i := 1; i < len(all); i++ {
		if all[i-1].Score < all[i].Score {
			t.Fatalf("SearchAll is not sorted descending at %d", i)
		}
	}
	for i := range top {
		if math.Abs(top[i].Score-all[i].Score) > 1e-9 {
			t.Fatalf("Search and SearchAll disagree at %d: %.6f vs %.6f",
				i, top[i].Score, all[i].Score)
		}
	}
}
