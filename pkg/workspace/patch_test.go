package workspace

import (
	"strings"
	"testing"
)

func TestApplyPatchSearchReplace(t *testing.T) {
	src := "func Hello() {\n\treturn\n}\n"
	patch := "<<<<<<< SEARCH\nfunc Hello() {\n\treturn\n}\n=======\nfunc Hello() string {\n\treturn \"hi\"\n}\n>>>>>>> REPLACE"
	next, summary, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, `return "hi"`) {
		t.Fatalf("next=%q summary=%s", next, summary)
	}
}

func TestApplyPatchUnifiedMinusPlus(t *testing.T) {
	src := "alpha\nbeta\ngamma\n"
	patch := "--- a/f\n+++ b/f\n@@\n-alpha\n+ALPHA\n beta\n gamma\n"
	next, _, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(next, "ALPHA\n") {
		t.Fatalf("got %q", next)
	}
}

func TestApplyPatchNotFound(t *testing.T) {
	_, _, err := ApplyPatch("hello", "<<<<<<< SEARCH\nnope\n=======\nyep\n>>>>>>> REPLACE")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyPatchRejectsJunk(t *testing.T) {
	_, _, err := ApplyPatch("hello", "Here is the patch: please update the file somehow")
	if err == nil {
		t.Fatal("expected junk reject")
	}
	_, _, err = ApplyPatch("hello", "--- a/a\n+++ b/a\n+x\n--- a/b\n+++ b/b\n+y\n")
	if err == nil {
		t.Fatal("expected multi-file reject")
	}
}

// The old fallback concatenated only the "-" lines and did
// strings.Replace(content, oldStr, newStr, 1) — first occurrence wins — which
// silently patched the wrong function when the context block missed.
func TestApplyPatchRefusesAmbiguousFallback(t *testing.T) {
	src := "func a() {\n\tv := 1\n}\n\nfunc b() {\n\tv := 1\n}\n"
	cases := []struct {
		name  string
		patch string
	}{
		{
			name:  "search replace with duplicated body",
			patch: "<<<<<<< SEARCH\n\tv := 1\n=======\n\tv := 2\n>>>>>>> REPLACE",
		},
		{
			name:  "unified hunk whose context misses",
			patch: "@@\n-\tv := 1\n+\tv := 2\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, _, err := ApplyPatch(src, tc.patch)
			if err == nil {
				t.Fatalf("ambiguous patch must be refused, got %q", next)
			}
			if !strings.Contains(err.Error(), "ambiguous") && !strings.Contains(err.Error(), "matches") {
				t.Fatalf("error must explain ambiguity: %v", err)
			}
		})
	}
}

func TestApplyPatchMultiHunk(t *testing.T) {
	src := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"
	patch := "--- a/f\n+++ b/f\n" +
		"@@ -1,3 +1,3 @@\n-l1\n+L1\n l2\n l3\n" +
		"@@ -8,3 +8,3 @@\n l8\n-l9\n+L9\n l10\n"
	next, summary, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatalf("multi-hunk diff must apply: %v", err)
	}
	if !strings.Contains(next, "L1\n") || !strings.Contains(next, "L9\n") {
		t.Fatalf("both hunks should apply: %q", next)
	}
	if !strings.Contains(summary, "2 hunk") {
		t.Fatalf("summary should count hunks: %q", summary)
	}
}

func TestApplyPatchPerHunkFailureReport(t *testing.T) {
	src := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"
	patch := "@@ -1,3 +1,3 @@\n-l1\n+L1\n l2\n l3\n" +
		"@@ -8,3 +8,3 @@\n MISSING\n-NOPE\n+YEP\n l10\n"
	next, _, err := ApplyPatch(src, patch)
	if err == nil {
		t.Fatalf("a failed hunk must fail the patch, got %q", next)
	}
	msg := err.Error()
	for _, want := range []string{"hunk 1/2 ok", "hunk 2/2 FAILED", "no changes were written"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("report missing %q:\n%s", want, msg)
		}
	}
}

// The old parser swallowed any line starting with --- / +++ as a file header,
// so a Markdown rule or Python docstring inside a hunk desynchronised the diff.
func TestApplyPatchKeepsContentLookingLikeHeaders(t *testing.T) {
	src := "intro\n---\nbody\n"
	patch := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n intro\n----\n+===\n body\n"
	next, _, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatalf("content lines starting with --- must not be dropped: %v", err)
	}
	if next != "intro\n===\nbody\n" {
		t.Fatalf("got %q", next)
	}
}

// A model emitting "" instead of " " for a blank context line used to drop the
// line from BOTH sides, silently deleting it from the file.
func TestApplyPatchKeepsUnprefixedBlankContext(t *testing.T) {
	src := "a\n\nb\n"
	patch := "@@ -1,3 +1,3 @@\n a\n\n-b\n+B\n"
	next, _, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatal(err)
	}
	if next != "a\n\nB\n" {
		t.Fatalf("blank context line must survive: %q", next)
	}
}

func TestApplyPatchAnchorsOnHunkLineNumbers(t *testing.T) {
	// Two identical blocks: only the @@ anchor can disambiguate.
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		if i == 10 || i == 50 {
			b.WriteString("target\n")
			continue
		}
		b.WriteString("filler\n")
	}
	src := b.String()
	// Whole-file search is ambiguous, but line 50 is unique inside ±20 lines.
	patch := "@@ -50,1 +50,1 @@\n-target\n+TARGET\n"
	next, summary, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatalf("anchored hunk must resolve the ambiguity: %v", err)
	}
	if strings.Count(next, "TARGET") != 1 {
		t.Fatalf("expected exactly one replacement: %q", summary)
	}
	lines := strings.Split(next, "\n")
	if lines[49] != "TARGET" {
		t.Fatalf("wrong occurrence patched; line 50 = %q", lines[49])
	}
	if !strings.Contains(summary, "anchored") {
		t.Fatalf("summary should report the anchored strategy: %q", summary)
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		name                     string
		line                     string
		oStart, oLen, nStart, nL int
	}{
		{"full", "@@ -5,3 +5,4 @@", 5, 3, 5, 4},
		{"no lengths", "@@ -12 +12 @@", 12, 1, 12, 1},
		{"trailing context", "@@ -1,2 +1,2 @@ func Foo() {", 1, 2, 1, 2},
		{"bare", "@@", 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := parseHunkHeader(tc.line)
			if h.OldStart != tc.oStart || h.NewStart != tc.nStart {
				t.Fatalf("starts %d/%d want %d/%d", h.OldStart, h.NewStart, tc.oStart, tc.nStart)
			}
			if tc.oLen > 0 && h.OldLen != tc.oLen {
				t.Fatalf("oldLen=%d want %d", h.OldLen, tc.oLen)
			}
			if tc.nL > 0 && h.NewLen != tc.nL {
				t.Fatalf("newLen=%d want %d", h.NewLen, tc.nL)
			}
		})
	}
}

func TestApplyPatchEmptySearchOnNonEmptyFile(t *testing.T) {
	_, _, err := ApplyPatch("existing\n", "<<<<<<< SEARCH\n=======\nnew\n>>>>>>> REPLACE")
	if err == nil {
		t.Fatal("empty SEARCH on a non-empty file must be refused")
	}
	if !strings.Contains(err.Error(), "empty SEARCH") {
		t.Fatalf("message should name the problem: %v", err)
	}
}

func TestApplyPatchUsesFallbackLadder(t *testing.T) {
	// Model re-emitted the block with trailing whitespace drift.
	src := "func F() {\n\treturn\n}\n"
	patch := "<<<<<<< SEARCH\nfunc F() {   \n\treturn\t\n}\n=======\nfunc F() int {\n\treturn 1\n}\n>>>>>>> REPLACE"
	next, summary, err := ApplyPatch(src, patch)
	if err != nil {
		t.Fatalf("ladder should rescue whitespace drift: %v", err)
	}
	if !strings.Contains(next, "return 1") {
		t.Fatalf("got %q", next)
	}
	if !strings.Contains(summary, "trailing whitespace") {
		t.Fatalf("summary should name the strategy: %q", summary)
	}
}
