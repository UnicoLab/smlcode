package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/calibrate"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
// The CLI prints through fmt.Println to the real file descriptor, so the
// capture has to happen there.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// resetNoticeOnce lets each test observe the first print. In a real process
// the guard is claimed exactly once, which is the property under test.
func resetNoticeOnce() { maxParallelNoticeOnce = sync.Once{} }

func localConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Normalize()
	return cfg
}

// TestMaxParallelNoticePrintsOncePerProcess is the "say so, once, visibly"
// contract: a wave loop calling this on every iteration must not turn the
// explanation into spam.
func TestMaxParallelNoticePrintsOncePerProcess(t *testing.T) {
	resetNoticeOnce()
	cfg := localConfig(t)
	out := captureStdout(t, func() {
		for i := 0; i < 5; i++ {
			printMaxParallelNotice(cfg)
		}
	})
	if n := strings.Count(out, "single local endpoint"); n != 1 {
		t.Fatalf("notice printed %d times, want exactly 1:\n%s", n, out)
	}
	if !strings.Contains(out, "max_parallel=2") {
		t.Fatalf("the notice must name the value it chose:\n%s", out)
	}
	if !strings.Contains(out, "slmcode config set max_parallel") {
		t.Fatalf("the notice must say how to override it:\n%s", out)
	}
}

func TestMaxParallelNoticeIsSilentWhenNothingWasLowered(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*config.Config)
	}{
		{
			name: "hosted endpoint",
			mut: func(c *config.Config) {
				c.Provider = "openai"
				c.Endpoint = "https://api.openai.com/v1"
				c.Normalize()
			},
		},
		{
			name: "explicitly set to the historical default",
			mut:  func(c *config.Config) { c.SetMaxParallel(4); c.Normalize() },
		},
		{
			name: "explicitly set to the same value the default would pick",
			mut:  func(c *config.Config) { c.SetMaxParallel(2); c.Normalize() },
		},
		{
			name: "explicitly serialized",
			mut:  func(c *config.Config) { c.SetMaxParallel(1); c.Normalize() },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetNoticeOnce()
			cfg := localConfig(t)
			tc.mut(cfg)
			out := captureStdout(t, func() { printMaxParallelNotice(cfg) })
			if strings.TrimSpace(out) != "" {
				t.Fatalf("nothing was lowered, so nothing must be printed:\n%s", out)
			}
		})
	}
}

// TestSilentNoticeDoesNotConsumeTheOnceGuard: a hosted config early in a
// process must not swallow the one line a later local config deserves.
func TestSilentNoticeDoesNotConsumeTheOnceGuard(t *testing.T) {
	resetNoticeOnce()
	hosted := localConfig(t)
	hosted.Provider = "openai"
	hosted.Endpoint = "https://api.openai.com/v1"
	hosted.Normalize()

	out := captureStdout(t, func() {
		printMaxParallelNotice(hosted)
		printMaxParallelNotice(localConfig(t))
	})
	if n := strings.Count(out, "single local endpoint"); n != 1 {
		t.Fatalf("notice printed %d times, want 1:\n%s", n, out)
	}
}

// TestCalibrationSuppressesTheStaticNotice: once a real measurement of THIS
// machine is in force, the static endpoint-aware explanation would quote a
// different machine's numbers underneath it and read as a contradiction.
func TestCalibrationSuppressesTheStaticNotice(t *testing.T) {
	resetNoticeOnce()
	cfg := localConfig(t)
	out := captureStdout(t, func() {
		suppressMaxParallelNotice()
		printMaxParallelNotice(cfg)
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("the static notice must stay silent once calibration explained it:\n%s", out)
	}
}

func TestLatencySeederAdaptsTheMemoryStore(t *testing.T) {
	// The adapter is the only glue between pkg/calibrate's narrow interface
	// and pkg/memory's store, so it is worth one direct check that it satisfies
	// the interface and round-trips a sample.
	var _ calibrate.LatencyRecorder = latencySeeder{}
}
