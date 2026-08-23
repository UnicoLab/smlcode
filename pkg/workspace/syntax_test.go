package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func haveTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed", name)
	}
}

func TestSyntaxCheckerSelection(t *testing.T) {
	cases := []struct {
		file string
		want string // expected checker binary, "" = none
	}{
		{"a.go", "gofmt"},
		{"a.py", "python3"},
		{"a.js", "node"},
		{"a.mjs", "node"},
		{"a.cjs", "node"},
		{"a.json", "python3"},
		{"a.ts", ""},  // deliberately skipped: tsc is far too slow in-band
		{"a.tsx", ""}, // ditto
		{"a.rs", ""},
		{"a.md", ""},
		{"a.txt", ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			argv := syntaxChecker("/tmp/" + tc.file)
			if tc.want == "" {
				if argv != nil {
					t.Fatalf("expected no checker, got %v", argv)
				}
				return
			}
			if len(argv) == 0 || argv[0] != tc.want {
				t.Fatalf("got %v want %s", argv, tc.want)
			}
		})
	}
}

func TestCheckSyntax(t *testing.T) {
	haveTool(t, "gofmt")
	dir := t.TempDir()
	cases := []struct {
		name, file, body string
		want             SyntaxStatus
	}{
		{"valid go", "ok.go", "package a\n\nfunc F() {}\n", SyntaxOK},
		{"broken go", "bad.go", "package a\n\nfunc F( {\n", SyntaxBroken},
		{"unformatted but valid go", "ugly.go", "package a\nfunc F(){\n_ = 1\n}\n", SyntaxOK},
		{"unknown extension", "x.rs", "fn main( {", SyntaxSkipped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.file)
			mustWrite(t, p, tc.body)
			got := CheckSyntax(context.Background(), p, 0)
			if got.Status != tc.want {
				t.Fatalf("status=%v want %v (errors=%q)", got.Status, tc.want, got.Errors)
			}
			if tc.want == SyntaxBroken && got.Errors == "" {
				t.Fatal("broken files must carry diagnostics")
			}
		})
	}
}

// The headline guardrail: an edit that introduces a NEW syntax error is
// reverted, while a file that was already broken is only warned about.
func TestEditRevertsOnNewSyntaxError(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	good := "package a\n\nfunc F() int {\n\treturn 1\n}\n"
	mustWrite(t, filepath.Join(root, "a.go"), good)
	w.Reads.Mark("a.go")

	out := strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "\treturn 1", "new_str": "\treturn 1 +",
	}))
	if !strings.Contains(out, "EDIT REVERTED") {
		t.Fatalf("a new syntax error must revert the edit, got %q", out)
	}
	if !strings.Contains(out, "Do NOT retry the identical edit") {
		t.Fatal("revert message must name the corrective action")
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(data) != good {
		t.Fatalf("file must be restored, got %q", data)
	}
}

func TestEditWarnsWhenFileWasAlreadyBroken(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	broken := "package a\n\nfunc F( {\n\treturn 1\n}\n"
	mustWrite(t, filepath.Join(root, "a.go"), broken)
	w.Reads.Mark("a.go")

	out := strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "\treturn 1", "new_str": "\treturn 2",
	}))
	if strings.Contains(out, "EDIT REVERTED") {
		t.Fatalf("a pre-existing break must not be blamed on this edit: %q", out)
	}
	if !strings.Contains(out, "syntax check failed") {
		t.Fatalf("the break must still be reported in-band: %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(data), "return 2") {
		t.Fatal("the edit itself should still have been applied")
	}
}

func TestGoodEditReportsNoSyntaxNoise(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\nfunc F() int {\n\treturn 1\n}\n")
	w.Reads.Mark("a.go")
	out := strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "\treturn 1", "new_str": "\treturn 2",
	}))
	if strings.Contains(out, "syntax check") || strings.Contains(out, "REVERTED") {
		t.Fatalf("a clean edit must not add noise: %q", out)
	}
}

func TestSyntaxCheckCanBeDisabled(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = false
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\nfunc F() int {\n\treturn 1\n}\n")
	w.Reads.Mark("a.go")
	out := strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "\treturn 1", "new_str": "\treturn 1 +",
	}))
	if strings.Contains(out, "REVERTED") {
		t.Fatal("the guard must be switchable off")
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(data), "return 1 +") {
		t.Fatal("with the guard off the broken edit is written")
	}
}

func TestWriteRevertsOnNewSyntaxError(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	good := "package a\n\nfunc F() int {\n\treturn 1\n}\n" + strings.Repeat("// pad\n", 40)
	mustWrite(t, filepath.Join(root, "a.go"), good)
	w.Reads.Mark("a.go")
	out := strOut(w.writeFile(context.Background(), map[string]interface{}{
		"path": "a.go", "content": "package a\n\nfunc F( int {\n" + strings.Repeat("// pad\n", 40),
	}))
	if !strings.Contains(out, "EDIT REVERTED") {
		t.Fatalf("ws_write must be guarded too, got %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(data) != good {
		t.Fatal("file must be restored")
	}
}

func TestNewFileWithSyntaxErrorIsWarnedNotReverted(t *testing.T) {
	haveTool(t, "gofmt")
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	out := strOut(w.writeFile(context.Background(), map[string]interface{}{
		"path": "new.go", "content": "package a\n\nfunc F( {\n",
	}))
	// There is no prior good version to restore, so warn and keep the file.
	if strings.Contains(out, "REVERTED") {
		t.Fatalf("a brand-new file has nothing to revert to: %q", out)
	}
	if !strings.Contains(out, "syntax check failed") {
		t.Fatalf("must still be reported: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "new.go")); err != nil {
		t.Fatal("file should exist")
	}
}

func TestFirstSyntaxErrors(t *testing.T) {
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"two of three", "a\nb\nc\n", 2, "a\nb"},
		{"skips blanks", "\n\n  \nreal error\n", 1, "real error"},
		{"fewer than n", "only\n", 3, "only"},
		{"empty", "", 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstSyntaxErrors(tc.in, tc.n); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPythonSyntaxGuard(t *testing.T) {
	if resolveChecker(syntaxChecker("x.py")) == nil {
		t.Skip("no python interpreter")
	}
	w, root := newTestWS(t)
	w.SyntaxCheck = true
	good := "def f():\n    return 1\n"
	mustWrite(t, filepath.Join(root, "a.py"), good)
	w.Reads.Mark("a.py")
	out := strOut(w.editFile(context.Background(), map[string]interface{}{
		"path": "a.py", "old_str": "    return 1", "new_str": "    return (1",
	}))
	if !strings.Contains(out, "EDIT REVERTED") {
		t.Fatalf("python break must revert, got %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.py"))
	if string(data) != good {
		t.Fatalf("file must be restored, got %q", data)
	}
}
