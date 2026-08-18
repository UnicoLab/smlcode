package main

import "testing"

func TestConfigPatchFromSchemaValueParsesReadinessGuard(t *testing.T) {
	patch, ok, err := configPatchFromSchemaValue("shell_whitelist", "on")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || patch.ShellWhitelist == nil || !*patch.ShellWhitelist {
		t.Fatalf("patch did not set shell_whitelist: ok=%v patch=%+v", ok, patch)
	}
}

func TestConfigPatchFromSchemaValueParsesStringArray(t *testing.T) {
	patch, ok, err := configPatchFromSchemaValue("enabled_models", "qwen:7b, qwen:14b")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || patch.EnabledModels == nil || len(*patch.EnabledModels) != 2 {
		t.Fatalf("patch did not parse enabled_models: ok=%v patch=%+v", ok, patch)
	}
}

func TestConfigPatchFromSchemaValueRejectsInvalidEnum(t *testing.T) {
	if _, _, err := configPatchFromSchemaValue("context_compact_engine", "remote-magic"); err == nil {
		t.Fatal("expected invalid enum to fail")
	}
}

func TestConfigPatchFromSchemaValueUnknown(t *testing.T) {
	if _, ok, err := configPatchFromSchemaValue("not_a_field", "true"); err != nil || ok {
		t.Fatalf("unknown field mismatch: ok=%v err=%v", ok, err)
	}
}
