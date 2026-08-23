package evolve

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairValidate(t *testing.T) {
	tests := []struct {
		name    string
		repair  Repair
		wantErr bool
	}{
		{"guidance ok", Repair{Kind: RepairGuidance, Guidance: "do this"}, false},
		{"guidance missing", Repair{Kind: RepairGuidance}, true},
		{"transform ok", Repair{Kind: RepairTransformArgs, Transform: TransformStripLineNumbers, Guidance: "g"}, false},
		{"transform unknown", Repair{Kind: RepairTransformArgs, Transform: "nope", Guidance: "g"}, true},
		{"switch tool ok", Repair{Kind: RepairSwitchTool, Tool: "ws_write", Guidance: "g"}, false},
		{"switch tool missing", Repair{Kind: RepairSwitchTool, Guidance: "g"}, true},
		{"edit format ok", Repair{Kind: RepairEditFormat, EditFormat: "search_replace", Guidance: "g"}, false},
		{"edit format missing", Repair{Kind: RepairEditFormat, Guidance: "g"}, true},
		{"config ok", Repair{Kind: RepairConfig, ConfigKey: "max_tokens", ConfigValue: "4096", Guidance: "g"}, false},
		{"config missing key", Repair{Kind: RepairConfig, Guidance: "g"}, true},
		{"shell ok", Repair{Kind: RepairShell, Command: "go mod tidy", Guidance: "g"}, false},
		{"shell missing", Repair{Kind: RepairShell, Guidance: "g"}, true},
		{"action ok", Repair{Kind: RepairAction, Action: ActionRereadFile, Guidance: "g"}, false},
		{"action missing", Repair{Kind: RepairAction, Guidance: "g"}, true},
		{"unknown kind", Repair{Kind: "teleport", Guidance: "g"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.repair.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && tc.repair.String() == "" {
				t.Error("valid repair has an empty String()")
			}
		})
	}
}

func TestRepairDeterministic(t *testing.T) {
	if (Repair{Kind: RepairGuidance}).Deterministic() {
		t.Error("guidance still needs a model round-trip; it is not deterministic")
	}
	for _, k := range []RepairKind{RepairTransformArgs, RepairEditFormat, RepairConfig, RepairAction, RepairShell, RepairSwitchTool} {
		if !(Repair{Kind: k}).Deterministic() {
			t.Errorf("%s should be deterministic", k)
		}
	}
}

func TestApplyTransform(t *testing.T) {
	tests := []struct {
		name       string
		transform  string
		args       string
		wantOK     bool
		wantField  string
		wantValue  string
		wantAbsent string
	}{
		{
			name:      "strip line-number gutter",
			transform: TransformStripLineNumbers,
			args:      `{"path":"a.go","old_str":"   42| if err != nil {\n   43|     return err"}`,
			wantOK:    true, wantField: "old_str", wantValue: "if err != nil {\n    return err",
		},
		{
			name:      "gutter absent is a no-op",
			transform: TransformStripLineNumbers,
			args:      `{"path":"a.go","old_str":"if err != nil {"}`,
			wantOK:    false,
		},
		{
			name:      "trim trailing whitespace",
			transform: TransformTrimTrailingWS,
			args:      "{\"old_str\":\"a   \\nb\\t\\n\"}",
			wantOK:    true, wantField: "old_str", wantValue: "a\nb\n",
		},
		{
			name:      "unfence a code block",
			transform: TransformUnfence,
			args:      "{\"new_str\":\"```go\\nfunc A() {}\\n```\"}",
			wantOK:    true, wantField: "new_str", wantValue: "func A() {}",
		},
		{
			name:      "shrink old_str to its first anchor line",
			transform: TransformShrinkOldStr,
			args:      `{"old_str":"line one\nline two\nline three\nline four"}`,
			wantOK:    true, wantField: "old_str", wantValue: "line one",
		},
		{
			name:      "shrink is a no-op on a short anchor",
			transform: TransformShrinkOldStr,
			args:      `{"old_str":"line one\nline two"}`,
			wantOK:    false,
		},
		{
			name:      "set replace_all",
			transform: TransformSetReplaceAll,
			args:      `{"path":"a.go","old_str":"x"}`,
			wantOK:    true, wantField: "replace_all",
		},
		{
			name:      "replace_all already set",
			transform: TransformSetReplaceAll,
			args:      `{"path":"a.go","replace_all":true}`,
			wantOK:    false,
		},
		{
			name:      "repair json with a trailing comma",
			transform: TransformRepairJSON,
			args:      `{"path": "a.go", "old_str": "x",}`,
			wantOK:    true, wantField: "old_str", wantValue: "x",
		},
		{
			name:      "repair json inside a fence",
			transform: TransformRepairJSON,
			args:      "```json\n{\"path\": \"a.go\"}\n```",
			wantOK:    true, wantField: "path", wantValue: "a.go",
		},
		{
			name:      "irreparable json is left alone",
			transform: TransformRepairJSON,
			args:      `{"path": "a.go", "old_str": "x`,
			wantOK:    false,
		},
		{
			name:      "unknown transform",
			transform: "teleport",
			args:      `{"a":1}`,
			wantOK:    false,
		},
		{
			name:      "unparsable args",
			transform: TransformStripLineNumbers,
			args:      `not json`,
			wantOK:    false,
		},
		{
			name:      "empty args",
			transform: TransformStripLineNumbers,
			args:      ``,
			wantOK:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ApplyTransform(tc.transform, tc.args)
			if ok != tc.wantOK {
				t.Fatalf("ApplyTransform ok = %v, want %v (out=%s)", ok, tc.wantOK, got)
			}
			if !ok {
				if got != tc.args {
					t.Errorf("a no-op transform changed the args: %q", got)
				}
				return
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("transform produced invalid JSON: %v\n%s", err, got)
			}
			if tc.wantField == "" {
				return
			}
			v, present := parsed[tc.wantField]
			if !present {
				t.Fatalf("field %q missing from %s", tc.wantField, got)
			}
			if tc.wantValue != "" {
				s, _ := v.(string)
				if s != tc.wantValue {
					t.Errorf("%s = %q, want %q", tc.wantField, s, tc.wantValue)
				}
			}
		})
	}
}

func TestStripLineNumberPrefixShapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"   42| code", "code"},
		{"42|code", "code"},
		{"\t7 | code", "code"},
		{"1| a\n2| b", "a\nb"},
		{"no gutter here", "no gutter here"},
		{"x | y", "x | y"}, // not a number: left alone
	}
	for _, tc := range tests {
		if got := stripLineNumberPrefix(tc.in); got != tc.want {
			t.Errorf("stripLineNumberPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnfenceShapes(t *testing.T) {
	if got := unfence("```\nplain\n```"); got != "plain" {
		t.Errorf("unfence bare fence = %q", got)
	}
	if got := unfence("no fence"); got != "no fence" {
		t.Errorf("unfence no-op = %q", got)
	}
	if got := unfence("```go\na\n```\ntrailing"); !strings.Contains(got, "trailing") {
		t.Errorf("unfence must not fire on partial fences: %q", got)
	}
}
