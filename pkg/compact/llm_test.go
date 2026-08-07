package compact

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIsContextOverflow(t *testing.T) {
	if !IsContextOverflow(errors.New("this model's maximum context length is 8192")) {
		t.Fatal("expected overflow")
	}
	if IsContextOverflow(errors.New("connection refused")) {
		t.Fatal("not overflow")
	}
}

func TestSummarizeAutoFallback(t *testing.T) {
	body := strings.Repeat("# Wave update\nlots of noise\n\n", 400)
	res := Summarize(context.Background(), "auto", body, 2048, func(ctx context.Context, b string, max int) (string, error) {
		return "", errors.New("llm down")
	})
	if !res.Compacted || res.AfterBytes >= res.BeforeBytes {
		t.Fatalf("%+v", res)
	}
}

func TestSummarizeLLM(t *testing.T) {
	body := strings.Repeat("x", 5000)
	res := Summarize(context.Background(), "llm", body, 1000, func(ctx context.Context, b string, max int) (string, error) {
		return "# CONTEXT (compacted)\nshort", nil
	})
	if !res.Compacted || !strings.Contains(res.Summary, "compacted") {
		t.Fatalf("%+v", res)
	}
}
