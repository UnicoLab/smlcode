package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenIsAlwaysUsable(t *testing.T) {
	tests := []struct {
		name    string
		project func(t *testing.T) string
		user    func(t *testing.T) string
		wantDir bool
	}{
		{"normal", func(t *testing.T) string { return t.TempDir() }, func(t *testing.T) string { return t.TempDir() }, true},
		{"no project dir", func(*testing.T) string { return "" }, func(t *testing.T) string { return t.TempDir() }, false},
		{"no user dir uses home", func(t *testing.T) string { return t.TempDir() }, func(t *testing.T) string { return t.TempDir() }, true},
		{"unwritable project dir", func(t *testing.T) string {
			dir := t.TempDir()
			// A file where the .slmcode directory should go.
			if err := os.WriteFile(filepath.Join(dir, ".slmcode"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}, func(t *testing.T) string { return t.TempDir() }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(tc.project(t), tc.user(t))
			if err != nil {
				t.Fatalf("Open must not fail: %v", err)
			}
			defer func() { _ = s.Close() }()
			if (s.Dir() != "") != tc.wantDir {
				t.Errorf("Dir = %q, wantDir=%v", s.Dir(), tc.wantDir)
			}
			// Every operation must work regardless.
			if err := s.RecordEpisode(ep("q", []string{"a.go"}, true)); err != nil {
				t.Errorf("RecordEpisode: %v", err)
			}
			s.Semantic().Observe(Fact{Kind: FactCommand, Subject: "s", Text: "t"})
			s.Procedural().Record(ProcKey{Topic: TopicEditFormat, Option: "o"}, true, "")
			s.Working().RecordTool(ToolEvent{Tool: "ws_read", Path: "a.go", OK: true})
			_ = s.RenderForPrompt("worker", 500)
			if err := s.Flush(); err != nil {
				t.Errorf("Flush: %v", err)
			}
		})
	}
}

func TestRenderForPromptBudgetAndRoles(t *testing.T) {
	s, _, _ := testStore(t)
	s.SetRunContext(RunContext{
		RunID: "r1", Query: "add retry to the HTTP client",
		Language: "go", Model: "qwen2.5-coder:14b", Role: "worker",
		Files: []string{"pkg/http/client.go"},
	})
	for i := 0; i < 6; i++ {
		_ = s.RecordEpisode(Episode{
			Query: "add retry to the HTTP client", FilesChanged: []string{"pkg/http/client.go"},
			Language: "go", Success: true,
			Commands: []Command{{Cmd: "go test ./...", OK: true}},
			Failures: []FailureNote{{Message: "old_str not found", Resolution: "re-read then retry", ResolvedBy: "rule_x"}},
		})
	}
	if err := s.Distill(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	s.Working().RecordTool(ToolEvent{Tool: "ws_read", Path: "pkg/http/client.go", OK: true})
	for i := 0; i < 10; i++ {
		s.Procedural().Record(ProcKey{
			Topic: TopicEditFormat, Option: "search_replace",
			ModelFamily: "qwen2.5-coder", Language: "go",
		}, true, "")
	}

	for _, role := range []string{"worker", "planner", "explorer", "tester", "reviewer", "unknown-role"} {
		t.Run(role, func(t *testing.T) {
			for _, budget := range []int{80, 200, 600, 1500} {
				out := s.RenderForPrompt(role, budget)
				if n := countTokens(nil, out); n > budget {
					t.Errorf("role %s budget %d: rendered %d tokens", role, budget, n)
				}
			}
		})
	}
	full := s.RenderForPrompt("worker", 1500)
	for _, want := range []string{"Working memory", "What we know about this project"} {
		if !strings.Contains(full, want) {
			t.Errorf("worker render missing %q:\n%s", want, full)
		}
	}
}

func TestRenderForPromptEmptyWhenNothingKnown(t *testing.T) {
	s, _, _ := testStore(t)
	if got := s.RenderForPrompt("worker", 800); got != "" {
		t.Fatalf("fresh store rendered %q, want empty", got)
	}
}

func TestForgetScopes(t *testing.T) {
	scopes := []Scope{ScopeWorking, ScopeEpisodic, ScopeSemantic, ScopeProcedural, ScopeProject, ScopeAll}
	for _, scope := range scopes {
		t.Run(string(scope), func(t *testing.T) {
			s, _, _ := testStore(t)
			_ = s.RecordEpisode(ep("q", []string{"a.go"}, true))
			s.Semantic().Observe(Fact{Kind: FactCommand, Subject: "s", Text: "t"})
			s.Procedural().Record(ProcKey{Topic: TopicEditFormat, Option: "o"}, true, "")
			s.Working().RecordTool(ToolEvent{Tool: "ws_read", Path: "a.go", OK: true})
			if err := s.Flush(); err != nil {
				t.Fatal(err)
			}
			if err := s.Forget(scope); err != nil {
				t.Fatalf("Forget(%s): %v", scope, err)
			}
			switch scope {
			case ScopeEpisodic, ScopeProject, ScopeAll:
				if s.Episodes().Count() != 0 {
					t.Errorf("episodes survived Forget(%s)", scope)
				}
			case ScopeSemantic:
				if s.Semantic().Count() != 0 {
					t.Errorf("facts survived Forget(%s)", scope)
				}
			case ScopeProcedural:
				if s.Procedural().Count() != 0 {
					t.Errorf("procedures survived Forget(%s)", scope)
				}
			}
			if scope == ScopeAll && s.Procedural().Count() != 0 {
				t.Error("procedures survived Forget(all)")
			}
			// The store must still be fully functional afterwards.
			if err := s.RecordEpisode(ep("after", nil, true)); err != nil {
				t.Errorf("store unusable after Forget: %v", err)
			}
		})
	}
	s, _, _ := testStore(t)
	if err := s.Forget("nonsense"); err == nil {
		t.Error("unknown scope should error")
	}
}

func TestResetRemovesDirectories(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	s, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.RecordEpisode(ep("q", nil, true))
	_ = s.Close()
	if err := Reset(proj, user); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".slmcode", "memory")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("project memory dir survived Reset: %v", err)
	}
	// Reopening after a manual rm -rf must be clean.
	s2, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if s2.Episodes().Count() != 0 {
		t.Error("reopened store has stale data")
	}
}

func TestPruneBoundsEveryLayer(t *testing.T) {
	now := time.Now().UTC()
	proj, user := t.TempDir(), t.TempDir()
	s, err := OpenWith(proj, user, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	for i := 0; i < 120; i++ {
		at := now.Add(-time.Duration(i) * 24 * time.Hour)
		_ = s.RecordEpisode(Episode{Query: "task " + itoa(i), At: at, Success: true})
		s.Semantic().Observe(Fact{Kind: FactFile, Subject: "f" + itoa(i), Text: "t" + itoa(i)})
		s.Procedural().Record(ProcKey{Topic: TopicKnob, Option: "o" + itoa(i)}, true, "")
	}
	rep, err := s.PruneReport(PrunePolicy{
		MaxEpisodes: 20, MaxEpisodeAge: 30 * 24 * time.Hour,
		MaxFacts: 10, MinFactConfidence: 0.1,
		MaxProcedures: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Episodes().Count() > 20 {
		t.Errorf("episodes = %d after prune (removed %d)", s.Episodes().Count(), rep.Episodes)
	}
	if s.Semantic().Count() > 10 {
		t.Errorf("facts = %d after prune", s.Semantic().Count())
	}
	if s.Procedural().Count() > 15 {
		t.Errorf("procedures = %d after prune", s.Procedural().Count())
	}

	// The JSONL log must actually shrink, not just the index.
	data, err := os.ReadFile(filepath.Join(proj, ".slmcode", "memory", "episodes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines > 20 {
		t.Errorf("log still has %d lines after prune", lines)
	}
}

func TestReadOnlyStoreWritesNothing(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	s, err := OpenWith(proj, user, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEpisode(ep("q", nil, true)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".slmcode", "memory")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("read-only store created %v", err)
	}
}
