package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestSlowEndpointDoesNotPoisonCapabilitiesForAWeek is the regression guard for
// a defect with the worst possible blast radius: a COLD LOCAL MODEL — the exact
// case slmcode exists to serve — silently losing structured decoding for seven
// days.
//
// THE DEFECT: one deadline covers up to six sequential probe requests. If
// weight-loading ate most of it, the plain probe still succeeded (setting
// `reachable`), and every later probe died on the shared deadline. `attempt`
// returns false for a transport error exactly as it does for a 400, so the
// negotiation could not tell "the server refused this field" from "we never got
// to ask". It then stamped Source="probe" and Probed=now on a wholesale-false
// record, which capCache persisted and honored for CapabilityTTL. Nothing
// re-probes a record that is still fresh, so there was no path back: every
// structured role on that endpoint degraded to prompt-only + repair for a week.
func TestSlowEndpointDoesNotPoisonCapabilitiesForAWeek(t *testing.T) {
	var calls int64
	// Every request costs 100ms — a stand-in for a model still loading weights.
	// The first one fits inside the budget and succeeds; the rest cannot.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	restore := ProbeTimeout
	ProbeTimeout = 150 * time.Millisecond
	defer func() { ProbeTimeout = restore }()

	// runProbe directly: Probe() memoises per key and would hide a second call.
	got := runProbe(context.Background(), "omlx", srv.URL, "slow-model", "k")

	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Fatalf("the server saw %d request(s); the test needs the first to "+
			"succeed and a later one to hit the shared deadline", n)
	}
	// The load-bearing assertion. A zero Probed is what capCache.put checks
	// before persisting, so this is precisely what keeps a half-finished
	// negotiation out of the on-disk cache and out of the next process.
	if !got.Probed.IsZero() {
		t.Fatalf("a probe cut short by its own deadline was stamped as a "+
			"completed one (Probed=%s, Source=%q) — capCache will now persist it "+
			"and honor it for %s", got.Probed, got.Source, CapabilityTTL)
	}
	if got.Source == "probe" {
		t.Fatalf("Source=%q claims a completed negotiation", got.Source)
	}
	// Structured decoding must survive. Falling back to the family preset is
	// safe in a way that all-false is not: an over-claimed mechanism costs one
	// 400 and is then recorded by demoteCapability, while an under-claimed one
	// has nothing that can ever notice it.
	if !got.JSONSchema || !got.JSONObject {
		t.Fatalf("a slow endpoint lost structured decoding entirely: %+v — "+
			"omlx's preset supports json_schema and json_object", got)
	}
}

// TestCompletedProbeStillWins is the control: without it the test above could
// pass by never trusting a probe at all.
func TestCompletedProbeStillWins(t *testing.T) {
	// json_schema is refused, everything else answered — a real negotiation
	// with a real negative result in it.
	f := newFakeServer(t, "json_object", "tools")
	restore := ProbeTimeout
	ProbeTimeout = 10 * time.Second
	defer func() { ProbeTimeout = restore }()

	got := runProbe(context.Background(), "omlx", f.URL, "fast-model", "k")

	if got.Source != "probe" || got.Probed.IsZero() {
		t.Fatalf("a negotiation that finished was not trusted: %+v", got)
	}
	if got.JSONSchema {
		t.Fatal("json_schema was reported supported though the server answered 400")
	}
	if !got.JSONObject {
		t.Fatal("json_object was refused though the server accepted it")
	}
}
