package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// LexicalEmbedder builds sparse bag-of-words vectors with TF weighting.
// Used as the default offline fallback when no embedding endpoint is configured.
type LexicalEmbedder struct {
	vocab map[string]int
}

func NewLexicalEmbedder() *LexicalEmbedder {
	return &LexicalEmbedder{vocab: map[string]int{}}
}

func (e *LexicalEmbedder) Name() string { return "lexical" }

func (e *LexicalEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	if e.vocab == nil {
		e.vocab = map[string]int{}
	}
	// Build shared vocab across the batch so cosine is in the same space.
	for _, t := range texts {
		for _, tok := range tokenize(t) {
			if _, ok := e.vocab[tok]; !ok {
				e.vocab[tok] = len(e.vocab)
			}
		}
	}
	dim := len(e.vocab)
	if dim == 0 {
		out := make([][]float64, len(texts))
		for i := range out {
			out[i] = []float64{}
		}
		return out, nil
	}
	// Document frequency for simple IDF.
	df := make([]int, dim)
	docs := make([]map[int]float64, len(texts))
	for i, t := range texts {
		tf := map[int]float64{}
		for _, tok := range tokenize(t) {
			idx := e.vocab[tok]
			tf[idx]++
		}
		docs[i] = tf
		seen := map[int]bool{}
		for idx := range tf {
			if !seen[idx] {
				df[idx]++
				seen[idx] = true
			}
		}
	}
	nDocs := float64(len(texts))
	out := make([][]float64, len(texts))
	for i, tf := range docs {
		vec := make([]float64, dim)
		for idx, c := range tf {
			idf := math.Log((nDocs+1)/(float64(df[idx])+1)) + 1
			vec[idx] = c * idf
		}
		out[i] = l2normalize(vec)
	}
	return out, nil
}

// OpenAIEmbedder calls POST {endpoint}/embeddings (OpenAI-compatible).
type OpenAIEmbedder struct {
	Endpoint string
	Model    string
	APIKey   string
	Client   *http.Client
}

func NewOpenAIEmbedder(endpoint, model, apiKey string) *OpenAIEmbedder {
	ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	// Accept bare host or /v1 suffix.
	if !strings.HasSuffix(ep, "/v1") && !strings.HasSuffix(ep, "/embeddings") {
		// keep as-is; request path appends /embeddings under /v1 when needed
	}
	return &OpenAIEmbedder{
		Endpoint: ep,
		Model:    model,
		APIKey:   apiKey,
		Client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (e *OpenAIEmbedder) Name() string {
	if e == nil || e.Model == "" {
		return "openai-embed"
	}
	return "openai-embed:" + e.Model
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if e == nil || e.Endpoint == "" || e.Model == "" {
		return nil, fmt.Errorf("embedding endpoint/model not configured")
	}
	url := e.Endpoint
	switch {
	case strings.HasSuffix(url, "/embeddings"):
		// ok
	case strings.HasSuffix(url, "/v1"):
		url += "/embeddings"
	default:
		url = strings.TrimRight(url, "/") + "/v1/embeddings"
	}
	payload := map[string]interface{}{
		"model": e.Model,
		"input": texts,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("empty embeddings response")
	}
	out := make([][]float64, len(texts))
	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	// Some servers omit index — fill sequentially.
	filled := 0
	for i := range out {
		if out[i] != nil {
			filled++
		}
	}
	if filled == 0 && len(parsed.Data) == len(texts) {
		for i, d := range parsed.Data {
			out[i] = d.Embedding
		}
	}
	for i := range out {
		if out[i] == nil {
			return nil, fmt.Errorf("missing embedding for index %d", i)
		}
	}
	return out, nil
}

// FakeEmbedder is a deterministic test double: vectors are hashed token bags.
type FakeEmbedder struct {
	Fail bool
}

func (f *FakeEmbedder) Name() string { return "fake" }

func (f *FakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	if f != nil && f.Fail {
		return nil, fmt.Errorf("fake embedder failure")
	}
	// Reuse lexical for stable ranking in tests.
	return NewLexicalEmbedder().Embed(context.Background(), texts)
}

func l2normalize(v []float64) []float64 {
	var n float64
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	inv := 1 / math.Sqrt(n)
	for i := range v {
		v[i] *= inv
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
