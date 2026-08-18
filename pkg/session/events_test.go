package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAppendAndReadEvents(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := AppendEvent(slm, "run-1", EventRecord{Phase: "init", Kind: "phase", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(slm, "run-1", EventRecord{Phase: "execute", Agent: "worker", Message: "tick"}); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadEvents(slm, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d", len(recs))
	}
	if recs[0].Message != "hello" || recs[1].Agent != "worker" {
		t.Fatalf("%+v", recs)
	}
}

func TestAppendAndReadEventData(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	payload := map[string]any{
		"summary": "dynamic",
		"phases":  []any{map[string]any{"id": "execute", "enabled": true}},
	}
	if err := AppendEvent(slm, "run-1", EventRecord{
		Phase: "compose", Kind: "composition", Message: "dynamic", Output: "# Dynamic pipeline", Data: payload,
	}); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadEvents(slm, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Output == "" || recs[0].Data == nil {
		t.Fatalf("%+v", recs)
	}
	raw, err := json.Marshal(recs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid data: %s", raw)
	}
}
