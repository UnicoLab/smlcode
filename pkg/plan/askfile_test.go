package plan

import (
	"errors"
	"os"
	"testing"
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
