package orchestrator

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/quality"
)

// The gate's own contract, from objectiveGate.Green: "A 'nothing to run' exit is
// NoTests, never Green — it verifies nothing and must not end a run early."
//
// It held only for runners that FAIL when they find nothing. `go test ./...` on
// a tree with no _test.go files prints "[no test files]" and exits 0, so the
// green-first switch reached Green and the contract was silently inverted for
// every Go project. Measured live: a run told "both parts must have tests" wrote
// none and finished ✔ with "qa_gate green".
func TestVacuousGoRunIsNotGreen(t *testing.T) {
	out := "?   \tbackend/cmd/backend\t[no test files]\n"
	g := classifySmoke("go test ./...", quality.SmokeResult{Ran: true, OK: true, Output: out})
	if g.Green {
		t.Error("a run that executed no tests was reported Green")
	}
	if !g.NoTests {
		t.Errorf("expected NoTests, got %+v", g)
	}
}

// The false positive that would make the fix worse than the bug: a healthy
// repository prints one "[no test files]" line per package without tests, right
// beside the "ok" lines of the packages that do have them. That run is verified.
func TestMixedGoRunIsStillGreen(t *testing.T) {
	out := "ok  \texample.com/x/pkg/a\t0.4s\n" +
		"?   \texample.com/x/pkg/b\t[no test files]\n" +
		"ok  \texample.com/x/pkg/c\t1.2s\n"
	g := classifySmoke("go test ./...", quality.SmokeResult{Ran: true, OK: true, Output: out})
	if !g.Green {
		t.Errorf("a run with real passing packages must stay Green, got %+v", g)
	}
	if g.NoTests {
		t.Error("a run with real passing packages must not be NoTests")
	}
}

func TestQARanNoTests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{"go with nothing at all", "?   \tm/cmd\t[no test files]\n", true},
		{"go with one real package", "ok  \tm/pkg\t0.1s\n?   \tm/cmd\t[no test files]\n", false},
		{"go verbose run", "=== RUN   TestX\n--- PASS: TestX (0.00s)\nPASS\nok  \tm\t0.1s\n", false},
		{"pytest collected nothing", "collected 0 items\n\nno tests ran in 0.01s\n", true},
		{"pytest with tests", "collected 3 items\n\ntest_x.py ...\n3 passed in 0.05s\n", false},
		{"a real failure is never vacuous", "?   \tm/cmd\t[no test files]\nFAIL\tm/pkg\t0.1s\n", false},
		{"a build error is never vacuous", "no test files\nbuild failed\n", false},
		{"empty output", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := qaRanNoTests(tc.output); got != tc.want {
				t.Errorf("qaRanNoTests() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A failing command that failed only because it found nothing keeps its old
// classification — this fix adds a success-path case, it does not replace the
// failure-path one.
func TestFailingNoTestsStillClassifiesAsNoTests(t *testing.T) {
	g := classifySmoke("pytest", quality.SmokeResult{
		Ran: true, OK: false, Output: "collected 0 items\n",
	})
	if !g.NoTests || g.Green {
		t.Errorf("expected NoTests and not Green, got %+v", g)
	}
}
