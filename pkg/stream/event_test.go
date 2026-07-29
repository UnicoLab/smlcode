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
