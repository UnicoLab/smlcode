package contextstore

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Per-document append ceilings, in bytes.
//
// Every prompt-facing document used to grow without bound: knowledge.Evolve
// appended a "## Auto-learned (timestamp)" section to PROJECT.md on EVERY run,
// and Store.Append had the same pattern. PROJECT.md is in the default doc set
// for context/explorer/docs/planner/tester, so an unbounded PROJECT.md silently
// eats a specialist's whole budget.
const (
	DefaultAppendMaxBytes = 48 * 1024
	ProjectAppendMaxBytes = 24 * 1024
	ContextAppendMaxBytes = 32 * 1024
	MemoryAppendMaxBytes  = 32 * 1024
	ScratchAppendMaxBytes = 64 * 1024
)

// AppendPolicy caps a document's size after an append.
type AppendPolicy struct {
	MaxBytes int
}

// DefaultAppendPolicy returns the built-in ceiling for a document name.
func DefaultAppendPolicy(name string) AppendPolicy {
	switch name {
	case DocProject:
		return AppendPolicy{MaxBytes: ProjectAppendMaxBytes}
	case DocContext:
		return AppendPolicy{MaxBytes: ContextAppendMaxBytes}
	case DocMemory:
		return AppendPolicy{MaxBytes: MemoryAppendMaxBytes}
	case DocScratch:
		return AppendPolicy{MaxBytes: ScratchAppendMaxBytes}
	default:
		return AppendPolicy{MaxBytes: DefaultAppendMaxBytes}
	}
}

// SetAppendPolicy overrides the ceiling for one document (0 disables capping).
func (s *Store) SetAppendPolicy(name string, maxBytes int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policies == nil {
		s.policies = map[string]AppendPolicy{}
	}
	s.policies[name] = AppendPolicy{MaxBytes: maxBytes}
}

func (s *Store) policyFor(name string) AppendPolicy {
	if s != nil && s.policies != nil {
		if p, ok := s.policies[name]; ok {
			return p
		}
	}
	return DefaultAppendPolicy(name)
}

// timestampedSectionRe matches "## Title (2024-01-02T03:04:05Z)" headings —
// the shape Append and knowledge.Evolve write.
var timestampedSectionRe = regexp.MustCompile(`(?m)^## .+ \(\d{4}-\d{2}-\d{2}T[0-9:+\-.Z]+\)\s*$`)

// PruneTimestampedSections shrinks md to at most maxBytes by dropping the
// OLDEST timestamped sections first, keeping the document head (title and any
// hand-written structure before the first timestamped section) intact.
func PruneTimestampedSections(md string, maxBytes int) string {
	if maxBytes <= 0 || len(md) <= maxBytes {
		return md
	}
	locs := timestampedSectionRe.FindAllStringIndex(md, -1)
	if len(locs) == 0 {
		// Nothing structured to drop: keep the head, mark the cut.
		return trimToBytes(md, maxBytes)
	}
	head := md[:locs[0][0]]
	type sec struct{ start, end int }
	secs := make([]sec, 0, len(locs))
	for i, loc := range locs {
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		secs = append(secs, sec{start: loc[0], end: end})
	}
	// Drop from the oldest (first) until the rest fits.
	drop := 0
	for drop < len(secs) {
		size := len(head)
		for _, sc := range secs[drop:] {
			size += sc.end - sc.start
		}
		if size <= maxBytes {
			break
		}
		drop++
	}
	if drop >= len(secs) {
		// Even one section does not fit — keep the newest, truncated.
		last := secs[len(secs)-1]
		return trimToBytes(head+md[last.start:last.end], maxBytes)
	}
	var b strings.Builder
	b.WriteString(head)
	if drop > 0 {
		fmt.Fprintf(&b, "_[%d older auto-appended sections pruned to stay within the context budget]_\n\n", drop)
	}
	for _, sc := range secs[drop:] {
		b.WriteString(md[sc.start:sc.end])
	}
	return trimToBytes(b.String(), maxBytes)
}

// Append adds a timestamped section to a document, then enforces the
// document's append policy so it cannot grow without bound.
func (s *Store) Append(name, sectionTitle, body string) error {
	return s.AppendCapped(name, sectionTitle, body, s.policyFor(name).MaxBytes)
}

// AppendCapped is Append with an explicit ceiling (0 = no cap).
func (s *Store) AppendCapped(name, sectionTitle, body string, maxBytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, _ := readFileString(s.Path(name))
	stamp := time.Now().Format(time.RFC3339)
	block := fmt.Sprintf("\n\n## %s (%s)\n\n%s\n", sectionTitle, stamp, strings.TrimSpace(body))
	merged := existing + block
	if maxBytes > 0 {
		merged = PruneTimestampedSections(merged, maxBytes)
	}
	return writeFileString(s.Path(name), merged)
}

// ReplaceSection replaces (or creates) a single "## heading" section in a
// document. This is what a per-run write-back should use instead of appending
// a new timestamped section on every run.
func (s *Store) ReplaceSection(name, heading, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, _ := readFileString(s.Path(name))
	return writeFileString(s.Path(name), replaceSection(existing, heading, body))
}
