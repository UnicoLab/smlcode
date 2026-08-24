package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/graph"
)

// `slmcode graph` — read the edge index that joins the harness's own records.
//
// pkg/memory and pkg/evolve already store the references: a fact names the
// episodes it was distilled from, an episode names the files it changed, a
// failure names the rule that fixed it. Until pkg/graph they were opaque
// strings that nothing followed, and the cross-store question — "what has
// broken in this file before, and what fixed it" — could not be asked at all.
// These subcommands are the readable end of that index; without them the store
// would be one more thing the harness writes and nobody can see.

// graphNodeKinds is the set of prefixes an argument may already carry. An
// argument with any other shape is read as a repo-relative path, because that
// is what a human types.
var graphNodeKinds = []string{
	graph.NodeFile, graph.NodeSymbol, graph.NodeEpisode, graph.NodeRun,
	graph.NodeTask, graph.NodeAttempt, graph.NodeRule, graph.NodeFact,
	graph.NodeFailure, graph.NodeCommit, graph.NodeArtifact,
}

// openGraph opens the edge index for the current workspace.
//
// Every read path passes readOnly=true: an inspection must never create the
// directory, append a line, rewrite the index or quarantine a corrupt file.
// Looking at state is not allowed to change it.
func openGraph(readOnly bool) (*graph.Store, string, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, "", err
	}
	s, err := graph.OpenWith(root, graph.Options{ReadOnly: readOnly})
	if err != nil {
		return nil, root, err
	}
	return s, root, nil
}

func graphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Inspect the edge index joining episodes, failures, rules, facts and files",
		Long: `Inspect the traversable index over records the harness already writes.

The graph stores nothing new: it materializes the references that already sit
inside episodes, facts and repair rules as typed edges, so a question spanning
several stores — "what failure classes has this file produced, and which rule
resolved them" — becomes one traversal instead of three lookups joined by hand.

It is derived data. Deleting it costs a backfill and nothing else.`,
		Example: `  slmcode graph stats
  slmcode graph file pkg/loop/runner.go
  slmcode graph neighbors episode:ep_1a2b3c --dir either
  slmcode graph walk pkg/loop/runner.go --depth 3 --type touched
  slmcode graph backfill
  slmcode graph prune --max-age 720h
  slmcode graph forget --yes`,
	}
	cmd.AddCommand(graphStatsCmd(), graphFileCmd(), graphNeighborsCmd(),
		graphWalkCmd(), graphBackfillCmd(), graphPruneCmd(), graphForgetCmd())
	return cmd
}

// graphNodeArg turns a command-line argument into a node id.
//
// "file:pkg/x.go" and "episode:ep_1a2b" are already node ids and pass through
// untouched. Anything else is treated as a repo-relative path, so the common
// case — `slmcode graph walk pkg/loop/runner.go` — needs no prefix. A bare word
// with a colon in it that is NOT a known kind (a Windows drive, a URL) still
// reads as a path rather than minting a node of an invented kind.
func graphNodeArg(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ""
	}
	kind := graph.NodeKind(arg)
	for _, k := range graphNodeKinds {
		if kind == k {
			return arg
		}
	}
	return graph.FileNode(arg)
}

// graphDirection parses the --dir flag.
func graphDirection(s string) (graph.Direction, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "out", "outgoing", "":
		return graph.Outgoing, nil
	case "in", "incoming":
		return graph.Incoming, nil
	case "either", "both":
		return graph.Either, nil
	default:
		return graph.Outgoing, failf(2, "graph: invalid --dir %q — allowed: in, out, either", s)
	}
}

// graphCheckTypes rejects an unknown --type early. Store.Add accepts any
// non-empty type so a caller can record something new without a release, but a
// typo in a filter silently returns nothing, which reads exactly like an empty
// graph and is the more likely mistake at a prompt.
func graphCheckTypes(types []string) error {
	known := map[string]bool{}
	for _, t := range graph.EdgeTypes() {
		known[t] = true
	}
	for _, t := range types {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !known[t] {
			return failf(2, "graph: unknown edge type %q — allowed: %s",
				t, strings.Join(graph.EdgeTypes(), ", "))
		}
	}
	return nil
}

// graphEmpty prints the "there is nothing here yet" line and reports whether it
// did. An empty graph is the normal state of a fresh workspace, not an error:
// the edges are derived, so the answer is a command to run, not a failure.
func graphEmpty(s *graph.Store) bool {
	if s.Count() > 0 {
		return false
	}
	fmt.Println(cli.Dim("  (no edges yet — the graph is derived from memory and evolve records)"))
	fmt.Println(cli.Dim("  run `slmcode graph backfill` to materialize them, or `slmcode run` once"))
	return true
}

// graphWarn prints a store's non-fatal problems. Never fatal by design: the
// graph degrades to whatever it could read rather than failing an inspection.
func graphWarn(s *graph.Store) {
	for _, w := range s.Warnings() {
		fmt.Println(cli.Warn(w))
	}
}

// graphBytes renders a file size for a one-line summary.
func graphBytes(n int64) string {
	switch {
	case n <= 0:
		return "0 B"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func graphStatsCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "stats",
		Short: "Edge counts by type, node count, file size and any warnings",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			s, root, err := openGraph(true)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			st := s.Stats()

			if asJSON {
				return emitJSON(map[string]any{
					"root":      root,
					"dir":       s.Dir(),
					"edges":     st.Edges,
					"nodes":     st.Nodes,
					"bytes":     st.Bytes,
					"by_type":   st.ByType,
					"max_edges": graph.DefaultMaxEdges,
					"warnings":  s.Warnings(),
				})
			}

			cli.Header("Graph")
			fmt.Println(cli.Dim("  Typed edges over records the harness already wrote: which episode touched"))
			fmt.Println(cli.Dim("  which file, what failed there, which rule resolved it. Derived data — the"))
			fmt.Println(cli.Dim("  store can be deleted and `slmcode graph backfill` rebuilds every edge."))
			fmt.Println()
			cli.KeyVal("dir", orDash(s.Dir()))
			cli.KeyVal("edges", fmt.Sprintf("%d of %d max", st.Edges, graph.DefaultMaxEdges))
			cli.KeyVal("nodes", strconv.Itoa(st.Nodes))
			cli.KeyVal("log size", graphBytes(st.Bytes))
			graphWarn(s)
			fmt.Println()
			if graphEmpty(s) {
				return nil
			}
			// Fixed vocabulary order, then anything a caller recorded outside it.
			for _, t := range graph.EdgeTypes() {
				if n := st.ByType[t]; n > 0 {
					fmt.Printf("  %s %s\n", cli.Accent(cli.PadWidth(t, 16)), cli.Dim(strconv.Itoa(n)))
				}
			}
			var extra []string
			known := map[string]bool{}
			for _, t := range graph.EdgeTypes() {
				known[t] = true
			}
			for t := range st.ByType {
				if !known[t] {
					extra = append(extra, t)
				}
			}
			sort.Strings(extra)
			for _, t := range extra {
				fmt.Printf("  %s %s\n", cli.Yellow(cli.PadWidth(t, 16)), cli.Dim(strconv.Itoa(st.ByType[t])))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func graphFileCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "file <path>",
		Short: "What the harness knows about a file: failures seen there, and what fixed them",
		Long: `Answer "what do we already know about this file".

Walks file <-touched- episode -produced-> failure -resolved_by-> rule, which is
the join no single store can do: episodes live in memory, failure fingerprints
in the episode records, repair rules in evolve. A fingerprint IS a failure
class — pkg/evolve hashes class, tool, language, model family and the salient
message into it — so two entries mean two distinct ways this file has broken.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			s, _, err := openGraph(true)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			// "." and "./" canonicalize to nothing, which would print a header
			// for a file with no name and a body saying it is unknown.
			rel := strings.TrimSpace(args[0])
			if graph.FileNode(rel) == "" {
				return failf(2, "graph file: %q is not a repo-relative file path", args[0])
			}
			known := graph.KnownAboutFile(s, rel)
			counts := graph.FailureClassesForFiles(s, []string{rel})

			if asJSON {
				return emitJSON(map[string]any{
					"file":        known.File,
					"node":        graph.FileNode(rel),
					"episodes":    known.Episodes,
					"failures":    known.Failures,
					"rules":       known.Rules,
					"fixed_by":    known.FixedBy,
					"seen_counts": counts,
					"warnings":    s.Warnings(),
				})
			}

			cli.Header("Graph: " + known.File)
			graphWarn(s)
			if graphEmpty(s) {
				return nil
			}
			if known.Empty() {
				fmt.Println(cli.Dim("  (nothing recorded for this file — no episode has changed it yet)"))
				fmt.Println(cli.Dim("  paths are repo-relative and matched exactly; there is no fuzzy matching"))
				return nil
			}
			fmt.Printf("  %s %s\n", cli.Dim(cli.PadWidth("episodes", 14)), strconv.Itoa(len(known.Episodes)))
			for _, id := range known.Episodes {
				fmt.Println("      " + cli.Dim(id))
			}
			fmt.Println()
			fmt.Printf("  %s %s\n", cli.Dim(cli.PadWidth("failures", 14)), strconv.Itoa(len(known.Failures)))
			if len(known.Failures) == 0 {
				fmt.Println("      " + cli.Green("nothing has broken here"))
			}
			for _, fp := range known.Failures {
				seen := ""
				if n := counts[fp]; n > 0 {
					seen = cli.Dim(fmt.Sprintf("seen %d×", n))
				}
				fmt.Printf("      %s %s\n", cli.Accent(cli.PadWidth(cli.Clip(fp, 24), 24)), seen)
				fixers := known.FixedBy[fp]
				if len(fixers) == 0 {
					// The distinction the whole command exists for: a class that
					// keeps happening and has never been fixed by a rule is the
					// one worth a human's attention.
					fmt.Println("        " + cli.Warn("no rule has resolved this class"))
					continue
				}
				fmt.Println("        " + cli.Green("fixed by ") + strings.Join(fixers, ", "))
			}
			if len(known.Rules) > 0 {
				fmt.Println()
				fmt.Printf("  %s %s\n", cli.Dim(cli.PadWidth("rules", 14)), strings.Join(known.Rules, ", "))
				fmt.Println(cli.Dim("  inspect one with `slmcode evolve rules`"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

// graphAdjacent returns the edges on a node in the requested direction.
// Store.Out and Store.In each sort by the far end already; Either concatenates
// them and re-sorts so the merged listing is ordered the same way.
func graphAdjacent(s *graph.Store, node string, dir graph.Direction, types []string) []graph.Edge {
	var edges []graph.Edge
	if dir == graph.Outgoing || dir == graph.Either {
		edges = append(edges, s.Out(node, types...)...)
	}
	if dir == graph.Incoming || dir == graph.Either {
		edges = append(edges, s.In(node, types...)...)
	}
	if dir == graph.Either {
		sort.SliceStable(edges, func(i, j int) bool {
			a, _ := edges[i].Other(node)
			b, _ := edges[j].Other(node)
			if a != b {
				return a < b
			}
			return edges[i].Type < edges[j].Type
		})
	}
	return edges
}

// graphPathString renders a walk path with direction-aware arrows.
//
// graph.Path.String() always writes "-type->", which is right for an outgoing
// walk and wrong for this command's default: `--dir either` follows edges
// backwards too, and printing "file:x -touched-> episode:y" states the reverse
// of what the store holds. The edge is the same either way; the arrow is the
// only thing that says which end is which.
func graphPathString(p graph.Path) string {
	if len(p.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(p.Nodes[0])
	for i, e := range p.Edges {
		to := ""
		if i+1 < len(p.Nodes) {
			to = p.Nodes[i+1]
		}
		if e.To == to {
			b.WriteString(" -" + e.Type + "-> ")
		} else {
			b.WriteString(" <-" + e.Type + "- ")
		}
		b.WriteString(to)
	}
	return b.String()
}

func graphNeighborsCmd() *cobra.Command {
	var asJSON bool
	var dirFlag string
	var types []string
	c := &cobra.Command{
		Use:   "neighbors <node>",
		Short: "The nodes one hop from a node, with the edges that connect them",
		Long: `List one hop out of a node.

<node> is a node id ("episode:ep_1a2b", "failure:fp_9c…") or a repo-relative
path, which is read as file:<path>. Node identity is exact: there is no fuzzy
matching and no entity resolution, so a path that does not match byte for byte
returns nothing rather than a plausible neighbor.

--dir defaults to "either" because most node kinds are reached from one side
only — every edge on a file node is incoming, so an outgoing-only listing of one
would always be empty.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			dir, err := graphDirection(dirFlag)
			if err != nil {
				return err
			}
			if err := graphCheckTypes(types); err != nil {
				return err
			}
			node := graphNodeArg(args[0])
			if node == "" {
				return failf(2, "graph neighbors: %q is not a node", args[0])
			}
			s, _, err := openGraph(true)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			edges := graphAdjacent(s, node, dir, types)
			nodes := s.Neighbors(node, dir, types...)

			if asJSON {
				return emitJSON(map[string]any{
					"node":      node,
					"direction": dir.String(),
					"types":     types,
					"neighbors": nodes,
					"edges":     edges,
					"warnings":  s.Warnings(),
				})
			}

			cli.Header("Neighbors: " + node)
			cli.KeyVal("direction", dir.String())
			cli.KeyVal("types", orDash(strings.Join(types, ", ")))
			graphWarn(s)
			fmt.Println()
			if graphEmpty(s) {
				return nil
			}
			if len(edges) == 0 {
				fmt.Println(cli.Dim("  (no edges on this node — node ids are matched exactly)"))
				fmt.Println(cli.Dim("  `slmcode graph stats` shows what the store does hold"))
				return nil
			}
			for _, e := range edges {
				other, _ := e.Other(node)
				arrow := cli.Dim("→")
				if e.To == node {
					arrow = cli.Dim("←")
				}
				fmt.Printf("  %s %s %s  %s\n",
					arrow,
					cli.Accent(cli.PadWidth(e.Type, 14)),
					other,
					cli.Dim(agoString(e.At)))
				if e.Note != "" {
					fmt.Println("      " + cli.Dim(cli.Clip(e.Note, 90)))
				}
			}
			fmt.Println()
			fmt.Println(cli.Dim(fmt.Sprintf("  %d edge(s), %d distinct neighbor(s)", len(edges), len(nodes))))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().StringVar(&dirFlag, "dir", "either", "in|out|either — which way to follow edges")
	c.Flags().StringSliceVar(&types, "type", nil, "only this edge type (repeatable)")
	return c
}

func graphWalkCmd() *cobra.Command {
	var asJSON bool
	var dirFlag string
	var types []string
	var depth int
	c := &cobra.Command{
		Use:   "walk <node>",
		Short: "Breadth-first traversal from a node, one path per node reached",
		Long: fmt.Sprintf(`Walk outward from a node, bounded on both axes.

Depth is capped at %d hops and the node budget at %d: this traverses a graph the
harness derived from its own logs, where one wrong edge would otherwise drag a
result through the entire store. The default depth of %d is the chain that
matters — file → episode → failure → rule.

The traversal is deterministic: neighbors are expanded in sorted order, so the
same store and the same flags print the same paths in the same order.`,
			graph.MaxWalkDepth, graph.DefaultWalkNodes, graph.DefaultWalkDepth),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			dir, err := graphDirection(dirFlag)
			if err != nil {
				return err
			}
			if err := graphCheckTypes(types); err != nil {
				return err
			}
			if depth < 0 {
				return failf(2, "graph walk: --depth must not be negative")
			}
			node := graphNodeArg(args[0])
			if node == "" {
				return failf(2, "graph walk: %q is not a node", args[0])
			}
			// The package clamps silently. Say so instead: a user who asked for
			// 12 hops and got 6 should not have to read the source to find out.
			capped := depth > graph.MaxWalkDepth
			if capped {
				depth = graph.MaxWalkDepth
			}
			s, _, err := openGraph(true)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			paths := s.Walk(node, graph.WalkOptions{MaxDepth: depth, Types: types, Direction: dir})

			if asJSON {
				return emitJSON(map[string]any{
					"node":         node,
					"direction":    dir.String(),
					"types":        types,
					"depth":        depth,
					"depth_capped": capped,
					"max_depth":    graph.MaxWalkDepth,
					"paths":        paths,
					"reached":      s.Reachable(node, graph.WalkOptions{MaxDepth: depth, Types: types, Direction: dir}),
					"warnings":     s.Warnings(),
				})
			}

			cli.Header("Walk: " + node)
			cli.KeyVal("direction", dir.String())
			cli.KeyVal("depth", fmt.Sprintf("%d (max %d)", depth, graph.MaxWalkDepth))
			cli.KeyVal("types", orDash(strings.Join(types, ", ")))
			if capped {
				fmt.Println(cli.Warn(fmt.Sprintf("--depth clamped to the package ceiling of %d", graph.MaxWalkDepth)))
			}
			graphWarn(s)
			fmt.Println()
			if graphEmpty(s) {
				return nil
			}
			if len(paths) == 0 {
				fmt.Println(cli.Dim("  (nothing reachable from this node — try --dir either)"))
				return nil
			}
			for _, p := range paths {
				fmt.Printf("  %s %s\n",
					cli.Dim(strconv.Itoa(p.Depth())),
					cli.Clip(graphPathString(p), 110))
			}
			fmt.Println()
			fmt.Println(cli.Dim(fmt.Sprintf("  %d node(s) reached", len(paths))))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().StringVar(&dirFlag, "dir", "either", "in|out|either — which way to follow edges")
	c.Flags().StringSliceVar(&types, "type", nil, "only follow this edge type (repeatable)")
	c.Flags().IntVar(&depth, "depth", graph.DefaultWalkDepth,
		fmt.Sprintf("hops to follow (hard cap %d)", graph.MaxWalkDepth))
	return c
}

func graphBackfillCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "backfill",
		Short: "Materialize the edges that records already on disk imply",
		Long: `Rebuild the edge index from episodes, facts and repair rules.

Edges are content-addressed on (from, to, type), so this is idempotent: running
it twice adds nothing the second time and leaves the log byte-identical. A run
does it automatically at the end of a turn; running it by hand is for a fresh
clone, after graph forget, or after editing the stores directly.

Nothing here calls a model and nothing guesses: a reference that resolves to no
known record is dropped rather than turned into a node that names nothing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			root, err := projectRoot()
			if err != nil {
				return err
			}
			added, err := graph.Backfill(root)
			if err != nil {
				return err
			}
			s, _, openErr := openGraph(true)
			total, warnings := 0, []string(nil)
			if openErr == nil {
				total, warnings = s.Count(), s.Warnings()
				_ = s.Close()
			}

			if asJSON {
				return emitJSON(map[string]any{
					"added":    added,
					"total":    total,
					"root":     root,
					"warnings": warnings,
				})
			}
			for _, w := range warnings {
				fmt.Println(cli.Warn(w))
			}
			if added == 0 {
				fmt.Println(cli.Success(fmt.Sprintf(
					"graph up to date — %d edge(s), nothing new to materialize", total)))
				return nil
			}
			fmt.Println(cli.Success(fmt.Sprintf(
				"materialized %d new edge(s) — %d total", added, total)))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func graphPruneCmd() *cobra.Command {
	var asJSON bool
	var maxEdges int
	var maxAge time.Duration
	c := &cobra.Command{
		Use:   "prune",
		Short: "Drop aged-out and excess edges, rewriting the log so the file shrinks",
		Long: fmt.Sprintf(`Bound the store on two axes: count and age.

The newest edges are kept. The JSONL log is rewritten atomically rather than
marked up, so the file on disk actually gets smaller — an append-only log that
is never compacted is an unbounded log, which would make the cap a lie.

Defaults are the shipped policy: %d edges, %d days. A negative value means "no
limit on this axis". Pruning is safe: every dropped edge is derivable again from
the record that implied it, so a later backfill brings back anything whose
source record still exists.`,
			graph.DefaultMaxEdges, int(graph.DefaultPruneMaxAge.Hours()/24)),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			s, root, err := openGraph(false)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			before := s.Count()
			removed, err := s.Prune(graph.PrunePolicy{MaxEdges: maxEdges, MaxAge: maxAge})
			if err != nil {
				return err
			}

			if asJSON {
				return emitJSON(map[string]any{
					"root":      root,
					"before":    before,
					"removed":   removed,
					"remaining": s.Count(),
					"max_edges": maxEdges,
					"max_age":   maxAge.String(),
					"warnings":  s.Warnings(),
				})
			}
			graphWarn(s)
			if removed == 0 {
				fmt.Println(cli.Success(fmt.Sprintf(
					"nothing to prune — %d edge(s) within bounds", before)))
				return nil
			}
			fmt.Println(cli.Success(fmt.Sprintf(
				"dropped %d edge(s) — %d remaining", removed, s.Count())))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().IntVar(&maxEdges, "max-edges", 0,
		fmt.Sprintf("keep at most this many edges (0: default %d, negative: no limit)", graph.DefaultMaxEdges))
	c.Flags().DurationVar(&maxAge, "max-age", 0,
		fmt.Sprintf("drop edges older than this (0: default %s, negative: no limit)", graph.DefaultPruneMaxAge))
	return c
}

func graphForgetCmd() *cobra.Command {
	var asJSON bool
	var yes bool
	c := &cobra.Command{
		Use:   "forget",
		Short: "Delete the whole edge index (.slmcode/graph)",
		Long: `Delete .slmcode/graph.

Equivalent to rm -rf .slmcode/graph, which is itself a supported operation.
Nothing depends on the graph existing: it holds no record of its own, only
edges between records that live in memory and evolve, so forgetting it loses
nothing that graph backfill cannot rebuild.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			if !yes && !asJSON {
				if !confirm("Delete the edge index (rebuildable with graph backfill)?", false) {
					return failf(2, "canceled")
				}
			}
			if !yes && asJSON {
				return failf(2, "graph forget --json requires --yes")
			}
			root, err := projectRoot()
			if err != nil {
				return err
			}
			if err := graph.Forget(root); err != nil {
				return err
			}
			if asJSON {
				return emitJSON(map[string]any{"forgot": "graph", "root": root})
			}
			fmt.Println(cli.Success("edge index deleted — `slmcode graph backfill` rebuilds it"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return c
}
