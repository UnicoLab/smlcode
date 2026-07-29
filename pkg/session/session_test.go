package session

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestSaveLoadList(t *testing.T) {
	dir := t.TempDir()
	path, err := Save(dir, Session{
		ID: "sess-1", Query: "hello", Success: true, Summary: "ok",
		Board: plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "x", Column: plan.ColDone}}},
	})
	if err != nil || path == "" {
		t.Fatal(err, path)
	}
	s, err := Load(dir, "sess-1")
	if err != nil || s.Query != "hello" || !s.Success {
		t.Fatalf("%+v %v", s, err)
	}
	list, err := List(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
}
