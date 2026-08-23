package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 5, ""},
		{"under", "abc", 10, "abc"},
		{"exact", "abc", 3, "abc"},
		{"ascii cut", "abcdef", 3, "abc"},
		{"zero", "abcdef", 0, ""},
		{"negative", "abcdef", -1, ""},
		{"multibyte boundary", "héllo", 2, "h"},
		{"multibyte exact", "héllo", 3, "hé"},
		{"emoji", "a👍b", 3, "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Clip(tc.in, tc.n)
			if got != tc.want {
				t.Fatalf("Clip(%q,%d)=%q want %q", tc.in, tc.n, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("invalid utf8: %q", got)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		n      int
		marker string
		want   string
	}{
		{"fits", "abc", 10, "…", "abc"},
		{"cut with marker", "abcdefghij", 6, "..", "abcd.."},
		{"marker too big", "abcdefghij", 2, "12345", "ab"},
		{"no marker", "abcdefghij", 4, "", "abcd"},
		{"zero", "abcdefghij", 0, "..", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.in, tc.n, tc.marker); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"single", "hello", 10, "hello"},
		{"multi", "hello\nworld", 10, "hello"},
		{"clip", "hello world", 5, "hello…"},
		{"unicode clip", "héllo wörld", 4, "héll…"},
		{"leading blank line", "   \n x", 10, "x"},
		{"all blank", "   \n\n  ", 10, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstLine(tc.in, tc.max); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestHeadTail(t *testing.T) {
	body := strings.Repeat("line\n", 200)
	got := HeadTail(body, 100, 100, "\n...[elided]\n")
	if !strings.Contains(got, "[elided]") {
		t.Fatalf("missing marker: %q", got)
	}
	if len(got) > 260 {
		t.Fatalf("too long: %d", len(got))
	}
	if HeadTail("short", 100, 100, "") != "short" {
		t.Fatal("short body should pass through")
	}
}

func TestClipRunes(t *testing.T) {
	if got := ClipRunes("héllo", 3); got != "hél" {
		t.Fatalf("got %q", got)
	}
	if got := ClipRunes("ab", 5); got != "ab" {
		t.Fatalf("got %q", got)
	}
}
