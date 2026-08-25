package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// THE UI CONTRACT.
//
// web/src/components/Settings/CalibrationPanel.tsx reads these exact JSON keys.
// A Go field rename that keeps compiling — `MaxParallel` to `Knee`, say — would
// silently turn the panel's numbers into zeros, because JSON decoding a missing
// key is not an error on either side. Nothing else in the tree fails when that
// happens, which is precisely why it needs pinning here.
//
// The route-level tests cover auth and origin; this covers shape.
func TestCalibrationResponseKeepsTheKeysTheUIReads(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/calibration", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/calibration -> %d %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}

	// Always present, because the panel branches on them before anything else.
	// `present` and `current` are booleans that must be emitted even when false
	// — they carry the "never measured" and "stale" states, so `omitempty` on
	// either would make the panel unable to tell them from a missing field.
	for _, k := range []string{"present", "current", "budgets", "model", "provider", "endpoint"} {
		if _, ok := got[k]; !ok {
			t.Errorf("response is missing %q — CalibrationPanel reads it unconditionally", k)
		}
	}

	budgets, ok := got["budgets"].(map[string]any)
	if !ok {
		t.Fatalf("budgets is %T, want an object", got["budgets"])
	}
	// The "Budgets in force" grid renders one row per key; a missing key renders
	// as 0, which reads as a real measurement of zero.
	for _, k := range []string{
		"context_limit", "max_tokens", "thinking_budget_tokens",
		"skill_token_budget", "knowledge_token_budget", "max_turns",
	} {
		if _, ok := budgets[k]; !ok {
			t.Errorf("budgets is missing %q", k)
		}
	}
}

// TestCalibrationIsHonestWhenNothingWasMeasured. An unmeasured pair must report
// present=false rather than zeros: the panel shows "not calibrated yet" for the
// first and a table of zeroes for the second, and the second is a lie.
func TestCalibrationIsHonestWhenNothingWasMeasured(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/calibration", nil))

	var got struct {
		Present bool `json:"present"`
		Levels  []struct {
			Concurrency int `json:"concurrency"`
		} `json:"levels"`
		ContextLimit int `json:"context_limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Present {
		// The unmeasured case: nothing derived from a measurement may be set.
		if got.ContextLimit != 0 || len(got.Levels) != 0 {
			t.Fatalf("present=false but measured fields are populated "+
				"(context_limit=%d levels=%d) — the panel would render invented evidence",
				got.ContextLimit, len(got.Levels))
		}
	}
}

// TestCalibrationNeverProbes is the property that makes this endpoint safe to
// poll: a GET that could spend a minute of GPU on a cold model is a GET a
// refreshing UI will spend repeatedly. Measurement belongs to startup and to
// ensureCalibrated before a run.
//
// Asserted structurally — the handler must return promptly and identically on
// repeated calls, which a probe would not.
func TestCalibrationNeverProbes(t *testing.T) {
	s := advServer(t, Options{NoAuth: true})
	var first string
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/calibration", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d -> %d", i, rec.Code)
		}
		if i == 0 {
			first = rec.Body.String()
			continue
		}
		if rec.Body.String() != first {
			t.Fatalf("call %d differs from the first — the endpoint has a side effect", i)
		}
	}
}

// jsonTags returns the JSON names a struct serializes, ignoring omitempty.
func jsonTags(t *testing.T, v any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = true
	}
	return out
}

// TestCalibrationTagsCoverEveryFieldTheUIReads pins the WHOLE contract, not just
// the part that survives an unmeasured pair.
//
// Most of calibrationView is `omitempty`, so a live response against a fresh
// store omits it — which means a response-body test cannot see a rename of
// `max_parallel` or `tokens_per_sec`. The panel reads them all, and renders a
// missing one as zero rather than as an error. Reading the tags directly is the
// only check that covers the measured fields without a measured store.
func TestCalibrationTagsCoverEveryFieldTheUIReads(t *testing.T) {
	view := jsonTags(t, calibrationView{})
	// Exactly the keys CalibrationPanel.tsx dereferences.
	for _, k := range []string{
		"present", "current", "model", "provider", "endpoint",
		"context_limit", "context_source", "max_parallel",
		"p50_ms", "p95_ms", "tokens_per_sec", "queue_inflation",
		"levels", "partial", "age_seconds", "budgets", "report",
	} {
		if !view[k] {
			t.Errorf("calibrationView no longer serializes %q — "+
				"CalibrationPanel.tsx reads it and would render zero", k)
		}
	}

	lvl := jsonTags(t, calibrationLevel{})
	for _, k := range []string{"concurrency", "efficiency", "throughput"} {
		if !lvl[k] {
			t.Errorf("calibrationLevel no longer serializes %q — "+
				"the concurrency ladder column would be blank", k)
		}
	}

	bud := jsonTags(t, calibrationBudgets{})
	for _, k := range []string{
		"context_limit", "max_tokens", "thinking_budget_tokens",
		"skill_token_budget", "knowledge_token_budget", "max_turns",
	} {
		if !bud[k] {
			t.Errorf("calibrationBudgets no longer serializes %q", k)
		}
	}
}
