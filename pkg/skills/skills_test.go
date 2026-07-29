package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentsAndTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	body := `---
name: specialist-worker
description: Implement scoped change
triggers: implement, code
agents: worker, deep
user-invocable: true
---

# Worker
Do tiny edits.
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "specialist-worker" || !sk.UserInvocable {
		t.Fatalf("%+v", sk)
	}
	if len(sk.Agents) != 2 || sk.Agents[0] != "worker" {
		t.Fatalf("agents=%v", sk.Agents)
	}
	if len(sk.Triggers) != 2 {
		t.Fatalf("triggers=%v", sk.Triggers)
	}
}

func TestExtractRefs(t *testing.T) {
	names, clean := ExtractRefs("please @skill:atomic-coding and /skill multipass-quality fix hello")
	if len(names) != 2 {
		t.Fatalf("names=%v", names)
	}
	if strings.Contains(clean, "@skill") || strings.Contains(clean, "/skill") {
		t.Fatalf("clean=%q", clean)
	}
	if !strings.Contains(clean, "fix hello") {
		t.Fatalf("clean=%q", clean)
	}
}

func TestMatchForAgentIncludesRoleSkill(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteSkill(dir, Skill{
		Name: "specialist-tester", Description: "verify", Agents: []string{"tester"},
		Body: "# Tester\nRun tests.", UserInvocable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = WriteSkill(dir, Skill{
		Name: "other-role", Description: "plan stuff", Agents: []string{"planner"},
		Triggers: []string{"roadmap"}, Body: "# Planner", UserInvocable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	l := NewLoader(dir)
	got := l.MatchForAgent("tester", "smoke check", 6)
	if len(got) == 0 || got[0].Name != "specialist-tester" {
		t.Fatalf("got=%v", names(got))
	}
}

func TestResolvePinnedAndAtSkill(t *testing.T) {
	dir := t.TempDir()
	_, _ = WriteSkill(dir, Skill{Name: "alpha", Description: "a", Body: "# A", UserInvocable: true})
	_, _ = WriteSkill(dir, Skill{Name: "beta", Description: "b", Body: "# B", UserInvocable: true})
	l := NewLoader(dir)
	got := l.ResolveForRun("use @skill:beta please", "", []string{"alpha"}, 6)
	have := map[string]bool{}
	for _, s := range got {
		have[s.Name] = true
	}
	if !have["alpha"] || !have["beta"] {
		t.Fatalf("got=%v", names(got))
	}
}

func TestListDoesNotSkipBundledRoot(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "skills", "_bundled")
	_, err := WriteSkill(bundled, Skill{Name: "from-bundled", Description: "b", Body: "# B", Agents: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	l := NewLoader(filepath.Join(dir, "skills"), bundled)
	list, err := l.List()
	if err != nil || len(list) != 1 || list[0].Name != "from-bundled" {
		t.Fatalf("list=%v err=%v", names(list), err)
	}
}

func TestWriteAndGet(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSkill(dir, Template("my custom", "worker, reviewer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(dir)
	sk, ok := l.Get("my-custom")
	if !ok || len(sk.Agents) != 2 {
		t.Fatalf("ok=%v sk=%+v", ok, sk)
	}
	pack := RenderPack([]Skill{sk}, 2000)
	if !strings.Contains(pack, "skill:my-custom") {
		t.Fatalf("pack=%s", pack)
	}
}

func names(list []Skill) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Name
	}
	return out
}
