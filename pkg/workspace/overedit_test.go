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
