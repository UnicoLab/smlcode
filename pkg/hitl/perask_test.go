package hitl

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

type perAskAnswer struct {
	AskID    string `json:"ask_id"`
	Decision string `json:"decision"`
}

// Two parallel workers each raise a shell ask; each must receive exactly its
// own decision, and neither may disturb the other's files. With the single
// ask.json/answers.json slot the second ask erased the first and the loser's
// WaitAnswersForID deleted the winner's answer.
func TestConcurrentAsksGetTheirOwnDecision(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAskID(dir, "shell", "shell-1", map[string]string{"id": "shell-1", "command": "go test ./a"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteAskID(dir, "shell", "shell-2", map[string]string{"id": "shell-2", "command": "rm -rf build"}); err != nil {
		t.Fatal(err)
	}
	ids, err := ListAskIDs(dir, "shell")
	if err != nil || len(ids) != 2 || ids[0] != "shell-1" || ids[1] != "shell-2" {
		t.Fatalf("ListAskIDs = %v err=%v", ids, err)
	}

	var wg sync.WaitGroup
	results := make([]perAskAnswer, 2)
	oks := make([]bool, 2)
	for i, id := range []string{"shell-1", "shell-2"} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			ok, err := WaitAnswerID(context.Background(), dir, "shell", id, 5*time.Second, &results[i])
			if err != nil {
				t.Errorf("wait %s: %v", id, err)
			}
			oks[i] = ok
		}(i, id)
	}
	// Answer the SECOND ask first with a deny, then the first with an approve.
	time.Sleep(60 * time.Millisecond)
	if err := WriteAnswerIDOnce(dir, "shell", "shell-2", perAskAnswer{AskID: "shell-2", Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := WriteAnswerIDOnce(dir, "shell", "shell-1", perAskAnswer{AskID: "shell-1", Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if !oks[0] || results[0].Decision != "approve" || results[0].AskID != "shell-1" {
		t.Fatalf("ask 1 got %+v ok=%v", results[0], oks[0])
	}
	if !oks[1] || results[1].Decision != "deny" || results[1].AskID != "shell-2" {
		t.Fatalf("ask 2 got %+v ok=%v", results[1], oks[1])
	}
	// A second decision on an answered ask is refused, not overwritten.
	if err := WriteAnswerIDOnce(dir, "shell", "shell-1", perAskAnswer{AskID: "shell-1", Decision: "deny"}); err == nil || !os.IsExist(err) {
		t.Fatalf("second answer should be refused with IsExist, got %v", err)
	}
	// Clearing one id leaves the other's files alone.
	ClearID(dir, "shell", "shell-1")
	if _, err := os.Stat(AskIDPath(dir, "shell", "shell-2")); err != nil {
		t.Fatalf("ClearID(shell-1) removed shell-2's ask: %v", err)
	}
	if _, err := os.Stat(AnswerIDPath(dir, "shell", "shell-1")); err == nil {
		t.Fatal("ClearID left shell-1's answer behind")
	}
}

func TestWaitAnswerIDTimesOutAndWithdraws(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAskID(dir, "shell", "shell-9", map[string]string{"id": "shell-9"}); err != nil {
		t.Fatal(err)
	}
	var got perAskAnswer
	ok, err := WaitAnswerID(context.Background(), dir, "shell", "shell-9", 300*time.Millisecond, &got)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(AskIDPath(dir, "shell", "shell-9")); err == nil {
		t.Fatal("timed-out ask was not withdrawn")
	}
	// ClearAll sweeps the per-ask directories too.
	_ = WriteAskID(dir, "shell", "shell-10", map[string]string{"id": "shell-10"})
	ClearAll(dir)
	if ids, _ := ListAskIDs(dir, "shell"); len(ids) != 0 {
		t.Fatalf("ClearAll left per-ask files: %v", ids)
	}
}
