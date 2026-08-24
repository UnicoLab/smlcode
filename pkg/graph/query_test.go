package graph

import (
	"reflect"
	"testing"
)

// knowledgeFixture builds the shape KnownAboutFile exists to read:
//
//	ep_1 touched client.go and http.go, produced fp_edit (fixed by rule_reread
//	     and rule_shrink) and fp_json (fixed by rule_repair)
//	ep_2 touched client.go, produced fp_edit again
//	ep_3 touched other.go only
//	     …plus a decoy edge of the right type between the wrong kinds of node.
func knowledgeFixture(t *testing.T) *Store {
	t.Helper()
	s, _ := testStore(t)
	client, other := FileNode("pkg/http/client.go"), FileNode("pkg/other.go")
	mustAdd(t, s,
		Edge{From: EpisodeNode("ep_1"), To: client, Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_1"), To: FileNode("pkg/http/http.go"), Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_2"), To: client, Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_3"), To: other, Type: Touched, At: testNow},

		Edge{From: EpisodeNode("ep_1"), To: FailureNode("fp_edit"), Type: Produced, At: testNow},
		Edge{From: EpisodeNode("ep_1"), To: FailureNode("fp_json"), Type: Produced, At: testNow},
		Edge{From: EpisodeNode("ep_2"), To: FailureNode("fp_edit"), Type: Produced, At: testNow},
		Edge{From: EpisodeNode("ep_3"), To: FailureNode("fp_timeout"), Type: Produced, At: testNow},

		Edge{From: FailureNode("fp_edit"), To: RuleNode("rule_reread"), Type: ResolvedBy, At: testNow},
		Edge{From: FailureNode("fp_edit"), To: RuleNode("rule_shrink"), Type: ResolvedBy, At: testNow},
		Edge{From: FailureNode("fp_json"), To: RuleNode("rule_repair"), Type: ResolvedBy, At: testNow},
		// fp_timeout was never fixed by a rule.

		// Decoys: the right edge types between the wrong kinds of node. A
		// traversal that trusts the type alone would report a commit as an
		// episode and an artifact as a failure.
		Edge{From: CommitNode("deadbeef"), To: client, Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_1"), To: ArtifactNode("dist/bin"), Type: Produced, At: testNow},
		Edge{From: FailureNode("fp_edit"), To: EpisodeNode("ep_9"), Type: ResolvedBy, At: testNow},
	)
	return s
}

func TestKnownAboutFile(t *testing.T) {
	s := knowledgeFixture(t)

	tests := []struct {
		name         string
		file         string
		wantEpisodes []string
		wantFailures []string
		wantRules    []string
	}{
		{
			name:         "the file everything happened to",
			file:         "pkg/http/client.go",
			wantEpisodes: []string{"ep_1", "ep_2"},
			wantFailures: []string{"fp_edit", "fp_json"},
			wantRules:    []string{"rule_repair", "rule_reread", "rule_shrink"},
		},
		{
			name:         "canonicalized path finds the same node",
			file:         "./pkg/http/client.go",
			wantEpisodes: []string{"ep_1", "ep_2"},
			wantFailures: []string{"fp_edit", "fp_json"},
			wantRules:    []string{"rule_repair", "rule_reread", "rule_shrink"},
		},
		{
			name:         "a file with an unfixed failure",
			file:         "pkg/other.go",
			wantEpisodes: []string{"ep_3"},
			wantFailures: []string{"fp_timeout"},
			wantRules:    nil,
		},
		{
			name:         "a file nothing has touched",
			file:         "pkg/untouched.go",
			wantEpisodes: nil,
			wantFailures: nil,
			wantRules:    nil,
		},
		{
			name:         "a blank path",
			file:         "  ",
			wantEpisodes: nil,
			wantFailures: nil,
			wantRules:    nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := KnownAboutFile(s, tc.file)
			if !reflect.DeepEqual(got.Episodes, tc.wantEpisodes) {
				t.Errorf("Episodes = %v, want %v", got.Episodes, tc.wantEpisodes)
			}
			if !reflect.DeepEqual(got.Failures, tc.wantFailures) {
				t.Errorf("Failures = %v, want %v", got.Failures, tc.wantFailures)
			}
			if !reflect.DeepEqual(got.Rules, tc.wantRules) {
				t.Errorf("Rules = %v, want %v", got.Rules, tc.wantRules)
			}
			if got.Empty() != (len(tc.wantEpisodes) == 0) {
				t.Errorf("Empty() = %v for %+v", got.Empty(), got)
			}
		})
	}
}

func TestKnownAboutFileMapsFailuresToTheRulesThatFixedThem(t *testing.T) {
	got := KnownAboutFile(knowledgeFixture(t), "pkg/http/client.go")
	want := map[string][]string{
		"fp_edit": {"rule_reread", "rule_shrink"},
		"fp_json": {"rule_repair"},
	}
	if !reflect.DeepEqual(got.FixedBy, want) {
		t.Errorf("FixedBy = %v, want %v", got.FixedBy, want)
	}
	if got.File != "pkg/http/client.go" {
		t.Errorf("File = %q, want the bare path", got.File)
	}
}

func TestKnownAboutFileIgnoresRightTypeWrongKindEdges(t *testing.T) {
	got := KnownAboutFile(knowledgeFixture(t), "pkg/http/client.go")
	for _, id := range got.Episodes {
		if id == "deadbeef" {
			t.Error("a commit was reported as an episode")
		}
	}
	for _, id := range got.Failures {
		if id == "dist/bin" {
			t.Error("an artifact was reported as a failure")
		}
	}
	for _, id := range got.Rules {
		if id == "ep_9" {
			t.Error("an episode was reported as a rule")
		}
	}
}

func TestFailureClassesForFiles(t *testing.T) {
	s := knowledgeFixture(t)

	tests := []struct {
		name  string
		files []string
		want  map[string]int
	}{
		{
			name:  "one file, one failure seen twice",
			files: []string{"pkg/http/client.go"},
			want:  map[string]int{"fp_edit": 2, "fp_json": 1},
		},
		{
			name:  "several files",
			files: []string{"pkg/http/client.go", "pkg/other.go"},
			want:  map[string]int{"fp_edit": 2, "fp_json": 1, "fp_timeout": 1},
		},
		{
			name: "an episode touching two of the listed files still counts once",
			// ep_1 touched both client.go and http.go and produced fp_edit and
			// fp_json — the question is how often it went wrong, not how many
			// files were open at the time.
			files: []string{"pkg/http/client.go", "pkg/http/http.go"},
			want:  map[string]int{"fp_edit": 2, "fp_json": 1},
		},
		{
			name:  "duplicate and blank inputs",
			files: []string{"pkg/other.go", "./pkg/other.go", "  ", ""},
			want:  map[string]int{"fp_timeout": 1},
		},
		{
			name:  "an unknown file",
			files: []string{"pkg/nope.go"},
			want:  map[string]int{},
		},
		{
			name:  "no files",
			files: nil,
			want:  map[string]int{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FailureClassesForFiles(s, tc.files); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("= %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQueryHelpersOnARealBackfill(t *testing.T) {
	// End to end: records on disk → Backfill → the question a prompt asks.
	root := t.TempDir()
	ids := seedRecords(t, root)
	if _, err := Backfill(root); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got := KnownAboutFile(s, "pkg/http/client.go")
	if want := []string{ids.episode, ids.otherEp}; !reflect.DeepEqual(got.Episodes, want) {
		t.Errorf("Episodes = %v, want %v", got.Episodes, want)
	}
	if want := []string{ids.fingerprint, "fp_llmfixed"}; !reflect.DeepEqual(got.Failures, want) {
		t.Errorf("Failures = %v, want %v", got.Failures, want)
	}
	if want := []string{"rule_reread"}; !reflect.DeepEqual(got.Rules, want) {
		t.Errorf("Rules = %v, want %v", got.Rules, want)
	}
	classes := FailureClassesForFiles(s, []string{"pkg/http/client.go"})
	if want := (map[string]int{"fp_llmfixed": 1, ids.fingerprint: 1}); !reflect.DeepEqual(classes, want) {
		t.Errorf("FailureClassesForFiles = %v, want %v", classes, want)
	}
}
