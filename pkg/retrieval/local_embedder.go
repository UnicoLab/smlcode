package retrieval

import (
	"context"
	"hash/fnv"
	"strings"
	"time"
	"unicode"
)

// LocalEmbedder is a pure-Go offline embedder: character n-gram + word hashing
// into a fixed dense projection (feature hashing). No network, no models.
//
// Better than bag-of-words TF-IDF for ranking related paraphrases because
// overlapping character n-grams capture morphological / token-stem affinity
// (rename/renamed, greet/greeting) while word hashes keep exact term signal.
type LocalEmbedder struct {
	Dim int // projection size; default 384
}

func NewLocalEmbedder() *LocalEmbedder {
	return &LocalEmbedder{Dim: 384}
}

func (e *LocalEmbedder) Name() string { return "local" }

func (e *LocalEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	dim := e.Dim
	if dim <= 0 {
		dim = 384
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = l2normalize(hashProject(t, dim))
	}
	return out, nil
}

func hashProject(text string, dim int) []float64 {
	vec := make([]float64, dim)
	lower := strings.ToLower(text)
	// Word tokens
	for _, tok := range tokenize(lower) {
		addHash(vec, "w:"+tok, 1.0)
		// light stem-ish prefixes for morph overlap
		if len(tok) > 4 {
			addHash(vec, "p:"+tok[:4], 0.4)
		}
		if len(tok) > 6 {
			addHash(vec, "p:"+tok[:6], 0.3)
		}
	}
	// Character n-grams (3–5) over alnum runs — paraphrase / morphology signal.
	var run strings.Builder
	flushRun := func() {
		s := run.String()
		run.Reset()
		if len(s) < 3 {
			return
		}
		padded := "  " + s + "  "
		for n := 3; n <= 5; n++ {
			for i := 0; i+n <= len(padded); i++ {
				gram := padded[i : i+n]
				w := 0.35
				if n == 4 {
					w = 0.45
				}
				addHash(vec, "g:"+gram, w)
			}
		}
	}
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			run.WriteRune(r)
		} else {
			flushRun()
		}
	}
	flushRun()
	return vec
}

func addHash(vec []float64, key string, weight float64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	v := h.Sum64()
	idx := int(v % uint64(len(vec)))
	// Signed hash to reduce collisions (Weinberger / feature hashing).
	sign := 1.0
	if v&1 == 1 {
		sign = -1.0
	}
	vec[idx] += sign * weight
	// Secondary bucket for denser signal.
	idx2 := int((v >> 17) % uint64(len(vec)))
	sign2 := 1.0
	if (v>>1)&1 == 1 {
		sign2 = -1.0
	}
	vec[idx2] += sign2 * weight * 0.5
}

// ProbeTimeout bounds the embedder reachability probe.
const ProbeTimeout = 2 * time.Second

// ProbeOpenAIEmbedder returns nil when a tiny probe embedding succeeds.
func ProbeOpenAIEmbedder(ctx context.Context, endpoint, model, apiKey string) error {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(model) == "" {
		return errEmbeddingsUnavailable
	}
	e := NewOpenAIEmbedder(endpoint, model, apiKey)
	_, err := e.Embed(ctx, []string{"slmcode probe"})
	return err
}

type embedUnavailable string

func (e embedUnavailable) Error() string { return string(e) }

const errEmbeddingsUnavailable = embedUnavailable("embeddings unavailable")

// ResolveEmbedder picks openai → local → lexical.
// mode is one of: "openai", "local", "lexical".
func ResolveEmbedder(ctx context.Context, cfg Config) (Embedder, string) {
	if cfg.Enabled && strings.TrimSpace(cfg.Endpoint) != "" && strings.TrimSpace(cfg.Model) != "" {
		// The probe timeout must ALWAYS apply. Honouring only a parent context
		// without a deadline meant a run-length parent deadline let an
		// unreachable endpoint stall embedder resolution for minutes.
		probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
		defer cancel()
		if err := ProbeOpenAIEmbedder(probeCtx, cfg.Endpoint, cfg.Model, cfg.APIKey); err == nil {
			return NewOpenAIEmbedder(cfg.Endpoint, cfg.Model, cfg.APIKey), "openai"
		}
		// Configured but unreachable → strong local fallback.
		return NewLocalEmbedder(), "local"
	}
	// No remote config: prefer local hashing over pure TF-IDF lexical.
	if cfg.ForceLexical {
		return NewLexicalEmbedder(), "lexical"
	}
	return NewLocalEmbedder(), "local"
}
