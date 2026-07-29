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
