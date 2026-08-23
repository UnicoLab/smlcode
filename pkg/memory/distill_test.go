package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDistillFactsDeterministic(t *testing.T) {
	now := time.Now().UTC()
	episodes := []Episode{
		{
			ID: "e1", At: now, Query: "add retry", Language: "go",
			FilesChanged: []string{"pkg/http/client.go"},
			Commands:     []Command{{Cmd: "go test ./... -count=1", OK: true}, {Cmd: "make ui", OK: false}},
			EditFormat:   "search_replace", EditsAttempted: 10, EditsApplied: 9,
			Failures: []FailureNote{{Fingerprint: "fp1", Message: "old_str not found", Resolution: "re-read then smaller anchor", ResolvedBy: "rule_a"}},
			Success:  true,
		},
		{
			ID: "e2", At: now, Query: "add timeout", Language: "go",
			FilesChanged: []string{"pkg/http/client.go", "pkg/http/server.go"},
			Commands:     []Command{{Cmd: "go test ./...", OK: true}},
			EditFormat:   "search_replace", EditsAttempted: 6, EditsApplied: 6,
			Failures: []FailureNote{{Fingerprint: "fp1", Message: "old_str not found", Resolution: "re-read then smaller anchor", ResolvedBy: "rule_a"}},
			Success:  true,
		},
	}

	first := DistillFacts(episodes, now)
	second := DistillFacts(episodes, now)
	if len(first) != len(second) {
		t.Fatalf("distillation is not deterministic: %d vs %d facts", len(first), len(second))
	}
	for i := range first {
		if first[i].Subject != second[i].Subject || first[i].Text != second[i].Text {
			t.Fatalf("distillation differs at %d: %+v vs %+v", i, first[i], second[i])
		}
	}

	kinds := map[FactKind][]Fact{}
	for _, f := range first {
		kinds[f.Kind] = append(kinds[f.Kind], f)
	}
	for _, want := range []FactKind{FactCommand, FactLayout, FactFile, FactGotcha, FactConvention, FactDependency} {
		if len(kinds[want]) == 0 {
			t.Errorf("no %s fact distilled from the corpus", want)
		}
	}
	var cmdText string
	for _, f := range kinds[FactCommand] {
		if strings.Contains(f.Text, "go test") {
			cmdText = f.Text
		}
	}
	if cmdText == "" {
		t.Fatalf("`go test` was not promoted to a command fact: %+v", kinds[FactCommand])
	}
	if !strings.Contains(cmdText, "2/2") {
		t.Errorf("command fact lost its evidence count: %q", cmdText)
	}
	// `make ui` failed both times it appeared once, so it must not be promoted.
	for _, f := range kinds[FactCommand] {
		if strings.Contains(f.Text, "make ui") {
			t.Errorf("a command that never succeeded was promoted: %q", f.Text)
		}
	}
	var conv string
	for _, f := range kinds[FactConvention] {
		if strings.Contains(f.Subject, "edit-format") {
			conv = f.Text
		}
	}
	if !strings.Contains(conv, "93%") {
		t.Errorf("edit-format apply rate wrong: %q (want 15/16 = 93%%)", conv)
	}
}

func TestDistillWorksWithoutLLMAndSurvivesABadOne(t *testing.T) {
	newStore := func() *Store {
		s, _, _ := testStore(t)
		for i := 0; i < 4; i++ {
			_ = s.RecordEpisode(Episode{
				Query: "task " + itoa(i), Language: "go", Success: true,
				FilesChanged: []string{"pkg/a/b.go"},
				Commands:     []Command{{Cmd: "go build ./...", OK: true}},
			})
		}
		return s
	}

	base := newStore()
	if err := base.Distill(context.Background(), nil); err != nil {
		t.Fatalf("Distill without an LLM: %v", err)
	}
	baseline := base.Semantic().Count()
	if baseline == 0 {
		t.Fatal("deterministic distillation produced nothing")
	}

	tests := []struct {
		name string
		sum  Summarizer
	}{
		{"erroring", func(context.Context, string) (string, error) { return "", errors.New("model died") }},
		{"empty", func(context.Context, string) (string, error) { return "   ", nil }},
		{"garbage", func(context.Context, string) (string, error) { return strings.Repeat("x", 100000), nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore()
			if err := s.Distill(context.Background(), tc.sum); err != nil {
				t.Fatalf("Distill with a %s summarizer: %v", tc.name, err)
			}
			if got := s.Semantic().Count(); got < baseline {
				t.Errorf("a bad LLM cost us facts: %d < %d", got, baseline)
			}
			for _, f := range s.Semantic().All() {
				if len(f.Text) > MaxFactTextLen {
					t.Errorf("unbounded fact text: %d bytes", len(f.Text))
				}
			}
		})
	}

	t.Run("good", func(t *testing.T) {
		s := newStore()
		err := s.Distill(context.Background(), func(context.Context, string) (string, error) {
			return "This is a Go library with a thin CLI on top.", nil
		})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := s.Semantic().Get(FactConvention, "project-brief")
		if !ok || !strings.Contains(got.Text, "Go library") {
			t.Fatalf("LLM brief not stored: %+v", got)
		}
		if got.Confidence > 0.7 {
			t.Errorf("an unverified LLM claim entered at confidence %.2f; it must be low-support", got.Confidence)
		}
	})
}

func TestNormalizeCommand(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"  go test ./...  ", "go test ./..."},
		{"$ go test ./...", "go test ./..."},
		{"go test ./... -count=1 -race -v", "go test ./..."},
		{"make -j4 test", "make test"},
		{"echo hi\nrm -rf /", "echo hi"},
	}
	for _, tc := range tests {
		if got := normalizeCommand(tc.in); got != tc.want {
			t.Errorf("normalizeCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDistillEmptyCorpus(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.Distill(context.Background(), nil); err != nil {
		t.Fatalf("Distill on an empty store: %v", err)
	}
	if s.Semantic().Count() != 0 {
		t.Error("facts invented from nothing")
	}
	if got := DistillFacts(nil, time.Now()); got != nil {
		t.Errorf("DistillFacts(nil) = %v", got)
	}
}
