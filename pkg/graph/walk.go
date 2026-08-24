package graph

import "strings"

// Traversal bounds. A walk over a graph the harness built itself is a walk
// over data it may have got wrong, so every axis is capped and the caps are
// not negotiable from the outside.
const (
	// DefaultWalkDepth is how far Walk goes when MaxDepth is unset. Three hops
	// is exactly the chain that matters here: file → episode → failure → rule.
	DefaultWalkDepth = 3
	// MaxWalkDepth is the hard ceiling. Past six hops in a graph this small
	// everything reaches everything, and a result that includes the whole
	// store answers nothing.
	MaxWalkDepth = 6
	// DefaultWalkNodes is the node budget when MaxNodes is unset.
	DefaultWalkNodes = 200
	// MaxWalkNodes is the hard ceiling on the node budget.
	MaxWalkNodes = 2000
)

// WalkOptions bounds a traversal. The zero value is a depth-3, 200-node,
// outgoing walk over every edge type.
type WalkOptions struct {
	// MaxDepth is the longest path returned, in edges. Default
	// DefaultWalkDepth, silently clamped to MaxWalkDepth.
	MaxDepth int
	// MaxNodes bounds how many distinct nodes the walk visits, the origin
	// included. Default DefaultWalkNodes, clamped to MaxWalkNodes.
	MaxNodes int
	// Types filters which edges may be followed. Empty means every type.
	Types []string
	// Direction selects which way edges are followed.
	Direction Direction
}

func (o WalkOptions) withDefaults() WalkOptions {
	switch {
	case o.MaxDepth <= 0:
		o.MaxDepth = DefaultWalkDepth
	case o.MaxDepth > MaxWalkDepth:
		o.MaxDepth = MaxWalkDepth
	}
	switch {
	case o.MaxNodes <= 0:
		o.MaxNodes = DefaultWalkNodes
	case o.MaxNodes > MaxWalkNodes:
		o.MaxNodes = MaxWalkNodes
	}
	return o
}

// Path is one route from a walk's origin to a node it reached. Nodes always
// has exactly one more element than Edges.
type Path struct {
	Nodes []string `json:"nodes"`
	Edges []Edge   `json:"edges"`
}

// End returns the node the path arrives at.
func (p Path) End() string {
	if len(p.Nodes) == 0 {
		return ""
	}
	return p.Nodes[len(p.Nodes)-1]
}

// Start returns the node the path leaves from.
func (p Path) Start() string {
	if len(p.Nodes) == 0 {
		return ""
	}
	return p.Nodes[0]
}

// Depth is the path length in edges.
func (p Path) Depth() int { return len(p.Edges) }

// String renders the path for logs and test failures.
func (p Path) String() string {
	if len(p.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(p.Nodes[0])
	for i, e := range p.Edges {
		b.WriteString(" -")
		b.WriteString(e.Type)
		b.WriteString("-> ")
		if i+1 < len(p.Nodes) {
			b.WriteString(p.Nodes[i+1])
		}
	}
	return b.String()
}

// extend returns a new path with one more hop. The slices are copied, not
// appended in place: two siblings extending the same parent would otherwise
// share a backing array and overwrite each other.
func (p Path) extend(e Edge, to string) Path {
	nodes := make([]string, len(p.Nodes), len(p.Nodes)+1)
	copy(nodes, p.Nodes)
	edges := make([]Edge, len(p.Edges), len(p.Edges)+1)
	copy(edges, p.Edges)
	return Path{Nodes: append(nodes, to), Edges: append(edges, e)}
}

// Walk returns one path to every node reachable from `from` within the
// options' bounds, breadth-first.
//
// It is cycle-safe (each node is reached once, by the shortest path found
// first) and deterministic: neighbors are expanded in sorted order, so the
// same store and the same options always produce byte-identical output. The
// origin itself is not returned — a zero-length path to yourself is noise.
//
// Bounded on both axes, and the bounds are the point: this runs on a graph the
// harness derived from its own logs, where one wrong edge could otherwise drag
// a prompt through the entire store.
func (s *Store) Walk(from string, opt WalkOptions) []Path {
	if s == nil {
		return nil
	}
	from = strings.TrimSpace(from)
	if from == "" {
		return nil
	}
	opt = opt.withDefaults()
	types := typeSet(opt.Types)

	visited := map[string]bool{from: true}
	queue := []Path{{Nodes: []string{from}}}
	var out []Path

	for len(queue) > 0 && len(visited) < opt.MaxNodes {
		cur := queue[0]
		queue = queue[1:]
		if cur.Depth() >= opt.MaxDepth {
			// Breadth-first: everything behind this in the queue is at least as
			// deep, so nothing further can be expanded either.
			break
		}
		for _, e := range s.adjacent(cur.End(), opt.Direction, types) {
			next, ok := e.Other(cur.End())
			if !ok || next == "" || visited[next] {
				continue
			}
			visited[next] = true
			p := cur.extend(e, next)
			out = append(out, p)
			queue = append(queue, p)
			if len(visited) >= opt.MaxNodes {
				break
			}
		}
	}
	return out
}

// Reachable returns the distinct nodes Walk reaches, sorted. It is Walk with
// the routes thrown away, for callers that only need the set.
func (s *Store) Reachable(from string, opt WalkOptions) []string {
	paths := s.Walk(from, opt)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.End())
	}
	return sortedUnique(out)
}
