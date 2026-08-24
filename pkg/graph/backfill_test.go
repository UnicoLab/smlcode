package graph

import (
	"os"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// seeded is the ids the fixture wrote, so the assertions can name them.
type seeded struct {
	episode     string
	otherEp     string
	run         string
	fingerprint string
	rule        string
	runRule     string
	fact        string
}

// seedRecords writes exactly the records a real run leaves behind, through the
// same APIs the harness uses. Backfill must find every edge they imply without
// this test telling it anything.
func seedRecords(t *testing.T, root string) seeded {
	t.Helper()
	got := seeded{
		episode:     "ep_test1",
		otherEp:     "ep_test2",
		run:         "run-1700000000",
		fingerprint: "fp_editnotfound",
	}

	mem, err := memory.Open(root, t.TempDir())
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	episodes := []memory.Episode{
		{
			ID:           got.episode,
			RunID:        got.run,
			At:           testNow,
			Query:        "fix the http client timeout",
			FilesChanged: []string{"pkg/http/client.go", "pkg/http/client_test.go"},
			Failures: []memory.FailureNote{
				{
					Fingerprint: got.fingerprint,
					Class:       "edit_not_found",
					Tool:        "ws_edit",
					Message:     "old_str not found",
					Resolution:  "re-read the file, then retried",
					ResolvedBy:  "rule:rule_reread",
				},
				{
					Fingerprint: "fp_llmfixed",
					Class:       "compile_error",
					Message:     "undefined: alpha",
					ResolvedBy:  "llm", // not a rule: must produce no resolved_by edge
				},
				{
					Fingerprint: "", // no fingerprint: nothing to point at
					Message:     "something went wrong",
				},
			},
			Success: true,
		},
		{
			ID:           got.otherEp,
			RunID:        got.run,
			At:           testNow,
			Query:        "add a retry",
			FilesChanged: []string{"pkg/http/client.go"},
			Success:      true,
		},
	}
	for _, e := range episodes {
		if err := mem.RecordEpisode(e); err != nil {
			t.Fatalf("RecordEpisode: %v", err)
		}
	}
	fact := mem.Semantic().Observe(memory.Fact{
		Kind:    memory.FactGotcha,
		Subject: got.fingerprint,
		Text:    "old_str not found → re-read the file and copy the anchor verbatim",
		Sources: []string{got.episode, "ep_pruned_away", "not-an-id"},
	})
	got.fact = fact.ID
	if err := mem.Flush(); err != nil {
		t.Fatalf("memory Flush: %v", err)
	}

	rules, err := evolve.OpenRulesWith(root, "", evolve.RulesOptions{NoSeed: true})
	if err != nil {
		t.Fatalf("OpenRulesWith: %v", err)
	}
	fromEpisode, ok := rules.Learn(
		evolve.Signal{Tool: "ws_edit", Message: "old_str not found in file", Language: "go"},
		evolve.Resolution{
			Repair:   evolve.Repair{Kind: evolve.RepairGuidance, Guidance: "re-read and copy the anchor verbatim"},
			Evidence: got.episode,
			Scope:    evolve.ScopeProject,
		},
	)
	if !ok {
		t.Fatal("Learn refused the episode-evidenced rule")
	}
	got.rule = fromEpisode.ID
	// pkg/evolve also writes a RUN id into Evidence, which must resolve to a
	// run node rather than being guessed at as an episode.
	fromRun, ok := rules.Learn(
		evolve.Signal{Tool: "ws_shell", Message: "command timed out after 30s", Language: "go"},
		evolve.Resolution{
			Repair:   evolve.Repair{Kind: evolve.RepairAction, Guidance: "split the task", Action: "split_task"},
			Evidence: got.run,
			Scope:    evolve.ScopeProject,
		},
	)
	if !ok {
		t.Fatal("Learn refused the run-evidenced rule")
	}
	got.runRule = fromRun.ID
	if err := rules.Save(); err != nil {
		t.Fatalf("rules Save: %v", err)
	}
	return got
}

func wantEdge(t *testing.T, s *Store, from, to, edgeType string) {
	t.Helper()
	if !s.Has(from, to, edgeType) {
		t.Errorf("missing edge %s -%s-> %s", from, edgeType, to)
	}
}

func TestBackfillMaterializesTheImpliedEdges(t *testing.T) {
	root := t.TempDir()
	ids := seedRecords(t, root)

	n, err := Backfill(root)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if n == 0 {
		t.Fatal("Backfill added nothing")
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	present := []struct {
		name           string
		from, to, kind string
	}{
		{
			"fact → the episode it was distilled from",
			FactNode(ids.fact), EpisodeNode(ids.episode), DerivedFrom,
		},
		{
			"fact → an episode that has since been pruned",
			FactNode(ids.fact), EpisodeNode("ep_pruned_away"), DerivedFrom,
		},
		{
			"rule → its episode evidence",
			RuleNode(ids.rule), EpisodeNode(ids.episode), DerivedFrom,
		},
		{
			"rule → its run evidence",
			RuleNode(ids.runRule), RunNode(ids.run), DerivedFrom,
		},
		{
			"run → episode",
			RunNode(ids.run), EpisodeNode(ids.episode), ParentOf,
		},
		{
			"episode → file",
			EpisodeNode(ids.episode), FileNode("pkg/http/client.go"), Touched,
		},
		{
			"episode → test file",
			EpisodeNode(ids.episode), FileNode("pkg/http/client_test.go"), Touched,
		},
		{
			"second episode → the same file",
			EpisodeNode(ids.otherEp), FileNode("pkg/http/client.go"), Touched,
		},
		{
			"episode → failure",
			EpisodeNode(ids.episode), FailureNode(ids.fingerprint), Produced,
		},
		{
			"failure → the rule that fixed it",
			FailureNode(ids.fingerprint), RuleNode("rule_reread"), ResolvedBy,
		},
		{
			"episode → an LLM-fixed failure is still produced",
			EpisodeNode(ids.episode), FailureNode("fp_llmfixed"), Produced,
		},
	}
	for _, tc := range present {
		t.Run(tc.name, func(t *testing.T) { wantEdge(t, s, tc.from, tc.to, tc.kind) })
	}

	absent := []struct {
		name           string
		from, to, kind string
	}{
		{
			"an LLM fix is not a rule",
			FailureNode("fp_llmfixed"), RuleNode("llm"), ResolvedBy,
		},
		{
			"a source that names no record invents no node",
			FactNode(ids.fact), EpisodeNode("not-an-id"), DerivedFrom,
		},
		{
			"nor a run node",
			FactNode(ids.fact), RunNode("not-an-id"), DerivedFrom,
		},
	}
	for _, tc := range absent {
		t.Run(tc.name, func(t *testing.T) {
			if s.Has(tc.from, tc.to, tc.kind) {
				t.Errorf("unexpected edge %s -%s-> %s", tc.from, tc.kind, tc.to)
			}
		})
	}

	// The failure with no fingerprint has nothing to point at.
	if got := len(s.Out(EpisodeNode(ids.episode), Produced)); got != 2 {
		t.Errorf("produced edges = %d, want 2 — the fingerprintless failure was materialized", got)
	}
	if got := s.Stats().Edges; got != n {
		t.Errorf("Stats().Edges = %d, want %d — Backfill's count disagrees with the store", got, n)
	}
}

func TestBackfillIsIdempotent(t *testing.T) {
	root := t.TempDir()
	seedRecords(t, root)

	first, err := Backfill(root)
	if err != nil {
		t.Fatalf("first Backfill: %v", err)
	}
	if first == 0 {
		t.Fatal("first Backfill added nothing to compare against")
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	statsAfterFirst := s.Stats()
	logAfterFirst, err := os.ReadFile(s.logPath())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for run := 2; run <= 4; run++ {
		added, err := Backfill(root)
		if err != nil {
			t.Fatalf("Backfill %d: %v", run, err)
		}
		if added != 0 {
			t.Errorf("Backfill %d added %d edge(s), want 0", run, added)
		}
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if got := reopened.Stats(); got.Edges != statsAfterFirst.Edges || got.Nodes != statsAfterFirst.Nodes {
		t.Errorf("Stats = %+v, want %+v", got, statsAfterFirst)
	}
	logNow, err := os.ReadFile(reopened.logPath())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(logNow) != string(logAfterFirst) {
		t.Errorf("the log changed across repeated backfills (%d → %d bytes)",
			len(logAfterFirst), len(logNow))
	}
}

func TestBackfillOnAnEmptyOrMissingProject(t *testing.T) {
	if n, err := Backfill(""); n != 0 || err != nil {
		t.Errorf("Backfill(\"\") = (%d, %v), want (0, nil)", n, err)
	}
	// A project the harness has never written anything for.
	empty := t.TempDir()
	n, err := Backfill(empty)
	if err != nil {
		t.Fatalf("Backfill on an empty project: %v", err)
	}
	if n != 0 {
		t.Errorf("Backfill = %d, want 0", n)
	}
}

func TestBackfillAfterTheGraphIsDeleted(t *testing.T) {
	root := t.TempDir()
	seedRecords(t, root)
	first, err := Backfill(root)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	// `rm -rf .slmcode/graph` is a supported operation.
	if err := Forget(root); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	again, err := Backfill(root)
	if err != nil {
		t.Fatalf("Backfill after Forget: %v", err)
	}
	if again != first {
		t.Errorf("rebuilt %d edges, want the original %d", again, first)
	}
}

func TestRecordNode(t *testing.T) {
	known := knownIDs{
		episodes: map[string]bool{"ep_known": true, "weird-id": true},
		runs:     map[string]bool{"run-known": true, "2026-run": true},
	}
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"blank", "  ", ""},
		{"already an episode node", "episode:ep_x", "episode:ep_x"},
		{"already a run node", "run:run-x", "run:run-x"},
		{"already a task node", "task:run-x/t1", "task:run-x/t1"},
		{"already an attempt node", "attempt:run-x/t1/2", "attempt:run-x/t1/2"},
		{"known episode", "ep_known", "episode:ep_known"},
		{"known run", "run-known", "run:run-known"},
		{"known but oddly shaped episode", "weird-id", "episode:weird-id"},
		{"known but oddly shaped run", "2026-run", "run:2026-run"},
		{"unknown but episode-shaped", "ep_pruned", "episode:ep_pruned"},
		{"unknown but run-shaped", "run-9", "run:run-9"},
		{"unknown and unshaped", "t-42", ""},
		{"a rule id is not evidence of an episode", "rule_ab12", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := recordNode(tc.ref, known); got != tc.want {
				t.Errorf("recordNode(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestRuleRef(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"namespaced", "rule:rule_ab12", "rule:rule_ab12"},
		{"bare rule id", "rule_ab12", "rule:rule_ab12"},
		{"padded", "  rule:rule_ab12  ", "rule:rule_ab12"},
		{"unresolved", "", ""},
		{"fixed by a model", "llm", ""},
		{"fixed by a retry", "retry", ""},
		{"fixed by a human", "human", ""},
		{"empty namespace", "rule:", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ruleRef(tc.in); got != tc.want {
				t.Errorf("ruleRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDeriveEdgesIsOrderedOldestFirst(t *testing.T) {
	root := t.TempDir()
	seedRecords(t, root)
	edges, err := deriveEdges(root)
	if err != nil {
		t.Fatalf("deriveEdges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("deriveEdges found nothing")
	}
	for i := 1; i < len(edges); i++ {
		if edges[i].At.Before(edges[i-1].At) {
			t.Fatalf("edge %d (%v) predates edge %d (%v) — the log is not a chronology",
				i, edges[i].At, i-1, edges[i-1].At)
		}
	}
	// Deriving twice from the same records yields the same slice.
	again, err := deriveEdges(root)
	if err != nil {
		t.Fatalf("deriveEdges: %v", err)
	}
	if len(again) != len(edges) {
		t.Fatalf("second derivation = %d edges, want %d", len(again), len(edges))
	}
	for i := range edges {
		if edges[i].ID() != again[i].ID() {
			t.Errorf("edge %d differs between derivations: %s vs %s",
				i, edges[i].ID(), again[i].ID())
		}
	}
}
