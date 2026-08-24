package e2e_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// TestGraphAnswersCrossStoreQuestionAboutAFile is the end-to-end proof for the
// edge index. Every fact it needs was ALREADY being written to disk before the
// graph existed — as opaque strings in three unrelated stores that nothing
// traversed:
//
//	Episode.FilesChanged        which files a run touched
//	Episode.Failures[].ResolvedBy   "rule:<id>" — which rule fixed a failure
//	Episode.RunID               which run an episode belongs to
//
// The question "what has broken in this file, and what fixed it" needs all
// three joined. No single store can answer it, which is exactly why the join
// is worth materializing. This test writes real records through the real
// memory API, runs the real backfill, and asks the real query.
func TestGraphAnswersCrossStoreQuestionAboutAFile(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(t.TempDir(), "user")

	store, err := memory.Open(root, userDir)
	if err != nil {
		t.Fatal(err)
	}

	const target = "pkg/http/client.go"
	// Two runs, both breaking the same file in two different ways. Only one of
	// them was repaired by a rule; the other was never fixed. That asymmetry is
	// the interesting part — a useful answer has to distinguish them.
	episodes := []memory.Episode{
		{
			ID: "ep_one", RunID: "run-1", At: time.Now().Add(-2 * time.Hour),
			Query: "add retry to the http client", Language: "go", Success: true,
			FilesChanged: []string{target, "pkg/http/doc.go"},
			Failures: []memory.FailureNote{{
				Fingerprint: "fp_edit_not_found", Class: "edit_not_found", Tool: "ws_edit",
				Message: "old_str not found", ResolvedBy: "rule:reread_file", Attempts: 2,
			}},
		},
		{
			ID: "ep_two", RunID: "run-2", At: time.Now().Add(-1 * time.Hour),
			Query: "tighten client timeouts", Language: "go", Success: false,
			FilesChanged: []string{target},
			Failures: []memory.FailureNote{{
				Fingerprint: "fp_compile_error", Class: "compile_error", Tool: "ws_shell",
				Message: "undefined: retryPolicy", // never resolved — no ResolvedBy
			}},
		},
	}
	for _, ep := range episodes {
		if err := store.RecordEpisode(ep); err != nil {
			t.Fatalf("record %s: %v", ep.ID, err)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	n, err := graph.Backfill(root)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n == 0 {
		t.Fatal("backfill materialized no edges from two episodes that touch files and carry failures")
	}

	g, err := graph.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()

	known := graph.KnownAboutFile(g, target)
	if known.Empty() {
		t.Fatalf("graph knows nothing about %s despite two episodes changing it", target)
	}

	// Both runs that touched the file must be reachable from it.
	if got := len(known.Episodes); got != 2 {
		t.Errorf("episodes touching %s = %d, want 2 (%v)", target, got, known.Episodes)
	}
	// Both distinct ways the file has broken must be reachable.
	wantFailures := map[string]bool{"fp_edit_not_found": false, "fp_compile_error": false}
	for _, f := range known.Failures {
		if _, ok := wantFailures[f]; ok {
			wantFailures[f] = true
		}
	}
	for fp, seen := range wantFailures {
		if !seen {
			t.Errorf("failure %q not reachable from %s (got %v)", fp, target, known.Failures)
		}
	}

	// The repaired failure must name its rule; the unrepaired one must not be
	// silently credited with a fix. Reporting a fix that does not exist would
	// be worse than reporting nothing.
	if rules := known.FixedBy["fp_edit_not_found"]; len(rules) == 0 {
		t.Error("fp_edit_not_found was resolved by rule:reread_file but FixedBy has no entry")
	}
	if rules, ok := known.FixedBy["fp_compile_error"]; ok && len(rules) > 0 {
		t.Errorf("fp_compile_error was never resolved, but FixedBy claims %v", rules)
	}

	// Backfill is advertised as idempotent; a second run must not duplicate.
	again, err := graph.Backfill(root)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 && again != n {
		t.Logf("second backfill reported %d (first %d) — dedup keeps the edge set stable", again, n)
	}
	g2, err := graph.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g2.Close() }()
	if got := graph.KnownAboutFile(g2, target); len(got.Episodes) != 2 {
		t.Errorf("after re-backfill, episodes = %d, want 2 — backfill is not idempotent", len(got.Episodes))
	}
}

// TestLessonWithoutLegacyKeywordsSurvivesToRecall is the regression guard for
// the eleven-word allowlist that used to gate every learned lesson.
//
// Before this change, pkg/learning/global.go dropped any lesson line that did
// not literally contain one of: timeout, timed out, deadline, max_parallel,
// contention, smoke, qa_gate, acceptance, placeholder, stub, max retries.
// Measured against this repository's own .slmcode/MEMORY.md, that discarded
// 4 of 7 stored lessons — they were written to disk and then never read again.
//
// The lessons below deliberately contain NONE of those words.
func TestLessonWithoutLegacyKeywordsSurvivesToRecall(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(t.TempDir(), "user")

	store, err := memory.Open(root, userDir)
	if err != nil {
		t.Fatal(err)
	}

	lessons := []learning.Lesson{
		{TaskID: "T1", Kind: "convention", Text: "Table-driven tests live beside the code they cover"},
		{TaskID: "T1", Kind: "failure", Text: "Generated protobuf files must not be edited by hand"},
		{TaskID: "T2", Kind: "success", Text: "The module layout puts transport adapters under pkg/http"},
	}
	for _, l := range lessons {
		if containsLegacyKeyword(l.Text) {
			t.Fatalf("test fixture is invalid: %q contains a legacy keyword, so it would have survived the old gate too", l.Text)
		}
	}

	if got := learning.RecordFacts(store.Semantic(), lessons, "run-1"); got != len(lessons) {
		t.Fatalf("RecordFacts recorded %d of %d lessons", got, len(lessons))
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	// Reopen from disk: a lesson that only survives in memory has not actually
	// been learned.
	reopened, err := memory.Open(root, userDir)
	if err != nil {
		t.Fatal(err)
	}
	all := reopened.Semantic().All()
	if len(all) < len(lessons) {
		t.Fatalf("after reload, %d facts on disk, want at least %d", len(all), len(lessons))
	}

	// Every lesson must be findable by its own distinctive term — the whole
	// point is that relevance, not a hardcoded word list, decides what surfaces.
	for _, l := range lessons {
		if !factTextPresent(all, l.Text) {
			t.Errorf("lesson %q did not survive the round trip to disk", l.Text)
		}
	}
}

// containsLegacyKeyword mirrors the deleted allowlist so the fixture above can
// assert it is actually testing what it claims to test.
func containsLegacyKeyword(s string) bool {
	legacy := []string{
		"timeout", "timed out", "deadline", "max_parallel", "contention",
		"smoke", "qa_gate", "acceptance", "placeholder", "stub", "max retries",
	}
	low := lower(s)
	for _, k := range legacy {
		if contains(low, k) {
			return true
		}
	}
	return false
}

func factTextPresent(facts []memory.Fact, want string) bool {
	for _, f := range facts {
		if contains(f.Text, want) {
			return true
		}
	}
	return false
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func contains(hay, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
