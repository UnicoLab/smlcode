package calibrate

import (
	"strings"
	"testing"
)

// TestProgressReportsEveryStage pins that calibration narrates itself.
//
// WHY IT MATTERS: calibration only runs for an unseen (model, endpoint) pair,
// but when it does it is the first thing a user sees after asking for work, and
// on a cold local model the warm-up call alone can be minutes — a 42GB
// Qwen3-Coder-Next is minutes of silence before anything is measurable. A
// harness that looks hung while measuring the very numbers that make its later
// timeouts correct is its own worst advertisement.
//
// The stages asserted here are the ones that actually take time. Losing any of
// them puts a silent gap back into startup.
func TestProgressReportsEveryStage(t *testing.T) {
	var seen []string
	opt := Options{OnProgress: func(p Progress) { seen = append(seen, p.Stage) }}.withDefaults()
	// withDefaults must carry the observer through; a dropped hook here is a
	// silent regression that no other test would notice.
	if opt.OnProgress == nil {
		t.Fatal("withDefaults dropped OnProgress — every stage below would be silent")
	}

	opt.note("warming up the model", 0, 0, "first call loads weights")
	opt.note("latency baseline", 1, 3, "")
	opt.note("concurrency 4", 0, 0, "")
	opt.note("reading the model's context window", 0, 0, "")

	want := []string{
		"warming up the model",
		"latency baseline",
		"concurrency 4",
		"reading the model's context window",
	}
	if len(seen) != len(want) {
		t.Fatalf("saw %d stage(s), want %d: %v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("stage %d = %q, want %q", i, seen[i], want[i])
		}
	}
}

// TestProgressIsOptionalAndSafe: the hook is absent on every path that is not a
// terminal, so a nil observer must never be a crash.
func TestProgressIsOptionalAndSafe(t *testing.T) {
	var opt Options // zero value: no observer
	opt.note("anything", 1, 2, "detail")
	opt = opt.withDefaults()
	opt.note("still nothing", 0, 0, "")
}

// TestProgressStringIsReadable pins the rendered line, since it is what a human
// actually reads while waiting.
func TestProgressStringIsReadable(t *testing.T) {
	for _, tc := range []struct {
		p    Progress
		want string
	}{
		{Progress{Stage: "warming up the model"}, "warming up the model"},
		{Progress{Stage: "latency baseline", Step: 2, Total: 3}, "latency baseline (2/3)"},
		{Progress{Stage: "concurrency 4", Detail: "312ms"}, "concurrency 4 — 312ms"},
		{Progress{Stage: "latency baseline", Step: 1, Total: 3, Detail: "88ms"},
			"latency baseline (1/3) — 88ms"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
	// An indeterminate stage must not render a bogus "(0/0)".
	if s := (Progress{Stage: "x"}).String(); strings.Contains(s, "0/0") {
		t.Errorf("indeterminate progress rendered a fake position: %q", s)
	}
}
