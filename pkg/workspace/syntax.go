package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Post-edit syntax diagnostics (SWE-agent's single highest-value ACI guardrail).
//
// After a write/edit/patch we run a cheap, file-local parser on the edited file
// only. Two behaviors follow from the result:
//
//  1. The file did not parse before and still does not → report the error
//     in-band so the model fixes it on the very next turn instead of
//     discovering it three tool calls later.
//  2. The file DID parse before and does NOT now → the edit introduced the
//     break. Revert it and tell the model exactly what it broke. SWE-agent
//     measured ~+3 points on SWE-bench from exactly this revert.
//
// TypeScript is deliberately NOT checked: `tsc --noEmit` needs the whole
// program and routinely takes 10s+, which is far too slow to sit in the middle
// of a tool call.

// DefaultSyntaxCheckTimeout bounds one syntax check.
const DefaultSyntaxCheckTimeout = 10 * time.Second

// SyntaxStatus is the tri-state outcome of a syntax check.
type SyntaxStatus int

const (
	// SyntaxSkipped means no checker was available for this file type.
	SyntaxSkipped SyntaxStatus = iota
	// SyntaxOK means the file parses.
	SyntaxOK
	// SyntaxBroken means the file does not parse.
	SyntaxBroken
)

// SyntaxResult carries the checker verdict plus its (trimmed) diagnostics.
type SyntaxResult struct {
	Status SyntaxStatus
	Errors string
	Tool   string
}

// syntaxChecker returns the argv for a file-local parse check, or nil.
func syntaxChecker(absPath string) []string {
	switch strings.ToLower(filepath.Ext(absPath)) {
	case ".go":
		// gofmt -e parses and exits non-zero with parse errors on stderr.
		return []string{"gofmt", "-e", "-l", absPath}
	case ".py":
		// compile() parses without importing — no side effects, no deps.
		return []string{"python3", "-c",
			"import sys;src=open(sys.argv[1],'rb').read();compile(src,sys.argv[1],'exec')",
			absPath}
	case ".js", ".mjs", ".cjs":
		return []string{"node", "--check", absPath}
	case ".json":
		return []string{"python3", "-c",
			"import json,sys;json.load(open(sys.argv[1]))", absPath}
	}
	return nil
}

// pythonBinary picks python3 then python, so the checker works on hosts that
// only ship an unversioned interpreter.
func resolveChecker(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	if _, err := exec.LookPath(argv[0]); err == nil {
		return argv
	}
	if argv[0] == "python3" {
		if _, err := exec.LookPath("python"); err == nil {
			out := append([]string{"python"}, argv[1:]...)
			return out
		}
	}
	return nil
}

// CheckSyntax runs the language-appropriate parse check on one file.
func CheckSyntax(ctx context.Context, absPath string, timeout time.Duration) SyntaxResult {
	argv := resolveChecker(syntaxChecker(absPath))
	if argv == nil {
		return SyntaxResult{Status: SyntaxSkipped}
	}
	if timeout <= 0 {
		timeout = DefaultSyntaxCheckTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// argv is built by resolveChecker/syntaxChecker from the file's own
	// extension against a fixed table of interpreters (python3, node, gofmt,
	// ...) — not attacker-controlled input.
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...) //nolint:gosec // argv comes from our fixed checker table, not external input
	cmd.Dir = filepath.Dir(absPath)
	buf := newHeadTailBuffer(16 * 1024)
	cmd.Stdout = buf
	cmd.Stderr = buf
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	out, _ := buf.String()
	if cctx.Err() != nil {
		// A timed-out checker must never be read as "broken".
		return SyntaxResult{Status: SyntaxSkipped, Tool: argv[0]}
	}
	if err == nil {
		return SyntaxResult{Status: SyntaxOK, Tool: argv[0]}
	}
	if _, isExit := err.(*exec.ExitError); !isExit {
		// Checker could not run at all (missing runtime, permission) → skip.
		return SyntaxResult{Status: SyntaxSkipped, Tool: argv[0]}
	}
	return SyntaxResult{Status: SyntaxBroken, Errors: FirstSyntaxErrors(out, 2), Tool: argv[0]}
}

// FirstSyntaxErrors trims checker output to the first n meaningful lines.
func FirstSyntaxErrors(out string, n int) string {
	var keep []string
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		keep = append(keep, t)
		if len(keep) >= n {
			break
		}
	}
	return strings.Join(keep, "\n")
}

// SyntaxWarning renders the in-band ⚠ block appended to a successful edit.
func SyntaxWarning(rel string, res SyntaxResult) string {
	if res.Status != SyntaxBroken {
		return ""
	}
	return fmt.Sprintf(
		"\n\n⚠ syntax check failed (%s) on %s:\n%s\n"+
			"FIX THIS NOW with ws_edit before doing anything else — the file will not compile/import as written. "+
			"ws_read the affected line span first if you are unsure of the exact current text.",
		res.Tool, rel, res.Errors,
	)
}

// SyntaxRevertMessage renders the model-facing explanation for a reverted edit.
func SyntaxRevertMessage(rel string, res SyntaxResult) string {
	return fmt.Sprintf(
		"EDIT REVERTED — %s parsed correctly before your change and does NOT parse after it (%s):\n%s\n\n"+
			"The file is unchanged on disk. Fix the syntax in your replacement text and retry:\n"+
			"  • check brackets/parens/quotes are balanced in new_str\n"+
			"  • check indentation matches the surrounding block\n"+
			"  • ws_read the exact span again if you are unsure of the current text\n"+
			"Do NOT retry the identical edit — it will be reverted again.",
		rel, res.Tool, res.Errors,
	)
}

// applyWithSyntaxGuard writes next to abs, running the pre/post syntax check
// when enabled. It returns the note to append to the tool result, whether the
// write was reverted, and any hard I/O error.
//
// prev is the file content before the edit ("" for a brand-new file).
func (w *Workspace) applyWithSyntaxGuard(ctx context.Context, rel, abs, prev, next string, existed bool) (note string, reverted bool, err error) {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil { //nolint:gosec // directory in the user's source tree — conventional 0755, not harness state
		return "", false, err
	}
	if w == nil || !w.SyntaxCheck || syntaxChecker(abs) == nil {
		// Ordinary project source file — conventional 0644, not secret state.
		if err := os.WriteFile(abs, []byte(next), 0o644); err != nil { //nolint:gosec // project source file, conventional perms
			return "", false, err
		}
		return "", false, nil
	}
	// Establish the "before" verdict from the in-memory previous content so we
	// never blame an edit for a break that was already there.
	before := SyntaxResult{Status: SyntaxSkipped}
	if existed {
		before = w.checkSyntaxOfText(ctx, abs, prev)
	}
	if err := os.WriteFile(abs, []byte(next), 0o644); err != nil { //nolint:gosec // project source file, conventional perms
		return "", false, err
	}
	after := CheckSyntax(ctx, abs, w.syntaxTimeout())
	if after.Status != SyntaxBroken {
		return "", false, nil
	}
	if before.Status == SyntaxOK {
		// Regression introduced by this edit → put the file back.
		if rerr := os.WriteFile(abs, []byte(prev), 0o644); rerr != nil { //nolint:gosec // project source file, conventional perms
			// Could not restore: keep the broken file but say so loudly.
			return SyntaxWarning(rel, after) +
				"\n(NOTE: automatic revert failed: " + rerr.Error() + ")", false, nil
		}
		return SyntaxRevertMessage(rel, after), true, nil
	}
	return SyntaxWarning(rel, after), false, nil
}

func (w *Workspace) syntaxTimeout() time.Duration {
	if w != nil && w.SyntaxCheckTimeout > 0 {
		return w.SyntaxCheckTimeout
	}
	return DefaultSyntaxCheckTimeout
}

// checkSyntaxOfText parses text under a temp file that keeps the original
// extension, so the "before" verdict never depends on disk state we just
// overwrote.
func (w *Workspace) checkSyntaxOfText(ctx context.Context, abs, text string) SyntaxResult {
	dir, err := os.MkdirTemp("", "slmcode-syntax-")
	if err != nil {
		return SyntaxResult{Status: SyntaxSkipped}
	}
	// Best-effort temp-dir cleanup; nothing actionable if it fails.
	defer func() { _ = os.RemoveAll(dir) }()
	tmp := filepath.Join(dir, "before"+filepath.Ext(abs))
	// Scratch copy under a private temp dir, read only by the checker below.
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil { //nolint:gosec // tmp is our own os.MkdirTemp-generated private scratch path, not external input
		return SyntaxResult{Status: SyntaxSkipped}
	}
	return CheckSyntax(ctx, tmp, w.syntaxTimeout())
}
