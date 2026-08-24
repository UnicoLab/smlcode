package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// graphFixture points the CLI at a throwaway project and pins color off so the
// assertions below compare text, not escape sequences.
func graphFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".slmcode"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	oldRoot, oldColor := flagRoot, cli.ColorEnabled()
	flagRoot = root
	cli.SetColorMode(cli.ColorNever)
	t.Cleanup(func() {
		flagRoot = oldRoot
		if oldColor {
			cli.SetColorMode(cli.ColorAlways)
		} else {
			cli.SetColorMode(cli.ColorNever)
		}
	})
	return root
}

// runGraph executes `slmcode graph <args…>` and returns what it printed.
// The subcommands write to os.Stdout (like every other command here), so the
// capture has to happen at the file descriptor the package actually uses.
func runGraph(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := graphCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	old := os.Stdout
	os.Stdout = w
	runErr := cmd.Execute()
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), runErr
}

// seedGraph writes edges straight into the store, which is what makes the read
// commands testable without running a whole turn through the harness.
func seedGraph(t *testing.T, root string, edges ...graph.Edge) {
	t.Helper()
	s, err := graph.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(edges...); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// seedKnownFile lays down the chain the `graph file` command exists to walk:
// episode -touched-> file, episode -produced-> failure, failure -resolved_by-> rule.
func seedKnownFile(t *testing.T, root string) {
	t.Helper()
	seedGraph(t, root,
		graph.Edge{From: graph.EpisodeNode("ep_seed01"), To: graph.FileNode("pkg/x.go"), Type: graph.Touched},
		graph.Edge{From: graph.EpisodeNode("ep_seed01"), To: graph.FailureNode("fp_dead"), Type: graph.Produced, Note: "edit_apply"},
		graph.Edge{From: graph.FailureNode("fp_dead"), To: graph.RuleNode("rule_ab12"), Type: graph.ResolvedBy},
		graph.Edge{From: graph.EpisodeNode("ep_seed01"), To: graph.FailureNode("fp_open"), Type: graph.Produced},
	)
}

func TestGraphCommandSurfaceIsComplete(t *testing.T) {
	want := map[string]bool{
		"stats": false, "file": false, "neighbors": false,
		"walk": false, "backfill": false, "prune": false, "forget": false,
	}
	for _, sub := range graphCmd().Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; !ok {
			t.Fatalf("unexpected subcommand %q", name)
		}
		want[name] = true
		// Every command in this CLI is scriptable; a subcommand without --json
		// is a hole in that contract.
		if sub.Flags().Lookup("json") == nil {
			t.Fatalf("graph %s has no --json flag", name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("graph %s is not registered", name)
		}
	}
	// Destruction needs the same guard `slmcode evolve reset` uses.
	forget, _, err := graphCmd().Find([]string{"forget"})
	if err != nil {
		t.Fatal(err)
	}
	if forget.Flags().Lookup("yes") == nil {
		t.Fatal("graph forget has no --yes confirmation flag")
	}
}

func TestGraphIsRegisteredOnTheRootCommand(t *testing.T) {
	// The store is only "inspectable" if the command is reachable. The root
	// command is assembled inline in main(), so the registration is checked at
	// the source: a store with no way to reach its CLI is the failure mode this
	// whole file exists to prevent.
	src, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "graphCmd()") {
		t.Fatal("graphCmd() is not registered in root.go")
	}
}

func TestGraphNodeArgReadsBarePathAsFile(t *testing.T) {
	cases := map[string]string{
		"pkg/loop/runner.go":   "file:pkg/loop/runner.go",
		"./pkg/loop/runner.go": "file:pkg/loop/runner.go",
		"file:pkg/x.go":        "file:pkg/x.go",
		"episode:ep_1a2b":      "episode:ep_1a2b",
		"failure:fp_9c":        "failure:fp_9c",
		// Not a node kind, so it is a path — never a node of an invented kind.
		"weird:thing": "file:weird:thing",
		"":            "",
	}
	for in, want := range cases {
		if got := graphNodeArg(in); got != want {
			t.Fatalf("graphNodeArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGraphDirectionAndTypeFlagsRejectTypos(t *testing.T) {
	if _, err := graphDirection("sideways"); err == nil {
		t.Fatal("--dir sideways was accepted")
	} else if exitCodeFor(err) != 2 {
		t.Fatalf("bad --dir exit code = %d, want 2", exitCodeFor(err))
	}
	for _, ok := range []string{"in", "out", "either", "both", ""} {
		if _, err := graphDirection(ok); err != nil {
			t.Fatalf("--dir %q rejected: %v", ok, err)
		}
	}
	// A mistyped filter returns nothing, which looks exactly like an empty
	// graph. Failing loudly is the only way the user can tell the difference.
	if err := graphCheckTypes([]string{"touched", "toched"}); err == nil {
		t.Fatal("unknown edge type was accepted")
	}
	if err := graphCheckTypes(graph.EdgeTypes()); err != nil {
		t.Fatalf("the package's own vocabulary was rejected: %v", err)
	}
}

func TestGraphStatsOnEmptyStoreExplainsHowToFillIt(t *testing.T) {
	graphFixture(t)
	out, err := runGraph(t, "stats")
	if err != nil {
		t.Fatalf("an empty graph is not an error: %v", err)
	}
	for _, want := range []string{"no edges yet", "slmcode graph backfill"} {
		if !strings.Contains(out, want) {
			t.Fatalf("empty-state output missing %q:\n%s", want, out)
		}
	}
}

func TestGraphReadPathsNeverCreateTheStore(t *testing.T) {
	root := graphFixture(t)
	for _, args := range [][]string{
		{"stats"},
		{"file", "pkg/x.go"},
		{"neighbors", "file:pkg/x.go"},
		{"walk", "pkg/x.go"},
	} {
		if _, err := runGraph(t, args...); err != nil {
			t.Fatalf("graph %v: %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".slmcode", "graph")); !os.IsNotExist(err) {
		t.Fatalf("an inspection created the store it was inspecting (stat err = %v)", err)
	}
}

func TestGraphFileJoinsEpisodesFailuresAndRules(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)

	out, err := runGraph(t, "file", "pkg/x.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pkg/x.go", "ep_seed01", "fp_dead", "fixed by rule_ab12"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// A class nothing has ever fixed is the finding worth surfacing.
	if !strings.Contains(out, "fp_open") || !strings.Contains(out, "no rule has resolved this class") {
		t.Fatalf("unfixed failure class not called out:\n%s", out)
	}
}

func TestGraphFileJSONCarriesTheJoin(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)

	out, err := runGraph(t, "file", "pkg/x.go", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		File     string              `json:"file"`
		Node     string              `json:"node"`
		Episodes []string            `json:"episodes"`
		Failures []string            `json:"failures"`
		Rules    []string            `json:"rules"`
		FixedBy  map[string][]string `json:"fixed_by"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not parseable: %v\n%s", err, out)
	}
	if got.File != "pkg/x.go" || got.Node != "file:pkg/x.go" {
		t.Fatalf("file/node = %q/%q", got.File, got.Node)
	}
	if len(got.Episodes) != 1 || got.Episodes[0] != "ep_seed01" {
		t.Fatalf("episodes = %v", got.Episodes)
	}
	if len(got.Failures) != 2 {
		t.Fatalf("failures = %v, want both classes", got.Failures)
	}
	if len(got.FixedBy["fp_dead"]) != 1 || got.FixedBy["fp_dead"][0] != "rule_ab12" {
		t.Fatalf("fixed_by = %v", got.FixedBy)
	}
	if _, unfixed := got.FixedBy["fp_open"]; unfixed {
		t.Fatalf("a class no rule resolved must not appear in fixed_by: %v", got.FixedBy)
	}
	// The ids are bare so they can be handed to `slmcode evolve rules`.
	if len(got.Rules) != 1 || strings.Contains(got.Rules[0], ":") {
		t.Fatalf("rules = %v, want bare ids", got.Rules)
	}
}

func TestGraphFileWithNothingRecordedIsNotAnError(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)

	out, err := runGraph(t, "file", "pkg/never/touched.go")
	if err != nil {
		t.Fatalf("an unknown file is not an error: %v", err)
	}
	if !strings.Contains(out, "nothing recorded for this file") {
		t.Fatalf("missing empty-file note:\n%s", out)
	}
}

func TestGraphFileRejectsAnArgumentThatIsNotAPath(t *testing.T) {
	graphFixture(t)
	for _, arg := range []string{"", ".", "./"} {
		if _, err := runGraph(t, "file", arg); err == nil {
			t.Fatalf("graph file %q was accepted", arg)
		} else if exitCodeFor(err) != 2 {
			t.Fatalf("graph file %q exit code = %d, want 2", arg, exitCodeFor(err))
		}
	}
}

func TestGraphNeighborsHonorsDirectionAndTypeFilters(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)

	// Every edge on a file node is incoming, which is why --dir defaults to
	// "either" rather than the package's outgoing zero value.
	out, err := runGraph(t, "neighbors", "pkg/x.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "episode:ep_seed01") || !strings.Contains(out, "touched") {
		t.Fatalf("default direction found nothing on a file node:\n%s", out)
	}
	if out, err := runGraph(t, "neighbors", "pkg/x.go", "--dir", "out"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "no edges on this node") {
		t.Fatalf("--dir out on a file node should be empty:\n%s", out)
	}

	out, err = runGraph(t, "neighbors", "episode:ep_seed01", "--type", "produced", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Direction string   `json:"direction"`
		Neighbors []string `json:"neighbors"`
		Edges     []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not parseable: %v\n%s", err, out)
	}
	if got.Direction != "either" {
		t.Fatalf("direction = %q", got.Direction)
	}
	if len(got.Neighbors) != 2 {
		t.Fatalf("neighbors = %v, want the two failure nodes only", got.Neighbors)
	}
	for _, e := range got.Edges {
		if e.Type != graph.Produced {
			t.Fatalf("--type produced leaked a %q edge", e.Type)
		}
	}
}

func TestGraphWalkFollowsTheChainAndClampsDepth(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)

	out, err := runGraph(t, "walk", "pkg/x.go", "--json", "--depth", "99")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Depth       int      `json:"depth"`
		DepthCapped bool     `json:"depth_capped"`
		MaxDepth    int      `json:"max_depth"`
		Reached     []string `json:"reached"`
		Paths       []struct {
			Nodes []string `json:"nodes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not parseable: %v\n%s", err, out)
	}
	if !got.DepthCapped || got.Depth != graph.MaxWalkDepth || got.MaxDepth != graph.MaxWalkDepth {
		t.Fatalf("depth was not clamped to the package ceiling: %+v", got)
	}
	// file <- episode -> failure -> rule, three hops, all four nodes but the origin.
	if len(got.Reached) != 4 {
		t.Fatalf("reached = %v", got.Reached)
	}
	if len(got.Paths) == 0 {
		t.Fatal("no paths returned")
	}

	text, err := runGraph(t, "walk", "pkg/x.go", "--depth", "99")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "clamped to the package ceiling") {
		t.Fatalf("the clamp was silent:\n%s", text)
	}
	// --dir either follows edges backwards, so the arrow has to say so: the
	// store holds episode -touched-> file, never the reverse.
	if !strings.Contains(text, "file:pkg/x.go <-touched- episode:ep_seed01") {
		t.Fatalf("path arrows do not reflect edge direction:\n%s", text)
	}
	if !strings.Contains(text, "-produced-> failure:fp_dead -resolved_by-> rule:rule_ab12") {
		t.Fatalf("forward hops lost their direction:\n%s", text)
	}
	// A type filter that cannot leave the origin still exits clean.
	if out, err := runGraph(t, "walk", "pkg/x.go", "--type", "depends_on"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "nothing reachable") {
		t.Fatalf("missing empty-walk note:\n%s", out)
	}
}

func TestGraphBackfillIsIdempotent(t *testing.T) {
	root := graphFixture(t)
	mem, err := memory.OpenWith(root, root, memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Episodes().Append(memory.Episode{
		ID:           "ep_back01",
		RunID:        "run-1",
		Query:        "fix the parser",
		FilesChanged: []string{"pkg/x.go"},
		Failures: []memory.FailureNote{{
			Fingerprint: "fp_dead",
			Class:       "edit_apply",
			Message:     "old_str not found",
			ResolvedBy:  "rule:rule_ab12",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}

	first, err := runGraph(t, "backfill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "materialized") {
		t.Fatalf("first backfill materialized nothing:\n%s", first)
	}
	// Edges are content-addressed, so a second pass must be a no-op — this is
	// what lets a run backfill after every turn without growing the log.
	second, err := runGraph(t, "backfill", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Added int `json:"added"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(second), &got); err != nil {
		t.Fatalf("--json is not parseable: %v\n%s", err, second)
	}
	if got.Added != 0 {
		t.Fatalf("re-running backfill added %d edge(s)", got.Added)
	}
	if got.Total < 4 {
		t.Fatalf("total = %d, want the run/touched/produced/resolved_by chain", got.Total)
	}

	stats, err := runGraph(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{graph.Touched, graph.Produced, graph.ResolvedBy, graph.ParentOf} {
		if !strings.Contains(stats, want) {
			t.Fatalf("stats missing %q:\n%s", want, stats)
		}
	}
}

func TestGraphPruneDropsAgedEdgesAndReportsTheCount(t *testing.T) {
	root := graphFixture(t)
	old := time.Now().Add(-400 * 24 * time.Hour)
	seedGraph(t, root,
		graph.Edge{From: graph.EpisodeNode("ep_old"), To: graph.FileNode("pkg/x.go"), Type: graph.Touched, At: old},
		graph.Edge{From: graph.EpisodeNode("ep_new"), To: graph.FileNode("pkg/x.go"), Type: graph.Touched},
	)

	out, err := runGraph(t, "prune", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Before    int `json:"before"`
		Removed   int `json:"removed"`
		Remaining int `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not parseable: %v\n%s", err, out)
	}
	if got.Before != 2 || got.Removed != 1 || got.Remaining != 1 {
		t.Fatalf("prune = %+v, want 2 → 1 dropped → 1 left", got)
	}
	// Nothing left to drop: the second pass says so instead of claiming work.
	again, err := runGraph(t, "prune")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "nothing to prune") {
		t.Fatalf("missing no-op note:\n%s", again)
	}
}

func TestGraphForgetDeletesTheStoreAndRequiresConfirmation(t *testing.T) {
	root := graphFixture(t)
	seedKnownFile(t, root)
	dir := filepath.Join(root, ".slmcode", "graph")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture did not create the store: %v", err)
	}

	// --json without --yes must refuse: a scripted invocation has no prompt.
	if _, err := runGraph(t, "forget", "--json"); err == nil {
		t.Fatal("graph forget --json ran without --yes")
	} else if exitCodeFor(err) != 2 {
		t.Fatalf("exit code = %d, want 2", exitCodeFor(err))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused forget still deleted the store: %v", err)
	}

	out, err := runGraph(t, "forget", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "slmcode graph backfill") {
		t.Fatalf("forget did not say how to get the edges back:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("store survived forget (stat err = %v)", err)
	}
	// And the store is gone, not broken: reading it is still clean.
	if _, err := runGraph(t, "stats"); err != nil {
		t.Fatalf("stats after forget: %v", err)
	}
}
