package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
)

func TestEvolveWritesSkillsAndLearned(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "PROJECT.md"), []byte("# Project\n"), 0o644)
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "Doc", Column: plan.ColDone, Files: []string{"hello.go"}, Output: `{"status":"done"}`},
	}}
	board.Tasks[0].Normalize()

	ev, err := Evolve(dir, "Add doc comment", board, "- ✓ prefer godoc\n", []skills.Skill{
		{Name: "atomic-coding", Description: "tiny edits", Path: "bundled/atomic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.SkillsIndex != "SKILLS.md" {
		t.Fatalf("%+v", ev)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "SKILLS.md"))
	if !strings.Contains(string(idx), "atomic-coding") {
		t.Fatalf("index=%s", idx)
	}
	learned, _ := os.ReadFile(filepath.Join(dir, "skills", "learned", "SKILL.md"))
	if !strings.Contains(string(learned), "godoc") || !strings.Contains(string(learned), "hello.go") {
		t.Fatalf("learned=%s", learned)
	}
	proj, _ := os.ReadFile(filepath.Join(dir, "PROJECT.md"))
	if !strings.Contains(string(proj), "hello.go") {
		t.Fatalf("project=%s", proj)
	}
}
