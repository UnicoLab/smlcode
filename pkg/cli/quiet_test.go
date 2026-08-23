package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestLogLineLevelClassification(t *testing.T) {
	cases := map[string]string{
		`time="…" level=info msg="registered tool"`: "",
		`time="…" level=debug msg="graph built"`:    "",
		`time="…" level=trace msg="x"`:              "",
		`time="…" level=warning msg="deprecated"`:   "warning",
		`time="…" level=warn msg="deprecated"`:      "warning",
		`time="…" level=error msg="boom"`:           "error",
		`time="…" level=fatal msg="dead"`:           "error",
		`panic: nil map`:                            "error",
		`goroutine 1 [running]:`:                    "error",
		`some unlabelled chatter from a dependency`: "",
	}
	for in, want := range cases {
		if got := LogLineLevel(in); got != want {
			t.Errorf("LogLineLevel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestQuietStderrDropsInfoKeepsWarnings(t *testing.T) {
	var mu sync.Mutex
	var kept []string
	QuietStderrIf(true, func() {
		for i := 0; i < 20; i++ {
			fmt.Fprintf(os.Stderr, "time=\"x\" level=info msg=\"registering tool %d\"\n", i)
		}
		fmt.Fprintln(os.Stderr, `time="x" level=warning msg="deprecated model id"`)
		fmt.Fprintln(os.Stderr, `time="x" level=error msg="endpoint refused"`)
	}, func(level, line string) {
		mu.Lock()
		kept = append(kept, level+"|"+line)
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if len(kept) != 2 {
		t.Fatalf("expected exactly the warn+error lines, got %d: %v", len(kept), kept)
	}
	if !strings.HasPrefix(kept[0], "warning|") || !strings.HasPrefix(kept[1], "error|") {
		t.Fatalf("kept=%v", kept)
	}
}

func TestQuietStderrRestoresOnNormalReturn(t *testing.T) {
	before := os.Stderr
	QuietStderrIf(true, func() {}, nil)
	if os.Stderr != before {
		t.Fatal("os.Stderr was not restored")
	}
}

func TestQuietStderrRestoresOnPanicAndRepanics(t *testing.T) {
	before := os.Stderr
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		QuietStderrIf(true, func() {
			fmt.Fprintln(os.Stderr, `time="x" level=error msg="about to blow up"`)
			panic("boom")
		}, nil)
	}()
	if recovered != "boom" {
		t.Fatalf("panic was swallowed: %v", recovered)
	}
	if os.Stderr != before {
		t.Fatal("os.Stderr was not restored after a panic")
	}
}

func TestQuietStderrDisabledPassesThrough(t *testing.T) {
	before := os.Stderr
	called := false
	QuietStderrIf(false, func() { called = true }, nil)
	if !called {
		t.Fatal("fn was not called")
	}
	if os.Stderr != before {
		t.Fatal("disabled mode must not touch os.Stderr")
	}
}

func TestQuietStderrNilFnIsSafe(t *testing.T) {
	QuietStderrIf(true, nil, nil) // must not panic
}

func TestQuietStderrFallsBackToRealStderrWithoutEmitter(t *testing.T) {
	// With no emitter, significant lines still have to reach a real stream.
	// Redirect the process stderr into a pipe around the whole call.
	r, w, err := os.Pipe()
	if err != nil {
		t.Skip("pipe unavailable")
	}
	orig := os.Stderr
	os.Stderr = w

	QuietStderrIf(true, func() {
		fmt.Fprintln(os.Stderr, `time="x" level=info msg="noise"`)
		fmt.Fprintln(os.Stderr, `time="x" level=error msg="signal"`)
	}, nil)

	os.Stderr = orig
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	out := buf.String()
	if strings.Contains(out, "noise") {
		t.Fatalf("info line leaked: %q", out)
	}
	if !strings.Contains(out, "signal") {
		t.Fatalf("error line was dropped: %q", out)
	}
}
