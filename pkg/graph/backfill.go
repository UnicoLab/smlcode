package graph

import (
	"errors"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Id prefixes the harness mints elsewhere. They are used only as a fallback,
// to classify a reference to a record that has since been pruned.
const (
	episodeIDPrefix = "ep_"   // memory.hashID("ep_", …)
	runIDPrefix     = "run-"  // orchestrator: "run-<unixnano>"
	ruleIDPrefix    = "rule_" // evolve.RuleID
)

// Backfill materializes the edges that records already on disk imply, and
// returns how many of them were not already stored.
//
// This is the whole point of the package. The harness has been writing these
// relationships for as long as it has been running, as strings inside other
// records that nothing can traverse:
//
//	fact.Sources          → fact    -derived_from-> episode
//	rule.Evidence         → rule    -derived_from-> episode | run
//	episode.RunID         → run     -parent_of->    episode
//	episode.FilesChanged  → episode -touched->      file
//	failure.Fingerprint   → episode -produced->     failure
//	failure.ResolvedBy    → failure -resolved_by->  rule
//
// It is idempotent by construction: edges are content-addressed, so running it
// after every run costs one pass over records already in memory and leaves the
// log byte-identical once everything has been seen. Nothing here calls a model
// and nothing here guesses — a reference that resolves to no known record is
// dropped rather than turned into a node that names nothing.
func Backfill(root string) (int, error) {
	if strings.TrimSpace(root) == "" {
		return 0, nil
	}
	s, err := Open(root)
	if err != nil {
		return 0, err
	}
	n, addErr := backfillInto(s, root)
	return n, errors.Join(addErr, s.Close())
}

// backfillInto is Backfill against an already-open store.
func backfillInto(s *Store, root string) (int, error) {
	if s == nil || strings.TrimSpace(root) == "" {
		return 0, nil
	}
	edges, err := deriveEdges(root)
	added, addErr := s.add(edges)
	return added, errors.Join(err, addErr)
}

// knownIDs is the set of record ids this backfill actually saw. References are
// resolved against it by exact match — there is no entity resolution here and
// no shape-guessing beyond the last-resort prefixes above.
type knownIDs struct {
	episodes map[string]bool
	runs     map[string]bool
}

// deriveEdges reads the harness's own records and returns the edges they
// imply. Pure with respect to the graph: it writes nothing.
func deriveEdges(root string) ([]Edge, error) {
	// The user-scoped memory directory is pointed at the project on purpose:
	// nothing Backfill needs lives under $HOME, and a derived project index
	// should not read (or, on a corrupt file, quarantine) a user's global store.
	mem, err := memory.OpenWith(root, root, memory.Options{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	// Project rules only, and without the shipped seed rules: a seeded rule has
	// no evidence, so merging them in would only add work.
	rules, rulesErr := evolve.OpenRulesWith(root, "", evolve.RulesOptions{NoSeed: true})

	episodes := mem.Episodes().All()
	known := knownIDs{
		episodes: make(map[string]bool, len(episodes)),
		runs:     map[string]bool{},
	}
	for _, e := range episodes {
		known.episodes[e.ID] = true
		if e.RunID != "" {
			known.runs[e.RunID] = true
		}
	}

	edges := make([]Edge, 0, len(episodes)*4)

	// Episodes: what they changed, what broke, and what fixed it.
	for _, e := range episodes {
		ep := EpisodeNode(e.ID)
		if ep == "" {
			continue
		}
		if run := RunNode(e.RunID); run != "" {
			edges = append(edges, Edge{From: run, To: ep, Type: ParentOf, RunID: e.RunID, At: e.At})
		}
		for _, f := range e.FilesChanged {
			if file := FileNode(f); file != "" {
				edges = append(edges, Edge{From: ep, To: file, Type: Touched, RunID: e.RunID, At: e.At})
			}
		}
		for _, f := range e.Failures {
			fail := FailureNode(f.Fingerprint)
			if fail == "" {
				continue
			}
			edges = append(edges, Edge{
				From: ep, To: fail, Type: Produced,
				RunID: e.RunID, At: e.At, Note: f.Class,
			})
			if rule := ruleRef(f.ResolvedBy); rule != "" {
				edges = append(edges, Edge{
					From: fail, To: rule, Type: ResolvedBy,
					RunID: e.RunID, At: e.At, Note: f.Resolution,
				})
			}
		}
	}

	// Facts: the episodes they were distilled from.
	for _, f := range mem.Semantic().All() {
		from := FactNode(f.ID)
		if from == "" {
			continue
		}
		for _, src := range f.Sources {
			if to := recordNode(src, known); to != "" {
				edges = append(edges, Edge{
					From: from, To: to, Type: DerivedFrom,
					At: f.LastSeen, Note: string(f.Kind),
				})
			}
		}
	}

	// Rules: the evidence that created them.
	if rules != nil {
		for _, r := range rules.All() {
			from := RuleNode(r.ID)
			if from == "" {
				continue
			}
			for _, ev := range r.Evidence {
				if to := recordNode(ev, known); to != "" {
					edges = append(edges, Edge{From: from, To: to, Type: DerivedFrom, At: r.CreatedAt})
				}
			}
		}
	}

	// Canonical order: oldest first, ties broken by content. The log is then a
	// chronology (which is what Prune's "keep the newest" assumes) and two
	// backfills of the same records produce the same file.
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		switch {
		case !a.At.Equal(b.At):
			return a.At.Before(b.At)
		case a.From != b.From:
			return a.From < b.From
		case a.To != b.To:
			return a.To < b.To
		default:
			return a.Type < b.Type
		}
	})
	return edges, rulesErr
}

// recordNode maps a reference stored inside a record onto a node id.
//
// Exact match only. Rule evidence is documented as an episode or task id, but
// pkg/evolve also writes a RUN id into it, so classifying by shape would mint
// episode:run-1724… nodes that name nothing. A reference matching no known
// record and carrying no recognizable prefix is dropped: a missing edge is a
// gap, an invented one is a lie that gets believed forever.
func recordNode(ref string, known knownIDs) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	switch NodeKind(ref) {
	case NodeEpisode, NodeRun, NodeTask, NodeAttempt:
		return ref // already a node id
	}
	switch {
	case known.episodes[ref]:
		return EpisodeNode(ref)
	case known.runs[ref]:
		return RunNode(ref)
	case strings.HasPrefix(ref, episodeIDPrefix):
		// A real episode id whose episode has since been pruned. The node
		// outliving the record is fine — that is what a fingerprint node is too.
		return EpisodeNode(ref)
	case strings.HasPrefix(ref, runIDPrefix):
		return RunNode(ref)
	default:
		return ""
	}
}

// ruleRef maps a memory.FailureNote.ResolvedBy onto a rule node.
//
// pkg/loop writes "rule:<id>" and pkg/evolve carries that through to the
// episode, but the field is documented as possibly holding a bare rule id, and
// it holds "llm", "retry" or "human" when no rule was involved. Only the two
// rule forms produce an edge.
func ruleRef(resolvedBy string) string {
	v := strings.TrimSpace(resolvedBy)
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, NodeRule+":"):
		return nodeID(NodeRule, strings.TrimPrefix(v, NodeRule+":"))
	case strings.HasPrefix(v, ruleIDPrefix):
		return RuleNode(v)
	default:
		return ""
	}
}
