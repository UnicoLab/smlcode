package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestAdaptiveMaxToolChars(t *testing.T) {
	tests := []struct {
		tokens int
		want   int
	}{
		// 8K token context tier (3000 chars)
		{1024, MaxToolChars8K},
		{4096, MaxToolChars8K},
		{8192, MaxToolChars8K},

		// 16K token context tier (5000 chars)
		{8193, MaxToolChars16K},
		{12000, MaxToolChars16K},
		{16384, MaxToolChars16K},

		// > 16K context tier (DefaultMaxToolChars = 8000 chars)
		{16385, DefaultMaxToolChars},
		{32768, DefaultMaxToolChars},
		{65536, DefaultMaxToolChars},
		{131072, DefaultMaxToolChars},

		// Unconfigured / invalid (DefaultMaxToolChars = 8000 chars)
		{0, DefaultMaxToolChars},
		{-100, DefaultMaxToolChars},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d_tokens", tc.tokens), func(t *testing.T) {
			got := AdaptiveMaxToolChars(tc.tokens)
			if got != tc.want {
				t.Fatalf("AdaptiveMaxToolChars(%d) = %d, want %d", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestWorkspaceMaxToolChars(t *testing.T) {
	// 1. Nil workspace returns default
	var nilWs *Workspace
	if got := nilWs.maxToolChars(); got != DefaultMaxToolChars {
		t.Fatalf("nil Workspace.maxToolChars() = %d, want %d", got, DefaultMaxToolChars)
	}

	// 2. Explicit MaxToolChars overrides everything
	wExplicit := &Workspace{
		MaxToolChars:       1234,
		ContextLimitTokens: 8192,
		MaxContextKB:       16,
	}
	if got := wExplicit.maxToolChars(); got != 1234 {
		t.Fatalf("explicit MaxToolChars override failed: got %d, want 1234", got)
	}

	// 3. ContextLimitTokens drives adaptive sizing
	w8K := &Workspace{ContextLimitTokens: 8192}
	if got := w8K.maxToolChars(); got != MaxToolChars8K {
		t.Fatalf("w8K.maxToolChars() = %d, want %d", got, MaxToolChars8K)
	}

	w16K := &Workspace{ContextLimitTokens: 16384}
	if got := w16K.maxToolChars(); got != MaxToolChars16K {
		t.Fatalf("w16K.maxToolChars() = %d, want %d", got, MaxToolChars16K)
	}

	w32K := &Workspace{ContextLimitTokens: 32768}
	if got := w32K.maxToolChars(); got != DefaultMaxToolChars {
		t.Fatalf("w32K.maxToolChars() = %d, want %d", got, DefaultMaxToolChars)
	}

	// 4. MaxContextKB fallback when ContextLimitTokens is not set
	wLegacyKB16 := &Workspace{MaxContextKB: 16} // 16 * 1024 / 4 = 4096 tokens <= 8K
	if got := wLegacyKB16.maxToolChars(); got != MaxToolChars8K {
		t.Fatalf("wLegacyKB16.maxToolChars() = %d, want %d", got, MaxToolChars8K)
	}

	wLegacyKB64 := &Workspace{MaxContextKB: 64} // 64 * 1024 / 4 = 16384 tokens <= 16K
	if got := wLegacyKB64.maxToolChars(); got != MaxToolChars16K {
		t.Fatalf("wLegacyKB64.maxToolChars() = %d, want %d", got, MaxToolChars16K)
	}

	wLegacyKB128 := &Workspace{MaxContextKB: 128} // 128 * 1024 / 4 = 32768 tokens > 16K
	if got := wLegacyKB128.maxToolChars(); got != DefaultMaxToolChars {
		t.Fatalf("wLegacyKB128.maxToolChars() = %d, want %d", got, DefaultMaxToolChars)
	}
}

func TestWorkspaceCappedToolOutputAdaptive(t *testing.T) {
	// Create mock tool executor that generates 12,000 characters
	longOutput := strings.Repeat("a123456789\n", 1200)

	// Test 8K context model (capped to 3000 chars shown)
	w8K := &Workspace{ContextLimitTokens: 8192}
	exec8K := w8K.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return longOutput, nil
	})

	res8K, err := exec8K(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s8K, ok := res8K.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", res8K)
	}
	// The body is truncated to MaxToolChars8K, plus the steering footer (~380 chars)
	if len(s8K) >= len(longOutput) {
		t.Fatalf("8K output was not truncated: length %d", len(s8K))
	}
	want8KMarker := fmt.Sprintf("%d shown", MaxToolChars8K)
	if !strings.Contains(s8K, want8KMarker) || !strings.Contains(s8K, "result truncated") {
		t.Fatalf("missing truncation indicator %q for 8K in:\n%s", want8KMarker, s8K[len(s8K)-400:])
	}

	// Test 16K context model (capped to 5000 chars shown)
	w16K := &Workspace{ContextLimitTokens: 16384}
	exec16K := w16K.capped(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return longOutput, nil
	})

	res16K, err := exec16K(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s16K, ok := res16K.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", res16K)
	}
	if len(s16K) >= len(longOutput) {
		t.Fatalf("16K output was not truncated: length %d", len(s16K))
	}
	want16KMarker := fmt.Sprintf("%d shown", MaxToolChars16K)
	if !strings.Contains(s16K, want16KMarker) || !strings.Contains(s16K, "result truncated") {
		t.Fatalf("missing truncation indicator %q for 16K in:\n%s", want16KMarker, s16K[len(s16K)-400:])
	}
}
