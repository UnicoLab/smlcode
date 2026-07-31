package cli

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
)

func TestParseAgentCommand(t *testing.T) {
	c, err := ParseAgentCommand("/agents")
	if err != nil || c.Action != "list" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = ParseAgentCommand("/agent show worker")
	if err != nil || c.Action != "show" || c.ID != "worker" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = ParseAgentCommand(`/agent new id=night title=Night provider=openai endpoint=http://127.0.0.1:9000/v1 tools=true max_tokens=1024`)
	if err != nil || c.Action != "new" {
		t.Fatalf("%+v %v", c, err)
	}
	if c.Fields["provider"] != "openai" || c.Fields["endpoint"] == "" {
		t.Fatalf("fields=%v", c.Fields)
	}
	c, err = ParseAgentCommand("/agent edit night model=qwen")
	if err != nil || c.Action != "edit" || c.ID != "night" || c.Fields["model"] != "qwen" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = ParseAgentCommand("/agent delete night")
	if err != nil || c.Action != "delete" || c.ID != "night" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseAgentCommand("/agent edit"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSpecFromFields(t *testing.T) {
	c := SpecFromFields("night", map[string]string{
		"title": "Night", "provider": "openai", "endpoint": "http://127.0.0.1:9000",
		"skills": "atomic-coding,multipass-quality", "tools": "true",
		"max_iter": "12", "temperature": "0.2", "max_tokens": "2048",
		"system_prompt": "Audit carefully.",
	}, nil)
	if err := agents.NormalizeCustom(&c); err != nil {
		t.Fatal(err)
	}
	if c.ID != "night" || c.Provider != "openai" || len(c.Skills) != 2 || c.MaxIter != 12 {
		t.Fatalf("%+v", c)
	}
	base := c
	base.Model = "old"
	edited := SpecFromFields("night", map[string]string{"model": "new"}, &base)
	if edited.Model != "new" || edited.Provider != "openai" {
		t.Fatalf("%+v", edited)
	}
}

func TestFormatAgentListContainsBuiltins(t *testing.T) {
	out := FormatAgentList(nil)
	if !strings.Contains(out, "@worker") || !strings.Contains(out, "/agent") {
		t.Fatalf("list:\n%s", out)
	}
}
