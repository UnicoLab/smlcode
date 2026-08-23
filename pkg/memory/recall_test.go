package memory

import (
	"strings"
	"testing"
	"time"
)

func seedRecallCorpus(t *testing.T) *Store {
	t.Helper()
	s, _, _ := testStore(t)
	corpus := []Episode{
		{Query: "add retry with backoff to the HTTP client", FilesChanged: []string{"pkg/http/client.go"}, ToolsUsed: []string{"ws_edit"}, Language: "go", Success: true},
		{Query: "fix flaky timeout in the HTTP client tests", FilesChanged: []string{"pkg/http/client_test.go"}, ToolsUsed: []string{"ws_edit"}, Language: "go", Success: true},
		{Query: "write the README installation section", FilesChanged: []string{"README.md"}, ToolsUsed: []string{"ws_write"}, Language: "go", Success: true},
		{Query: "rename the kanban board columns", FilesChanged: []string{"pkg/plan/kanban.go"}, ToolsUsed: []string{"ws_edit"}, Language: "go", Success: false},
		{Query: "add a python parser for the config file", FilesChanged: []string{"tools/parse.py"}, Language: "python", Success: true},
	}
	for _, e := range corpus {
		if err := s.RecordEpisode(e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestRecallEpisodesRanksBySalience(t *testing.T) {
	s := seedRecallCorpus(t)
	tests := []struct {
		name      string
		query     Query
		wantFirst string
		wantNone  bool
		maxCount  int
	}{
		{
			name:      "topical query finds the http work",
			query:     Query{Text: "retry the http client request on failure"},
			wantFirst: "add retry with backoff to the HTTP client",
			maxCount:  3,
		},
		{
			name:      "file overlap dominates",
			query:     Query{Text: "unrelated words", Files: []string{"pkg/plan/kanban.go"}},
			wantFirst: "rename the kanban board columns",
			maxCount:  3,
		},
		{
			name:     "language filter excludes other stacks",
			query:    Query{Text: "python parser config", Language: "go"},
			wantNone: true,
		},
		{
			name:      "language filter admits matching stack",
			query:     Query{Text: "python parser config", Language: "python"},
			wantFirst: "add a python parser for the config file",
			maxCount:  3,
		},
		{
			name:     "nonsense query recalls nothing",
			query:    Query{Text: "quantum chromodynamics kubernetes helm"},
			wantNone: true,
		},
		{
			name:     "empty query recalls nothing",
			query:    Query{},
			wantNone: true,
		},
		{
			name:      "success filter",
			query:     Query{Text: "kanban board columns", SuccessOnly: true},
			wantNone:  true,
			wantFirst: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Episodes().RecallEpisodes(tc.query, 3)
			if tc.wantNone {
				if len(got) != 0 {
					t.Fatalf("recalled %d episodes, want none: %+v", len(got), got[0].Query)
				}
				return
			}
			if len(got) == 0 {
				t.Fatal("recalled nothing")
			}
			if got[0].Query != tc.wantFirst {
				t.Errorf("first = %q, want %q", got[0].Query, tc.wantFirst)
			}
			if tc.maxCount > 0 && len(got) > tc.maxCount {
				t.Errorf("recalled %d, want at most %d", len(got), tc.maxCount)
			}
		})
	}
}

// A single plausible-but-irrelevant memory is worse than none for a small
// model, so recall must be precision-biased: it should not pad results.
func TestRecallPrefersPrecisionOverRecall(t *testing.T) {
	s := seedRecallCorpus(t)
	got := s.Episodes().RecallEpisodes(Query{Text: "add retry with backoff to the HTTP client"}, 5)
	if len(got) == 0 {
		t.Fatal("exact query recalled nothing")
	}
	for _, e := range got {
		if strings.Contains(e.Query, "README") || strings.Contains(e.Query, "kanban") {
			t.Errorf("weak distractor %q survived the relative floor", e.Query)
		}
	}
}

func TestRecallRecencyBreaksTies(t *testing.T) {
	s, _, _ := testStore(t)
	base := time.Now().UTC()
	old := Episode{Query: "refactor the widget factory", At: base.Add(-20 * 24 * time.Hour), Success: true}
	fresh := Episode{Query: "refactor the widget factory", At: base.Add(-1 * time.Hour), Success: true}
	if err := s.RecordEpisode(old); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEpisode(fresh); err != nil {
		t.Fatal(err)
	}
	got := s.Episodes().RecallScored(Query{Text: "refactor the widget factory"}, 2)
	if len(got) < 2 {
		t.Fatalf("recalled %d, want 2", len(got))
	}
	if !got[0].Episode.At.After(got[1].Episode.At) {
		t.Errorf("recency did not order results: %v then %v", got[0].Episode.At, got[1].Episode.At)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"the and for", nil},
		{"HTTPClient.go", []string{"http", "client", "httpclient", "go"}},
		{"pkg/loop/runner.go", []string{"pkg", "loop", "runner", "go"}},
		{"a b cd", []string{"cd"}},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := tokenize(tc.in)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPathTokens(t *testing.T) {
	got := pathTokens("pkg/loop/runner.go")
	for _, want := range []string{"pkg", "loop", "runner", "go"} {
		if !strings.Contains(got, want) {
			t.Errorf("pathTokens missing %q: %q", want, got)
		}
	}
	if pathTokens("  ") != "" {
		t.Error("blank path should tokenize to nothing")
	}
}

func TestRecallCheapOnLargeCorpus(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector's instrumentation makes wall-clock budgets meaningless")
	}
	s, _, _ := testStore(t)
	for i := 0; i < 300; i++ {
		_ = s.RecordEpisode(Episode{
			Query:        "task " + itoa(i) + " touching the loop runner and the plan board",
			FilesChanged: []string{"pkg/loop/runner" + itoa(i%20) + ".go"},
			Language:     "go", Success: i%3 != 0,
		})
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		_ = s.Episodes().RecallEpisodes(Query{Text: "fix the loop runner wave scheduling", Language: "go"}, 3)
	}
	perCall := time.Since(start) / 20
	if perCall > 25*time.Millisecond {
		t.Errorf("recall took %v per call over 300 episodes; hot path budget is a few ms", perCall)
	}
}
