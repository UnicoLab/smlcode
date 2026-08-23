package cli

import (
	"strings"
	"testing"
)

func TestRuneWidth(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{' ', 1},
		{'●', 1},    // U+25CF — 3 bytes, one cell
		{'▸', 1},    // U+25B8
		{'─', 1},    // U+2500
		{'⚠', 1},    // U+26A0
		{'✔', 1},    // U+2714
		{'世', 2},    // CJK
		{'あ', 2},    // Hiragana
		{'０', 2},    // fullwidth digit
		{0x0301, 0}, // combining acute
		{0x200D, 0}, // ZWJ
		{'\n', 0},
	}
	for _, c := range cases {
		if got := RuneWidth(c.r); got != c.want {
			t.Errorf("RuneWidth(%q)=%d want %d", c.r, got, c.want)
		}
	}
}

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	plain := "● worker"
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	styled := Green("●") + " " + Bold("worker")
	if got, want := VisibleWidth(styled), StringWidth(plain); got != want {
		t.Fatalf("VisibleWidth(styled)=%d want %d (styled=%q)", got, want, styled)
	}
	if len(styled) == len(plain) {
		t.Fatal("expected the styled string to carry escape bytes")
	}
}

func TestVisibleWidthByteCountingRegression(t *testing.T) {
	// Every one of these is 3 bytes but exactly one cell: byte counting
	// under-pads each by 2 and breaks the box borders.
	s := "●▸─⚠"
	if len(s) != 12 {
		t.Fatalf("precondition: expected 12 bytes, got %d", len(s))
	}
	if got := VisibleWidth(s); got != 4 {
		t.Fatalf("VisibleWidth=%d want 4", got)
	}
}

func TestVisibleWidthOSCHyperlink(t *testing.T) {
	// OSC 8 hyperlinks wrap a label in escape sequences terminated by ST.
	s := "\033]8;;http://x\033\\link\033]8;;\033\\"
	if got := VisibleWidth(s); got != 4 {
		t.Fatalf("VisibleWidth=%d want 4", got)
	}
	if got := StripANSI(s); got != "link" {
		t.Fatalf("StripANSI=%q want %q", got, "link")
	}
}

func TestTruncateWidthRuneBoundary(t *testing.T) {
	s := "●●●●"
	got := TruncateWidth(s, 2)
	if got != "●●" {
		t.Fatalf("TruncateWidth=%q want %q", got, "●●")
	}
	if strings.ContainsRune(got, '�') {
		t.Fatal("truncation produced a replacement character")
	}
}

func TestTruncateWidthWideRunes(t *testing.T) {
	// "世界" is 2 runes / 4 cells; a 3-cell budget fits only the first.
	if got := TruncateWidth("世界", 3); got != "世" {
		t.Fatalf("TruncateWidth=%q want %q", got, "世")
	}
	if got := TruncateWidth("世界", 4); got != "世界" {
		t.Fatalf("TruncateWidth=%q want full", got)
	}
}

func TestTruncateWidthKeepsStyleAndResets(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	s := Green("hello world")
	got := TruncateWidth(s, 5)
	if VisibleWidth(got) != 5 {
		t.Fatalf("width=%d want 5", VisibleWidth(got))
	}
	if !strings.HasSuffix(got, resetSeq) {
		t.Fatalf("expected trailing reset in %q", got)
	}
}

func TestPadWidthExact(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	for _, s := range []string{"", "abc", "●▸", Green("●") + "x", "世"} {
		got := PadWidth(s, 10)
		if VisibleWidth(got) != 10 {
			t.Fatalf("PadWidth(%q) width=%d want 10", s, VisibleWidth(got))
		}
	}
}

func TestClipWidthAddsEllipsis(t *testing.T) {
	got := ClipWidth("abcdefgh", 5)
	if VisibleWidth(got) != 5 {
		t.Fatalf("width=%d want 5 (%q)", VisibleWidth(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis in %q", got)
	}
	if got := ClipWidth("abc", 5); got != "abc" {
		t.Fatalf("short strings must pass through, got %q", got)
	}
}

func TestClipPreservesFullStringForNonPositiveN(t *testing.T) {
	if got := Clip("a  b", 0); got != "a b" {
		t.Fatalf("Clip(_,0)=%q want %q", got, "a b")
	}
}

func TestClipWidthKeepsIndentation(t *testing.T) {
	// clipMid used to collapse whitespace, eating the deliberate two-space
	// indent of every live line.
	in := "  ▸ worker  editing"
	if got := ClipWidth(in, 40); !strings.HasPrefix(got, "  ") {
		t.Fatalf("indent lost: %q", got)
	}
}
