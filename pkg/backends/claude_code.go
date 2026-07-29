package backends

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// ClaudeCodeRunner optionally shells out to the Claude Code CLI for a scoped task.
// Used when Config.Backend == "claude-code". The prompt is already SLM-scoped
// (atomic task + file pack); Claude Code is just the executor.
type ClaudeCodeRunner struct {
	Bin     string
	WorkDir string
	Timeout time.Duration
}

func NewClaudeCodeRunner(cfg *config.Config) *ClaudeCodeRunner {
	return &ClaudeCodeRunner{
		Bin:     cfg.ClaudeCodeBin,
		WorkDir: cfg.Root,
		Timeout: cfg.TaskTimeout,
	}
}

// Available reports whether the Claude Code binary is on PATH.
func (r *ClaudeCodeRunner) Available() bool {
	_, err := exec.LookPath(r.Bin)
	return err == nil
}

// Run executes a non-interactive prompt in the project directory.
func (r *ClaudeCodeRunner) Run(ctx context.Context, prompt string) (string, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	// Prefer print/non-interactive modes used by recent Claude Code CLIs.
	args := []string{"-p", prompt, "--output-format", "text"}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = r.WorkDir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("claude-code: %s", msg)
	}
	return stdout.String(), nil
}
