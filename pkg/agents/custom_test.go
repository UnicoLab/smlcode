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
		Tools:        BoolPtr(true),
		Model:        "qwen2.5-coder:14b",
		Provider:     "ollama",
		Endpoint:     "http://127.0.0.1:11434",
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
	if got.ID != "night-auditor" || !got.ToolsEnabled() || len(got.Skills) != 2 {
		t.Fatalf("%+v", got)
	}
	if got.Endpoint != "http://127.0.0.1:11434" || got.Provider != "ollama" {
		t.Fatalf("provider/endpoint: %+v", got)
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

func TestReadCustomFileFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	first := CustomSpec{
		ID:           "recover-agent",
		Title:        "Backup Agent",
		SystemPrompt: "Use the backup.",
	}
	path, err := WriteCustom(dir, first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Title = "Current Agent"
	if _, err := WriteCustom(dir, second); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("id: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCustomFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Backup Agent" {
		t.Fatalf("title=%q", got.Title)
	}
}

func TestBuiltinOverrideAllowed(t *testing.T) {
	dir := t.TempDir()
	c := CustomSpec{
		ID:       "worker",
		Provider: "ollama",
		Model:    "qwen2.5-coder:7b",
		Endpoint: "http://127.0.0.1:11434",
		MaxIter:  20,
	}
	path, err := WriteCustom(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadCustomFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Override || !got.Builtin || got.Custom {
		t.Fatalf("expected override flags: %+v", got)
	}
	coding := []string{"ws_read", "ws_write"}
	base := RoleSpec{ID: "worker", Title: "Implement", Tools: coding, MaxIter: 16, Temperature: 0.15, MaxTokens: 4096}
	ApplyOverride(&base, got, coding)
	if base.Provider != "ollama" || base.Model != "qwen2.5-coder:7b" || base.MaxIter != 20 {
		t.Fatalf("merge failed: %+v", base)
	}
	if len(base.Tools) == 0 {
		t.Fatal("tools must remain when override omits tools key")
	}
	if err := DeleteCustom(dir, "worker"); err != nil {
		t.Fatal(err)
	}
}

func TestToRoleSpecAppliesSettings(t *testing.T) {
	c := CustomSpec{
		ID: "night-auditor", Title: "Night", SystemPrompt: "Audit.",
		Tools: BoolPtr(true), Model: "m1", Provider: "lmstudio",
		Endpoint: "http://127.0.0.1:1234/v1", MaxIter: 12, Temperature: 0.3, MaxTokens: 1024,
		Skills: []string{"atomic-coding"},
	}
	if err := NormalizeCustom(&c); err != nil {
		t.Fatal(err)
	}
	spec := c.ToRoleSpec([]string{"ws_edit"})
	if spec.Model != "m1" || spec.Provider != "lmstudio" || spec.Endpoint == "" {
		t.Fatalf("%+v", spec)
	}
	if spec.MaxIter != 12 || spec.Temperature != 0.3 || len(spec.Tools) == 0 {
		t.Fatalf("%+v", spec)
	}
}
