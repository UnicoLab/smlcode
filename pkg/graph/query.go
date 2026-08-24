package graph

import "sort"

// FileKnowledge is what the graph knows about one file: what has broken in it
// before, and what fixed it.
//
// The ids are bare — "ep_1a2b", not "episode:ep_1a2b" — so they can be handed
// straight to pkg/memory and pkg/evolve, which is the only reason to ask.
type FileKnowledge struct {
	// File is the repo-relative path, canonicalized.
	File string `json:"file"`
	// Episodes are the episodic-memory records that changed this file.
	Episodes []string `json:"episodes,omitempty"`
	// Failures are the failure fingerprints those episodes produced. A
	// fingerprint IS the failure class here: pkg/evolve hashes the class, tool,
	// language, model family and salient message into it, so two entries mean
	// two distinct ways this file has broken.
	Failures []string `json:"failures,omitempty"`
	// Rules are the repair rules that resolved those failures.
	Rules []string `json:"rules,omitempty"`
	// FixedBy maps a fingerprint to the rules that resolved it. A fingerprint
	// present in Failures but absent here has never been fixed by a rule.
	FixedBy map[string][]string `json:"fixed_by,omitempty"`
}

// Empty reports whether the graph knows nothing about the file.
func (k FileKnowledge) Empty() bool {
	return len(k.Episodes) == 0 && len(k.Failures) == 0 && len(k.Rules) == 0
}

// KnownAboutFile answers "what do we already know about this file" by walking
//
//	file: <-touched- episode: -produced-> failure: -resolved_by-> rule:
//
// Every hop checks the node kind at the far end, so a hand-added edge of the
// right type between the wrong kinds of node cannot smuggle a run id into the
// episode list.
//
// Results are sorted and contain no duplicates: the same answer, in the same
// order, on every call.
func KnownAboutFile(s *Store, rel string) FileKnowledge {
	node := FileNode(rel)
	k := FileKnowledge{File: NodeValue(node)}
	if s == nil || node == "" {
		return k
	}

	var episodes, failures, rules []string
	fixedBy := map[string]map[string]bool{}

	for _, te := range s.In(node, Touched) {
		if !IsKind(te.From, NodeEpisode) {
			continue
		}
		episodes = append(episodes, NodeValue(te.From))
		for _, pe := range s.Out(te.From, Produced) {
			if !IsKind(pe.To, NodeFailure) {
				continue
			}
			fp := NodeValue(pe.To)
			failures = append(failures, fp)
			for _, re := range s.Out(pe.To, ResolvedBy) {
				if !IsKind(re.To, NodeRule) {
					continue
				}
				rule := NodeValue(re.To)
				rules = append(rules, rule)
				if fixedBy[fp] == nil {
					fixedBy[fp] = map[string]bool{}
				}
				fixedBy[fp][rule] = true
			}
		}
	}

	k.Episodes = sortedUnique(episodes)
	k.Failures = sortedUnique(failures)
	k.Rules = sortedUnique(rules)
	if len(fixedBy) > 0 {
		k.FixedBy = make(map[string][]string, len(fixedBy))
		for fp, set := range fixedBy {
			ids := make([]string, 0, len(set))
			for id := range set {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			k.FixedBy[fp] = ids
		}
	}
	return k
}

// FailureClassesForFiles counts how often each failure fingerprint has been
// produced by an episode that touched one of these files.
//
// Each (episode, failure) observation counts once however many of the listed
// files that episode touched — the question is "how often has this gone
// wrong", not "how many files were open at the time".
func FailureClassesForFiles(s *Store, files []string) map[string]int {
	out := map[string]int{}
	if s == nil || len(files) == 0 {
		return out
	}
	seen := map[string]bool{}
	for _, rel := range files {
		node := FileNode(rel)
		if node == "" {
			continue
		}
		for _, te := range s.In(node, Touched) {
			if !IsKind(te.From, NodeEpisode) {
				continue
			}
			for _, pe := range s.Out(te.From, Produced) {
				if !IsKind(pe.To, NodeFailure) {
					continue
				}
				key := te.From + "\x00" + pe.To
				if seen[key] {
					continue
				}
				seen[key] = true
				out[NodeValue(pe.To)]++
			}
		}
	}
	return out
}
