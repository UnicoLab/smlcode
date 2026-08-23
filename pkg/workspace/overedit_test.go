package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverEditRefused(t *testing.T) {
	root := t.TempDir()
	// Build a file > MinOverEditBytes
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < 40; i++ {
		b.WriteString("func F")
		b.WriteString(strings.Repeat("x", i%5))
		b.WriteString("() int { return ")
		b.WriteString(strings.Repeat("1+", i%3))
		b.WriteString("0 }\n")
	}
	body := b.String()
	if len(body) < MinOverEditBytes {
		t.Fatalf("fixture too small: %d", len(body))
	}
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, OverEditGuard: true, ReadBeforeEdit: true, Reads: NewReadTracker()}
	w.Reads.Mark("big.go")
	out, err := w.editFile(context.Background(), map[string]interface{}{
		"path": "big.go", "old_str": body, "new_str": "package demo\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "Over-edit refused") {
		t.Fatalf("expected over-edit refuse, got %v", out)
	}
}

func TestNoopEditRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, ReadBeforeEdit: true, Reads: NewReadTracker()}
	w.Reads.Mark("a.go")
	out, err := w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "package a", "new_str": "package a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "No-op") {
		t.Fatalf("got %v", out)
	}
}

func TestAssessOverEditSmallFileAllowed(t *testing.T) {
	if msg := AssessOverEdit("tiny", "tiny", "x"); msg != "" {
		t.Fatal("small files should skip ratio guard")
	}
}

func TestAssessOverEditTable(t *testing.T) {
	small := "package tiny\n\n" + strings.Repeat("// filler comment line\n", 18)
	big := "package big\n\n" + strings.Repeat("// a line of real code here\n", 80)
	stub := "package s\n\n" + strings.Repeat("// TODO: implement this\n", 30)
	cases := []struct {
		name             string
		file, old, newer string
		refuse           bool
	}{
		{"empty file", "", "x", "y", false},
		{"empty old_str defers to the specific message", big, "", "y", false},
		{"whitespace old_str defers too", big, "   \n ", "y", false},
		{"tiny file below the byte floor", "short", "short", "x", false},
		{"file under the line floor", small, small, "package tiny\n", false},
		{"small surgical edit", big, "// a line of real code here\n", "// changed\n", false},
		{"whole-file rewrite", big, big, "package big\n", true},
		{"stub-heavy expansion allowed", stub, stub, stub + strings.Repeat("real code\n", 200), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := AssessOverEdit(tc.file, tc.old, tc.newer)
			if (msg != "") != tc.refuse {
				t.Fatalf("refuse=%v want %v (msg=%q)", msg != "", tc.refuse, msg)
			}
			if tc.refuse && !strings.Contains(msg, "DO THIS INSTEAD") {
				t.Fatalf("refusal must name the corrective action: %s", msg)
			}
		})
	}
}

func TestAssessOverEditForNamesTheTool(t *testing.T) {
	big := "package big\n\n" + strings.Repeat("// a line of real code here\n", 80)
	editMsg := AssessOverEditFor("ws_edit", big, big, "package big\n")
	patchMsg := AssessOverEditFor("ws_patch", big, big, "package big\n")
	if !strings.Contains(editMsg, "ws_edit a unique") {
		t.Fatalf("ws_edit advice missing: %s", editMsg)
	}
	if !strings.Contains(patchMsg, "single-hunk unified diff") {
		t.Fatalf("ws_patch advice missing: %s", patchMsg)
	}
}
