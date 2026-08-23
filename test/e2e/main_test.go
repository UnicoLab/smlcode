package e2e_test

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
)

// TestMain filters the flood of "level=info" lines that the GoLangGraph
// dependency writes to os.Stderr while these e2e tests spin up real
// orchestrators (it registers ~20 tools + providers per orchestrator via a
// *private* logrus.New() logger, so logrus.SetLevel on the standard logger
// has no effect on it). Without this, "level=info" lines make up ~83% of
// `go test ./test/e2e/...` output, burying real failures.
//
// This only filters Info-level noise. It does NOT hide panics: an uncaught
// panic is written by the Go runtime directly to OS file descriptor 2, not
// through the os.Stderr *os.File variable reassigned below, so it still
// reaches the terminal untouched. Test failures (t.Fatal/t.Error) go through
// go test's own stdout-based reporting and are never touched here either.
// Any non-"level=info" stderr line (warnings, errors, panics printed via
// os.Stderr, etc.) is passed through unfiltered.
func TestMain(m *testing.M) {
	// Defensive: silence the package-level logrus default logger too, in
	// case anything in this tree logs through it directly. This has no
	// effect on GoLangGraph's private per-instance loggers, which is why
	// the stderr-pipe filter below is still required.
	logrus.SetOutput(io.Discard)

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		// Can't set up the filter — fall back to running tests unfiltered
		// rather than losing output entirely.
		os.Exit(m.Run())
	}
	os.Stderr = w

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		// Log lines from the GoLangGraph dependency can be long; raise the
		// scanner's buffer so we don't drop/truncate legitimate output.
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "level=info") {
				continue
			}
			_, _ = fmt.Fprintln(origStderr, line) // best-effort log passthrough; nothing to do if stderr write fails
		}
	}()

	code := m.Run()

	// The acceptance test builds a real slmcode + fakemodel into a temp dir;
	// clean it up here rather than leaving ~40MB behind per `go test` run.
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}

	// Restore os.Stderr before closing the pipe so anything running after
	// m.Run() (e.g. test binary teardown) writes to the real stderr again.
	os.Stderr = origStderr
	_ = w.Close()
	wg.Wait()

	os.Exit(code)
}
