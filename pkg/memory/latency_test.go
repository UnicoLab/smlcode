package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// The two model ids that produced the live regression must land in DIFFERENT
// latency namespaces. Folding them together would hand a 27B dense model the
// budget measured for a 30B MoE (or vice versa), which is exactly the class of
// mistake this store exists to stop.
func TestModelFamilyKeepsRealModelIdsApart(t *testing.T) {
	const (
		dense = "Qwen3.8-27B-4bit"
		moe   = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"
	)
	fDense, fMoE := ModelFamily(dense), ModelFamily(moe)
	if fDense != "qwen3.8" {
		t.Errorf("ModelFamily(%q) = %q, want %q", dense, fDense, "qwen3.8")
	}
	if fMoE != "qwen3-coder" {
		t.Errorf("ModelFamily(%q) = %q, want %q", moe, fMoE, "qwen3-coder")
	}
	if fDense == fMoE {
		t.Fatalf("both ids folded to %q — distinct models must not share a namespace", fDense)
	}

	// And the namespaces must actually be separate in the store.
	l := openLatencies("", 0, nil)
	l.Record(LatencyKey{Role: "context", ModelFamily: fDense}, 120*time.Second)
	l.Record(LatencyKey{Role: "context", ModelFamily: fDense}, 120*time.Second)
	l.Record(LatencyKey{Role: "context", ModelFamily: fDense}, 120*time.Second)
	if _, n := l.P95("context", fMoE); n != 0 {
		t.Fatalf("MoE family sees %d samples recorded against the dense family", n)
	}
	if _, n := l.P95("context", fDense); n != 3 {
		t.Fatalf("dense family sees %d samples, want 3", n)
	}
}

// A quantile, not a mean: nine fast samples must not hide the one slow run.
func TestP95NotMean(t *testing.T) {
	l := openLatencies("", 0, nil)
	key := LatencyKey{Role: "explorer", ModelFamily: "qwen3.8"}
	for i := 0; i < 9; i++ {
		l.Record(key, 10*time.Second)
	}
	l.Record(key, 200*time.Second)

	p95, n := l.P95("explorer", "qwen3.8")
	if n != 10 {
		t.Fatalf("samples = %d, want 10", n)
	}
	mean := (9*10 + 200) * time.Second / 10 // 29s
	if p95 <= mean {
		t.Fatalf("p95 = %v, must exceed the mean %v — the tail is what times out", p95, mean)
	}
	if p95 != 200*time.Second {
		t.Fatalf("p95 = %v, want the slow sample (200s)", p95)
	}
}

func TestQuantileNearestRank(t *testing.T) {
	l := openLatencies("", 0, nil)
	key := LatencyKey{Role: "worker", ModelFamily: "m"}
	// 1..20 seconds, recorded out of order to prove the quantile sorts.
	for _, s := range []int{7, 3, 20, 1, 15, 2, 9, 11, 5, 4, 6, 8, 10, 12, 13, 14, 16, 17, 18, 19} {
		l.Record(key, time.Duration(s)*time.Second)
	}
	r, ok := l.Get(key)
	if !ok {
		t.Fatal("no record")
	}
	cases := []struct {
		q    float64
		want time.Duration
	}{
		{0, 1 * time.Second},
		{0.5, 10 * time.Second},
		{0.95, 19 * time.Second},
		{1, 20 * time.Second},
	}
	for _, tc := range cases {
		if got := r.Quantile(tc.q); got != tc.want {
			t.Errorf("Quantile(%.2f) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestLatencyDeterministicAcrossStores(t *testing.T) {
	samples := []time.Duration{
		30 * time.Second, 12 * time.Second, 95 * time.Second, 41 * time.Second,
		7 * time.Second, 60 * time.Second, 22 * time.Second,
	}
	run := func() time.Duration {
		l := openLatencies("", 0, fixedClock(time.Unix(1700000000, 0)))
		for _, d := range samples {
			l.Record(LatencyKey{Role: "planner", ModelFamily: "qwen3.8"}, d)
		}
		p95, _ := l.P95("planner", "qwen3.8")
		return p95
	}
	first := run()
	for i := 0; i < 20; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d gave %v, first gave %v — the quantile must be deterministic", i, got, first)
		}
	}
}

func TestLatencyNeverWidensAcrossFamilies(t *testing.T) {
	l := openLatencies("", 0, nil)
	for i := 0; i < 5; i++ {
		l.Record(LatencyKey{Role: "context", ModelFamily: "qwen3.8"}, 100*time.Second)
	}
	// A different family, and the generic (empty) family, must both be unknown:
	// borrowing a 27B's latency for a 1.2B is worse than having no data.
	if d, n := l.P95("context", "llama3.2"); n != 0 || d != 0 {
		t.Fatalf("cross-family read returned %v over %d samples", d, n)
	}
	if d, n := l.P95("context", ""); n != 0 || d != 0 {
		t.Fatalf("generic read returned %v over %d samples", d, n)
	}
	if d, n := l.P95("planner", "qwen3.8"); n != 0 || d != 0 {
		t.Fatalf("cross-role read returned %v over %d samples", d, n)
	}
}

func TestLatencyBoundedSamplesAndKeys(t *testing.T) {
	l := openLatencies("", 4, nil)
	key := LatencyKey{Role: "worker", ModelFamily: "m"}
	for i := 0; i < DefaultLatencySamples*4; i++ {
		l.Record(key, time.Duration(i+1)*time.Millisecond)
	}
	r, _ := l.Get(key)
	if r.Count() != DefaultLatencySamples {
		t.Fatalf("retained %d samples, want the %d cap", r.Count(), DefaultLatencySamples)
	}
	if r.Observations != int64(DefaultLatencySamples*4) {
		t.Fatalf("observations = %d, want %d", r.Observations, DefaultLatencySamples*4)
	}
	// The retained window is the RECENT one.
	if r.Max() != time.Duration(DefaultLatencySamples*4)*time.Millisecond {
		t.Fatalf("max = %v, want the newest sample", r.Max())
	}

	for i := 0; i < 40; i++ {
		l.Record(LatencyKey{Role: "role" + itoa(i), ModelFamily: "m"}, time.Second)
	}
	if l.Count() > 4*2 {
		t.Fatalf("key count = %d, cap is 4 (pruned at 2x)", l.Count())
	}
}

func TestLatencyPruneByAgeAndCount(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock := now
	l := openLatencies("", 0, func() time.Time { return clock })
	l.Record(LatencyKey{Role: "old", ModelFamily: "m"}, time.Second)
	clock = now.Add(200 * 24 * time.Hour)
	l.Record(LatencyKey{Role: "fresh", ModelFamily: "m"}, time.Second)

	if removed := l.Prune(DefaultPrunePolicy()); removed != 1 {
		t.Fatalf("removed %d, want 1 stale key", removed)
	}
	if _, ok := l.Get(LatencyKey{Role: "old", ModelFamily: "m"}); ok {
		t.Error("stale key survived")
	}
	if _, ok := l.Get(LatencyKey{Role: "fresh", ModelFamily: "m"}); !ok {
		t.Error("fresh key was pruned")
	}
}

func TestLatencyRoundTripAndCorruption(t *testing.T) {
	dir := t.TempDir()
	l := openLatencies(dir, 0, nil)
	for i := 0; i < 5; i++ {
		l.Record(LatencyKey{Role: "context", ModelFamily: "qwen3.8"}, time.Duration(10+i)*time.Second)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}

	reopened := openLatencies(dir, 0, nil)
	got, n := reopened.P95("context", "qwen3.8")
	if n != 5 || got != 14*time.Second {
		t.Fatalf("after reload p95 = %v over %d samples, want 14s over 5", got, n)
	}
	md, err := os.ReadFile(filepath.Join(dir, "LATENCY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "qwen3.8") {
		t.Error("LATENCY.md does not mention the model family")
	}

	// Corruption is never fatal: the file is set aside and the store restarts.
	if err := os.WriteFile(filepath.Join(dir, "latency.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := openLatencies(dir, 0, nil)
	if broken.Count() != 0 {
		t.Fatalf("corrupt store loaded %d keys", broken.Count())
	}
	if len(broken.Warnings()) == 0 {
		t.Error("corrupt store recorded no warning")
	}
	if _, err := os.Stat(filepath.Join(dir, "latency.json.corrupt")); err != nil {
		t.Errorf("corrupt file was not set aside: %v", err)
	}
	// Still usable after the corruption.
	broken.Record(LatencyKey{Role: "context", ModelFamily: "qwen3.8"}, time.Second)
	if broken.Count() != 1 {
		t.Error("store unusable after corruption")
	}
}

func TestLatencyRejectsGarbageSamples(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"roles":[{"key":{"role":"worker","model_family":"m"},"samples_ms":[-5,0,1500,-1]}]}`
	if err := os.WriteFile(filepath.Join(dir, "latency.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l := openLatencies(dir, 0, nil)
	r, ok := l.Get(LatencyKey{Role: "worker", ModelFamily: "m"})
	if !ok {
		t.Fatal("valid sample was dropped with the invalid ones")
	}
	if r.Count() != 1 || r.Max() != 1500*time.Millisecond {
		t.Fatalf("kept %d samples (max %v), want only the positive one", r.Count(), r.Max())
	}
	// Non-positive durations are a clock artifact, never evidence.
	if got := l.Record(LatencyKey{Role: "worker", ModelFamily: "m"}, -time.Second); got.ID != "" {
		t.Error("a negative duration was recorded")
	}
}

func TestStoreExposesAndForgetsLatency(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	s, err := OpenWith(proj, user, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Latency() == nil {
		t.Fatal("Store.Latency() is nil")
	}
	for i := 0; i < 4; i++ {
		s.Latency().Record(LatencyKey{Role: "context", ModelFamily: "qwen3.8"}, 90*time.Second)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(user, SlmDirName, DirName, "latency.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("latency.json not written to the user memory dir: %v", err)
	}
	// Forgetting procedural memory takes the same-namespace latency with it.
	if err := s.Forget(ScopeProcedural); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("latency.json survived Forget(procedural): %v", err)
	}
	if s.Latency().Count() != 0 {
		t.Errorf("in-process latency survived Forget: %d keys", s.Latency().Count())
	}
}
