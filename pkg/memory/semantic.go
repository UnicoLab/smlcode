package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// FactKind partitions semantic memory. The kind is part of a fact's identity,
// so "the test command" and "the build command" never collide.
type FactKind string

const (
	FactCommand    FactKind = "command"    // a build/test/lint command that actually worked
	FactLayout     FactKind = "layout"     // where things live in this repo
	FactConvention FactKind = "convention" // naming/style/process observed to hold
	FactGotcha     FactKind = "gotcha"     // a trap that cost a run
	FactFile       FactKind = "file"       // a per-file summary
	FactDependency FactKind = "dependency" // toolchain / module facts
)

var factKindOrder = []FactKind{FactCommand, FactGotcha, FactLayout, FactConvention, FactDependency, FactFile}

// Semantic-memory caps.
const (
	MaxFactSources     = 5
	MaxFactTextLen     = 240
	DefaultSemanticTk  = 400
	DefaultMaxFacts    = 200
	factHalfLifeDays   = 60
	renderMaxPerKind   = 6
	minRenderConfiden  = 0.34
	contradictionRatio = 1.0
)

// Fact is one durable, deduplicated claim about the project.
type Fact struct {
	ID         string    `json:"id"`
	Kind       FactKind  `json:"kind"`
	Subject    string    `json:"subject"`
	Text       string    `json:"text"`
	Support    int       `json:"support"`
	Contradict int       `json:"contradict,omitempty"`
	Confidence float64   `json:"confidence"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Sources    []string  `json:"sources,omitempty"`
	Pinned     bool      `json:"pinned,omitempty"` // user-authored: never pruned, never decayed
}

// FactID is the stable identity of a fact: kind plus normalized subject.
func FactID(kind FactKind, subject string) string {
	return hashID("f_", string(kind), strings.ToLower(strings.TrimSpace(subject)))
}

// confidence is the posterior mean of a Beta(1,1)-prior Bernoulli over
// "this fact held when observed". One observation gives 0.67, not 1.0 — a
// single sighting is a hint, not a law.
func (f *Fact) recompute() {
	f.Confidence = float64(f.Support+1) / float64(f.Support+f.Contradict+2)
}

// Score is the ranking value: confidence decayed by staleness. Pinned facts
// never decay.
func (f Fact) Score(now time.Time) float64 {
	if f.Pinned {
		return 1
	}
	return f.Confidence * recency(f.LastSeen, now, factHalfLifeDays)
}

type factsFile struct {
	Version int       `json:"version"`
	Updated time.Time `json:"updated"`
	Facts   []Fact    `json:"facts"`
}

// Facts is the semantic (distilled, project-scoped) memory store.
type Facts struct {
	mu       sync.RWMutex
	dir      string
	byID     map[string]*Fact
	order    []string
	max      int
	dirty    bool
	warnings []string
	now      func() time.Time
	count    TokenCounter
}

func openFacts(dir string, max int, now func() time.Time, count TokenCounter) *Facts {
	if max <= 0 {
		max = DefaultMaxFacts
	}
	if now == nil {
		now = time.Now
	}
	f := &Facts{dir: dir, byID: map[string]*Fact{}, max: max, now: now, count: count}
	f.load()
	return f
}

func (s *Facts) path() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "facts.json")
}

func (s *Facts) mdPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "SEMANTIC.md")
}

func (s *Facts) load() {
	path := s.path()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		return
	}
	var ff factsFile
	if err := json.Unmarshal(data, &ff); err != nil {
		// Corrupt store: start clean rather than refusing to run. The bad file
		// is preserved as .corrupt so nothing is silently destroyed.
		s.warnings = append(s.warnings, "facts.json unreadable; starting empty")
		_ = os.Rename(path, path+".corrupt")
		return
	}
	for i := range ff.Facts {
		f := ff.Facts[i]
		if f.Subject == "" && f.Text == "" {
			continue
		}
		if f.ID == "" {
			f.ID = FactID(f.Kind, f.Subject)
		}
		if f.Confidence <= 0 {
			f.recompute()
		}
		if _, dup := s.byID[f.ID]; dup {
			continue
		}
		s.byID[f.ID] = &f
		s.order = append(s.order, f.ID)
	}
}

// Observe folds an observation into semantic memory.
//
// Same subject + same text  → support increases (the fact is being confirmed).
// Same subject + new text   → the old text is contradicted; when the new claim
//
//	has outweighed the old one, the text is replaced
//	and the counters reset. That is how a fact decays
//	when the project changes under it.
func (s *Facts) Observe(f Fact) Fact {
	now := s.now()
	f.Kind = FactKind(strings.ToLower(strings.TrimSpace(string(f.Kind))))
	if f.Kind == "" {
		f.Kind = FactConvention
	}
	f.Subject = clip(f.Subject, 160)
	f.Text = clip(f.Text, MaxFactTextLen)
	if f.Subject == "" || f.Text == "" {
		return Fact{}
	}
	f.ID = FactID(f.Kind, f.Subject)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true

	cur, ok := s.byID[f.ID]
	if !ok {
		nf := f
		nf.Support = 1
		nf.Contradict = 0
		nf.FirstSeen = now
		nf.LastSeen = now
		nf.Sources = dedupe(f.Sources, MaxFactSources)
		nf.recompute()
		s.byID[nf.ID] = &nf
		s.order = append(s.order, nf.ID)
		s.enforceCapLocked()
		return nf
	}

	cur.LastSeen = now
	cur.Sources = dedupe(append(cur.Sources, f.Sources...), MaxFactSources)
	if sameClaim(cur.Text, f.Text) {
		cur.Support++
		// Refresh the wording so embedded evidence counts stay current. This
		// is NOT a contradiction: "works here (2/2 runs)" and "works here
		// (7/8 runs)" are the same claim with fresher arithmetic, and treating
		// them as rival claims would make every re-distillation thrash the
		// store back and forth forever.
		cur.Text = f.Text
	} else {
		cur.Contradict++
		if float64(cur.Contradict) > float64(cur.Support)*contradictionRatio && !cur.Pinned {
			cur.Text = f.Text
			cur.Support = 1
			cur.Contradict = 0
		}
	}
	cur.recompute()
	return *cur
}

// Refute records evidence against an existing fact (e.g. a command that used
// to work just failed). Unknown ids are ignored.
func (s *Facts) Refute(kind FactKind, subject string) bool {
	id := FactID(kind, subject)
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.byID[id]
	if !ok || cur.Pinned {
		return false
	}
	cur.Contradict++
	cur.LastSeen = s.now()
	cur.recompute()
	s.dirty = true
	return true
}

// Get returns one fact.
func (s *Facts) Get(kind FactKind, subject string) (Fact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.byID[FactID(kind, subject)]
	if !ok {
		return Fact{}, false
	}
	return *f, true
}

// All returns every fact, ranked best first.
func (s *Facts) All() []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rankedLocked(nil)
}

// OfKind returns the facts of one kind, ranked best first.
func (s *Facts) OfKind(kind FactKind) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rankedLocked(&kind)
}

// Count returns how many facts are stored.
func (s *Facts) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

func (s *Facts) rankedLocked(kind *FactKind) []Fact {
	now := s.now()
	out := make([]Fact, 0, len(s.order))
	for _, id := range s.order {
		f, ok := s.byID[id]
		if !ok || (kind != nil && f.Kind != *kind) {
			continue
		}
		out = append(out, *f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].Score(now), out[j].Score(now)
		if si != sj {
			return si > sj
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func (s *Facts) enforceCapLocked() {
	if s.max <= 0 || len(s.order) <= s.max*2 {
		return
	}
	s.pruneLocked(PrunePolicy{MaxFacts: s.max, MinFactConfidence: 0})
}

// Render emits the injectable semantic block, at most budgetTokens (0 uses
// DefaultSemanticTk). Facts are grouped by kind in a fixed, action-first order
// and only the highest-scoring few of each kind are included: precision over
// recall, because a plausible-but-wrong fact is worse than no fact.
func (s *Facts) Render(budgetTokens int) string {
	if budgetTokens <= 0 {
		budgetTokens = DefaultSemanticTk
	}
	s.mu.RLock()
	now := s.now()
	grouped := map[FactKind][]Fact{}
	for _, id := range s.order {
		f, ok := s.byID[id]
		if !ok || f.Score(now) < minRenderConfiden {
			continue
		}
		grouped[f.Kind] = append(grouped[f.Kind], *f)
	}
	s.mu.RUnlock()

	if len(grouped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## What we know about this project\n\n")
	wrote := false
	for _, kind := range factKindOrder {
		list := grouped[kind]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Score(now) > list[j].Score(now) })
		if len(list) > renderMaxPerKind {
			list = list[:renderMaxPerKind]
		}
		fmt.Fprintf(&b, "%s:\n", factKindHeading(kind))
		for _, f := range list {
			fmt.Fprintf(&b, "- %s\n", f.Text)
		}
		b.WriteString("\n")
		wrote = true
	}
	if !wrote {
		return ""
	}
	return fitToTokens(strings.TrimRight(b.String(), "\n")+"\n", budgetTokens, s.count)
}

// sameClaim reports whether two fact texts assert the same thing, ignoring
// numeric drift (counts, percentages, ratios) and case/whitespace.
func sameClaim(a, b string) bool {
	return claimKey(a) == claimKey(b)
}

func claimKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	inNum := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= '0' && r <= '9':
			if !inNum {
				b.WriteByte('#')
				inNum = true
			}
			lastSpace = false
		case r == ' ' || r == '\t' || r == '\n':
			inNum = false
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			inNum = false
			lastSpace = false
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func factKindHeading(k FactKind) string {
	switch k {
	case FactCommand:
		return "Commands that work"
	case FactGotcha:
		return "Gotchas"
	case FactLayout:
		return "Layout"
	case FactConvention:
		return "Conventions"
	case FactDependency:
		return "Toolchain"
	case FactFile:
		return "Files"
	default:
		return strings.ToTitle(string(k))
	}
}

// Flush persists facts.json and the human-readable SEMANTIC.md mirror.
func (s *Facts) Flush() error {
	s.mu.Lock()
	dirty := s.dirty
	dir := s.dir
	ff := factsFile{Version: 1, Updated: s.now().UTC()}
	for _, id := range s.order {
		if f, ok := s.byID[id]; ok {
			ff.Facts = append(ff.Facts, *f)
		}
	}
	s.dirty = false
	s.mu.Unlock()

	if dir == "" || !dirty {
		return nil
	}
	data, err := json.MarshalIndent(ff, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	md := "# Semantic memory\n\n_Distilled from episodes. Edit freely; `pinned: true` facts are never pruned or overwritten._\n\n" +
		s.Render(4000)
	return atomicfile.Write(s.mdPath(), []byte(md), 0o600)
}

// Warnings returns non-fatal load problems.
func (s *Facts) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}
