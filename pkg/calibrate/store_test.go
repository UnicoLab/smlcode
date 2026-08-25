package calibrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleProfile(model string, at time.Time) Profile {
	return Profile{
		Version:     CalibratorVersion,
		Key:         Key{Model: model, Endpoint: "http://127.0.0.1:8000/v1", Provider: "omlx"}.Normalize(),
		Model:       model,
		Endpoint:    "http://127.0.0.1:8000",
		Provider:    "omlx",
		MaxParallel: 2,
		Levels: []Level{
			{Concurrency: 1, WallMs: 580, PerRequestMs: 580, Throughput: 1, Efficiency: 1},
			{Concurrency: 2, WallMs: 850, PerRequestMs: 820, Throughput: 1.3647, Efficiency: 0.6824},
			{Concurrency: 4, WallMs: 1490, PerRequestMs: 1430, Throughput: 1.557, Efficiency: 0.3893},
		},
		FloorUsed:        DefaultEfficiencyFloor,
		QueueInflation:   1.4138,
		P50Ms:            580,
		P95Ms:            610,
		SoloSamples:      3,
		CompletionTokens: 16,
		TokensPerSec:     27.6,
		ContextLimit:     262144,
		ContextSource:    "GET /v1/models max_model_len",
		MeasuredAt:       at,
	}
}

func TestStoreRoundTripsAProfile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := OpenWith(dir, 0, func() time.Time { return now })
	want := sampleProfile("Qwen3.8-27B-4bit", now)
	s.Put(want)
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	reopened := OpenWith(dir, 0, func() time.Time { return now })
	got, current := reopened.Lookup("Qwen3.8-27B-4bit", "http://127.0.0.1:8000/v1")
	if !current {
		t.Fatal("a freshly written profile must be current")
	}
	if got.MaxParallel != want.MaxParallel || got.ContextLimit != want.ContextLimit ||
		got.TokensPerSec != want.TokensPerSec || len(got.Levels) != len(want.Levels) {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, want)
	}
	if got.Levels[2].Efficiency != want.Levels[2].Efficiency {
		t.Fatalf("efficiency %v != %v — ratios must survive JSON exactly",
			got.Levels[2].Efficiency, want.Levels[2].Efficiency)
	}
	// The evidence is mirrored for humans.
	md, err := os.ReadFile(filepath.Join(dir, MDFileName))
	if err != nil {
		t.Fatalf("markdown mirror: %v", err)
	}
	if !strings.Contains(string(md), "Qwen3.8-27B-4bit") || !strings.Contains(string(md), "Concurrency evidence") {
		t.Fatalf("markdown mirror is missing the evidence:\n%s", md)
	}
}

func TestStoreQuarantinesACorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(dir)
	if s.Count() != 0 {
		t.Fatalf("a corrupt file must start the store empty, got %d", s.Count())
	}
	if len(s.Warnings()) == 0 {
		t.Fatal("a corrupt file must be reported as a warning")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("the corrupt file must be quarantined, not deleted: %v", err)
	}
	// And the store still works.
	now := time.Now()
	s.Put(sampleProfile("m", now))
	if err := s.Flush(); err != nil {
		t.Fatalf("a store that survived corruption must still persist: %v", err)
	}
	if _, ok := Open(dir).Lookup("m", "http://127.0.0.1:8000/v1"); !ok {
		t.Fatal("the profile written after recovery was lost")
	}
}

func TestStoreDropsNonsenseEntriesOnLoad(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"profiles":[
	  {"key":{"model":"","endpoint":"http://h:1"},"max_parallel":2},
	  {"key":{"model":"ok","endpoint":"http://h:1"},"max_parallel":0},
	  {"key":{"model":"huge","endpoint":"http://h:1"},"max_parallel":9999},
	  {"key":{"model":"good","endpoint":"http://h:1"},"max_parallel":2,"version":1,"measured_at":"2026-08-24T12:00:00Z"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := OpenWith(dir, 0, func() time.Time { return time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC) })
	if s.Count() != 2 {
		t.Fatalf("count = %d, want 2 (nameless and zero-knee entries dropped)", s.Count())
	}
	huge, _ := s.Lookup("huge", "http://h:1")
	if huge.MaxParallel != MaxConcurrencyLevel {
		t.Fatalf("a hand-edited 9999 must be clamped to %d, got %d", MaxConcurrencyLevel, huge.MaxParallel)
	}
}

func TestStoreTreatsAnOlderCalibratorAsIncomplete(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := OpenWith(dir, 0, func() time.Time { return now })
	old := sampleProfile("m", now)
	old.Version = CalibratorVersion - 1
	s.Put(old)

	got, current := s.Lookup("m", "http://127.0.0.1:8000/v1")
	if current {
		t.Fatal("a profile from an older calibrator must not count as current")
	}
	if got.MaxParallel == 0 {
		t.Fatal("the stale profile must still be returned so callers can show it")
	}
}

func TestStoreAgesProfilesOut(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := OpenWith(dir, 0, func() time.Time { return now })
	s.Put(sampleProfile("m", now.Add(-DefaultTTL-time.Hour)))
	if _, current := s.Lookup("m", "http://127.0.0.1:8000/v1"); current {
		t.Fatal("a profile older than the TTL must not be current")
	}
	if n := s.Prune(DefaultTTL, 0); n != 1 {
		t.Fatalf("prune removed %d, want 1", n)
	}
	if s.Count() != 0 {
		t.Fatalf("count = %d after pruning the only (stale) profile", s.Count())
	}
}

func TestStoreIsBounded(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	now := base
	s := OpenWith(dir, 3, func() time.Time { return now })
	for i := 0; i < 10; i++ {
		now = base.Add(time.Duration(i) * time.Minute)
		p := sampleProfile("model-"+string(rune('a'+i)), now)
		s.Put(p)
	}
	if s.Count() > 3 {
		t.Fatalf("count = %d, want at most the cap of 3", s.Count())
	}
	// The newest survive.
	if _, ok := s.Lookup("model-j", "http://127.0.0.1:8000/v1"); !ok {
		t.Fatal("the newest profile was evicted")
	}
}

func TestStoreForgetAndRmRfAreSupported(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := OpenWith(dir, 0, func() time.Time { return now })
	s.Put(sampleProfile("m", now))
	if !s.Forget("m", "http://127.0.0.1:8000/v1") {
		t.Fatal("Forget must report the removal")
	}
	if s.Forget("m", "http://127.0.0.1:8000/v1") {
		t.Fatal("Forget must be idempotent")
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// rm -rf the whole store directory: the next Open must work from nothing.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	fresh := Open(dir)
	if fresh.Count() != 0 || len(fresh.Warnings()) != 0 {
		t.Fatalf("a deleted store must reopen empty and quiet: count=%d warnings=%v",
			fresh.Count(), fresh.Warnings())
	}
	fresh.Put(sampleProfile("m", now))
	if err := fresh.Flush(); err != nil {
		t.Fatalf("a recreated store must persist: %v", err)
	}
}

func TestStoreWorksEntirelyInMemory(t *testing.T) {
	s := Open("")
	if s.Path() != "" {
		t.Fatalf("path = %q, want empty for an in-memory store", s.Path())
	}
	s.Put(sampleProfile("m", time.Now()))
	if _, ok := s.Lookup("m", "http://127.0.0.1:8000/v1"); !ok {
		t.Fatal("an in-memory store must still answer lookups")
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flushing an in-memory store must be a no-op, got %v", err)
	}
}

func TestStaleAgainstOnlyFiresOnARealChange(t *testing.T) {
	p := sampleProfile("m", time.Now())
	if p.StaleAgainst(Metadata{ContextLimit: 262144}) {
		t.Fatal("the same window is not staleness")
	}
	if !p.StaleAgainst(Metadata{ContextLimit: 32768}) {
		t.Fatal("a changed window must mark the profile stale")
	}
	if p.StaleAgainst(Metadata{}) {
		t.Fatal("an unreported window must never trigger a re-probe storm")
	}
	unknown := p
	unknown.ContextLimit = 0
	if unknown.StaleAgainst(Metadata{ContextLimit: 4096}) {
		t.Fatal("a profile that never knew the window cannot be stale by it")
	}
}
