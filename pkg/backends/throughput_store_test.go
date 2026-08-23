package backends

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The whole point of persisting throughput is that `slmcode doctor` runs in a
// DIFFERENT process from the run that measured it. Simulate that: observe,
// then wipe the in-memory tracker and read it back.
func TestObservedThroughputSurvivesAProcessBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() {
		ResetThroughputStore()
		GlobalThroughput.Reset()
	})
	ResetThroughputStore()
	GlobalThroughput.Reset()
	SetThroughputCacheDir(dir)

	// Two completions of 100 tokens in 5s = 20 tok/s.
	for i := 0; i < 2; i++ {
		observeAndPersist("m1", 100, 5*time.Second)
		// saveThroughput throttles to one write every 5s; force the second.
		throughputStore.mu.Lock()
		throughputStore.lastSave = time.Time{}
		throughputStore.mu.Unlock()
	}
	if _, err := os.Stat(filepath.Join(dir, throughputFileName)); err != nil {
		t.Fatalf("throughput was never written to disk: %v", err)
	}

	// A fresh process: nothing in memory, the same project dir.
	GlobalThroughput.Reset()
	ResetThroughputStore()
	SetThroughputCacheDir(dir)

	tps, samples, ok := ObservedThroughput("m1")
	if !ok {
		t.Fatal("ObservedThroughput reported nothing for a model measured in a previous process")
	}
	if samples != 2 {
		t.Errorf("samples = %d, want 2", samples)
	}
	if tps < 19 || tps > 21 {
		t.Errorf("tokens/sec = %.2f, want ≈20", tps)
	}
	snap := ThroughputSnapshot()
	if len(snap) != 1 || snap[0].Model != "m1" {
		t.Fatalf("snapshot = %+v, want one entry for m1", snap)
	}
}

// An unmeasured model must report ok=false, never DefaultTokensPerSec: the
// prior exists to size request deadlines, and passing it off as an observation
// is the one thing the CLI and doctor must not do.
func TestUnobservedModelIsNotGivenThePrior(t *testing.T) {
	t.Cleanup(func() {
		ResetThroughputStore()
		GlobalThroughput.Reset()
	})
	ResetThroughputStore()
	GlobalThroughput.Reset()
	SetThroughputCacheDir(t.TempDir())

	tps, samples, ok := ObservedThroughput("never-run")
	if ok || samples != 0 || tps != 0 {
		t.Fatalf("unobserved model reported tps=%v samples=%d ok=%v", tps, samples, ok)
	}
}

// A stale record must not be presented as current: hardware, quantization and
// server flags all change the answer.
func TestExpiredThroughputRecordIsIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() {
		ResetThroughputStore()
		GlobalThroughput.Reset()
	})
	ResetThroughputStore()
	GlobalThroughput.Reset()

	stale := time.Now().Add(-2 * ThroughputTTL).Format(time.RFC3339Nano)
	body := `{"m1":{"tokens_per_sec":42,"samples":9,"at":"` + stale + `"}}`
	if err := os.WriteFile(filepath.Join(dir, throughputFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	SetThroughputCacheDir(dir)
	if _, _, ok := ObservedThroughput("m1"); ok {
		t.Fatal("a record older than ThroughputTTL was reported as measured")
	}
}
