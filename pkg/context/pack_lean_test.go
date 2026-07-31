package contextstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLeanDocsAndPackBudget(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(slm, DocQuery), []byte("# Q\n\n"+longText(4000)), 0o644)
	_ = os.WriteFile(filepath.Join(slm, DocContext), []byte("# C\n\n"+longText(4000)), 0o644)
	_ = os.WriteFile(filepath.Join(slm, DocPlan), []byte("# P\n\n"+longText(4000)), 0o644)
	_ = os.WriteFile(filepath.Join(slm, DocMemory), []byte("# M\n\n"+longText(4000)), 0o644)
	_ = os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n\n"+longText(12000)), 0o644)

	store := New(slm)
	p := NewPacker(store, root, 32)
	docs := LeanDocsForRole("worker")
	if len(docs) > 2 {
		t.Fatalf("lean docs too fat: %v", docs)
	}
	pack, err := p.Build("worker", "q", docs, []string{"big.go"}, longText(5000))
	if err != nil {
		t.Fatal(err)
	}
	if !pack.LeanFiles {
		t.Fatal("expected lean role")
	}
	if pack.BudgetUsed > 16*1024 {
		t.Fatalf("budget too large: %d", pack.BudgetUsed)
	}
	if body := pack.Files["big.go"]; len(body) > 3200 {
		t.Fatalf("file excerpt too large: %d", len(body))
	}
	// Cache reuse
	pack2, err := p.Build("worker", "q", docs, []string{"big.go"}, longText(5000))
	if err != nil {
		t.Fatal(err)
	}
	if pack2.BudgetUsed != pack.BudgetUsed {
		t.Fatalf("cache miss / diverge: %d vs %d", pack2.BudgetUsed, pack.BudgetUsed)
	}
	p.ClearCache()
	if len(p.cache) != 0 {
		t.Fatal("cache not cleared")
	}
}

func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
