package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, root
}

func mustAdd(t *testing.T, s *Store, edges ...Edge) {
	t.Helper()
	if err := s.Add(edges...); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// dirSnapshot records every file in dir with its exact bytes, for asserting
// that a read-only store changed nothing at all.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("readdir %s: %v", dir, err)
	}
	for _, en := range entries {
		data, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", en.Name(), err)
		}
		out[en.Name()] = string(data)
	}
	return out
}

func rawLine(t *testing.T, e Edge) string {
	t.Helper()
	e.Normalize(testNow)
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data) + "\n"
}

func TestAddDedupsAndDoesNotGrowTheLog(t *testing.T) {
	s, _ := testStore(t)
	logPath := s.logPath()

	e := Edge{From: EpisodeNode("ep_1"), To: FileNode("pkg/x.go"), Type: Touched, At: testNow}
	mustAdd(t, s, e)
	firstSize := fileSize(t, logPath)

	// The same edge, observed again later with more information.
	again := e
	again.At = testNow.Add(48 * time.Hour)
	again.Confidence = 0.75
	again.RunID = "run-9"
	again.Note = "seen again"
	mustAdd(t, s, again)

	if got := s.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1 — re-adding the same edge created a second one", got)
	}
	if got := fileSize(t, logPath); got != firstSize {
		t.Errorf("log = %d bytes, want %d — a duplicate was appended", got, firstSize)
	}
	stored, ok := s.Get(e.ID())
	if !ok {
		t.Fatal("Get(id) missed the edge that was just added")
	}
	if !stored.At.Equal(again.At.UTC().Truncate(time.Second)) {
		t.Errorf("At = %v, want it refreshed to %v", stored.At, again.At)
	}
	if stored.Confidence != 0.75 {
		t.Errorf("Confidence = %v, want it refreshed to 0.75", stored.Confidence)
	}
	if stored.RunID != "run-9" || stored.Note != "seen again" {
		t.Errorf("RunID/Note = %q/%q, want them refreshed", stored.RunID, stored.Note)
	}
}

func TestAddDedupsWithinOneBatch(t *testing.T) {
	s, _ := testStore(t)
	e := Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow}
	mustAdd(t, s, e, e, e)
	if got := s.Count(); got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
}

func TestAddRejectsInvalidEdgesButKeepsTheGoodOnes(t *testing.T) {
	s, _ := testStore(t)
	good := Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow}
	err := s.Add(
		Edge{To: FileNode("a.go"), Type: Touched},                           // no source
		Edge{From: EpisodeNode("ep_1"), Type: Touched},                      // no target
		Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go")},               // no type
		Edge{From: FileNode("a.go"), To: FileNode("a.go"), Type: DependsOn}, // self edge
		good,
	)
	if err == nil {
		t.Fatal("Add returned nil for a batch containing four invalid edges")
	}
	if got := s.Count(); got != 1 {
		t.Errorf("Count = %d, want 1 — the valid edge in the batch was lost", got)
	}
	if !s.Has(good.From, good.To, good.Type) {
		t.Error("Has() missed the one valid edge in the batch")
	}
}

func TestOutInAndNeighborsFilterByType(t *testing.T) {
	s, _ := testStore(t)
	ep := EpisodeNode("ep_1")
	mustAdd(t, s,
		Edge{From: ep, To: FileNode("b.go"), Type: Touched, At: testNow},
		Edge{From: ep, To: FileNode("a.go"), Type: Touched, At: testNow},
		Edge{From: ep, To: FailureNode("fp1"), Type: Produced, At: testNow},
		Edge{From: RunNode("run-1"), To: ep, Type: ParentOf, At: testNow},
	)

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			"out all types",
			nodesOf(s.Out(ep), ep),
			[]string{"failure:fp1", "file:a.go", "file:b.go"},
		},
		{
			"out filtered",
			nodesOf(s.Out(ep, Touched), ep),
			[]string{"file:a.go", "file:b.go"},
		},
		{
			"out unknown type",
			nodesOf(s.Out(ep, Supersedes), ep),
			nil,
		},
		{
			"in",
			nodesOf(s.In(ep), ep),
			[]string{"run:run-1"},
		},
		{
			"neighbors outgoing",
			s.Neighbors(ep, Outgoing),
			[]string{"failure:fp1", "file:a.go", "file:b.go"},
		},
		{
			"neighbors incoming",
			s.Neighbors(ep, Incoming),
			[]string{"run:run-1"},
		},
		{
			"neighbors either",
			s.Neighbors(ep, Either),
			[]string{"failure:fp1", "file:a.go", "file:b.go", "run:run-1"},
		},
		{
			"neighbors filtered",
			s.Neighbors(ep, Either, Produced, ParentOf),
			[]string{"failure:fp1", "run:run-1"},
		},
		{
			"neighbors of an unknown node",
			s.Neighbors(FileNode("nope.go"), Either),
			nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("= %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// nodesOf projects edges onto the far end, preserving order.
func nodesOf(edges []Edge, from string) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if other, ok := e.Other(from); ok {
			out = append(out, other)
		}
	}
	return out
}

func TestOutIsOrderedIndependentlyOfInsertion(t *testing.T) {
	ep := EpisodeNode("ep_1")
	files := []string{"z.go", "a.go", "m.go", "b.go"}

	forward, _ := testStore(t)
	for _, f := range files {
		mustAdd(t, forward, Edge{From: ep, To: FileNode(f), Type: Touched, At: testNow})
	}
	reverse, _ := testStore(t)
	for i := len(files) - 1; i >= 0; i-- {
		mustAdd(t, reverse, Edge{From: ep, To: FileNode(files[i]), Type: Touched, At: testNow})
	}
	a, b := nodesOf(forward.Out(ep), ep), nodesOf(reverse.Out(ep), ep)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Out order depends on insertion order: %v vs %v", a, b)
	}
	if !sort.StringsAreSorted(a) {
		t.Errorf("Out = %v, want it sorted by target", a)
	}
}

func TestStats(t *testing.T) {
	s, _ := testStore(t)
	mustAdd(t, s,
		Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_1"), To: FileNode("b.go"), Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_1"), To: FailureNode("fp1"), Type: Produced, At: testNow},
	)
	st := s.Stats()
	if st.Edges != 3 {
		t.Errorf("Edges = %d, want 3", st.Edges)
	}
	// episode:ep_1, file:a.go, file:b.go, failure:fp1
	if st.Nodes != 4 {
		t.Errorf("Nodes = %d, want 4", st.Nodes)
	}
	if st.ByType[Touched] != 2 || st.ByType[Produced] != 1 {
		t.Errorf("ByType = %v, want touched:2 produced:1", st.ByType)
	}
	if st.Bytes <= 0 {
		t.Errorf("Bytes = %d, want the size of edges.jsonl", st.Bytes)
	}
	if st.Bytes != fileSize(t, s.logPath()) {
		t.Errorf("Bytes = %d, want %d", st.Bytes, fileSize(t, s.logPath()))
	}
}

func TestIndexIsRebuiltAfterTheIndexFileIsDeleted(t *testing.T) {
	s, root := testStore(t)
	ep := EpisodeNode("ep_1")
	mustAdd(t, s,
		Edge{From: ep, To: FileNode("a.go"), Type: Touched, At: testNow},
		Edge{From: ep, To: FailureNode("fp1"), Type: Produced, At: testNow},
		Edge{From: FailureNode("fp1"), To: RuleNode("rule_1"), Type: ResolvedBy, At: testNow},
	)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	indexPath := s.indexPath()
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index was never written: %v", err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index: %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := reopened.Count(); got != 3 {
		t.Fatalf("Count = %d, want 3 — the index was not rebuilt from the log", got)
	}
	if got := reopened.Neighbors(ep, Outgoing); len(got) != 2 {
		t.Errorf("Neighbors = %v, want the adjacency rebuilt too", got)
	}
	if !reopened.Has(FailureNode("fp1"), RuleNode("rule_1"), ResolvedBy) {
		t.Error("a rebuilt edge is missing")
	}
}

func TestStaleIndexIsRebuiltFromTheLog(t *testing.T) {
	s, root := testStore(t)
	mustAdd(t, s,
		Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_2"), To: FileNode("b.go"), Type: Touched, At: testNow},
	)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Edit the log by hand — the case the offsets in the index cannot survive.
	// Prepending shifts every record, so the index's last offset no longer
	// names the record it claims.
	prepended := Edge{From: EpisodeNode("ep_0"), To: FileNode("c.go"), Type: Touched, At: testNow}
	existing, err := os.ReadFile(s.logPath())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.WriteFile(s.logPath(), append([]byte(rawLine(t, prepended)), existing...), 0o600); err != nil {
		t.Fatalf("rewrite log: %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := reopened.Count(); got != 3 {
		t.Errorf("Count = %d, want 3 — the stale index was trusted", got)
	}
	if !hasWarning(reopened.Warnings(), "stale") {
		t.Errorf("Warnings = %v, want one mentioning a stale index", reopened.Warnings())
	}
	if !reopened.Has(prepended.From, prepended.To, prepended.Type) {
		t.Error("the hand-added record is missing from the rebuilt index")
	}
}

func appendRaw(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func hasWarning(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func TestCorruptLogLineIsSkippedAndReported(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".slmcode", DirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(dir, LogFileName)

	first := Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow}
	last := Edge{From: EpisodeNode("ep_2"), To: FileNode("b.go"), Type: Touched, At: testNow}
	appendRaw(t, logPath, rawLine(t, first))
	appendRaw(t, logPath, "{\"from\": not json at all\n")
	appendRaw(t, logPath, "\n")                                     // blank lines are not corruption
	appendRaw(t, logPath, "{\"from\":\"file:a\",\"type\":\"x\"}\n") // parses, but has no target
	appendRaw(t, logPath, rawLine(t, last))

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2 — the records either side of the damage must survive", got)
	}
	if !s.Has(first.From, first.To, first.Type) || !s.Has(last.From, last.To, last.Type) {
		t.Error("a record next to the corrupt line was lost")
	}
	if !hasWarning(s.Warnings(), "corrupt") {
		t.Errorf("Warnings = %v, want one mentioning corrupt records", s.Warnings())
	}
}

func TestCorruptIndexIsMovedAsideAndRebuilt(t *testing.T) {
	s, root := testStore(t)
	mustAdd(t, s,
		Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow},
		Edge{From: EpisodeNode("ep_2"), To: FileNode("b.go"), Type: Touched, At: testNow},
	)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	indexPath := s.indexPath()
	if err := os.WriteFile(indexPath, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("clobber index: %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(indexPath + ".corrupt"); err != nil {
		t.Errorf("no %s.corrupt: the damaged file was destroyed instead of preserved", IndexFileName)
	}
	if got := reopened.Count(); got != 2 {
		t.Errorf("Count = %d, want 2 — the log should have rebuilt the index", got)
	}
	if !hasWarning(reopened.Warnings(), "unreadable") {
		t.Errorf("Warnings = %v, want one mentioning the unreadable index", reopened.Warnings())
	}
}

func TestReadOnlyNeverWrites(t *testing.T) {
	// A read-only store on a fresh root must not even create the directory.
	fresh := t.TempDir()
	ro, err := OpenWith(fresh, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if err := ro.Add(Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched}); err != nil {
		t.Fatalf("Add on a read-only store: %v", err)
	}
	if n, err := ro.Prune(PrunePolicy{MaxEdges: 1}); n != 0 || err != nil {
		t.Errorf("Prune = (%d, %v), want (0, nil)", n, err)
	}
	if err := ro.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if ro.Count() != 0 {
		t.Errorf("Count = %d, want 0 — a read-only store accepted an edge", ro.Count())
	}
	if _, err := os.Stat(filepath.Join(fresh, ".slmcode")); !os.IsNotExist(err) {
		t.Errorf("a read-only store created .slmcode (stat err = %v)", err)
	}

	// A read-only store over an existing one must leave every byte alone,
	// including the quarantine it would otherwise perform on a corrupt index.
	s, root := testStore(t)
	mustAdd(t, s, Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.WriteFile(s.indexPath(), []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("clobber index: %v", err)
	}
	before := dirSnapshot(t, s.Dir())

	ro2, err := OpenWith(root, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if got := ro2.Count(); got != 1 {
		t.Errorf("Count = %d, want 1 — a read-only store must still read", got)
	}
	if err := ro2.Add(Edge{From: EpisodeNode("ep_9"), To: FileNode("z.go"), Type: Touched}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := ro2.Prune(PrunePolicy{MaxEdges: 1}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if err := ro2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if after := dirSnapshot(t, s.Dir()); !reflect.DeepEqual(before, after) {
		t.Errorf("a read-only store changed the directory:\nbefore %v\nafter  %v",
			sortedKeys(before), sortedKeys(after))
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestInMemoryStoreWorksWithoutARoot(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	if s.Dir() != "" {
		t.Errorf("Dir = %q, want in-memory", s.Dir())
	}
	mustAdd(t, s, Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow})
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}
	if st := s.Stats(); st.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0 for an in-memory store", st.Bytes)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestHardBackstopBoundsAStoreThatIsNeverPruned(t *testing.T) {
	root := t.TempDir()
	s, err := OpenWith(root, Options{MaxEdges: 3, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var edges []Edge
	for i := 0; i < 8; i++ {
		edges = append(edges, Edge{
			From: EpisodeNode("ep_" + string(rune('a'+i))),
			To:   FileNode("x.go"),
			Type: Touched,
			At:   testNow,
		})
	}
	mustAdd(t, s, edges...)
	if got := s.Count(); got > 3 {
		t.Errorf("Count = %d, want the backstop to bound it at MaxEdges=3", got)
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if err := s.Add(Edge{From: "a", To: "b", Type: Touched}); err != nil {
		t.Errorf("Add on a nil store: %v", err)
	}
	if got := s.Out("a"); got != nil {
		t.Errorf("Out = %v, want nil", got)
	}
	if got := s.Neighbors("a", Either); len(got) != 0 {
		t.Errorf("Neighbors = %v, want empty", got)
	}
	if got := s.Walk("a", WalkOptions{}); got != nil {
		t.Errorf("Walk = %v, want nil", got)
	}
	if n, err := s.Prune(PrunePolicy{}); n != 0 || err != nil {
		t.Errorf("Prune = (%d, %v), want (0, nil)", n, err)
	}
	if got := s.Stats(); got.Edges != 0 {
		t.Errorf("Stats = %+v, want zero", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if got := KnownAboutFile(s, "a.go"); !got.Empty() {
		t.Errorf("KnownAboutFile = %+v, want empty", got)
	}
	if got := FailureClassesForFiles(s, []string{"a.go"}); len(got) != 0 {
		t.Errorf("FailureClassesForFiles = %v, want empty", got)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	// Gives `go test -race` something to detect: the store is mutex-guarded and
	// the orchestrator calls into it from a run that is doing other things.
	s, _ := testStore(t)
	const workers = 8
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 25; i++ {
				id := strconv.Itoa(w) + "_" + strconv.Itoa(i)
				_ = s.Add(Edge{
					From: EpisodeNode("ep_" + id),
					To:   FileNode("f" + strconv.Itoa(i) + ".go"),
					Type: Touched,
					At:   testNow,
				})
				_ = s.Out(EpisodeNode("ep_" + id))
				_ = s.In(FileNode("f" + strconv.Itoa(i) + ".go"))
				_ = s.Neighbors(FileNode("f"+strconv.Itoa(i)+".go"), Either)
				_ = s.Walk(FileNode("f"+strconv.Itoa(i)+".go"), WalkOptions{Direction: Either})
				_ = s.Stats()
				_ = s.Warnings()
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	if got := s.Count(); got != workers*25 {
		t.Errorf("Count = %d, want %d", got, workers*25)
	}
	if err := s.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}

func TestForgetRemovesTheStore(t *testing.T) {
	s, root := testStore(t)
	mustAdd(t, s, Edge{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := Forget(root); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
		t.Errorf("graph dir still exists after Forget (stat err = %v)", err)
	}
	// …and reopening is fine, which is what makes `rm -rf` supported.
	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open after Forget: %v", err)
	}
	if reopened.Count() != 0 {
		t.Errorf("Count = %d, want 0", reopened.Count())
	}
	if err := Forget(""); err != nil {
		t.Errorf("Forget(\"\"): %v", err)
	}
}
