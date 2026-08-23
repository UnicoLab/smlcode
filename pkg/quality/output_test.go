package quality

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestFailureExcerptKeepsTheActualError is the whole point of the change: every
// runner this harness drives puts its verdict LAST, so head-only truncation fed
// the corrector collection noise with the assertion cut off.
func TestFailureExcerptKeepsTheActualError(t *testing.T) {
	noise := strings.Repeat("collecting ... ok\n", 2000)

	cases := []struct {
		name  string
		out   string
		want  []string
		limit int
	}{
		{
			name:  "pytest summary at the end",
			out:   noise + "=== FAILURES ===\nE       assert 1 == 2\nFAILED test_x.py::test_a - assert 1 == 2\n",
			want:  []string{"assert 1 == 2", "FAILED test_x.py::test_a"},
			limit: 5000,
		},
		{
			name:  "go test FAIL lines at the end",
			out:   noise + "--- FAIL: TestThing (0.00s)\n    x_test.go:12: got 1 want 2\nFAIL\nFAIL\tdemo/pkg\t0.2s\n",
			want:  []string{"FAIL"},
			limit: 5000,
		},
		{
			name:  "tsc error summary at the end",
			out:   noise + "src/a.ts(3,5): error: Cannot find name 'foo'.\n",
			want:  []string{"error: Cannot find name 'foo'"},
			limit: 5000,
		},
		{
			name:  "python traceback at the end",
			out:   noise + "Traceback (most recent call last):\n  File \"a.py\", line 1\nValueError: boom\n",
			want:  []string{"Traceback (most recent call last)"},
			limit: 5000,
		},
		{
			name:  "panic at the end",
			out:   noise + "panic: runtime error: index out of range [3]\n",
			want:  []string{"panic: runtime error"},
			limit: 5000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureExcerpt(tc.out, tc.limit)
			if len(got) > tc.limit {
				t.Fatalf("excerpt is %d chars, over the %d budget", len(got), tc.limit)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("excerpt lost %q — the corrector would be asked to fix an error it cannot see.\n%s",
						want, got)
				}
			}
			// The failure must be near the TOP, so a further head-only cut
			// downstream still keeps it.
			head := got
			if len(head) > 1200 {
				head = head[:1200]
			}
			if !strings.Contains(head, tc.want[0]) {
				t.Fatalf("failure line %q is not pinned to the top of the excerpt", tc.want[0])
			}
		})
	}
}

func TestTruncateOutputKeepsBothEnds(t *testing.T) {
	s := "HEAD-MARKER\n" + strings.Repeat("x\n", 20000) + "TAIL-MARKER\n"
	got := TruncateOutput(s, 4000)
	if len(got) > 4200 {
		t.Fatalf("truncated to %d chars, want ~4000", len(got))
	}
	if !strings.Contains(got, "HEAD-MARKER") {
		t.Fatal("head was dropped")
	}
	if !strings.Contains(got, "TAIL-MARKER") {
		t.Fatal("tail was dropped — this is the head-only truncation defect")
	}
	if short := TruncateOutput("small", 4000); short != "small" {
		t.Fatalf("output that fits must be untouched, got %q", short)
	}
}

func TestFailureLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"nothing matches", "all good\nok\n", 0},
		{"dedupes repeats", "FAIL\nFAIL\nFAIL\n", 1},
		{"indented pytest detail", "    E       assert x\n", 1},
		{"error in the middle of a line", "src/a.ts(1,1): error: bad\n", 1},
		{"case insensitive", "fail: nope\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(FailureLines(tc.in, 0)); got != tc.want {
				t.Fatalf("FailureLines = %d lines, want %d", got, tc.want)
			}
		})
	}
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("ERROR distinct ")
		b.WriteString(strings.Repeat("y", i%7+1))
		b.WriteString("\n")
	}
	if got := len(FailureLines(b.String(), 0)); got > MaxPinnedFailureLines {
		t.Fatalf("pinned %d lines, over the %d cap", got, MaxPinnedFailureLines)
	}
}

// TestSmokeSectionKeepsFailureOutput covers the prompt-section call site: the
// text that actually reaches the reviewer and corrector.
func TestSmokeSectionKeepsFailureOutput(t *testing.T) {
	noise := strings.Repeat("compiling package n\n", 500)
	sr := SmokeResult{
		Ran: true, OK: false, Command: "go test ./...",
		Output: noise + "--- FAIL: TestZ\nFAIL\tdemo/pkg\n",
	}
	for _, section := range []string{FormatSmokeSection(sr), FormatAcceptanceSection(sr)} {
		if !strings.Contains(section, "FAIL: TestZ") {
			t.Fatalf("the gate section dropped the failure line:\n%s", section)
		}
		if !strings.Contains(section, SmokeFailedMarker) {
			t.Fatal("a failing smoke section must still be marked FAILED")
		}
	}
}

// TestRunSmokeKeepsTheFailureTail exercises the real command path: a runner
// that prints a lot and fails at the very end must still hand back the failure.
func TestRunSmokeKeepsTheFailureTail(t *testing.T) {
	root := t.TempDir()
	sr := RunSmoke(context.Background(), root,
		`for i in $(seq 1 4000); do echo "collecting item $i ok"; done; echo "FAIL demo/pkg"; exit 1`,
		30*time.Second)
	if sr.OK {
		t.Fatal("command was expected to fail")
	}
	if len(sr.Output) > MaxSmokeOutput+200 {
		t.Fatalf("output is %d chars, over the %d cap", len(sr.Output), MaxSmokeOutput)
	}
	if !strings.Contains(sr.Output, "FAIL demo/pkg") {
		t.Fatalf("the failure at the end of the output was truncated away:\n%s", clipLine(sr.Output, 400))
	}
}
