package cli

import (
	"strings"
	"testing"
)

func init() { SetColorMode(ColorNever) }

func TestDiffNoChange(t *testing.T) {
	fd := Diff("a.go", "one\ntwo\n", "one\ntwo\n", 3)
	if !fd.Empty() {
		t.Fatalf("expected empty diff, got %+v", fd)
	}
	if fd.UnifiedText() != "" {
		t.Fatalf("expected no unified text, got %q", fd.UnifiedText())
	}
}

func TestDiffSingleLineReplace(t *testing.T) {
	fd := Diff("a.go", "one\ntwo\nthree\n", "one\nTWO\nthree\n", 3)
	if fd.Added != 1 || fd.Removed != 1 {
		t.Fatalf("added=%d removed=%d want 1/1", fd.Added, fd.Removed)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(fd.Hunks))
	}
	txt := fd.UnifiedText()
	if !strings.Contains(txt, "-two") || !strings.Contains(txt, "+TWO") {
		t.Fatalf("unified text missing the change:\n%s", txt)
	}
	if !strings.Contains(txt, "@@ -1,3 +1,3 @@") {
		t.Fatalf("bad hunk header:\n%s", txt)
	}
}

func TestDiffPureInsertion(t *testing.T) {
	fd := Diff("a.go", "one\ntwo\n", "one\nmid\ntwo\n", 3)
	if fd.Added != 1 || fd.Removed != 0 {
		t.Fatalf("added=%d removed=%d want 1/0", fd.Added, fd.Removed)
	}
}

func TestDiffPureDeletion(t *testing.T) {
	fd := Diff("a.go", "one\nmid\ntwo\n", "one\ntwo\n", 3)
	if fd.Added != 0 || fd.Removed != 1 {
		t.Fatalf("added=%d removed=%d want 0/1", fd.Added, fd.Removed)
	}
}

func TestDiffNewFileIsAllAdditions(t *testing.T) {
	fd := Diff("new.go", "", "package main\n\nfunc main() {}\n", 3)
	if !fd.IsNew {
		t.Fatal("expected IsNew")
	}
	if fd.Removed != 0 || fd.Added != 3 {
		t.Fatalf("added=%d removed=%d want 3/0", fd.Added, fd.Removed)
	}
	if !strings.Contains(fd.UnifiedText(), "--- /dev/null") {
		t.Fatalf("new-file header missing:\n%s", fd.UnifiedText())
	}
}

func TestDiffDeletedFile(t *testing.T) {
	fd := Diff("gone.go", "x\n", "", 3)
	if !fd.IsDeleted {
		t.Fatal("expected IsDeleted")
	}
	if !strings.Contains(fd.UnifiedText(), "+++ /dev/null") {
		t.Fatalf("deleted-file header missing:\n%s", fd.UnifiedText())
	}
}

func TestDiffSeparateHunks(t *testing.T) {
	before := strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}, "\n") + "\n"
	after := strings.Join([]string{"a", "B", "c", "d", "e", "f", "g", "h", "i", "j", "K", "l"}, "\n") + "\n"
	fd := Diff("x", before, after, 1)
	if len(fd.Hunks) != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", len(fd.Hunks), fd.UnifiedText())
	}
}

func TestDiffMergesNearbyChangesIntoOneHunk(t *testing.T) {
	before := "a\nb\nc\nd\ne\n"
	after := "a\nB\nc\nD\ne\n"
	fd := Diff("x", before, after, 3)
	if len(fd.Hunks) != 1 {
		t.Fatalf("want 1 merged hunk, got %d", len(fd.Hunks))
	}
}

func TestDiffLineNumbers(t *testing.T) {
	fd := Diff("x", "a\nb\nc\n", "a\nB\nc\n", 1)
	var del, ins DiffOp
	for _, op := range fd.Hunks[0].Ops {
		switch op.Kind {
		case '-':
			del = op
		case '+':
			ins = op
		}
	}
	if del.OldLine != 2 || del.NewLine != 0 {
		t.Fatalf("delete op line numbers: %+v", del)
	}
	if ins.NewLine != 2 || ins.OldLine != 0 {
		t.Fatalf("insert op line numbers: %+v", ins)
	}
}

func TestDiffBinary(t *testing.T) {
	fd := Diff("bin", "abc\x00def", "abc\x00xyz", 3)
	if !fd.Binary {
		t.Fatal("expected binary detection")
	}
	if !strings.Contains(fd.UnifiedText(), "Binary files") {
		t.Fatal("expected binary notice")
	}
}

func TestDiffNoTrailingNewline(t *testing.T) {
	fd := Diff("x", "a\nb", "a\nc", 3)
	if fd.Added != 1 || fd.Removed != 1 {
		t.Fatalf("added=%d removed=%d", fd.Added, fd.Removed)
	}
}

func TestRenderDiffHeaderStats(t *testing.T) {
	fd := Diff("pkg/x.go", "a\n", "a\nb\nc\n", 3)
	head := RenderDiffHeader(fd)
	if !strings.Contains(head, "pkg/x.go") || !strings.Contains(head, "+2") || !strings.Contains(head, "-0") {
		t.Fatalf("header=%q", head)
	}
}

func TestRenderDiffContainsHunkHeaderAndBody(t *testing.T) {
	fd := Diff("x", "one\ntwo\n", "one\n2\n", 3)
	out := RenderDiff(fd, DiffRenderOptions{Width: 100, LineNums: true})
	if !strings.Contains(out, "@@") {
		t.Fatalf("missing hunk header:\n%s", out)
	}
	if !strings.Contains(out, "- two") || !strings.Contains(out, "+ 2") {
		t.Fatalf("missing body:\n%s", out)
	}
}

func TestRenderDiffRespectsWidth(t *testing.T) {
	long := strings.Repeat("x", 300)
	fd := Diff("x", "a\n", long+"\n", 3)
	out := RenderDiff(fd, DiffRenderOptions{Width: 60, LineNums: true})
	for _, line := range strings.Split(out, "\n") {
		if VisibleWidth(line) > 60 {
			t.Fatalf("line exceeds width: %d %q", VisibleWidth(line), line)
		}
	}
}

func TestRenderDiffMaxLinesTruncates(t *testing.T) {
	var before, after []string
	for i := 0; i < 200; i++ {
		before = append(before, "line")
		after = append(after, "LINE")
	}
	fd := Diff("x", strings.Join(before, "\n")+"\n", strings.Join(after, "\n")+"\n", 3)
	out := RenderDiff(fd, DiffRenderOptions{Width: 100, MaxLines: 10})
	if !strings.Contains(out, "diff truncated") {
		t.Fatalf("expected truncation notice:\n%s", out)
	}
}

func TestWordHighlightMarksOnlyTheChange(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	oldHL, newHL := wordHighlight("func Foo(a int) error", "func Foo(a string) error")
	if StripANSI(oldHL) != "func Foo(a int) error" {
		t.Fatalf("old text mangled: %q", StripANSI(oldHL))
	}
	if StripANSI(newHL) != "func Foo(a string) error" {
		t.Fatalf("new text mangled: %q", StripANSI(newHL))
	}
	if !strings.Contains(oldHL, "\033[7m") || !strings.Contains(newHL, "\033[7m") {
		t.Fatal("expected reverse-video highlighting")
	}
}

func TestWordHighlightSkipsUnrelatedLines(t *testing.T) {
	a := "completely different content here"
	b := "xxxxxxxx"
	oldHL, newHL := wordHighlight(a, b)
	if oldHL != a || newHL != b {
		t.Fatal("expected the plain text when the lines share almost nothing")
	}
}

func TestTokenizeLine(t *testing.T) {
	got := tokenizeLine("a b(c)")
	want := []string{"a", " ", "b", "(", "c", ")"}
	if len(got) != len(want) {
		t.Fatalf("got %q want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %q want %q", i, got[i], want[i])
		}
	}
}

func TestPairReplacements(t *testing.T) {
	ops := []DiffOp{
		{Kind: ' '}, {Kind: '-'}, {Kind: '+'}, {Kind: ' '},
	}
	pairs := pairReplacements(ops)
	if pairs[1] != 2 {
		t.Fatalf("expected 1→2 pairing, got %v", pairs)
	}
	// Unbalanced runs must not pair.
	ops = []DiffOp{{Kind: '-'}, {Kind: '-'}, {Kind: '+'}}
	if len(pairReplacements(ops)) != 0 {
		t.Fatal("unbalanced runs must not word-diff")
	}
}

func TestDiffStatLine(t *testing.T) {
	fd := Diff("pkg/a.go", "a\n", "b\n", 3)
	line := DiffStatLine(fd)
	if !strings.Contains(line, "pkg/a.go") || !strings.Contains(line, "+1 -1") {
		t.Fatalf("stat line=%q", line)
	}
}

func TestDiffLargeInputDegradesGracefully(t *testing.T) {
	var a, b []string
	for i := 0; i < 3000; i++ {
		a = append(a, "aaaa")
		b = append(b, "bbbb")
	}
	fd := Diff("big", strings.Join(a, "\n"), strings.Join(b, "\n"), 3)
	if fd.Added == 0 || fd.Removed == 0 {
		t.Fatalf("expected a replace diff, got %+v", fd.Stat())
	}
}
