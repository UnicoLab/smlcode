package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestWS(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	// t.TempDir can sit behind a symlink (/tmp → /private/tmp on macOS);
	// resolve it so the jail comparison is apples to apples.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return &Workspace{
		Root: root, Reads: NewReadTracker(),
		ReadBeforeEdit: true, WriteGuard: true, OverEditGuard: true,
	}, root
}

// strOut renders a tool result for assertions. Errors and non-strings are
// rendered too, so an assertion on the message fails loudly instead of
// panicking. Written as a single-argument helper so it can wrap a two-value
// tool call directly: strOut(w.editFile(ctx, args)).
func strOut(v interface{}, err error) string {
	if err != nil {
		return "ERR: " + err.Error()
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("NON-STRING(%T): %v", v, v)
	}
	return s
}

// ── item 1: empty old_str ──────────────────────────────────────────────────

func TestEditRejectsEmptyOldStr(t *testing.T) {
	cases := []struct {
		name       string
		oldStr     string
		replaceAll interface{}
	}{
		{"empty", "", false},
		{"spaces", "   ", false},
		{"newline", "\n", false},
		{"empty with replace_all", "", true},
		{"empty with replace_all as string", "", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, root := newTestWS(t)
			body := "package a\n\nfunc F() {}\n"
			mustWrite(t, filepath.Join(root, "a.go"), body)
			w.Reads.Mark("a.go")
			out := strOut(w.editFile(context.Background(), map[string]interface{}{
				"path": "a.go", "old_str": tc.oldStr, "new_str": "X",
				"replace_all": tc.replaceAll,
			}))
			if !strings.Contains(out, "old_str is empty") {
				t.Fatalf("expected empty-old_str refusal, got %q", out)
			}
			if !strings.Contains(out, "ws_write") {
				t.Fatal("refusal must name the corrective action (ws_write / append recipe)")
			}
			got, _ := os.ReadFile(filepath.Join(root, "a.go"))
			if string(got) != body {
				t.Fatalf("file must be untouched, got %q", got)
			}
		})
	}
}

func TestAssessOverEditDoesNotMaskEmptyOldStr(t *testing.T) {
	// AssessOverEdit returns "" for an empty search; editFile must catch it first.
	if msg := AssessOverEdit("some longer body text", "", "x"); msg != "" {
		t.Fatalf("over-edit guard should defer to the specific message: %q", msg)
	}
}

// ── item 11: line-number prefixes ──────────────────────────────────────────

func TestEditRejectsLineNumberedOldStr(t *testing.T) {
	cases := []struct{ name, oldStr string }{
		{"single line", "     3|func F() {}"},
		{"multi line", "     1|package a\n     2|"},
		{"no padding", "3|func F() {}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, root := newTestWS(t)
			mustWrite(t, filepath.Join(root, "a.go"), "package a\n\nfunc F() {}\n")
			w.Reads.Mark("a.go")
			out := strOut(w.editFile(context.Background(), map[string]interface{}{
				"path": "a.go", "old_str": tc.oldStr, "new_str": "func G() {}",
			}))
			if !strings.Contains(out, "line-number prefix") {
				t.Fatalf("expected line-number diagnosis, got %q", out)
			}
		})
	}
}

func TestFuzzyHintHasNoLineNumbers(t *testing.T) {
	hint := fuzzyEditHint("package demo\n\nfunc Hello() {}\n", "func Hello( ) {}")
	if hint == "" {
		t.Fatal("expected a hint")
	}
	if lineNumberPrefixRe.MatchString(hint) {
		t.Fatalf("hint must not carry a copyable line-number prefix:\n%s", hint)
	}
	if !strings.Contains(hint, "(lines ") {
		t.Fatal("hint should put the range in the caption instead")
	}
	if !strings.Contains(hint, "VERBATIM") {
		t.Fatal("hint should say the text is copyable verbatim")
	}
}

func TestPatchRejectsLineNumberedBody(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	w.Reads.Mark("a.go")
	out := strOut(w.patchFile(context.Background(), map[string]interface{}{
		"path": "a.go", "patch": "<<<<<<< SEARCH\n     1|package a\n=======\npackage b\n>>>>>>> REPLACE",
	}))
	if !strings.Contains(out, "line-number prefix") {
		t.Fatalf("got %q", out)
	}
}

// ── item 4: symlink jail ───────────────────────────────────────────────────

func TestResolveRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	w, root := newTestWS(t)
	outside := t.TempDir()
	if real, err := filepath.EvalSymlinks(outside); err == nil {
		outside = real
	}
	mustWrite(t, filepath.Join(outside, "secret.txt"), "top secret\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cases := []string{"escape/secret.txt", "link.txt", "escape/new.txt", "escape"}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			if _, err := w.resolve(rel); err == nil {
				t.Fatalf("symlink escape %q must be refused", rel)
			} else if !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("error should name the cause: %v", err)
			}
		})
	}
	// A read through the symlink is refused too (not just a write).
	if _, err := w.readFile(context.Background(), map[string]interface{}{"path": "link.txt"}); err == nil {
		t.Fatal("read through an escaping symlink must fail")
	}
	// An in-jail symlink still works.
	mustWrite(t, filepath.Join(root, "real.go"), "package a\n")
	if err := os.Symlink(filepath.Join(root, "real.go"), filepath.Join(root, "inside.go")); err == nil {
		if _, err := w.resolve("inside.go"); err != nil {
			t.Fatalf("in-jail symlink must be allowed: %v", err)
		}
	}
}

func TestResolveRefusesLexicalEscape(t *testing.T) {
	w, _ := newTestWS(t)
	for _, rel := range []string{"../outside", "../../etc/passwd", "a/../../b"} {
		if _, err := w.resolve(rel); err == nil {
			t.Fatalf("%q must be refused", rel)
		}
	}
}

// ── item 5: .slmcode write boundary ────────────────────────────────────────

func TestHarnessStateWriteBoundary(t *testing.T) {
	cases := []struct {
		path    string
		allowed bool
	}{
		{".slmcode/hooks.json", false},
		{".slmcode/config.yaml", false},
		{".slmcode/pending/x.patch.json", false},
		{".slmcode", false},
		{".slmcode/scratch/notes.md", true},
		{".slmcode/scratch", true},
		{"pkg/a.go", true},
		{"slmcode/other.txt", true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			err := CheckHarnessStateWrite(tc.path)
			if tc.allowed && err != nil {
				t.Fatalf("expected allowed, got %v", err)
			}
			if !tc.allowed {
				if err == nil {
					t.Fatal("expected refusal")
				}
				if !strings.Contains(err.Error(), ScratchDir) {
					t.Fatalf("refusal must point at the scratch dir: %v", err)
				}
			}
		})
	}
}

func TestWriteToolRefusesHooksJSON(t *testing.T) {
	w, _ := newTestWS(t)
	_, err := w.writeFile(context.Background(), map[string]interface{}{
		"path": ".slmcode/hooks.json", "content": `{"hooks":{"PreToolUse":[{"command":"id"}]}}`,
	})
	if err == nil {
		t.Fatal("writing hooks.json through a tool must be refused")
	}
	// Focus guard disabled here — the boundary must hold on its own.
	if !strings.Contains(err.Error(), "harness control state") {
		t.Fatalf("got %v", err)
	}
}

func TestFocusGuardDeniesSlmDirButAllowsScratch(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"pkg/loop/runner.go"}})
	if g.Allow(".slmcode/hooks.json") {
		t.Fatal(".slmcode writes must not be blanket-allowed any more")
	}
	if !g.Allow(".slmcode/scratch/plan.md") {
		t.Fatal("scratch must stay writable")
	}
}

// ── item 12: path normalization in every tool ──────────────────────────────

func TestReadThenEditWithRootAnchoredPath(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	for _, spelling := range []string{"/main.go", "./main.go", "main.go"} {
		t.Run(spelling, func(t *testing.T) {
			w.Reads.Clear()
			if _, err := w.readFile(context.Background(), map[string]interface{}{"path": spelling}); err != nil {
				t.Fatal(err)
			}
			out := strOut(w.editFile(context.Background(), map[string]interface{}{
				"path": spelling, "old_str": "func main() {}", "new_str": "func main() { _ = 1 }",
			}))
			if strings.Contains(out, "must be read first") {
				t.Fatalf("read/edit path keys must agree, got %q", out)
			}
			// put it back for the next spelling
			mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
		})
	}
}

func TestGrepAndListNormalizePaths(t *testing.T) {
	w, root := newTestWS(t)
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	mustWrite(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n// NEEDLE\n")
	for _, p := range []string{"/pkg", "./pkg", "pkg"} {
		out := strOut(w.grep(context.Background(), map[string]interface{}{"pattern": "NEEDLE", "path": p}))
		if !strings.Contains(out, "pkg/a.go") {
			t.Fatalf("grep path=%q → %q", p, out)
		}
		out = strOut(w.listDir(context.Background(), map[string]interface{}{"path": p}))
		if !strings.Contains(out, "a.go") {
			t.Fatalf("list path=%q → %q", p, out)
		}
	}
}

// ── item 13: real regex grep with truncation notice ────────────────────────

func TestGrepRegexAndLiteralFallback(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "a.go"),
		"package a\n\nfunc NewFoo() {}\nfunc NewBar() {}\nvar x = compute(\n")
	cases := []struct {
		name    string
		pattern string
		want    []string
		absent  []string
	}{
		{"regex", `func New\w+`, []string{"NewFoo", "NewBar", "regex match"}, nil},
		{"anchored regex", `^func NewBar`, []string{"NewBar"}, []string{"NewFoo"}},
		{"invalid regex falls back to literal", "compute(", []string{"compute(", "literal match"}, nil},
		{"case insensitive", `(?i)NEWFOO`, []string{"NewFoo"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strOut(w.grep(context.Background(), map[string]interface{}{"pattern": tc.pattern}))
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in:\n%s", want, out)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(out, no) {
					t.Fatalf("unexpected %q in:\n%s", no, out)
				}
			}
		})
	}
}

func TestGrepAnnouncesTruncation(t *testing.T) {
	w, root := newTestWS(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("hit here\n")
	}
	mustWrite(t, filepath.Join(root, "many.txt"), b.String())
	w.MaxToolChars = 1 << 20 // isolate grep's own cap from the global one
	out := strOut(w.grep(context.Background(), map[string]interface{}{"pattern": "hit"}))
	if !strings.Contains(out, "of 200 matches shown") {
		t.Fatalf("truncation must be announced with the true total:\n%s", out)
	}
	if !strings.Contains(out, "narrow with") {
		t.Fatal("truncation notice must say how to narrow")
	}
}

func TestGrepNoMatchIsActionable(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	out := strOut(w.grep(context.Background(), map[string]interface{}{"pattern": "zzz"}))
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty result is a stall trigger")
	}
	if !strings.Contains(out, "no matches") || !strings.Contains(out, "ws_glob") {
		t.Fatalf("got %q", out)
	}
}

// ── item 14: glob ** and non-empty results ─────────────────────────────────

func TestMatchGlobDoubleStar(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.go", "a.go", true},
		{"**/*.go", "pkg/a/b.go", true},
		{"pkg/**/*.go", "pkg/a/b.go", true},
		{"pkg/**/*.go", "pkg/a.go", true},
		{"pkg/**/*.go", "cmd/a.go", false},
		{"pkg/**/*_test.go", "pkg/x/y/z_test.go", true},
		{"cmd/*/main.go", "cmd/app/main.go", true},
		{"cmd/*/main.go", "cmd/a/b/main.go", false},
		{"*.go", "a.go", true},
		{"*.go", "pkg/a.go", false},
		{"**", "any/thing.txt", true},
		{"**/*.py", "pkg/a.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"~"+tc.path, func(t *testing.T) {
			if got := MatchGlob(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("MatchGlob(%q,%q)=%v want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestGlobToolFindsNestedPattern(t *testing.T) {
	w, root := newTestWS(t)
	_ = os.MkdirAll(filepath.Join(root, "pkg", "deep"), 0o755)
	mustWrite(t, filepath.Join(root, "pkg", "deep", "x.go"), "package deep\n")
	mustWrite(t, filepath.Join(root, "cmd.go"), "package main\n")
	out := strOut(w.glob(context.Background(), map[string]interface{}{"pattern": "pkg/**/*.go"}))
	if !strings.Contains(out, "pkg/deep/x.go") {
		t.Fatalf("pkg/**/*.go must match nested files:\n%s", out)
	}
	if strings.Contains(out, "cmd.go") {
		t.Fatalf("pattern should not match outside pkg/:\n%s", out)
	}
}

func TestEmptyResultsAreExplained(t *testing.T) {
	w, root := newTestWS(t)
	_ = os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	cases := []struct {
		name string
		run  func() (interface{}, error)
		want string
	}{
		{"glob no match", func() (interface{}, error) {
			return w.glob(context.Background(), map[string]interface{}{"pattern": "**/*.rs"})
		}, "no files match"},
		{"list empty dir", func() (interface{}, error) {
			return w.listDir(context.Background(), map[string]interface{}{"path": "empty"})
		}, "is empty"},
		{"list missing dir", func() (interface{}, error) {
			return w.listDir(context.Background(), map[string]interface{}{"path": "nope"})
		}, "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strOut(tc.run())
			if strings.TrimSpace(out) == "" {
				t.Fatal("empty tool result is a known SLM stall trigger")
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("got %q want it to contain %q", out, tc.want)
			}
		})
	}
}

// ── item 15: lenient args reach the tools ──────────────────────────────────

func TestToolsAcceptStringifiedArgs(t *testing.T) {
	w, root := newTestWS(t)
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		b.WriteString("line\n")
	}
	mustWrite(t, filepath.Join(root, "big.txt"), b.String())
	out := strOut(w.readFile(context.Background(), map[string]interface{}{
		"path": "big.txt", "offset": "200", "limit": "3",
	}))
	if !strings.Contains(out, "   200|line") || !strings.Contains(out, "   202|line") {
		t.Fatalf("string offset/limit must be honored:\n%s", out)
	}
	if strings.Contains(out, "   203|") {
		t.Fatalf("string limit must be honored:\n%s", out)
	}

	mustWrite(t, filepath.Join(root, "dup.txt"), "x\nx\nx\n")
	w.Reads.Mark("dup.txt")
	out = strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "dup.txt", "old_str": "x", "new_str": "y", "replace_all": "true",
	}))
	if !strings.Contains(out, "3 replacement") {
		t.Fatalf(`replace_all:"true" must be honored: %q`, out)
	}
}

// ── item 16: ws_write escape hatch + truncation guard ──────────────────────

func TestWriteGuardOverwriteRules(t *testing.T) {
	body := strings.Repeat("existing line\n", 60)
	cases := []struct {
		name        string
		markRead    bool
		content     string
		allowShrink interface{}
		wantOK      bool
		wantMsg     string
	}{
		{"unread file refused", false, body, nil, false, "already exists"},
		{"read file may be rewritten", true, strings.Repeat("new line\n", 60), nil, true, "overwrote"},
		{"catastrophic truncation refused", true, "tiny\n", nil, false, "would shrink"},
		{"truncation with allow_shrink", true, "tiny\n", true, true, "overwrote"},
		{"truncation with allow_shrink as string", true, "tiny\n", "true", true, "overwrote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, root := newTestWS(t)
			mustWrite(t, filepath.Join(root, "f.txt"), body)
			if tc.markRead {
				w.Reads.Mark("f.txt")
			}
			args := map[string]interface{}{"path": "f.txt", "content": tc.content}
			if tc.allowShrink != nil {
				args["allow_shrink"] = tc.allowShrink
			}
			out := strOut(w.writeFile(context.Background(), args))
			if !strings.Contains(out, tc.wantMsg) {
				t.Fatalf("got %q want %q", out, tc.wantMsg)
			}
			got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
			if tc.wantOK && string(got) != tc.content {
				t.Fatalf("file should have been written")
			}
			if !tc.wantOK && string(got) != body {
				t.Fatalf("file must be untouched")
			}
		})
	}
}

// ── item 17: over-edit guard on small files and on ws_patch ────────────────

func TestOverEditExemptsSmallFiles(t *testing.T) {
	// 20 lines of real content — the target function IS most of the file.
	small := "package tiny\n\n" + strings.Repeat("// filler comment line\n", 18)
	if len(small) < MinOverEditBytes {
		t.Fatalf("fixture must exceed the byte floor, got %d", len(small))
	}
	if msg := AssessOverEdit(small, small, "package tiny\n"); msg != "" {
		t.Fatalf("files under %d lines must be exempt: %s", MinOverEditLines, msg)
	}
	big := small + strings.Repeat("// more\n", 40)
	if msg := AssessOverEdit(big, big, "package tiny\n"); msg == "" {
		t.Fatal("a genuine whole-file rewrite must still be refused")
	} else if !strings.Contains(msg, "DO THIS INSTEAD") {
		t.Fatalf("refusal must offer a concrete alternative: %s", msg)
	}
}

func TestOverEditAppliesToPatch(t *testing.T) {
	w, root := newTestWS(t)
	body := "package demo\n\n" + strings.Repeat("// a line of code here\n", 60)
	mustWrite(t, filepath.Join(root, "big.go"), body)
	w.Reads.Mark("big.go")
	patch := "<<<<<<< SEARCH\n" + body + "=======\npackage demo\n>>>>>>> REPLACE"
	out := strOut(w.patchFile(context.Background(), map[string]interface{}{
		"path": "big.go", "patch": patch,
	}))
	if !strings.Contains(out, "Over-edit refused") {
		t.Fatalf("ws_patch must not bypass the over-edit guard, got %q", out)
	}
	if !strings.Contains(out, "single-hunk unified diff") {
		t.Fatalf("patch refusal should give patch-shaped advice: %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "big.go"))
	if string(got) != body {
		t.Fatal("file must be untouched")
	}
}

// ── item 10: irreversible ops are checkpointed and safe ────────────────────

func TestDeleteAndMoveCheckpoint(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	w := &Workspace{
		Root: root, Reads: NewReadTracker(), SlmDir: slm,
		Checkpointer: NewFileCheckpointer(slm, root, "test"),
	}
	mustWrite(t, filepath.Join(root, "gone.go"), "package gone\n")
	mustWrite(t, filepath.Join(root, "old.go"), "package old\n")

	strOut(w.deleteFile(context.Background(), map[string]interface{}{"path": "gone.go"}))
	if err := w.Checkpointer.Restore("gone.go"); err != nil {
		t.Fatalf("delete must be checkpointed and restorable: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "gone.go")); string(data) != "package gone\n" {
		t.Fatalf("restore produced %q", data)
	}

	strOut(w.moveFile(context.Background(), map[string]interface{}{"from": "old.go", "to": "new.go"}))
	if _, err := os.Stat(filepath.Join(root, "new.go")); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if err := w.Checkpointer.Restore("old.go"); err != nil {
		t.Fatalf("move must checkpoint the source: %v", err)
	}
}

func TestMoveErrorsAreActionable(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "b.go"), "package b\n")
	cases := []struct{ name, from, to, want string }{
		{"missing source", "nope.go", "x.go", "does not exist"},
		{"existing destination", "a.go", "b.go", "already exists"},
		{"missing args", "", "x.go", "both from and to are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strOut(w.moveFile(context.Background(),
				map[string]interface{}{"from": tc.from, "to": tc.to}))
			if !strings.Contains(out, tc.want) {
				t.Fatalf("got %q want %q", out, tc.want)
			}
		})
	}
	// Both files survive every refusal.
	for _, f := range []string{"a.go", "b.go"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("%s must survive: %v", f, err)
		}
	}
}

// ── item D: windowed reads ─────────────────────────────────────────────────

func TestReadWindowsByDefault(t *testing.T) {
	w, root := newTestWS(t)
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		b.WriteString("x\n")
	}
	mustWrite(t, filepath.Join(root, "big.txt"), b.String())
	w.MaxToolChars = 1 << 20
	out := strOut(w.readFile(context.Background(), map[string]interface{}{"path": "big.txt"}))
	lines := strings.Split(out, "\n")
	numbered := 0
	for _, l := range lines {
		if lineNumberPrefixRe.MatchString(l) {
			numbered++
		}
	}
	if numbered != DefaultReadWindow {
		t.Fatalf("expected a %d-line window, got %d lines", DefaultReadWindow, numbered)
	}
	if !strings.Contains(out, "showing lines 1–120 of 501") {
		t.Fatalf("window must announce its range:\n%s", out)
	}
	if !strings.Contains(out, `"offset":121`) {
		t.Fatalf("window must hand back the next-page call:\n%s", out)
	}
}

func TestReadPastEOFIsActionable(t *testing.T) {
	w, root := newTestWS(t)
	mustWrite(t, filepath.Join(root, "s.txt"), "a\nb\n")
	out := strOut(w.readFile(context.Background(), map[string]interface{}{"path": "s.txt", "offset": 99}))
	if !strings.Contains(out, "past the end") || !strings.Contains(out, "between 1 and") {
		t.Fatalf("got %q", out)
	}
}

func TestReadMissingFileIsActionable(t *testing.T) {
	w, _ := newTestWS(t)
	out := strOut(w.readFile(context.Background(), map[string]interface{}{"path": "nope.go"}))
	if !strings.Contains(out, "does not exist") || !strings.Contains(out, "ws_glob") {
		t.Fatalf("got %q", out)
	}
}

// ── item E: global result cap with steering ────────────────────────────────

func TestCapResultSteers(t *testing.T) {
	w := &Workspace{MaxToolChars: 200}
	short := "small"
	if got := w.capResult(short); got != short {
		t.Fatal("short results must pass through untouched")
	}
	long := strings.Repeat("y", 5000)
	got := w.capResult(long)
	if len(got) >= len(long) {
		t.Fatal("long results must be capped")
	}
	for _, want := range []string{"result truncated", "NARROW THE QUERY", "ws_grep", "ws_read"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cap notice missing %q", want)
		}
	}
}

func TestCappedWrapperApplies(t *testing.T) {
	w := &Workspace{MaxToolChars: 100}
	fn := w.capped(func(context.Context, map[string]interface{}) (interface{}, error) {
		return strings.Repeat("z", 4000), nil
	})
	out, err := fn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "NARROW THE QUERY") {
		t.Fatal("wrapper must cap")
	}
}

// ── item F: actionable errors ──────────────────────────────────────────────

func TestToolErrorsNameACorrectiveAction(t *testing.T) {
	w, _ := newTestWS(t)
	ctx := context.Background()
	cases := []struct {
		name string
		run  func() (interface{}, error)
		want []string
	}{
		{"read no path", func() (interface{}, error) { return w.readFile(ctx, map[string]interface{}{}) },
			[]string{"path is required"}},
		{"write no path", func() (interface{}, error) { return w.writeFile(ctx, map[string]interface{}{}) },
			[]string{"path is required"}},
		{"edit no path", func() (interface{}, error) { return w.editFile(ctx, map[string]interface{}{}) },
			[]string{"path is required"}},
		{"patch no patch", func() (interface{}, error) {
			return w.patchFile(ctx, map[string]interface{}{"path": "a.go"})
		}, []string{"patch is required", "SEARCH"}},
		{"glob no pattern", func() (interface{}, error) { return w.glob(ctx, map[string]interface{}{}) },
			[]string{"pattern is required"}},
		{"grep no pattern", func() (interface{}, error) { return w.grep(ctx, map[string]interface{}{}) },
			[]string{"pattern is required"}},
		{"delete no path", func() (interface{}, error) { return w.deleteFile(ctx, map[string]interface{}{}) },
			[]string{"path is required"}},
		{"shell no command", func() (interface{}, error) { return w.shell(ctx, map[string]interface{}{}) },
			[]string{"command is required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strOut(tc.run())
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("got %q want %q", out, want)
				}
			}
		})
	}
}

// ── item 9: shell timeout, process group, bounded output ───────────────────

func TestShellTimesOutWithActionableMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not guaranteed on Windows")
	}
	w, _ := newTestWS(t)
	w.ShellTimeout = 300 * time.Millisecond
	w.MaxToolChars = 1 << 20
	start := time.Now()
	out := strOut(w.shell(context.Background(), map[string]interface{}{"command": "sleep 30"}))
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout not enforced (%s)", elapsed)
	}
	if !strings.Contains(out, "timed out") || !strings.Contains(out, "NEXT STEP") {
		t.Fatalf("timeout must be an actionable result, not an error: %q", out)
	}
}

func TestShellKillsChildProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups differ on Windows")
	}
	w, root := newTestWS(t)
	w.ShellTimeout = 500 * time.Millisecond
	w.MaxToolChars = 1 << 20
	marker := filepath.Join(root, "child-alive")
	// The child outlives its parent shell and would keep running (and holding
	// the pipe) unless the whole process group is killed.
	cmd := "bash -c 'sleep 5; touch " + marker + "' & wait"
	start := time.Now()
	_ = strOut(w.shell(context.Background(), map[string]interface{}{"command": cmd}))
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("orphaned child held the call open for %s", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child process survived the timeout — process group was not killed")
	}
}

func TestShellOutputIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not guaranteed on Windows")
	}
	w, _ := newTestWS(t)
	w.MaxToolChars = 1 << 20
	out := strOut(w.shell(context.Background(), map[string]interface{}{
		"command": "for i in $(seq 1 200000); do echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; done",
	}))
	if len(out) > MaxCapturedOutput+4096 {
		t.Fatalf("output not bounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "dropped") {
		t.Fatalf("truncation must be announced")
	}
}

func TestShellSucceedsQuietly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not guaranteed on Windows")
	}
	w, _ := newTestWS(t)
	out := strOut(w.shell(context.Background(), map[string]interface{}{"command": "true"}))
	if strings.TrimSpace(out) == "" {
		t.Fatal("silent success must still say something")
	}
}

func TestHeadTailBuffer(t *testing.T) {
	b := newHeadTailBuffer(4096)
	for i := 0; i < 1000; i++ {
		_, _ = b.Write([]byte(strings.Repeat("x", 100)))
	}
	s, truncated := b.String()
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(s) > 4096+300 {
		t.Fatalf("buffer grew to %d", len(s))
	}
	if !strings.Contains(s, "bytes of output dropped") {
		t.Fatal("truncation must be announced")
	}
	// A small write is kept verbatim.
	small := newHeadTailBuffer(4096)
	_, _ = small.Write([]byte("hello"))
	if s, tr := small.String(); s != "hello" || tr {
		t.Fatalf("got %q truncated=%v", s, tr)
	}
}

func TestRunBoundedReportsFullWriteCount(t *testing.T) {
	// io.Writer contract: Write must return len(p) or an error.
	b := newHeadTailBuffer(16)
	n, err := b.Write([]byte(strings.Repeat("q", 500)))
	if err != nil || n != 500 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
