package graph

import (
	"strconv"
	"testing"
	"time"
)

func pruneStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := OpenWith(root, Options{Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, root
}

func TestPrunePolicyDefaults(t *testing.T) {
	tests := []struct {
		name    string
		in      PrunePolicy
		wantMax int
		wantAge time.Duration
	}{
		{"zero uses the defaults", PrunePolicy{}, DefaultMaxEdges, DefaultPruneMaxAge},
		{"explicit wins", PrunePolicy{MaxEdges: 5, MaxAge: time.Hour}, 5, time.Hour},
		{"negative means no limit", PrunePolicy{MaxEdges: -1, MaxAge: -1}, -1, -1},
		{"one axis only", PrunePolicy{MaxEdges: 7}, 7, DefaultPruneMaxAge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.withDefaults()
			if got.MaxEdges != tc.wantMax || got.MaxAge != tc.wantAge {
				t.Errorf("= %+v, want MaxEdges %d MaxAge %v", got, tc.wantMax, tc.wantAge)
			}
		})
	}
}

func TestPruneDropsOldEdgesAndShrinksTheFile(t *testing.T) {
	s, _ := pruneStore(t)
	old := testNow.Add(-200 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		mustAdd(t, s, Edge{
			From: EpisodeNode("old_" + strconv.Itoa(i)),
			To:   FileNode("a.go"),
			Type: Touched,
			At:   old,
		})
	}
	for i := 0; i < 3; i++ {
		mustAdd(t, s, Edge{
			From: EpisodeNode("new_" + strconv.Itoa(i)),
			To:   FileNode("a.go"),
			Type: Touched,
			At:   testNow,
		})
	}
	before := fileSize(t, s.logPath())

	removed, err := s.Prune(PrunePolicy{MaxAge: 180 * 24 * time.Hour, MaxEdges: -1})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 5 {
		t.Errorf("removed = %d, want 5", removed)
	}
	if got := s.Count(); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
	after := fileSize(t, s.logPath())
	if after >= before {
		t.Errorf("log = %d bytes after prune, was %d — the file did not shrink", after, before)
	}
	// The survivors are the recent ones, and the log is the proof.
	for i := 0; i < 3; i++ {
		if !s.Has(EpisodeNode("new_"+strconv.Itoa(i)), FileNode("a.go"), Touched) {
			t.Errorf("new_%d was pruned", i)
		}
	}
	if s.Has(EpisodeNode("old_0"), FileNode("a.go"), Touched) {
		t.Error("old_0 survived a prune that should have dropped it")
	}
}

func TestPruneRespectsTheCap(t *testing.T) {
	s, _ := pruneStore(t)
	const total = 12
	for i := 0; i < total; i++ {
		mustAdd(t, s, Edge{
			From: EpisodeNode("ep_" + strconv.Itoa(i)),
			To:   FileNode("a.go"),
			Type: Touched,
			At:   testNow,
		})
	}
	removed, err := s.Prune(PrunePolicy{MaxEdges: 4, MaxAge: -1})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != total-4 {
		t.Errorf("removed = %d, want %d", removed, total-4)
	}
	if got := s.Count(); got != 4 {
		t.Fatalf("Count = %d, want 4", got)
	}
	// Keeping the tail means the newest survive.
	for i := total - 4; i < total; i++ {
		if !s.Has(EpisodeNode("ep_"+strconv.Itoa(i)), FileNode("a.go"), Touched) {
			t.Errorf("ep_%d should have survived", i)
		}
	}
	for i := 0; i < total-4; i++ {
		if s.Has(EpisodeNode("ep_"+strconv.Itoa(i)), FileNode("a.go"), Touched) {
			t.Errorf("ep_%d should have been pruned", i)
		}
	}
}

func TestPruneSurvivesAReopen(t *testing.T) {
	s, root := pruneStore(t)
	for i := 0; i < 10; i++ {
		mustAdd(t, s, Edge{
			From: EpisodeNode("ep_" + strconv.Itoa(i)),
			To:   FileNode("a.go"),
			Type: Touched,
			At:   testNow,
		})
	}
	if _, err := s.Prune(PrunePolicy{MaxEdges: 3, MaxAge: -1}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The index the prune wrote must agree with the log it rewrote — including
	// the offsets, or the next open would report a stale index.
	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := reopened.Count(); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
	if hasWarning(reopened.Warnings(), "stale") {
		t.Errorf("Warnings = %v — prune left the offsets inconsistent", reopened.Warnings())
	}
	if got := reopened.Neighbors(FileNode("a.go"), Incoming); len(got) != 3 {
		t.Errorf("Neighbors = %v, want 3 — the adjacency did not survive", got)
	}
}

func TestPruneIsANoOpWhenNothingExceedsThePolicy(t *testing.T) {
	s, _ := pruneStore(t)
	mustAdd(t, s, Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow})
	before := fileSize(t, s.logPath())

	removed, err := s.Prune(DefaultPrunePolicy())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if got := fileSize(t, s.logPath()); got != before {
		t.Errorf("log = %d bytes, want %d — a no-op prune rewrote the file", got, before)
	}
}
