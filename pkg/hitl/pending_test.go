package hitl

import (
	"context"
	"testing"
	"time"
)

func TestWriteWaitAnswers(t *testing.T) {
	dir := t.TempDir()
	type ask struct {
		ID string `json:"id"`
	}
	type ans struct {
		Decision string `json:"decision"`
	}
	if err := WriteAsk(dir, "plan", ask{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	var got ask
	ok, err := ReadAsk(dir, "plan", &got)
	if err != nil || !ok || got.ID != "p1" {
		t.Fatalf("ask %+v ok=%v err=%v", got, ok, err)
	}

	done := make(chan error, 1)
	go func() {
		time.Sleep(80 * time.Millisecond)
		done <- WriteAnswers(dir, "plan", ans{Decision: "approve"})
	}()

	var a ans
	ok, err = WaitAnswers(context.Background(), dir, "plan", time.Second, &a)
	if err != nil || !ok || a.Decision != "approve" {
		t.Fatalf("wait %+v ok=%v err=%v", a, ok, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	Clear(dir, "plan")
}
