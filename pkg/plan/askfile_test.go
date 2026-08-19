package plan

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWriteScopeAnswersOnceDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	first := ScopeAnswers{
		AskID: "scope-1",
		Answers: []ScopeAnswer{{
			QuestionID: "language",
			Selected:   []string{"Go"},
		}},
	}
	second := ScopeAnswers{
		AskID: "scope-1",
		Answers: []ScopeAnswer{{
			QuestionID: "language",
			Selected:   []string{"Python"},
		}},
	}
	if err := WriteScopeAnswersOnce(dir, first); err != nil {
		t.Fatal(err)
	}
	err := WriteScopeAnswersOnce(dir, second)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected exists error, got %v", err)
	}

	got, ok, err := ReadScopeAnswers(dir)
	if err != nil || !ok {
		t.Fatalf("read ok=%v err=%v", ok, err)
	}
	if len(got.Answers) != 1 || got.Answers[0].Selected[0] != "Go" {
		t.Fatalf("answer overwritten: %+v", got)
	}
}

func TestWaitScopeAnswersForIDTimeoutClearsPendingAsk(t *testing.T) {
	dir := t.TempDir()
	ask := ScopeAsk{
		ID:        "scope-1",
		Kind:      "clarify",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteScopeAsk(dir, ask); err != nil {
		t.Fatal(err)
	}

	got, ok, err := WaitScopeAnswersForID(context.Background(), dir, ask.ID, time.Nanosecond)
	if err != nil || ok {
		t.Fatalf("wait %+v ok=%v err=%v", got, ok, err)
	}
	if _, err := os.Stat(ClarifyAskPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("timeout did not clear clarify ask err=%v", err)
	}
	if _, err := os.Stat(ClarifyAnswersPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("timeout did not clear clarify answers err=%v", err)
	}
}
