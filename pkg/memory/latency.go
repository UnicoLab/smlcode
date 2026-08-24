package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Latency memory: how long each ROLE actually takes on a given MODEL FAMILY,
// measured across projects.
//
// Why this is a sibling of procedures.json rather than a Procedure:
// a Procedure is two counters behind a Beta posterior — it answers "does this
// option work?". A timeout budget needs the SHAPE of a distribution: a high
// quantile of the observed durations, so the tail that actually causes the
// timeout is represented. Two counters cannot reconstruct that, and encoding
// milliseconds into Successes/Failures would be an abuse of the type. So the
// payload differs while everything else is shared with procedural memory: the
// same user-scoped directory (~/.slmcode/memory), the same model-family
// folding (ModelFamily), the same bounded + prunable + corruption-safe
// contract, and the same "rm -rf is supported" promise.
//
// The namespace is (role, model family) and it NEVER widens. Procedural
// memory can fall back from "this model in this language" to "any model",
// because a lesson about edit formats partly generalizes. A duration does not:
// a 1.2B model and a 30B model differ by more than an order of magnitude, so
// borrowing one family's latency for another is worse than having no data at
// all — and "no data" already has a safe answer (the full budget).
//
// Language is deliberately not part of the key. Latency is a property of model
// speed times role shape; the project's language changes what the model is
// asked to do, not how many tokens per second it emits. Splitting by language
// would multiply the cold-start period for no gain.

// Latency store caps.
const (
	// DefaultMaxLatencyKeys bounds how many (role, family) pairs are kept.
	DefaultMaxLatencyKeys = 200
	// DefaultLatencySamples is how many recent durations are retained per key.
	// Enough to compute a stable high quantile, small enough that the file
	// stays a few kilobytes.
	DefaultLatencySamples = 32
	// MinLatencySamples is the evidence a caller must have before trusting a
	// measured quantile. Below it, callers must fall back to their generous
	// default — being slow once is far cheaper than failing every run.
	MinLatencySamples = 3
)

// LatencyKey namespaces one observed duration series.
type LatencyKey struct {
	Role        string `json:"role"`
	ModelFamily string `json:"model_family,omitempty"`
}

// Normalize lowercases and trims the key fields.
func (k LatencyKey) Normalize() LatencyKey {
	k.Role = strings.ToLower(strings.TrimSpace(k.Role))
	k.ModelFamily = strings.ToLower(strings.TrimSpace(k.ModelFamily))
	return k
}

// ID is the stable identity of a namespaced series.
func (k LatencyKey) ID() string {
	k = k.Normalize()
	return hashID("l_", k.Role, k.ModelFamily)
}

func (k LatencyKey) String() string {
	k = k.Normalize()
	return fmt.Sprintf("%s [%s]", k.Role, orAny(k.ModelFamily))
}

// RoleLatency is the retained duration series for one (role, model family).
type RoleLatency struct {
	ID  string     `json:"id"`
	Key LatencyKey `json:"key"`
	// SamplesMs holds the most recent observations, oldest first. Bounded.
	SamplesMs []int64 `json:"samples_ms"`
	// Observations counts everything ever recorded, retained or not.
	Observations int64     `json:"observations"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}

// Count is how many retained samples back this series.
func (r RoleLatency) Count() int { return len(r.SamplesMs) }

// Quantile returns the nearest-rank quantile of the retained samples.
//
// Nearest rank (index = ceil(q*n)-1 over the ascending samples) is used rather
// than an interpolated quantile because it is exact integer arithmetic on the
// recorded values: the same samples always yield the same duration, on every
// platform, with no floating-point drift in the result. With few samples a
// high quantile degenerates to the maximum, which is the conservative answer a
// timeout budget wants.
func (r RoleLatency) Quantile(q float64) time.Duration {
	n := len(r.SamplesMs)
	if n == 0 {
		return 0
	}
	if q < 0 || math.IsNaN(q) {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	sorted := make([]int64, n)
	copy(sorted, r.SamplesMs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(q*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return time.Duration(sorted[idx]) * time.Millisecond
}

// P95 is the 95th percentile of the retained samples.
func (r RoleLatency) P95() time.Duration { return r.Quantile(0.95) }

// Max is the slowest retained sample.
func (r RoleLatency) Max() time.Duration { return r.Quantile(1) }

type latencyFile struct {
	Version int           `json:"version"`
	Updated time.Time     `json:"updated"`
	Roles   []RoleLatency `json:"roles"`
}

// Latencies is user-scoped, cross-project latency memory.
type Latencies struct {
	mu       sync.RWMutex
	dir      string
	byID     map[string]*RoleLatency
	order    []string
	max      int // maximum number of (role, family) keys
	keep     int // retained samples per key
	dirty    bool
	warnings []string
	now      func() time.Time
}

func openLatencies(dir string, max int, now func() time.Time) *Latencies {
	if max <= 0 {
		max = DefaultMaxLatencyKeys
	}
	if now == nil {
		now = time.Now
	}
	l := &Latencies{
		dir: dir, byID: map[string]*RoleLatency{},
		max: max, keep: DefaultLatencySamples, now: now,
	}
	l.load()
	return l
}

func (s *Latencies) path() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "latency.json")
}

func (s *Latencies) mdPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "LATENCY.md")
}

// load reads latency.json. A truncated, corrupt or hand-mangled file is never
// fatal: it is set aside and the store starts empty, exactly like procedures.
func (s *Latencies) load() {
	path := s.path()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		return
	}
	var lf latencyFile
	if err := json.Unmarshal(data, &lf); err != nil {
		s.warnings = append(s.warnings, "latency.json unreadable; starting empty")
		_ = os.Rename(path, path+".corrupt")
		return
	}
	for i := range lf.Roles {
		r := lf.Roles[i]
		r.Key = r.Key.Normalize()
		if r.Key.Role == "" {
			continue
		}
		r.ID = r.Key.ID()
		if _, dup := s.byID[r.ID]; dup {
			continue
		}
		// A hand-edited file can carry nonsense: drop non-positive durations
		// and re-apply the retention bound rather than trusting the input.
		clean := make([]int64, 0, len(r.SamplesMs))
		for _, ms := range r.SamplesMs {
			if ms > 0 {
				clean = append(clean, ms)
			}
		}
		r.SamplesMs = tailInts(clean, s.keep)
		if len(r.SamplesMs) == 0 {
			continue
		}
		if r.Observations < int64(len(r.SamplesMs)) {
			r.Observations = int64(len(r.SamplesMs))
		}
		s.byID[r.ID] = &r
		s.order = append(s.order, r.ID)
	}
}

// Record folds one observed duration into latency memory. Non-positive
// durations are ignored — they are a clock artifact, not evidence.
func (s *Latencies) Record(key LatencyKey, d time.Duration) RoleLatency {
	key = key.Normalize()
	if key.Role == "" || d <= 0 {
		return RoleLatency{}
	}
	ms := d.Milliseconds()
	if ms <= 0 {
		ms = 1 // sub-millisecond calls are still observations
	}
	id := key.ID()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true

	r, exists := s.byID[id]
	if !exists {
		r = &RoleLatency{ID: id, Key: key, FirstSeen: now}
		s.byID[id] = r
		s.order = append(s.order, id)
	}
	r.SamplesMs = tailInts(append(r.SamplesMs, ms), s.keep)
	r.Observations++
	r.LastSeen = now
	s.enforceCapLocked()
	return *r
}

// Get returns one exact namespaced series.
func (s *Latencies) Get(key LatencyKey) (RoleLatency, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[key.ID()]
	if !ok {
		return RoleLatency{}, false
	}
	out := *r
	out.SamplesMs = append([]int64(nil), r.SamplesMs...)
	return out, true
}

// Quantile returns the measured quantile for one role on one model family and
// how many samples back it. It never widens the namespace: a family with no
// evidence returns (0, 0) so the caller falls back to its generous default.
func (s *Latencies) Quantile(role, modelFamily string, q float64) (time.Duration, int) {
	r, ok := s.Get(LatencyKey{Role: role, ModelFamily: modelFamily})
	if !ok {
		return 0, 0
	}
	return r.Quantile(q), r.Count()
}

// P95 is Quantile at 0.95 — the value a timeout budget should be derived from.
func (s *Latencies) P95(role, modelFamily string) (time.Duration, int) {
	return s.Quantile(role, modelFamily, 0.95)
}

// Count returns how many (role, family) series are stored.
func (s *Latencies) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// All returns every series in a deterministic order (role, then family).
func (s *Latencies) All() []RoleLatency {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RoleLatency, 0, len(s.order))
	for _, id := range s.order {
		r, ok := s.byID[id]
		if !ok {
			continue
		}
		cp := *r
		cp.SamplesMs = append([]int64(nil), r.SamplesMs...)
		out = append(out, cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key.Role != out[j].Key.Role {
			return out[i].Key.Role < out[j].Key.Role
		}
		return out[i].Key.ModelFamily < out[j].Key.ModelFamily
	})
	return out
}

func (s *Latencies) enforceCapLocked() {
	if s.max <= 0 || len(s.order) <= s.max*2 {
		return
	}
	s.pruneLocked(PrunePolicy{MaxLatencyKeys: s.max})
}

// Flush persists latency.json plus a Markdown mirror.
func (s *Latencies) Flush() error {
	s.mu.Lock()
	dirty := s.dirty
	dir := s.dir
	lf := latencyFile{Version: 1, Updated: s.now().UTC()}
	for _, id := range s.order {
		if r, ok := s.byID[id]; ok {
			cp := *r
			cp.SamplesMs = append([]int64(nil), r.SamplesMs...)
			lf.Roles = append(lf.Roles, cp)
		}
	}
	s.dirty = false
	s.mu.Unlock()

	if dir == "" || !dirty {
		return nil
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path(), append(data, '\n'), 0o600); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Role latency (cross-project)\n\n")
	b.WriteString("_How long each role actually takes on each model family. Timeout budgets are derived from p95, never from a fraction of the configured task_timeout._\n\n")
	b.WriteString("| Role | Model family | p50 | p95 | max | samples | observations |\n")
	b.WriteString("|------|--------------|-----|-----|-----|---------|--------------|\n")
	for _, r := range sortLatencies(lf.Roles) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %d |\n",
			r.Key.Role, orAny(r.Key.ModelFamily),
			r.Quantile(0.5).Round(time.Millisecond),
			r.P95().Round(time.Millisecond),
			r.Max().Round(time.Millisecond),
			r.Count(), r.Observations)
	}
	return atomicfile.Write(s.mdPath(), []byte(b.String()), 0o600)
}

// Warnings returns non-fatal load problems.
func (s *Latencies) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// Prune drops stale and excess series.
func (s *Latencies) Prune(policy PrunePolicy) int {
	policy = policy.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(policy)
}

func (s *Latencies) pruneLocked(policy PrunePolicy) int {
	now := s.now()
	before := len(s.order)
	type scored struct {
		id string
		sc int
	}
	var keep []scored
	for _, id := range s.order {
		r, ok := s.byID[id]
		if !ok {
			continue
		}
		if policy.MaxLatencyAge > 0 && !r.LastSeen.IsZero() && now.Sub(r.LastSeen) > policy.MaxLatencyAge {
			continue
		}
		keep = append(keep, scored{id, r.Count()})
	}
	if policy.MaxLatencyKeys > 0 && len(keep) > policy.MaxLatencyKeys {
		sort.SliceStable(keep, func(i, j int) bool { return keep[i].sc > keep[j].sc })
		keep = keep[:policy.MaxLatencyKeys]
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

// sortLatencies orders a slice for rendering without touching the input.
func sortLatencies(in []RoleLatency) []RoleLatency {
	out := append([]RoleLatency(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key.Role != out[j].Key.Role {
			return out[i].Key.Role < out[j].Key.Role
		}
		return out[i].Key.ModelFamily < out[j].Key.ModelFamily
	})
	return out
}

// tailInts keeps the last max entries of in.
func tailInts(in []int64, max int) []int64 {
	if max <= 0 || len(in) <= max {
		return in
	}
	return append([]int64(nil), in[len(in)-max:]...)
}
