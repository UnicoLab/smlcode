package graph

import (
	"reflect"
	"strconv"
	"testing"
)

// chain builds n0 -depends_on-> n1 -depends_on-> … and returns the store.
func chain(t *testing.T, n int) (*Store, []string) {
	t.Helper()
	s, _ := testStore(t)
	nodes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, FileNode("n"+strconv.Itoa(i)+".go"))
	}
	for i := 0; i+1 < n; i++ {
		mustAdd(t, s, Edge{From: nodes[i], To: nodes[i+1], Type: DependsOn, At: testNow})
	}
	return s, nodes
}

func endsOf(paths []Path) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.End())
	}
	return out
}

func TestWalkRespectsMaxDepth(t *testing.T) {
	s, nodes := chain(t, 10)

	tests := []struct {
		name      string
		maxDepth  int
		wantEnds  []string
		wantDepth int
	}{
		{"explicit 1", 1, nodes[1:2], 1},
		{"explicit 2", 2, nodes[1:3], 2},
		{"default is 3", 0, nodes[1:4], DefaultWalkDepth},
		{"negative falls back to the default", -5, nodes[1:4], DefaultWalkDepth},
		{"clamped to the hard cap", 99, nodes[1 : MaxWalkDepth+1], MaxWalkDepth},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			paths := s.Walk(nodes[0], WalkOptions{MaxDepth: tc.maxDepth})
			if got := endsOf(paths); !reflect.DeepEqual(got, tc.wantEnds) {
				t.Fatalf("ends = %v, want %v", got, tc.wantEnds)
			}
			deepest := 0
			for _, p := range paths {
				if p.Depth() > deepest {
					deepest = p.Depth()
				}
				if len(p.Nodes) != p.Depth()+1 {
					t.Errorf("path %s has %d nodes for %d edges", p, len(p.Nodes), p.Depth())
				}
				if p.Start() != nodes[0] {
					t.Errorf("path %s does not start at the origin", p)
				}
			}
			if deepest != tc.wantDepth {
				t.Errorf("deepest = %d, want %d", deepest, tc.wantDepth)
			}
		})
	}
}

func TestWalkRespectsMaxNodes(t *testing.T) {
	s, nodes := chain(t, 10)

	tests := []struct {
		name      string
		maxNodes  int
		wantPaths int
	}{
		// The budget counts the origin, so a budget of n yields n-1 paths.
		{"one", 1, 0},
		{"two", 2, 1},
		{"three", 3, 2},
		// Past MaxDepth the depth bound wins.
		{"generous", 100, DefaultWalkDepth},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			paths := s.Walk(nodes[0], WalkOptions{MaxNodes: tc.maxNodes})
			if len(paths) != tc.wantPaths {
				t.Errorf("paths = %d (%v), want %d", len(paths), endsOf(paths), tc.wantPaths)
			}
		})
	}

	// A wide graph: one origin with 50 neighbors, budget 10.
	wide, _ := testStore(t)
	origin := EpisodeNode("ep_wide")
	for i := 0; i < 50; i++ {
		mustAdd(t, wide, Edge{From: origin, To: FileNode("f" + strconv.Itoa(i) + ".go"), Type: Touched, At: testNow})
	}
	if got := len(wide.Walk(origin, WalkOptions{MaxNodes: 10})); got != 9 {
		t.Errorf("wide walk returned %d paths, want 9 (10 nodes minus the origin)", got)
	}
}

func TestWalkTerminatesOnACycle(t *testing.T) {
	s, _ := testStore(t)
	a, b, c := FileNode("a.go"), FileNode("b.go"), FileNode("c.go")
	mustAdd(t, s,
		Edge{From: a, To: b, Type: DependsOn, At: testNow},
		Edge{From: b, To: c, Type: DependsOn, At: testNow},
		Edge{From: c, To: a, Type: DependsOn, At: testNow}, // closes the loop
		Edge{From: b, To: a, Type: DependsOn, At: testNow}, // and a 2-cycle
	)

	for _, dir := range []Direction{Outgoing, Incoming, Either} {
		t.Run(dir.String(), func(t *testing.T) {
			// A depth budget far larger than the cycle: without a visited set
			// this never returns.
			paths := s.Walk(a, WalkOptions{MaxDepth: MaxWalkDepth, MaxNodes: MaxWalkNodes, Direction: dir})
			if got := endsOf(paths); !reflect.DeepEqual(sortedUnique(got), []string{b, c}) {
				t.Errorf("ends = %v, want each of b and c exactly once", got)
			}
			for _, p := range paths {
				if seen := sortedUnique(p.Nodes); len(seen) != len(p.Nodes) {
					t.Errorf("path %s revisits a node", p)
				}
			}
		})
	}
}

func TestWalkIsDeterministic(t *testing.T) {
	// The same graph, built in two different insertion orders.
	build := func(reverse bool) *Store {
		s, _ := testStore(t)
		edges := []Edge{
			{From: EpisodeNode("ep_1"), To: FileNode("z.go"), Type: Touched, At: testNow},
			{From: EpisodeNode("ep_1"), To: FileNode("a.go"), Type: Touched, At: testNow},
			{From: EpisodeNode("ep_1"), To: FailureNode("fp_b"), Type: Produced, At: testNow},
			{From: EpisodeNode("ep_1"), To: FailureNode("fp_a"), Type: Produced, At: testNow},
			{From: FailureNode("fp_a"), To: RuleNode("rule_z"), Type: ResolvedBy, At: testNow},
			{From: FailureNode("fp_a"), To: RuleNode("rule_a"), Type: ResolvedBy, At: testNow},
			{From: FailureNode("fp_b"), To: RuleNode("rule_m"), Type: ResolvedBy, At: testNow},
		}
		if reverse {
			for i, j := 0, len(edges)-1; i < j; i, j = i+1, j-1 {
				edges[i], edges[j] = edges[j], edges[i]
			}
		}
		for _, e := range edges {
			mustAdd(t, s, e)
		}
		return s
	}
	forward, backward := build(false), build(true)
	opt := WalkOptions{MaxDepth: 4, MaxNodes: 50}

	want := forward.Walk(EpisodeNode("ep_1"), opt)
	if len(want) == 0 {
		t.Fatal("the walk found nothing to compare")
	}
	// Same store, repeated calls.
	for i := 0; i < 20; i++ {
		if got := forward.Walk(EpisodeNode("ep_1"), opt); !reflect.DeepEqual(got, want) {
			t.Fatalf("call %d differs:\n got %v\nwant %v", i, pathStrings(got), pathStrings(want))
		}
	}
	// Same graph, different insertion order.
	if got := backward.Walk(EpisodeNode("ep_1"), opt); !reflect.DeepEqual(got, want) {
		t.Errorf("insertion order changed the walk:\n got %v\nwant %v", pathStrings(got), pathStrings(want))
	}
	// And the order is the sorted, breadth-first one the doc promises.
	wantOrder := []string{
		"failure:fp_a", "failure:fp_b", "file:a.go", "file:z.go",
		"rule:rule_a", "rule:rule_z", "rule:rule_m",
	}
	if got := endsOf(want); !reflect.DeepEqual(got, wantOrder) {
		t.Errorf("order = %v, want %v", got, wantOrder)
	}
}

func pathStrings(paths []Path) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.String())
	}
	return out
}

func TestWalkDirectionAndTypeFilter(t *testing.T) {
	s, _ := testStore(t)
	run, ep, file := RunNode("run-1"), EpisodeNode("ep_1"), FileNode("a.go")
	fail := FailureNode("fp_1")
	mustAdd(t, s,
		Edge{From: run, To: ep, Type: ParentOf, At: testNow},
		Edge{From: ep, To: file, Type: Touched, At: testNow},
		Edge{From: ep, To: fail, Type: Produced, At: testNow},
	)

	tests := []struct {
		name string
		from string
		opt  WalkOptions
		want []string
	}{
		{"outgoing", ep, WalkOptions{}, []string{fail, file}},
		{"incoming", ep, WalkOptions{Direction: Incoming}, []string{run}},
		{"either", ep, WalkOptions{Direction: Either}, []string{fail, file, run}},
		{"type filtered", ep, WalkOptions{Types: []string{Touched}}, []string{file}},
		{"type filtered upstream", file, WalkOptions{Direction: Incoming, Types: []string{Touched}}, []string{ep}},
		{"unknown type", ep, WalkOptions{Types: []string{Supersedes}}, nil},
		{"unknown origin", FileNode("nope.go"), WalkOptions{}, nil},
		{"blank origin", "", WalkOptions{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := endsOf(s.Walk(tc.from, tc.opt))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("= %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWalkFindsTheFileToRuleChain(t *testing.T) {
	// The chain the package exists for, walked upstream from a file.
	s, _ := testStore(t)
	ep, file := EpisodeNode("ep_1"), FileNode("pkg/http/client.go")
	fail, rule := FailureNode("fp_edit"), RuleNode("rule_reread")
	mustAdd(t, s,
		Edge{From: ep, To: file, Type: Touched, At: testNow},
		Edge{From: ep, To: fail, Type: Produced, At: testNow},
		Edge{From: fail, To: rule, Type: ResolvedBy, At: testNow},
	)
	paths := s.Walk(file, WalkOptions{Direction: Either, MaxDepth: 3})
	if got, want := endsOf(paths), []string{ep, fail, rule}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ends = %v, want %v", got, want)
	}
	if got, want := paths[2].String(),
		"file:pkg/http/client.go -touched-> episode:ep_1 -produced-> failure:fp_edit -resolved_by-> rule:rule_reread"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestReachable(t *testing.T) {
	s, nodes := chain(t, 6)
	got := s.Reachable(nodes[0], WalkOptions{MaxDepth: 2})
	if want := []string{nodes[1], nodes[2]}; !reflect.DeepEqual(got, want) {
		t.Errorf("Reachable = %v, want %v", got, want)
	}
}

func TestPathAccessorsOnAnEmptyPath(t *testing.T) {
	var p Path
	if p.End() != "" || p.Start() != "" || p.Depth() != 0 || p.String() != "" {
		t.Errorf("the zero Path is not inert: %q %q %d %q", p.End(), p.Start(), p.Depth(), p.String())
	}
}
