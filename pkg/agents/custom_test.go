package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomAgentCRUD(t *testing.T) {
	dir := t.TempDir()
	c := CustomSpec{
		ID:           "night-auditor",
		Title:        "Night Auditor",
		Description:  "Reviews quietly",
		SystemPrompt: "You audit code at night.",
		Skills:       []string{"multipass-quality", "atomic-coding"},
		Tools:        true,
		Model:        "qwen2.5-coder:14b",
		Provider:     "ollama",
	}
	path, err := WriteCustom(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "night-auditor.yaml" {
		t.Fatalf("path=%s", path)
	}
	got, err := ReadCustomFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "night-auditor" || !got.Tools || len(got.Skills) != 2 {
		t.Fatalf("%+v", got)
	}
	list, err := LoadCustomSpecs(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := DeleteCustom(dir, "night-auditor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected deleted")
	}
}

func TestCustomRejectsBuiltinID(t *testing.T) {
	c := CustomSpec{ID: "worker", SystemPrompt: "x"}
	if err := NormalizeCustom(&c); err == nil {
		t.Fatal("expected builtin reject")
	}
}
