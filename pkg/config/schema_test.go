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
	budget := 2048
	head := 60
	pct := 70
	on := true
	off := false
	c.ApplyPatch(Patch{
		ContextCompactEngine:  &engine,
		EnabledModels:         &models,
		LLMRetryCount:         &retry,
		AutoRefine:            &on,
		SessionEventLog:       &on,
		ShellWriteGuard:       &on,
		FileCheckpoints:       &on,
		RequireSmoke:          &on,
		ClaimsGate:            &on,
		OverEditGuard:         &on,
		FinalizeWarn:          &on,
		AutoTextTools:         &on,
		ThinkingBudgetTokens:  &budget,
		ReadHeadLines:         &head,
		ReactCompactAtPercent: &pct,
		ShellWhitelist:        &off,
	})
	if c.ContextCompactEngine != "auto" || len(c.EnabledModels) != 1 || c.LLMRetryCount != 5 {
		t.Fatalf("%+v", c)
	}
	if !c.AutoRefine || !c.SessionEventLog {
		t.Fatal("bools")
	}
	if !c.ShellWriteGuard || !c.FileCheckpoints || !c.RequireSmoke || !c.ClaimsGate ||
		!c.OverEditGuard || !c.FinalizeWarn || !c.AutoTextTools {
		t.Fatalf("guards: %+v", c)
	}
	if c.ThinkingBudgetTokens != 2048 || c.ReadHeadLines != 60 || c.ReactCompactAtPercent != 70 {
		t.Fatalf("budgets: %+v", c)
	}
	if c.ShellWhitelist {
		t.Fatalf("shell_whitelist patch failed: %+v", c)
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
