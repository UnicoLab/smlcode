package session

import (
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
