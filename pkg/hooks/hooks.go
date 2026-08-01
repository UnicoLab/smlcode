package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// Config mirrors a Claude Code–style hooks.json subset.
type Config struct {
	Hooks map[string][]Hook `json:"hooks" yaml:"hooks"`
}

// Hook is one matcher + command (or prompt — command only for SLMs).
type Hook struct {
	Matcher string `json:"matcher" yaml:"matcher"` // regex on tool name
	Command string `json:"command" yaml:"command"` // bash -lc in project root
	Timeout int    `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty"`
}

// Runner executes Pre/Post tool hooks.
type Runner struct {
	Root   string
	Cfg    Config
	Log    func(string, ...interface{})
}

// Load reads .slmcode/hooks.json (or path). Missing file → empty config.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// DefaultPath returns <slmDir>/hooks.json.
func DefaultPath(slmDir string) string {
	return filepath.Join(slmDir, "hooks.json")
}

// RunEvent executes hooks for PreToolUse / PostToolUse.
// Returns error if a PreToolUse hook exits non-zero (blocks the tool).
func (r *Runner) RunEvent(ctx context.Context, event, toolName string, args map[string]interface{}, result string) error {
	if r == nil {
		return nil
	}
	list := r.Cfg.Hooks[event]
	if len(list) == 0 {
		return nil
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"event": event, "tool": toolName, "args": args, "result": truncate(result, 4000),
	})
	for _, h := range list {
		if !match(h.Matcher, toolName) {
			continue
		}
		if strings.TrimSpace(h.Command) == "" {
			continue
		}
		timeout := time.Duration(h.Timeout) * time.Second
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(cctx, "bash", "-lc", h.Command)
		cmd.Dir = r.Root
		cmd.Env = append(os.Environ(),
			"SLMCODE_HOOK_EVENT="+event,
			"SLMCODE_HOOK_TOOL="+toolName,
			"SLMCODE_HOOK_PAYLOAD="+string(payload),
		)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		out := buf.String()
		cancel()
		if r.Log != nil {
			r.Log("hook %s %s: ok=%v %s", event, toolName, err == nil, truncate(out, 200))
		}
		if err != nil && event == "PreToolUse" {
			return fmt.Errorf("PreToolUse hook blocked %s: %v\n%s", toolName, err, truncate(out, 800))
		}
	}
	return nil
}

func match(pattern, tool string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return strings.Contains(tool, pattern)
	}
	return re.MatchString(tool)
}

// WrapHandler wraps a ToolExecutor with Pre/Post hooks.
func WrapHandler(r *Runner, name string, fn tools.ToolExecutor) tools.ToolExecutor {
	if r == nil || fn == nil {
		return fn
	}
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		if err := r.RunEvent(ctx, "PreToolUse", name, args, ""); err != nil {
			return nil, err
		}
		out, err := fn(ctx, args)
		res := ""
		if out != nil {
			res = fmt.Sprintf("%v", out)
		}
		_ = r.RunEvent(ctx, "PostToolUse", name, args, res)
		return out, err
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
