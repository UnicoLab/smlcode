package config

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/authstore"
)

func TestSchemaAndPatchNewFields(t *testing.T) {
	if len(Schema()) < 5 {
		t.Fatal("schema too small")
	}
	c := Default(t.TempDir())
	engine := "auto"
	models := []string{"gpt-4o-mini"}
	retry := 5
	on := true
	c.ApplyPatch(Patch{
		ContextCompactEngine: &engine,
		EnabledModels:        &models,
		LLMRetryCount:        &retry,
		AutoRefine:           &on,
		SessionEventLog:      &on,
	})
	if c.ContextCompactEngine != "auto" || len(c.EnabledModels) != 1 || c.LLMRetryCount != 5 {
		t.Fatalf("%+v", c)
	}
	if !c.AutoRefine || !c.SessionEventLog {
		t.Fatal("bools")
	}
}

func TestResolveAPIKeyFromAuthJSON(t *testing.T) {
	root := t.TempDir()
	c := Default(root)
	c.Provider = "openai"
	c.APIKey = ""
	if err := authstore.Set(c.SlmDir(), "openai", "sk-from-file"); err != nil {
		t.Fatal(err)
	}
	c.ResolveAPIKey()
	if c.APIKey != "sk-from-file" {
		t.Fatalf("got %q", c.APIKey)
	}
}
