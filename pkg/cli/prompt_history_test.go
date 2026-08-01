package cli

import (
	"path/filepath"
	"testing"
)

func TestPromptHistoryPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.json")
	h := LoadPromptHistory(path)
	h.Add("first task")
	h.Add("second task")
	h.Add("second task") // dedupe
	if len(h.Recent(10)) != 2 {
		t.Fatalf("%v", h.Recent(10))
	}
	h2 := LoadPromptHistory(path)
	got := h2.Recent(10)
	if len(got) != 2 || got[1] != "second task" {
		t.Fatalf("%v", got)
	}
	prev, ok := h2.Prev()
	if !ok || prev != "second task" {
		t.Fatalf("prev=%q ok=%v", prev, ok)
	}
	prev, ok = h2.Prev()
	if !ok || prev != "first task" {
		t.Fatalf("older=%q", prev)
	}
}
