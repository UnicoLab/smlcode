package graph

import (
	"encoding/json"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Prune bounds. A zero field means "use the default"; an explicitly negative
// field means "no limit on this axis".
type PrunePolicy struct {
	// MaxEdges caps how many edges survive. The newest are kept.
	MaxEdges int
	// MaxAge drops edges last observed longer ago than this.
	MaxAge time.Duration
}

// DefaultPruneMaxAge is how long an unrefreshed edge survives.
const DefaultPruneMaxAge = 180 * 24 * time.Hour

// DefaultPrunePolicy is what a run applies at the end of a turn.
func DefaultPrunePolicy() PrunePolicy {
	return PrunePolicy{MaxEdges: DefaultMaxEdges, MaxAge: DefaultPruneMaxAge}
}

func (p PrunePolicy) withDefaults() PrunePolicy {
	d := DefaultPrunePolicy()
	if p.MaxEdges == 0 {
		p.MaxEdges = d.MaxEdges
	}
	if p.MaxAge == 0 {
		p.MaxAge = d.MaxAge
	}
	return p
}

// Prune drops aged-out and excess edges and returns how many it removed.
//
// The JSONL log is rewritten so the file actually shrinks: an append-only log
// that is never compacted is an unbounded log, which would make every claim
// about the store's ceiling a lie. The rewrite is atomic — a crash mid-prune
// leaves the previous log intact.
//
// A read-only store returns (0, nil).
func (s *Store) Prune(policy PrunePolicy) (int, error) {
	if s == nil || s.readOnly {
		return 0, nil
	}
	policy = policy.withDefaults()
	s.mu.Lock()
	removed := s.pruneLocked(policy)
	err := s.flushLocked()
	s.mu.Unlock()
	return removed, err
}

func (s *Store) pruneLocked(policy PrunePolicy) int {
	before := len(s.entries)

	cutoff := time.Time{}
	if policy.MaxAge > 0 {
		cutoff = s.now().Add(-policy.MaxAge)
	}
	kept := make([]indexEntry, 0, len(s.entries))
	for _, en := range s.entries {
		if !cutoff.IsZero() && en.At.Before(cutoff) {
			continue
		}
		kept = append(kept, en)
	}
	// Keep the tail: the most recently observed edges are the ones a traversal
	// is most likely to want, and dropping from the front keeps insertion
	// order (and therefore every traversal's tie-breaks) stable.
	if policy.MaxEdges > 0 && len(kept) > policy.MaxEdges {
		kept = kept[len(kept)-policy.MaxEdges:]
	}
	if len(kept) == before {
		return 0
	}

	if path := s.logPath(); path != "" {
		var (
			buf     []byte
			rebuilt = make([]indexEntry, 0, len(kept))
		)
		for _, en := range kept {
			data, err := json.Marshal(en.Edge)
			if err != nil {
				continue
			}
			rebuilt = append(rebuilt, indexEntry{Edge: en.Edge, Offset: int64(len(buf))})
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		if err := atomicfile.Write(path, buf, 0o600); err == nil {
			kept = rebuilt
		}
	}
	s.setEntries(kept)
	s.dirty = true
	return before - len(s.entries)
}
