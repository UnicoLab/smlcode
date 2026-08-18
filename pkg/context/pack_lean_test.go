package contextstore

import (
	"os"
	"path/filepath"
	"strings"
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

func TestPackerCacheRefreshesWhenFocusFileChanges(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(slm, DocQuery), []byte("# Q\n"), 0o644)
	target := filepath.Join(root, "a.go")
	_ = os.WriteFile(target, []byte("package a\n\nconst Version = 1\n"), 0o644)

	p := NewPacker(New(slm), root, 16)
	pack, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Files["a.go"], "Version = 1") {
		t.Fatalf("initial pack missing v1: %+v", pack.Files)
	}

	_ = os.WriteFile(target, []byte("package a\n\nconst Version = 2\n"), 0o644)
	next, err := p.Build("worker", "q", []string{DocQuery}, []string{"a.go"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Files["a.go"], "Version = 2") {
		t.Fatalf("pack reused stale file content: %q", next.Files["a.go"])
	}
}

func TestPackerCacheRefreshesWhenContextDocChanges(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	store := New(slm)
	if err := store.Write(DocContext, "# Context\n\nold finding\n"); err != nil {
		t.Fatal(err)
	}

	p := NewPacker(store, root, 16)
	pack, err := p.Build("worker", "q", []string{DocContext}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Docs[DocContext], "old finding") {
		t.Fatalf("initial pack missing old doc: %+v", pack.Docs)
	}

	if err := store.Write(DocContext, "# Context\n\nnew wave lesson\n"); err != nil {
		t.Fatal(err)
	}
	next, err := p.Build("worker", "q", []string{DocContext}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Docs[DocContext], "new wave lesson") {
		t.Fatalf("pack reused stale doc content: %q", next.Docs[DocContext])
	}
}

func TestPackerPrioritizesRunCollaborationContractBeforeSkills(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(slm, DocContext), []byte("# Context\n\ncontext body\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)

	skills := "## Run collaboration contract\n\n- Touch only a.go\n- Verify with go test ./...\n\n" +
		"## Skill: noisy\n\n" + longText(3000)
	p := NewPacker(New(slm), root, 8)
	pack, err := p.Build("worker", "q", []string{DocContext}, []string{"a.go"}, skills)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.Priority, "Touch only a.go") {
		t.Fatalf("priority handoff missing: %q", pack.Priority)
	}
	if strings.Contains(pack.Priority, "Skill: noisy") {
		t.Fatalf("ordinary skills leaked into priority: %q", pack.Priority)
	}
	rendered := pack.Render()
	priorityIdx := strings.Index(rendered, "## Run collaboration contract")
	skillIdx := strings.Index(rendered, "## Skill: noisy")
	fileIdx := strings.Index(rendered, "## File: a.go")
	if priorityIdx < 0 {
		t.Fatalf("rendered priority missing:\n%s", rendered)
	}
	if skillIdx >= 0 && skillIdx < priorityIdx {
		t.Fatalf("priority should render before skills:\n%s", rendered)
	}
	if fileIdx >= 0 && fileIdx < priorityIdx {
		t.Fatalf("priority should render before files:\n%s", rendered)
	}
}

func TestPackerDoesNotRenderSkillsWhenBudgetExhausted(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(slm, DocContext), []byte("# Context\n\n"+longText(2000)), 0o644)
	_ = os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n\n"+longText(6000)), 0o644)

	p := NewPacker(New(slm), root, 1)
	pack, err := p.Build("worker", "q", []string{DocContext}, []string{"big.go"}, "## Skill: must-fit\n\n"+longText(2000))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Skills != "" {
		t.Fatalf("skills should be empty after exhausted budget, got %d bytes", len(pack.Skills))
	}
	if strings.Contains(pack.Render(), "must-fit") {
		t.Fatalf("rendered pack leaked uncapped skills:\n%s", pack.Render())
	}
}

func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
