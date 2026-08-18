package hitl

import (
	"context"
	"errors"
	"os"
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

func TestWaitAnswersForIDIgnoresStaleAnswer(t *testing.T) {
	dir := t.TempDir()
	type ans struct {
		AskID    string `json:"ask_id,omitempty"`
		Decision string `json:"decision"`
	}

	if err := WriteAnswers(dir, "plan", ans{AskID: "old", Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(80 * time.Millisecond)
		done <- WriteAnswers(dir, "plan", ans{AskID: "current", Decision: "replan"})
	}()

	var got ans
	ok, err := WaitAnswersForID(context.Background(), dir, "plan", "current", time.Second, &got)
	if err != nil || !ok || got.AskID != "current" || got.Decision != "replan" {
		t.Fatalf("wait %+v ok=%v err=%v", got, ok, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWaitAnswersForIDRejectsLateAnswerFile(t *testing.T) {
	dir := t.TempDir()
	type ans struct {
		AskID    string `json:"ask_id,omitempty"`
		Decision string `json:"decision"`
	}
	if err := WriteAnswers(dir, "plan", ans{AskID: "current", Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(AnswersPath(dir, "plan"), future, future); err != nil {
		t.Fatal(err)
	}

	var got ans
	ok, err := WaitAnswersForID(context.Background(), dir, "plan", "current", time.Nanosecond, &got)
	if err != nil || ok {
		t.Fatalf("wait %+v ok=%v err=%v", got, ok, err)
	}
}

func TestWriteAnswersOnceDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	type ans struct {
		AskID    string `json:"ask_id,omitempty"`
		Decision string `json:"decision"`
	}
	if err := WriteAnswersOnce(dir, "plan", ans{AskID: "current", Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	err := WriteAnswersOnce(dir, "plan", ans{AskID: "current", Decision: "replan"})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected exists error, got %v", err)
	}

	var got ans
	ok, err := ReadAnswers(dir, "plan", &got)
	if err != nil || !ok {
		t.Fatalf("read ok=%v err=%v", ok, err)
	}
	if got.Decision != "approve" {
		t.Fatalf("answer overwritten: %+v", got)
	}
}

func TestClearAllRemovesKnownHITLKinds(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"clarify", "plan", "continue", "escalate", "shell"} {
		if err := WriteAsk(dir, kind, map[string]string{"id": kind}); err != nil {
			t.Fatal(err)
		}
		if err := WriteAnswers(dir, kind, map[string]string{"ask_id": kind}); err != nil {
			t.Fatal(err)
		}
	}
	ClearAll(dir)
	for _, kind := range []string{"clarify", "plan", "continue", "escalate", "shell"} {
		if _, err := os.Stat(AskPath(dir, kind)); !os.IsNotExist(err) {
			t.Fatalf("%s ask still exists err=%v", kind, err)
		}
		if _, err := os.Stat(AnswersPath(dir, kind)); !os.IsNotExist(err) {
			t.Fatalf("%s answer still exists err=%v", kind, err)
		}
	}
}
