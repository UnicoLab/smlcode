package main

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestConfigSetFromSchemaValue(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string // substring; empty means the write must succeed
		check   func(*config.Config) bool
	}{
		{
			name:  "bool alias",
			key:   "shell_whitelist",
			value: "on",
			check: func(c *config.Config) bool { return c.ShellWhitelist },
		},
		{
			name:  "string array splits on commas",
			key:   "enabled_models",
			value: "qwen:7b, qwen:14b",
			check: func(c *config.Config) bool { return len(c.EnabledModels) == 2 },
		},
		{
			name:  "duration accepts a human spelling",
			key:   "escalate_ask_timeout",
			value: "12m",
			check: func(c *config.Config) bool { return c.EscalateAskTimeout.Minutes() == 12 },
		},
		{
			name:  "duration accepts bare seconds",
			key:   "shell_timeout",
			value: "90",
			check: func(c *config.Config) bool { return c.ShellTimeout.Seconds() == 90 },
		},
		{
			name:  "alias resolves to the real key",
			key:   "parallel",
			value: "6",
			check: func(c *config.Config) bool { return c.MaxParallel == 6 },
		},
		{
			name:  "new enum",
			key:   "qa_bootstrap",
			value: "auto",
			check: func(c *config.Config) bool { return c.QABootstrap == "auto" },
		},
		{name: "invalid enum", key: "context_compact_engine", value: "remote-magic", wantErr: "allowed"},
		{name: "invalid enum names the key", key: "permission", value: "sudo", wantErr: "permission"},
		{name: "invalid int", key: "max_parallel", value: "abc", wantErr: "whole number"},
		{name: "invalid bool", key: "qa_gate", value: "maybe", wantErr: "boolean"},
		{name: "invalid duration", key: "escalate_ask_timeout", value: "soon", wantErr: "duration"},
		{name: "unknown key", key: "not_a_field", value: "true", wantErr: "unknown config key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default(t.TempDir())
			err := c.Set(tc.key, tc.value)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Set(%q, %q) should have failed", tc.key, tc.value)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not mention %q", err.Error(), tc.wantErr)
				}
				// Every write error must name the offending value.
				if !strings.Contains(err.Error(), tc.value) && !strings.Contains(err.Error(), tc.key) {
					t.Fatalf("error %q names neither key nor value", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q, %q): %v", tc.key, tc.value, err)
			}
			c.Normalize()
			if !tc.check(c) {
				t.Fatalf("Set(%q, %q) did not take effect", tc.key, tc.value)
			}
		})
	}
}

func TestConfigValueErrorListsAllowedSet(t *testing.T) {
	c := config.Default(t.TempDir())
	err := c.Set("structured_decoding", "sometimes")
	if err == nil {
		t.Fatal("expected an enum error")
	}
	msg := err.Error()
	for _, want := range []string{"structured_decoding", "sometimes", "auto", "off"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q is missing %q", msg, want)
		}
	}
}
