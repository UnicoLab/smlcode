package permissions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Modes mirror Claude Code permission styles for local SLMs.
const (
	ModeAuto   = "auto"    // write immediately
	ModeDryRun = "dry-run" // never write
	ModeReview = "review"  // write proposed diffs under .slmcode/pending/
)

// Shell permission modes for ws_shell (independent of file write policy).
const (
	ShellAllow = "allow" // run commands (default)
	ShellAsk   = "ask"   // record pending; do not execute
	ShellDeny  = "deny"  // reject shell tools
)

func Normalize(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case ModeAuto, "allow", "yes":
		return ModeAuto
	case ModeDryRun, "dryrun", "dry":
		return ModeDryRun
	case ModeReview, "ask", "pending":
		return ModeReview
	default:
		if m == "" {
			return ModeAuto
		}
		return ModeAuto
	}
}

// NormalizeShell normalizes ws_shell policy.
func NormalizeShell(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case ShellAllow, "auto", "yes", "":
		return ShellAllow
	case ShellAsk, "review", "pending":
		return ShellAsk
	case ShellDeny, "no", "block", "off":
		return ShellDeny
	default:
		return ShellAllow
	}
}

// RecordPending stores a proposed file change for human apply.
func RecordPending(slmDir, path, kind, content string) (string, error) {
	dir := filepath.Join(slmDir, "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	safe := strings.ReplaceAll(path, string(os.PathSeparator), "__")
	name := fmt.Sprintf("%d_%s_%s.patch.json", time.Now().UnixNano(), kind, safe)
	full := filepath.Join(dir, name)
	body := fmt.Sprintf("{\n  \"path\": %q,\n  \"kind\": %q,\n  \"content\": %q\n}\n", path, kind, content)
	return full, atomicfile.Write(full, []byte(body), 0o644)
}
