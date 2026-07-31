package repair

import (
	"encoding/json"
	"testing"
)

func TestRepairJSONTrailingComma(t *testing.T) {
	raw := `{"status":"done","summary":"ok",}`
	fixed, err := RepairJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(fixed), &m); err != nil {
		t.Fatal(err)
	}
	if m["status"] != "done" {
		t.Fatalf("%v", m)
	}
}

func TestRepairJSONSingleQuotes(t *testing.T) {
	raw := `{'path':'a.go','content':'x'}`
	fixed, err := RepairJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(fixed), &m); err != nil {
		t.Fatal(err)
	}
	if m["path"] != "a.go" {
		t.Fatalf("%v", m)
	}
}

func TestRepairJSONFence(t *testing.T) {
	raw := "Sure:\n```json\n{\"passed\":true,\"failures\":[]}\n```\n"
	fixed, err := RepairJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(fixed), &m); err != nil {
		t.Fatal(err)
	}
	if m["passed"] != true {
		t.Fatalf("%v", m)
	}
}

func TestRepairJSONCloseBraces(t *testing.T) {
	raw := `{"status":"done","summary":"x"`
	fixed, err := RepairJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(fixed)) {
		t.Fatalf("still invalid: %s", fixed)
	}
}

func TestRepairToolArgsKV(t *testing.T) {
	fixed, err := RepairToolArgs(`path=pkg/a.go, old=hi, new=hello`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(fixed), &m); err != nil {
		t.Fatal(err)
	}
	if m["path"] != "pkg/a.go" {
		t.Fatalf("%v", m)
	}
}

func TestExtractJSON(t *testing.T) {
	got := ExtractJSON(`blah {"a":1} trailing`)
	if got != `{"a":1}` {
		t.Fatalf("%q", got)
	}
}
