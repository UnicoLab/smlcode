package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Edit-tool ergonomics for small models: argument aliases, the empty-content
// refusal, automatic gutter stripping and replace_all on drifted matches.

func TestEditAcceptsArgAliases(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args map[string]interface{}
	}{
		{"claude-style", map[string]interface{}{"old_string": "b = 2", "new_string": "b = 20"}},
		{"aider-style", map[string]interface{}{"search": "b = 2", "replace": "b = 20"}},
		{"bare", map[string]interface{}{"old": "b = 2", "new": "b = 20"}},
		{"canonical-wins", map[string]interface{}{"old_str": "b = 2", "old": "nope", "new_str": "b = 20", "new": "nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, root := newTestWS(t)
			p := filepath.Join(root, "f.txt")
			if err := os.WriteFile(p, []byte("a = 1\nb = 2\nc = 3\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			w.markRead("f.txt")
			args := map[string]interface{}{"path": "f.txt"}
			for k, v := range tc.args {
				args[k] = v
			}
			out := strOut(w.editFile(ctx, args))
			if !strings.HasPrefix(out, "edited f.txt") {
				t.Fatalf("alias edit refused: %q", out)
			}
			got, _ := os.ReadFile(p)
			if string(got) != "a = 1\nb = 20\nc = 3\n" {
				t.Fatalf("content after alias edit: %q", got)
			}
		})
	}

	t.Run("write-contents-alias", func(t *testing.T) {
		w, root := newTestWS(t)
		w.SyntaxCheck = false
		out := strOut(w.writeFile(ctx, map[string]interface{}{"path": "n.txt", "contents": "hello\n"}))
		if !strings.HasPrefix(out, "wrote n.txt") {
			t.Fatalf("contents alias not honored: %q", out)
		}
		got, _ := os.ReadFile(filepath.Join(root, "n.txt"))
		if string(got) != "hello\n" {
			t.Fatalf("content: %q", got)
		}
	})

	t.Run("patch-diff-alias", func(t *testing.T) {
		w, root := newTestWS(t)
		w.SyntaxCheck = false
		if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.markRead("f.txt")
		out := strOut(w.patchFile(ctx, map[string]interface{}{
			"path": "f.txt", "diff": "<<<<<<< SEARCH\nb\n=======\nB\n>>>>>>> REPLACE",
		}))
		if !strings.HasPrefix(out, "patched f.txt") {
			t.Fatalf("diff alias not honored: %q", out)
		}
	})
}

func TestWriteRefusesEmptyContentNamesAliases(t *testing.T) {
	ctx := context.Background()
	w, root := newTestWS(t)
	w.SyntaxCheck = false

	out := strOut(w.writeFile(ctx, map[string]interface{}{"path": "new.go", "code": "package x\n"}))
	if !strings.Contains(out, "Write refused") || !strings.Contains(out, "0-byte") {
		t.Fatalf("empty write not refused: %q", out)
	}
	for _, k := range []string{"content", "contents", "text", "body"} {
		if !strings.Contains(out, k) {
			t.Errorf("message does not name accepted key %q: %q", k, out)
		}
	}
	if !strings.Contains(out, "code") {
		t.Errorf("message should point at the unrecognized key the model used: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "new.go")); err == nil {
		t.Fatal("0-byte file was created")
	}

	// Existing file: same trap truncates; allow_shrink is the way through.
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.markRead("old.txt")
	out = strOut(w.writeFile(ctx, map[string]interface{}{"path": "old.txt", "content": ""}))
	if !strings.Contains(out, "truncate") || !strings.Contains(out, "allow_shrink") {
		t.Fatalf("empty overwrite not refused with the escape hatch named: %q", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "old.txt")); string(got) != "keep" {
		t.Fatalf("file truncated: %q", got)
	}
	out = strOut(w.writeFile(ctx, map[string]interface{}{"path": "old.txt", "content": "", "allow_shrink": true}))
	if !strings.HasPrefix(out, "overwrote old.txt (0 bytes)") {
		t.Fatalf("allow_shrink empty write: %q", out)
	}
}

func TestEditStripsGutterAutomatically(t *testing.T) {
	ctx := context.Background()
	w, root := newTestWS(t)
	w.SyntaxCheck = false
	src := "package x\n\nfunc f() {\n\tif err != nil {\n\t\treturn err\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	w.markRead("f.go")

	out := strOut(w.editFile(ctx, map[string]interface{}{
		"path":    "f.go",
		"old_str": "     4|\tif err != nil {\n     5|\t\treturn err\n     6|\t}",
		"new_str": "     4|\tif err != nil {\n     5|\t\treturn fmt.Errorf(\"f: %w\", err)\n     6|\t}",
	}))
	if !strings.HasPrefix(out, "edited f.go (1 replacement(s))") {
		t.Fatalf("gutter edit did not apply: %q", out)
	}
	if !strings.Contains(out, "[stripped ws_read line numbers from old_str") {
		t.Fatalf("note missing so evolve cannot see the drift: %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.go"))
	if !strings.Contains(string(got), "\t\treturn fmt.Errorf(\"f: %w\", err)\n") || strings.Contains(string(got), "|") {
		t.Fatalf("content after gutter strip: %q", got)
	}

	// A PARTIAL gutter is not a paste: still refused by name.
	out = strOut(w.editFile(ctx, map[string]interface{}{
		"path": "f.go", "old_str": "     4|\tif err != nil {\n\t\treturn err", "new_str": "x",
	}))
	if !strings.Contains(out, "line-number prefix") {
		t.Fatalf("mixed gutter should still be refused: %q", out)
	}

	// ws_patch: same treatment, marker lines exempt.
	out = strOut(w.patchFile(ctx, map[string]interface{}{
		"path":  "f.go",
		"patch": "<<<<<<< SEARCH\n     1|package x\n=======\n     1|package y\n>>>>>>> REPLACE",
	}))
	if !strings.HasPrefix(out, "patched f.go") || !strings.Contains(out, "[stripped ws_read line numbers") {
		t.Fatalf("patch gutter strip: %q", out)
	}
	got, _ = os.ReadFile(filepath.Join(root, "f.go"))
	if !strings.HasPrefix(string(got), "package y\n") {
		t.Fatalf("patched content: %q", got)
	}
}

func TestReplaceAllOnDriftedMatches(t *testing.T) {
	ctx := context.Background()
	w, root := newTestWS(t)
	w.SyntaxCheck = false
	// Three call sites, all indented deeper than the model's old_str.
	src := "func a() {\n\t\tlog.Println(\"x\")\n}\nfunc b() {\n\t\tlog.Println(\"x\")\n}\nfunc c() {\n\t\tlog.Println(\"x\")\n}\n"
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	w.markRead("f.go")

	// Without replace_all the ladder is (correctly) ambiguous.
	out := strOut(w.editFile(ctx, map[string]interface{}{
		"path": "f.go", "old_str": "log.Println(\"x\")", "new_str": "slog.Info(\"x\")",
	}))
	if !strings.Contains(out, "Ambiguous") && !strings.Contains(out, "found 3 times") {
		t.Fatalf("expected ambiguity without replace_all: %q", out)
	}
	// Exact count is 3 here (the search has no leading whitespace), so drive
	// the LADDER path with trailing-whitespace drift instead.
	out = strOut(w.editFile(ctx, map[string]interface{}{
		"path": "f.go", "old_str": "\tlog.Println(\"x\")   ", "new_str": "\tslog.Info(\"x\")", "replace_all": true,
	}))
	if !strings.HasPrefix(out, "edited f.go (3 replacement(s))") {
		t.Fatalf("replace_all on drifted matches: %q", out)
	}
	if !strings.Contains(out, "[matched") {
		t.Fatalf("strategy note missing: %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.go"))
	if strings.Count(string(got), "slog.Info") != 3 || strings.Contains(string(got), "log.Println") {
		t.Fatalf("content after replace_all: %q", got)
	}

	// Helper-level: overlapping exact spans are deduplicated.
	next, n, via := ReplaceAllDrifted("aaa", "aa", "b")
	if n != 1 || next != "ba" || via != MatchExact {
		t.Fatalf("ReplaceAllDrifted(aaa,aa,b) = %q n=%d via=%s", next, n, via)
	}
}
