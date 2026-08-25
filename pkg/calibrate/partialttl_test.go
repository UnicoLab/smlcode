package calibrate

import (
	"testing"
	"time"
)

// TestPartialProfileIsNotHonoredForAMonth is the regression guard for the
// slowest models being silently pinned to max_parallel=1 for thirty days.
//
// THE DEFECT: the concurrency measurement runs inside a fixed wall-clock
// budget, and every unit of work inside it is a model call — the very thing
// being measured. On a cold local server the warm-up plus the solo baseline can
// exhaust the budget before ANY concurrency level is measured; the run sets
// Partial and SelectKnee, left with only the synthetic single-level entry,
// returns 1. Profile.Current then checked ID, MaxParallel, Version and age but
// never Partial, so that verdict was served from cache for DefaultTTL. Apply
// only tests MaxParallel > 0, and the "partial" marker shows up solely in
// Summary(), which the auto path never prints — so nothing could notice.
//
// The models most likely to be cold are the large local ones, i.e. exactly the
// ones with the most to gain from a real measurement.
func TestPartialProfileIsNotHonoredForAMonth(t *testing.T) {
	now := time.Now()
	partial := Profile{
		ID:          "model@endpoint",
		Version:     CalibratorVersion,
		MaxParallel: 1, // the degenerate verdict a cold start produces
		Partial:     true,
		MeasuredAt:  now.Add(-24 * time.Hour),
	}

	if partial.Current(now, DefaultTTL) {
		t.Fatalf("a partial profile measured %s ago is still being honored under "+
			"a %s TTL — one cold start pins concurrency for the whole month",
			24*time.Hour, DefaultTTL)
	}

	// Fresh enough to reuse: re-probing on every run would cost more than the
	// stale answer does, and a cold server usually has not warmed up yet.
	fresh := partial
	fresh.MeasuredAt = now.Add(-5 * time.Minute)
	if !fresh.Current(now, DefaultTTL) {
		t.Fatal("a partial profile measured 5 minutes ago was discarded — that " +
			"re-probes on every run, which is the opposite failure")
	}
}

// TestCompleteProfileStillGetsTheFullTTL is the control: without it the test
// above could pass by expiring every profile.
func TestCompleteProfileStillGetsTheFullTTL(t *testing.T) {
	now := time.Now()
	full := Profile{
		ID:          "model@endpoint",
		Version:     CalibratorVersion,
		MaxParallel: 4,
		MeasuredAt:  now.Add(-24 * time.Hour),
	}
	if !full.Current(now, DefaultTTL) {
		t.Fatal("a COMPLETE profile measured a day ago was discarded; the short " +
			"expiry must apply only to partial measurements")
	}
	stale := full
	stale.MeasuredAt = now.Add(-2 * DefaultTTL)
	if stale.Current(now, DefaultTTL) {
		t.Fatal("a profile past DefaultTTL is still current")
	}
}
