package workspace

import (
	"strings"
	"testing"
)

// Pure-insertion hunks ("-N,0") insert AFTER line N, as in every unified-diff
// consumer; they used to land one line early.
func TestApplyPatchPureInsertionIsAfterLine(t *testing.T) {
	content := "a\nb\nc\n"
	for _, tc := range []struct {
		name, patch, want string
	}{
		{"after line 1", "@@ -1,0 +2,1 @@\n+X", "a\nX\nb\nc\n"},
		{"top of file (line 0)", "@@ -0,0 +1,1 @@\n+X", "X\na\nb\nc\n"},
		{"after last line", "@@ -3,0 +4,1 @@\n+X", "a\nb\nc\nX\n"},
		{"two lines after line 2", "@@ -2,0 +3,2 @@\n+X\n+Y", "a\nb\nX\nY\nc\n"},
		{"no trailing newline", "@@ -2,0 +3,1 @@\n+X", ""}, // handled below
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := content
			want := tc.want
			if tc.want == "" {
				src = "a\nb"
				want = "a\nb\nX\n"
			}
			got, _, err := ApplyPatch(src, tc.patch)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}

	// Multi-hunk: the line delta of an earlier insertion carries forward.
	got, _, err := ApplyPatch("a\nb\nc\nd\n", "@@ -1,0 +2,1 @@\n+X\n@@ -3,0 +5,1 @@\n+Y")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nX\nb\nc\nY\nd\n" {
		t.Fatalf("multi-hunk insertion: %q", got)
	}

	// Empty file, GNU-style header.
	got, _, err = ApplyPatch("", "@@ -0,0 +1,2 @@\n+one\n+two")
	if err != nil {
		t.Fatal(err)
	}
	if got != "one\ntwo\n" {
		t.Fatalf("insert into empty file: %q", got)
	}
}

// The marker-less SEARCH/REPLACE form was routed to the <<<<<<< parser, which
// rejected it as malformed, so it never worked at all.
func TestApplyPatchBareSearchReplace(t *testing.T) {
	content := "package a\n\nfunc F() int { return 1 }\n"
	for _, tc := range []struct{ name, patch string }{
		{"SEARCH/REPLACE headers", "SEARCH\nfunc F() int { return 1 }\n=======\nREPLACE\nfunc F() int { return 2 }"},
		{"SEARCH header only", "SEARCH\nfunc F() int { return 1 }\n=======\nfunc F() int { return 2 }"},
		{"trailer only", "func F() int { return 1 }\n=======\nfunc F() int { return 2 }\n>>>>>>> REPLACE"},
		{"colon headers", "SEARCH:\nfunc F() int { return 1 }\n=======\nREPLACE:\nfunc F() int { return 2 }"},
		{"no words at all", "func F() int { return 1 }\n=======\nfunc F() int { return 2 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, summary, err := ApplyPatch(content, tc.patch)
			if err != nil {
				t.Fatalf("bare form rejected: %v", err)
			}
			if !strings.Contains(got, "return 2") || strings.Contains(got, "return 1") {
				t.Fatalf("got %q", got)
			}
			if strings.Contains(got, "SEARCH") || strings.Contains(got, "REPLACE") || strings.Contains(got, "=======") {
				t.Fatalf("headers leaked into the file: %q (summary %q)", got, summary)
			}
		})
	}

	// A "=======" INSIDE a replacement (Markdown rule) after the first
	// separator is content, not a second separator.
	got, _, err := ApplyPatch("# T\n\nbody\n", "SEARCH\nbody\n=======\nbody\n\nRule\n=======\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Rule\n=======") {
		t.Fatalf("second ======= was not kept as content: %q", got)
	}

	// A unified-diff context line " =======" is not a bare separator.
	got, _, err = ApplyPatch("x\n=======\ny\n", "@@ -1,3 +1,3 @@\n-x\n+X\n =======\n y")
	if err != nil {
		t.Fatal(err)
	}
	if got != "X\n=======\ny\n" {
		t.Fatalf("diff with ======= context: %q", got)
	}
}

// Trailing prose and Markdown fences around a hunk are dropped instead of
// becoming context lines that can never match.
func TestApplyPatchIgnoresTrailingProseAndFences(t *testing.T) {
	content := "a\nb\nc\n"
	for _, tc := range []struct{ name, patch string }{
		{"closing fence", "@@ -2,1 +2,1 @@\n-b\n+B\n```"},
		{"opening and closing fence", "```diff\n@@ -2,1 +2,1 @@\n-b\n+B\n```"},
		{"trailing sentence", "@@ -2,1 +2,1 @@\n-b\n+B\nThis changes b to B."},
		{"fence then sentence", "@@ -2,1 +2,1 @@\n-b\n+B\n```\nThat should do it."},
		// ("Here is the patch:" essays are still rejected outright by the
		// anti-wander junk rule; a shorter lead-in reaches the parser.)
		{"anchorless with leading prose", "Apply this change:\n-b\n+B"},
		{"anchorless with trailing prose", "-b\n+B\nDone."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := ApplyPatch(content, tc.patch)
			if err != nil {
				t.Fatalf("patch rejected: %v", err)
			}
			if got != "a\nB\nc\n" {
				t.Fatalf("got %q", got)
			}
		})
	}

	// An unprefixed line FOLLOWED by a diff line is still context (models
	// drop the leading space), and blank context in the middle still counts.
	got, _, err := ApplyPatch("a\n\nb\nc\n", "@@ -1,4 +1,4 @@\na\n\n-b\n+B\nc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\n\nB\nc\n" {
		t.Fatalf("unprefixed context: %q", got)
	}

	// Trailing unprefixed lines in a MULTI-hunk diff are dropped per hunk.
	got, _, err = ApplyPatch("a\nb\nc\nd\n", "@@ -1,1 +1,1 @@\n-a\n+A\nnote one\n@@ -3,1 +3,1 @@\n-c\n+C\nnote two")
	if err != nil {
		t.Fatal(err)
	}
	if got != "A\nb\nC\nd\n" {
		t.Fatalf("multi-hunk with trailing notes: %q", got)
	}
}
