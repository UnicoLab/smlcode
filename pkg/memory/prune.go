package memory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// PrunePolicy bounds every store. A zero value means "use the defaults";
// an explicitly negative field means "no limit on this axis".
type PrunePolicy struct {
	MaxEpisodes       int
	MaxEpisodeAge     time.Duration
	MaxFacts          int
	MinFactConfidence float64
	MaxFactAge        time.Duration
	MaxProcedures     int
	MaxProcedureAge   time.Duration
}

// DefaultPrunePolicy is what a run applies at the end of a turn.
func DefaultPrunePolicy() PrunePolicy {
	return PrunePolicy{
		MaxEpisodes:       DefaultMaxEpisodes,
		MaxEpisodeAge:     180 * 24 * time.Hour,
		MaxFacts:          DefaultMaxFacts,
		MinFactConfidence: 0.25,
		MaxFactAge:        365 * 24 * time.Hour,
		MaxProcedures:     DefaultMaxProcedures,
		MaxProcedureAge:   365 * 24 * time.Hour,
	}
}

func (p PrunePolicy) withDefaults() PrunePolicy {
	d := DefaultPrunePolicy()
	if p.MaxEpisodes == 0 {
		p.MaxEpisodes = d.MaxEpisodes
	}
	if p.MaxEpisodeAge == 0 {
		p.MaxEpisodeAge = d.MaxEpisodeAge
	}
	if p.MaxFacts == 0 {
		p.MaxFacts = d.MaxFacts
	}
	if p.MinFactConfidence == 0 {
		p.MinFactConfidence = d.MinFactConfidence
	}
	if p.MaxFactAge == 0 {
		p.MaxFactAge = d.MaxFactAge
	}
	if p.MaxProcedures == 0 {
		p.MaxProcedures = d.MaxProcedures
	}
	if p.MaxProcedureAge == 0 {
		p.MaxProcedureAge = d.MaxProcedureAge
	}
	return p
}

// PruneReport says what a prune removed.
type PruneReport struct {
	Episodes   int `json:"episodes_removed"`
	Facts      int `json:"facts_removed"`
	Procedures int `json:"procedures_removed"`
}

// Prune enforces the policy across every layer and rewrites the on-disk stores.
func (s *Store) Prune(policy PrunePolicy) error {
	_, err := s.PruneReport(policy)
	return err
}

// PruneReport runs Prune and returns what it removed.
func (s *Store) PruneReport(policy PrunePolicy) (PruneReport, error) {
	if s.readOnly {
		return PruneReport{}, nil
	}
	policy = policy.withDefaults()
	rep := PruneReport{
		Episodes:   s.episodes.Prune(policy),
		Facts:      s.facts.Prune(policy),
		Procedures: s.procedures.Prune(policy),
	}
	return rep, s.Flush()
}

// Prune drops old and excess episodes and rewrites the JSONL log compactly.
func (s *Episodes) Prune(policy PrunePolicy) int {
	policy = policy.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(policy)
}

func (s *Episodes) pruneLocked(policy PrunePolicy) int {
	before := len(s.entries)
	cutoff := time.Time{}
	if policy.MaxEpisodeAge > 0 {
		cutoff = s.now().Add(-policy.MaxEpisodeAge)
	}
	kept := make([]indexEntry, 0, len(s.entries))
	for _, en := range s.entries {
		if !cutoff.IsZero() && en.At.Before(cutoff) {
			continue
		}
		kept = append(kept, en)
	}
	if policy.MaxEpisodes > 0 && len(kept) > policy.MaxEpisodes {
		kept = kept[len(kept)-policy.MaxEpisodes:]
	}
	if len(kept) == before {
		return 0
	}

	// Rewrite the log so the file shrinks too — an append-only log that is
	// never compacted is an unbounded log.
	if path := s.logPath(); path != "" {
		keepIDs := make(map[string]bool, len(kept))
		for _, en := range kept {
			keepIDs[en.ID] = true
		}
		var buf []byte
		var rebuilt []indexEntry
		for _, en := range kept {
			e, ok := s.hydrateLocked(en)
			if !ok {
				continue
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			rebuilt = append(rebuilt, entryOf(e, int64(len(buf))))
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		if err := atomicfile.Write(path, buf, 0o600); err == nil {
			kept = rebuilt
		}
	}
	s.setEntries(kept)
	s.dirty = true
	return before - len(kept)
}

// hydrateLocked is hydrate for callers already holding the lock.
func (s *Episodes) hydrateLocked(en indexEntry) (Episode, bool) {
	if s.dir == "" {
		return Episode{
			ID: en.ID, At: en.At, Query: en.Query, Summary: en.Summary,
			FilesChanged: en.Files, ToolsUsed: en.Tools, Tags: en.Tags,
			Language: en.Language, Model: en.Model, Success: en.Success,
		}, true
	}
	if en.Offset >= 0 {
		if e, ok := s.readAt(en.Offset); ok && e.ID == en.ID {
			return e, true
		}
	}
	return s.scanFor(en.ID)
}

// Prune drops stale, weak and excess facts.
func (s *Facts) Prune(policy PrunePolicy) int {
	policy = policy.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(policy)
}

func (s *Facts) pruneLocked(policy PrunePolicy) int {
	now := s.now()
	before := len(s.order)
	type scored struct {
		id string
		sc float64
	}
	var keep []scored
	for _, id := range s.order {
		f, ok := s.byID[id]
		if !ok {
			continue
		}
		if f.Pinned {
			keep = append(keep, scored{id, 2})
			continue
		}
		if policy.MinFactConfidence > 0 && f.Confidence < policy.MinFactConfidence {
			continue
		}
		if policy.MaxFactAge > 0 && !f.LastSeen.IsZero() && now.Sub(f.LastSeen) > policy.MaxFactAge {
			continue
		}
		keep = append(keep, scored{id, f.Score(now)})
	}
	if policy.MaxFacts > 0 && len(keep) > policy.MaxFacts {
		sort.SliceStable(keep, func(i, j int) bool { return keep[i].sc > keep[j].sc })
		keep = keep[:policy.MaxFacts]
	}
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k.id] = true
	}
	order := s.order[:0]
	for _, id := range s.order {
		if keepSet[id] {
			order = append(order, id)
			continue
		}
		delete(s.byID, id)
	}
	s.order = order
	if before != len(s.order) {
		s.dirty = true
	}
	return before - len(s.order)
}

// Prune drops stale and excess procedures.
func (s *Procedures) Prune(policy PrunePolicy) int {
	policy = policy.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(policy)
}

func (s *Procedures) pruneLocked(policy PrunePolicy) int {
	now := s.now()
	before := len(s.order)
	type scored struct {
		id string
		sc float64
	}
	var keep []scored
	for _, id := range s.order {
		p, ok := s.byID[id]
		if !ok {
			continue
		}
		if policy.MaxProcedureAge > 0 && !p.LastUsed.IsZero() && now.Sub(p.LastUsed) > policy.MaxProcedureAge {
			continue
		}
		keep = append(keep, scored{id, float64(p.Samples())})
	}
	if policy.MaxProcedures > 0 && len(keep) > policy.MaxProcedures {
		sort.SliceStable(keep, func(i, j int) bool { return keep[i].sc > keep[j].sc })
		keep = keep[:policy.MaxProcedures]
	}
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k.id] = true
	}
	order := s.order[:0]
	for _, id := range s.order {
		if keepSet[id] {
			order = append(order, id)
			continue
		}
		delete(s.byID, id)
	}
	s.order = order
	if before != len(s.order) {
		s.dirty = true
	}
	return before - len(s.order)
}

// Scope names a memory layer for Forget.
type Scope string

const (
	ScopeWorking    Scope = "working"
	ScopeEpisodic   Scope = "episodic"
	ScopeSemantic   Scope = "semantic"
	ScopeProcedural Scope = "procedural"
	ScopeProject    Scope = "project" // episodic + semantic
	ScopeAll        Scope = "all"
)

// Forget erases a memory layer, on disk and in process. This is the supported
// reversal path: `mem.Forget(memory.ScopeAll)` is equivalent to deleting
// .slmcode/memory and ~/.slmcode/memory by hand, and neither breaks anything.
func (s *Store) Forget(scope Scope) error {
	if s.readOnly {
		return nil
	}
	var errs []error
	forgetProject := func() {
		s.working.Reset()
		if s.memDir != "" {
			for _, name := range []string{"episodes.jsonl", "episodes.index.json", "facts.json", "facts.json.corrupt", "SEMANTIC.md", "WORKING.md", "REFLECTION.md"} {
				if err := os.Remove(filepath.Join(s.memDir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
		s.episodes = openEpisodes(s.memDir, s.limits.MaxEpisodes, s.now)
		s.facts = openFacts(s.memDir, s.limits.MaxFacts, s.now, s.count)
	}
	switch scope {
	case ScopeWorking:
		s.working.Reset()
	case ScopeEpisodic:
		if s.memDir != "" {
			for _, name := range []string{"episodes.jsonl", "episodes.index.json"} {
				if err := os.Remove(filepath.Join(s.memDir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
		s.episodes = openEpisodes(s.memDir, s.limits.MaxEpisodes, s.now)
	case ScopeSemantic:
		if s.memDir != "" {
			for _, name := range []string{"facts.json", "facts.json.corrupt", "SEMANTIC.md"} {
				if err := os.Remove(filepath.Join(s.memDir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
		s.facts = openFacts(s.memDir, s.limits.MaxFacts, s.now, s.count)
	case ScopeProcedural:
		if s.userMemDir != "" {
			for _, name := range []string{"procedures.json", "procedures.json.corrupt", "PROCEDURES.md"} {
				if err := os.Remove(filepath.Join(s.userMemDir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
		s.procedures = openProcedures(s.userMemDir, s.limits.MaxProcedures, s.now, s.count)
	case ScopeProject:
		forgetProject()
	case ScopeAll:
		forgetProject()
		if err := s.Forget(ScopeProcedural); err != nil {
			errs = append(errs, err)
		}
	default:
		return errors.New("memory: unknown scope " + string(scope))
	}
	return errors.Join(errs...)
}

// Reset deletes both memory directories outright. Equivalent to
// `rm -rf <projectDir>/.slmcode/memory ~/.slmcode/memory`.
func Reset(projectDir, userDir string) error {
	var errs []error
	if projectDir != "" {
		if err := os.RemoveAll(filepath.Join(projectDir, SlmDirName, DirName)); err != nil {
			errs = append(errs, err)
		}
	}
	if userDir != "" {
		if err := os.RemoveAll(filepath.Join(userDir, SlmDirName, DirName)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
