package stream

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	if Truncate("  hi  ", 10) != "hi" {
		t.Fatalf("trim failed: %q", Truncate("  hi  ", 10))
	}
	long := strings.Repeat("a", 50)
	got := Truncate(long, 10)
	if len(got) < 10 || !hasEllipsis(got) {
		t.Fatalf("truncate=%q", got)
	}
	if Truncate("short", 0) != "short" {
		t.Fatal("n<=0 should return full")
	}
}

func TestKindConstants(t *testing.T) {
	for _, k := range []string{KindPhase, KindAgentStart, KindAgentEnd, KindFileChange, KindCoord} {
		if k == "" {
			t.Fatal("empty kind")
		}
	}
}

func hasEllipsis(s string) bool {
	return strings.HasSuffix(s, "…") || strings.HasSuffix(s, "...")
}

func TestTruncateCutsOnRuneBoundary(t *testing.T) {
	// Byte slicing used to split multi-byte runes into replacement characters.
	s := "●●●●●"
	got := Truncate(s, 3)
	if got != "●●●…" {
		t.Fatalf("Truncate=%q want %q", got, "●●●…")
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncation produced a replacement character: %q", got)
	}
}

func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// Five 3-byte runes are 15 bytes but 5 runes: a byte-based cap of 10 would
	// cut mid-rune; a rune-based cap of 10 leaves the string alone.
	s := "●●●●●"
	if got := Truncate(s, 10); got != s {
		t.Fatalf("Truncate=%q want the input unchanged", got)
	}
}

func TestTokenKindAndPayload(t *testing.T) {
	if KindToken == "" {
		t.Fatal("KindToken must be defined for token-by-token streaming")
	}
	e := Event{Kind: KindToken, Agent: "worker", Data: Token{Delta: "hel", Tokens: 3}}
	tok, ok := e.Data.(Token)
	if !ok || tok.Delta != "hel" || tok.Tokens != 3 {
		t.Fatalf("token payload=%+v ok=%v", e.Data, ok)
	}
}
