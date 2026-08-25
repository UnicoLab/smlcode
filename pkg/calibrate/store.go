package calibrate

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

// Calibration profiles are USER-scoped, next to the other cross-project stores
// (~/.slmcode/memory/): what a model server can do is a property of this
// machine and this endpoint, not of one repository, and re-measuring it per
// project would be pure waste.
//
// The house contract is the same as pkg/memory's stores: bounded, prunable,
// corruption-safe with a `.corrupt` quarantine, non-fatal Warnings(), a
// Markdown mirror for humans, and `rm -rf` fully supported — deleting the
// directory costs one re-probe, never a broken workspace.

// Store caps.
const (
	// FileName is the on-disk store, beside procedures.json and latency.json.
	FileName = "calibration.json"
	// MDFileName is the human-readable mirror.
	MDFileName = "CALIBRATION.md"
	// DirName is the user-scoped directory the store shares with pkg/memory.
	DirName = "memory"
	// SlmDirName is the state directory that holds it.
	SlmDirName = ".slmcode"

	// DefaultMaxProfiles bounds how many (model, endpoint) pairs are kept.
	// Someone who tries every model in a catalog should not grow an unbounded
	// file; the least recently measured are dropped first.
	DefaultMaxProfiles = 50

	// DefaultTTL is how long a profile stays trustworthy. Hardware, server
	// flags, quantization and a model re-download all change the answer, so a
	// month-old measurement is not evidence about today's setup. It matches
	// backends.ThroughputTTL for the same reason.
	DefaultTTL = 30 * 24 * time.Hour
	// PartialTTL is how long a profile that did not finish measuring is
	// honored. Short on purpose: a partial profile is usually the fingerprint
	// of a cold server, which is a condition that clears itself in minutes,
	// and its verdict is almost always the degenerate max_parallel=1. See
	// Profile.Current.
	PartialTTL = time.Hour
)

type storeFile struct {
	Version  int       `json:"version"`
	Updated  time.Time `json:"updated"`
	Profiles []Profile `json:"profiles"`
}

// Store is the user-scoped calibration store. It is always usable: a corrupt
// or unwritable file degrades to in-memory operation and a warning.
type Store struct {
	mu       sync.RWMutex
	dir      string
	byID     map[string]*Profile
	order    []string
	max      int
	dirty    bool
	warnings []string
	now      func() time.Time
}

// UserDir returns the store directory for a home directory. An empty home
// resolves to os.UserHomeDir; when that fails the store runs in memory.
func UserDir(home string) string {
	h := strings.TrimSpace(home)
	if h == "" {
		var err error
		if h, err = os.UserHomeDir(); err != nil || h == "" {
			return ""
		}
	}
	return filepath.Join(h, SlmDirName, DirName)
}

// Open opens (or creates) the calibration store rooted at dir. An empty dir
// yields a fully in-memory store, which is the right behavior for tests and
// for `slmcode` invoked where no home is writable.
func Open(dir string) *Store {
	return OpenWith(dir, DefaultMaxProfiles, nil)
}

// OpenWith is Open with an explicit cap and clock (tests).
func OpenWith(dir string, max int, now func() time.Time) *Store {
	if max <= 0 {
		max = DefaultMaxProfiles
	}
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: strings.TrimSpace(dir), byID: map[string]*Profile{}, max: max, now: now}
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o750); err != nil {
			s.warnings = append(s.warnings, "calibration store disabled: "+err.Error())
			s.dir = ""
		}
	}
	s.load()
	return s
}

// Path is the store file ("" when the store is in-memory only).
func (s *Store) Path() string {
	if s == nil || s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, FileName)
}

func (s *Store) mdPath() string {
	if s == nil || s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, MDFileName)
}

// load reads calibration.json. A truncated, corrupt or hand-mangled file is
// never fatal: it is quarantined as <name>.corrupt and the store starts empty,
// exactly like procedures.json and latency.json.
func (s *Store) load() {
	path := s.Path()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the caller's own store dir
	if err != nil {
		return
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		s.warnings = append(s.warnings, FileName+" unreadable; starting empty (kept as "+FileName+".corrupt)")
		_ = os.Rename(path, path+".corrupt")
		return
	}
	for i := range sf.Profiles {
		p := sf.Profiles[i]
		p.Key = p.Key.Normalize()
		if p.Key.Model == "" || p.MaxParallel <= 0 {
			// A hand-edited file can carry nonsense. Drop it rather than
			// letting a zero knee stall every wave to one worker.
			continue
		}
		p.ID = p.Key.ID()
		if _, dup := s.byID[p.ID]; dup {
			continue
		}
		if p.MaxParallel > MaxConcurrencyLevel {
			p.MaxParallel = MaxConcurrencyLevel
		}
		s.byID[p.ID] = &p
		s.order = append(s.order, p.ID)
	}
}

// Lookup returns the profile for an exact (model, endpoint) pair and whether
// it is current — the right generation and inside the TTL. A stored profile
// that is not current is still returned, so callers can show it and say why it
// is being re-measured.
func (s *Store) Lookup(model, endpoint string) (Profile, bool) {
	return s.LookupWithTTL(model, endpoint, DefaultTTL)
}

// LookupWithTTL is Lookup with an explicit freshness window, so a caller that
// wants to re-measure more often than the store's default retention can say so
// without the store having to guess.
func (s *Store) LookupWithTTL(model, endpoint string, ttl time.Duration) (Profile, bool) {
	if s == nil {
		return Profile{}, false
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	key := Key{Model: model, Endpoint: endpoint}.Normalize()
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[key.ID()]
	if !ok {
		return Profile{}, false
	}
	out := *p
	out.Levels = append([]Level(nil), p.Levels...)
	return out, out.Current(s.now(), ttl)
}

// Put stores a profile, replacing any previous measurement of the same pair.
func (s *Store) Put(p Profile) {
	if s == nil {
		return
	}
	p.Key = p.Key.Normalize()
	if p.Key.Model == "" || p.MaxParallel <= 0 {
		return
	}
	p.ID = p.Key.ID()
	if p.MeasuredAt.IsZero() {
		p.MeasuredAt = s.now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	if _, exists := s.byID[p.ID]; !exists {
		s.order = append(s.order, p.ID)
	}
	s.byID[p.ID] = &p
	s.enforceCapLocked()
}

// Forget drops one pair. It reports whether anything was removed.
func (s *Store) Forget(model, endpoint string) bool {
	if s == nil {
		return false
	}
	id := Key{Model: model, Endpoint: endpoint}.Normalize().ID()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return false
	}
	delete(s.byID, id)
	order := s.order[:0]
	for _, o := range s.order {
		if o != id {
			order = append(order, o)
		}
	}
	s.order = order
	s.dirty = true
	return true
}

// Count is how many profiles are stored.
func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// All returns every profile in a deterministic order (model, then endpoint).
func (s *Store) All() []Profile {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allLocked()
}

func (s *Store) allLocked() []Profile {
	out := make([]Profile, 0, len(s.order))
	for _, id := range s.order {
		p, ok := s.byID[id]
		if !ok {
			continue
		}
		cp := *p
		cp.Levels = append([]Level(nil), p.Levels...)
		out = append(out, cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key.Model != out[j].Key.Model {
			return out[i].Key.Model < out[j].Key.Model
		}
		return out[i].Key.Endpoint < out[j].Key.Endpoint
	})
	return out
}

// Warnings returns non-fatal load problems. Callers surface these but must
// never abort a run over them.
func (s *Store) Warnings() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// Prune drops profiles older than ttl and trims to the cap, newest first.
// It returns how many were removed.
func (s *Store) Prune(ttl time.Duration, max int) int {
	if s == nil {
		return 0
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if max <= 0 {
		max = s.max
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(ttl, max)
}

func (s *Store) enforceCapLocked() {
	if s.max <= 0 || len(s.order) <= s.max {
		return
	}
	s.pruneLocked(DefaultTTL, s.max)
}

func (s *Store) pruneLocked(ttl time.Duration, max int) int {
	now := s.now()
	before := len(s.order)
	keep := make([]string, 0, len(s.order))
	for _, id := range s.order {
		p, ok := s.byID[id]
		if !ok {
			continue
		}
		if ttl > 0 && p.Age(now) > ttl {
			continue
		}
		keep = append(keep, id)
	}
	if max > 0 && len(keep) > max {
		// Newest measurements win: the oldest are both least likely to still
		// be true and least likely to be used again.
		sort.SliceStable(keep, func(i, j int) bool {
			return s.byID[keep[i]].MeasuredAt.After(s.byID[keep[j]].MeasuredAt)
		})
		keep = keep[:max]
	}
	keepSet := make(map[string]bool, len(keep))
	for _, id := range keep {
		keepSet[id] = true
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

// Flush persists calibration.json plus a Markdown mirror. It is a no-op when
// nothing changed or the store is in-memory.
func (s *Store) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	dirty := s.dirty
	dir := s.dir
	sf := storeFile{Version: CalibratorVersion, Updated: s.now().UTC(), Profiles: s.allLocked()}
	s.dirty = false
	s.mu.Unlock()

	if dir == "" || !dirty {
		return nil
	}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.Path(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	return atomicfile.Write(s.mdPath(), []byte(renderMarkdown(sf)), 0o600)
}

// Close flushes the store.
func (s *Store) Close() error { return s.Flush() }

func renderMarkdown(sf storeFile) string {
	var b strings.Builder
	b.WriteString("# Endpoint calibration\n\n")
	b.WriteString("_What each (model, endpoint) pair was measured to do: the concurrency knee, a solo latency baseline, decode rate and the context window the server reports. Delete this directory to force a re-measurement._\n\n")
	b.WriteString("| Model | Endpoint | max_parallel | p50 | p95 | tok/s | ctx | measured |\n")
	b.WriteString("|-------|----------|--------------|-----|-----|-------|-----|----------|\n")
	for _, p := range sf.Profiles {
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s | %s | %s |\n",
			orDash(p.Key.Model), orDash(p.Key.Endpoint), p.MaxParallel,
			msOrDash(p.P50Ms), msOrDash(p.P95Ms), floatOrDash(p.TokensPerSec),
			intOrDash(p.ContextLimit), p.MeasuredAt.UTC().Format(time.RFC3339))
	}
	b.WriteString("\n## Concurrency evidence\n\n")
	for _, p := range sf.Profiles {
		if len(p.Levels) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", p.Key.String())
		b.WriteString("| Concurrency | Wall | Per request | Throughput | Efficiency |\n")
		b.WriteString("|-------------|------|-------------|------------|------------|\n")
		for _, l := range p.Levels {
			fmt.Fprintf(&b, "| %d | %s | %s | %.2fx | %.0f%% |\n",
				l.Concurrency, msOrDash(l.WallMs), msOrDash(l.PerRequestMs),
				l.Throughput, l.Efficiency*100)
		}
		fmt.Fprintf(&b, "\nChosen: **%d** (efficiency floor %.0f%%)\n\n",
			p.MaxParallel, p.FloorUsed*100)
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func msOrDash(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String()
}

func intOrDash(n int) string {
	if n <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}

func floatOrDash(f float64) string {
	if f <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", f)
}
