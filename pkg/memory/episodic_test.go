package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	proj := t.TempDir()
	user := t.TempDir()
	s, err := Open(proj, user)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, proj, user
}

func ep(query string, files []string, success bool) Episode {
	return Episode{Query: query, FilesChanged: files, Success: success, Language: "go"}
}

func TestEpisodeNormalizeBoundsEverything(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := Episode{Query: strings.Repeat("q", 5000)}
	for i := 0; i < 200; i++ {
		e.FilesChanged = append(e.FilesChanged, "f"+itoa(i)+".go")
		e.ToolsUsed = append(e.ToolsUsed, "t"+itoa(i))
		e.Plan = append(e.Plan, "step "+itoa(i))
		e.Tags = append(e.Tags, "tag"+itoa(i))
		e.Failures = append(e.Failures, FailureNote{Message: strings.Repeat("m", 900)})
		e.Gates = append(e.Gates, GateOutcome{Name: "g" + itoa(i)})
		e.Commands = append(e.Commands, Command{Cmd: "c" + itoa(i)})
	}
	e.Normalize(now)

	caps := []struct {
		name string
		got  int
		max  int
	}{
		{"query", len(e.Query), MaxEpisodeTextLen},
		{"files", len(e.FilesChanged), MaxEpisodeFiles},
		{"tools", len(e.ToolsUsed), MaxEpisodeTools},
		{"plan", len(e.Plan), MaxEpisodePlan},
		{"tags", len(e.Tags), MaxEpisodeTags},
		{"failures", len(e.Failures), MaxEpisodeFailures},
		{"gates", len(e.Gates), MaxEpisodeGates},
		{"commands", len(e.Commands), MaxEpisodeCommands},
	}
	for _, c := range caps {
		if c.got > c.max {
			t.Errorf("%s = %d, exceeds cap %d", c.name, c.got, c.max)
		}
	}
	if e.ID == "" || !strings.HasPrefix(e.ID, "ep_") {
		t.Errorf("ID = %q", e.ID)
	}
	if e.Verdict != VerdictPartial {
		t.Errorf("verdict = %q, want partial (files changed but not success)", e.Verdict)
	}
}

func TestEpisodeVerdicts(t *testing.T) {
	tests := []struct {
		name  string
		in    Episode
		want  string
		alive bool
	}{
		{"success", Episode{Success: true}, VerdictSuccess, true},
		{"partial", Episode{FilesChanged: []string{"a.go"}}, VerdictPartial, true},
		{"failure", Episode{}, VerdictFailure, true},
		{"explicit wins", Episode{Verdict: "custom"}, "custom", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.in
			e.Normalize(time.Now())
			if e.Verdict != tc.want {
				t.Errorf("verdict = %q, want %q", e.Verdict, tc.want)
			}
		})
	}
}

func TestEpisodesRoundTripAndReopen(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	s, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := s.RecordEpisode(ep("task "+itoa(i), []string{"pkg/a/f" + itoa(i) + ".go"}, i%2 == 0)); err != nil {
			t.Fatalf("RecordEpisode: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	logPath := filepath.Join(proj, ".slmcode", "memory", "episodes.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("episodes.jsonl missing: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 20 {
		t.Fatalf("log has %d lines, want 20", lines)
	}

	s2, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if got := s2.Episodes().Count(); got != 20 {
		t.Fatalf("reopened count = %d, want 20", got)
	}
	recent := s2.Episodes().Recent(3)
	if len(recent) != 3 || recent[0].Query != "task 19" {
		t.Fatalf("recent = %+v", recent)
	}
	if recent[0].FilesChanged[0] != "pkg/a/f19.go" {
		t.Fatalf("hydration lost fields: %+v", recent[0])
	}
}

func TestEpisodesSurvivesCorruptLog(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	dir := filepath.Join(proj, ".slmcode", "memory")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"id":"ep_ok1","at":"2026-01-01T00:00:00Z","query":"good one","success":true}`,
		`{"id":"ep_broken", "at": TRUNCATED`,
		``,
		`not json at all`,
		`{"id":"ep_ok2","at":"2026-01-02T00:00:00Z","query":"another good one","success":true}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "episodes.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(proj, user)
	if err != nil {
		t.Fatalf("Open must tolerate a corrupt log: %v", err)
	}
	defer func() { _ = s.Close() }()
	if got := s.Episodes().Count(); got != 2 {
		t.Fatalf("count = %d, want 2 usable records", got)
	}
	if len(s.Warnings()) == 0 {
		t.Error("corrupt records should be reported through Warnings")
	}
	if _, ok := s.Episodes().Get("ep_ok2"); !ok {
		t.Error("valid record after a corrupt one must still be readable")
	}
}

func TestEpisodesSurvivesStaleIndex(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	s, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = s.RecordEpisode(ep("task "+itoa(i), []string{"a.go"}, true))
	}
	_ = s.Close()

	// Simulate a hand-edited log: prepend a comment line so every offset shifts.
	logPath := filepath.Join(proj, ".slmcode", "memory", "episodes.jsonl")
	data, _ := os.ReadFile(logPath) //nolint:gosec
	if err := os.WriteFile(logPath, append([]byte("{\"id\":\"ep_manual\",\"query\":\"hand added\"}\n"), data...), 0o600); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if got := s2.Episodes().Count(); got != 6 {
		t.Fatalf("count = %d, want 6 after rebuild", got)
	}
	all := s2.Episodes().All()
	for _, e := range all {
		if e.ID == "" {
			t.Fatalf("hydration produced an empty record: %+v", e)
		}
	}
}

func TestEpisodesInMemoryWhenNoProjectDir(t *testing.T) {
	s, err := Open("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.RecordEpisode(ep("no disk", []string{"a.go"}, true)); err != nil {
		t.Fatalf("RecordEpisode: %v", err)
	}
	if got := s.Episodes().Count(); got != 1 {
		t.Fatalf("count = %d", got)
	}
	if s.Dir() != "" {
		t.Errorf("Dir = %q, want empty for an in-memory store", s.Dir())
	}
}

func TestEpisodesDuplicateIDIgnored(t *testing.T) {
	s, _, _ := testStore(t)
	e := ep("same", []string{"a.go"}, true)
	e.ID = "ep_fixed"
	_ = s.RecordEpisode(e)
	_ = s.RecordEpisode(e)
	if got := s.Episodes().Count(); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}
