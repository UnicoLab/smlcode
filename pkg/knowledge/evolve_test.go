package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/skills"
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

func TestEvolveReplacesAutoLearnedSectionInsteadOfAppending(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PROJECT.md"),
		[]byte("# Project\n\n## Overview\n\nhand written overview\n\n## Key paths\n\n| a | b |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "Doc", Column: plan.ColDone, Files: []string{"hello.go"}, Output: `{"status":"done"}`},
	}}
	board.Tasks[0].Normalize()

	for i := 0; i < 60; i++ {
		if _, err := Evolve(dir, fmt.Sprintf("run number %d", i), board, "- lesson\n", nil); err != nil {
			t.Fatal(err)
		}
	}
	proj, err := os.ReadFile(filepath.Join(dir, "PROJECT.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(proj)

	if n := strings.Count(body, "## "+AutoLearnedHeading); n != 1 {
		t.Fatalf("expected exactly one Auto-learned section, got %d:\n%s", n, body)
	}
	if len(body) > MaxProjectBytes {
		t.Fatalf("PROJECT.md grew to %d bytes (cap %d)", len(body), MaxProjectBytes)
	}
	if !strings.Contains(body, "hand written overview") {
		t.Fatalf("hand-written content destroyed:\n%s", body)
	}
	if !strings.Contains(body, "run number 59") {
		t.Fatalf("latest run note missing:\n%s", body)
	}
	notes := strings.Count(sectionBody(body, AutoLearnedHeading), "\n- ") + 1
	if notes > MaxAutoLearnedNotes {
		t.Fatalf("kept %d notes, cap %d", notes, MaxAutoLearnedNotes)
	}
}

func TestEvolveBoundsLearnedSkill(t *testing.T) {
	dir := t.TempDir()
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Column: plan.ColDone, Files: []string{"a.go"}},
	}}
	board.Tasks[0].Normalize()
	lesson := "- " + strings.Repeat("é", 3000) + "\n"
	for i := 0; i < 40; i++ {
		if _, err := Evolve(dir, "q", board, lesson, nil); err != nil {
			t.Fatal(err)
		}
	}
	learned, _ := os.ReadFile(filepath.Join(dir, "skills", "learned", "SKILL.md"))
	if len(learned) > MaxLearnedSkillBytes+64 {
		t.Fatalf("learned skill grew to %d bytes", len(learned))
	}
	if !utf8.Valid(learned) {
		t.Fatal("learned skill truncation produced invalid UTF-8")
	}
}

func TestEvolveEmptyDir(t *testing.T) {
	if _, err := Evolve("", "q", nil, "", nil); err == nil {
		t.Fatal("expected an error for an empty slm dir")
	}
}
