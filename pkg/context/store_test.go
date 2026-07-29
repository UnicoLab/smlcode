package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreInitReadWriteBundle(t *testing.T) {
	dir := t.TempDir()
	slm := filepath.Join(dir, ".slmcode")
	s := New(slm)
	if err := s.Init("demo"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQuery("add caching"); err != nil {
		t.Fatal(err)
	}
	q, err := s.Read(DocQuery)
	if err != nil || !strings.Contains(q, "add caching") {
		t.Fatalf("query=%q err=%v", q, err)
	}
	bundle, err := s.Bundle(4096, DocProject, DocQuery)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bundle, "PROJECT.md") || !strings.Contains(bundle, "add caching") {
		t.Fatalf("bundle=%s", bundle)
	}
	if err := s.Append(DocMemory, "Lesson", "prefer small PRs"); err != nil {
		t.Fatal(err)
	}
	mem, _ := s.Read(DocMemory)
	if !strings.Contains(mem, "prefer small PRs") {
		t.Fatalf("memory=%s", mem)
	}
	if _, err := os.Stat(filepath.Join(slm, "sessions")); err != nil {
		t.Fatal(err)
	}
}
