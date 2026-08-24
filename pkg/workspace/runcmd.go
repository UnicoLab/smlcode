package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultShellTimeout bounds any command the model launches. Without it a
// runaway `go test ./...` or a REPL waiting on stdin wedges the whole run.
const DefaultShellTimeout = 2 * time.Minute

// MaxShellTimeout is the ceiling a caller can request.
const MaxShellTimeout = 15 * time.Minute

// MaxCapturedOutput is the hard memory cap for a single command's combined
// output. Anything beyond is dropped in the middle (head+tail is what a model
// actually needs: the command banner and the final failure).
const MaxCapturedOutput = 256 * 1024

// shellWaitDelay is how long we let a killed process group flush its pipes
// before Wait gives up. Without it an orphan holding stdout blocks forever.
const shellWaitDelay = 3 * time.Second

// CommandResult is the outcome of a bounded command execution.
type CommandResult struct {
	Output    string
	Err       error
	TimedOut  bool
	Truncated bool
	Duration  time.Duration
}

// headTailBuffer is an io.Writer that retains the first head bytes and the
// last tail bytes of the stream, with a bounded middle. Memory is O(head+tail)
// regardless of how much a command prints.
type headTailBuffer struct {
	mu       sync.Mutex
	head     []byte
	tail     []byte
	headMax  int
	tailMax  int
	total    int
	overflow bool
}

func newHeadTailBuffer(max int) *headTailBuffer {
	if max <= 0 {
		max = MaxCapturedOutput
	}
	head := max * 2 / 3
	tail := max - head
	if tail < 1024 {
		tail = 1024
		head = max - tail
	}
	if head < 1024 {
		head = 1024
	}
	return &headTailBuffer{headMax: head, tailMax: tail}
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += n
	if len(b.head) < b.headMax {
		take := b.headMax - len(b.head)
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	b.overflow = true
	if len(p) >= b.tailMax {
		b.tail = append(b.tail[:0], p[len(p)-b.tailMax:]...)
		return n, nil
	}
	b.tail = append(b.tail, p...)
	if len(b.tail) > b.tailMax {
		b.tail = b.tail[len(b.tail)-b.tailMax:]
	}
	return n, nil
}

// String renders head + an explicit truncation marker + tail.
func (b *headTailBuffer) String() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.overflow {
		return string(b.head), false
	}
	dropped := b.total - len(b.head) - len(b.tail)
	if dropped < 0 {
		dropped = 0
	}
	return string(b.head) +
		fmt.Sprintf("\n\n...[%d bytes of output dropped — %d total; re-run with a narrower target or pipe through `tail -n 50`]...\n\n", dropped, b.total) +
		string(b.tail), true
}

// RunBounded executes command with bash -c in dir under a hard timeout, in its
// own process group, capturing at most maxOutput bytes head+tail.
//
// `bash -c` (NOT `bash -lc`): the login shell sources the user's profile, which
// is slow, non-reproducible across machines, and can silently change PATH or
// activate a virtualenv mid-run.
func RunBounded(ctx context.Context, dir, command string, timeout time.Duration, maxOutput int) CommandResult {
	if timeout <= 0 {
		timeout = DefaultShellTimeout
	}
	if timeout > MaxShellTimeout {
		timeout = MaxShellTimeout
	}
	if maxOutput <= 0 {
		maxOutput = MaxCapturedOutput
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "bash", "-c", command) //nolint:gosec // running an operator/LLM-issued shell command in the workspace IS the feature (the run_command tool); scoped to the local user's own project
	cmd.Dir = dir
	buf := newHeadTailBuffer(maxOutput)
	cmd.Stdout = buf
	cmd.Stderr = buf
	setProcessGroup(cmd)
	// Cancel kills the whole group, not just bash — otherwise `go test`
	// children survive as orphans and keep holding the output pipes.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = shellWaitDelay

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	out, truncated := buf.String()
	res := CommandResult{Output: out, Err: err, Truncated: truncated, Duration: elapsed}
	if cctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
	}
	return res
}

// TimeoutMessage is the model-facing (non-error) result for a timed-out
// command. It must name a corrective action, never just report failure.
func TimeoutMessage(command string, timeout time.Duration, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"command timed out after %s and was killed (whole process group): %s\n\n"+
			"NEXT STEP: run a narrower command — target one package/test instead of the whole tree "+
			"(e.g. `go test ./pkg/foo -run TestBar -short`, `pytest tests/test_x.py::test_y -q`). "+
			"If the command needs stdin it will always time out; do not retry it verbatim.",
		timeout.Round(time.Second), truncateSnippet(command, 200),
	)
	if strings.TrimSpace(output) != "" {
		b.WriteString("\n\nOutput captured before the kill:\n")
		b.WriteString(truncateToolOutput(strings.TrimSpace(output), 4000))
	}
	return b.String()
}
