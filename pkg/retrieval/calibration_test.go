package retrieval

import (
	"context"
	"sort"
	"testing"
)

var unrelatedSentences = []string{
	"rewrite the payment rounding logic in the billing service",
	"configure nginx with cert-manager for TLS on staging cluster",
	"update the button border radius in styles.css theme tokens",
	"add a kubernetes ingress for the staging namespace",
	"bump the go module dependency for the grpc client",
	"write a migration to add a created_at column to users",
}

var relatedPairs = [][2]string{
	{"finish the Greet rename work", "Renamed Hello to Greet in pkg/greet/greet.go and updated call sites"},
	{"fix ProcessPayment rounding", "ProcessPayment now rounds half-up before returning the total"},
	{"how should workers edit files for SLMs?", "Prefer ws_edit for tiny SLM patches. Never invent main.go."},
}

// pairScores returns the sorted cosine similarities for every unrelated pair.
func pairScores(t *testing.T, e Embedder, texts []string, shared bool) []float64 {
	t.Helper()
	var out []float64
	if shared {
		vecs, err := e.Embed(context.Background(), texts)
		if err != nil {
			t.Fatal(err)
		}
		for i := range vecs {
			for j := i + 1; j < len(vecs); j++ {
				c, err := cosine(vecs[i], vecs[j])
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, c)
			}
		}
	} else {
		for i := range texts {
			for j := i + 1; j < len(texts); j++ {
				vecs, err := e.Embed(context.Background(), []string{texts[i], texts[j]})
				if err != nil {
					t.Fatal(err)
				}
				c, err := cosine(vecs[0], vecs[1])
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, c)
			}
		}
	}
	sort.Float64s(out)
	return out
}

// TestNoiseFloorsAreCalibrated pins the thresholds to the measured noise band
// of each embedder. If an embedder's maths changes, this fails loudly instead
// of silently re-admitting noise as "Retrieved prior knowledge".
func TestNoiseFloorsAreCalibrated(t *testing.T) {
	tests := []struct {
		name     string
		embedder Embedder
		shared   bool // lexical builds one vocab across the batch
		floor    float64
	}{
		{"local", NewLocalEmbedder(), false, MinScoreLocal},
		{"lexical", NewLexicalEmbedder(), true, MinScoreLexical},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			noise := pairScores(t, tc.embedder, unrelatedSentences, tc.shared)
			median := noise[(len(noise)-1)/2]
			if tc.floor <= median {
				t.Fatalf("floor %.3f is at or below the noise median %.3f (band %.3f..%.3f)",
					tc.floor, median, noise[0], noise[len(noise)-1])
			}
			if tc.floor <= 0.02 {
				t.Fatalf("floor %.3f is still the old noise-band value", tc.floor)
			}
		})
	}
}

func TestRelatedPairsClearTheLocalFloor(t *testing.T) {
	e := NewLocalEmbedder()
	for _, pair := range relatedPairs {
		t.Run(pair[0], func(t *testing.T) {
			vecs, err := e.Embed(context.Background(), []string{pair[0], pair[1]})
			if err != nil {
				t.Fatal(err)
			}
			c, err := cosine(vecs[0], vecs[1])
			if err != nil {
				t.Fatal(err)
			}
			if c < MinScoreLocal {
				t.Fatalf("related pair scores %.3f, under the floor %.3f", c, MinScoreLocal)
			}
		})
	}
}

func TestNoiseFloorAdaptsToCorpus(t *testing.T) {
	tests := []struct {
		name  string
		hits  []Scored
		want  float64
		exact bool
	}{
		{"too few chunks", []Scored{{Score: 0.9}, {Score: 0.1}}, 0, true},
		{
			name:  "median plus margin",
			hits:  []Scored{{Score: 0.9}, {Score: 0.5}, {Score: 0.4}, {Score: 0.3}, {Score: 0.2}},
			want:  0.4 + NoiseMargin,
			exact: true,
		},
		{"empty", nil, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NoiseFloor(tc.hits)
			if tc.exact && (got < tc.want-1e-9 || got > tc.want+1e-9) {
				t.Fatalf("got %.4f want %.4f", got, tc.want)
			}
		})
	}
}

func TestCalibratedThreshold(t *testing.T) {
	highBaseline := []Scored{{Score: 0.9}, {Score: 0.85}, {Score: 0.84}, {Score: 0.8}, {Score: 0.79}}
	tests := []struct {
		name     string
		mode     string
		hits     []Scored
		override float64
		want     float64
	}{
		{"override wins", "local", highBaseline, 0.7, 0.7},
		{"absolute floor for a small corpus", "local", []Scored{{Score: 0.9}}, 0, MinScoreLocal},
		{"noise floor wins on a high-baseline corpus", "local", highBaseline, 0, 0.84 + NoiseMargin},
		{"openai floor", "openai", []Scored{{Score: 0.9}}, 0, MinScoreOpenAI},
		{"lexical floor", "lexical", []Scored{{Score: 0.9}}, 0, MinScoreLexical},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CalibratedThreshold(tc.mode, tc.hits, tc.override)
			if got < tc.want-1e-9 || got > tc.want+1e-9 {
				t.Fatalf("got %.4f want %.4f", got, tc.want)
			}
		})
	}
}
