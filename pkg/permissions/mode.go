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
	if err := os.MkdirAll(dir, 0o750); err != nil { // pending proposals, owner-only
		return "", err
	}
	name := fmt.Sprintf("%d_%s_%s.patch.json", time.Now().UnixNano(), sanitizeComponent(kind), sanitizeComponent(path))
	full := filepath.Join(dir, name)
	body := fmt.Sprintf("{\n  \"path\": %q,\n  \"kind\": %q,\n  \"content\": %q\n}\n", path, kind, content)
	return full, atomicfile.Write(full, []byte(body), 0o644)
}

// maxPendingNameBytes bounds the flattened path inside a queue file name so a
// model-supplied path cannot blow past the filesystem's NAME_MAX.
const maxPendingNameBytes = 120

// sanitizeComponent flattens an arbitrary caller-supplied string into one safe
// file-name component.
//
// The old version replaced only os.PathSeparator, so on Windows a forward
// slash survived and `../../x` became a real relative path escaping the queue
// directory; `kind` was interpolated with no sanitizing at all. Everything
// outside [A-Za-z0-9._-] now collapses to "_", and the result is bounded.
func sanitizeComponent(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "file"
	}
	if len(out) > maxPendingNameBytes {
		out = out[len(out)-maxPendingNameBytes:]
	}
	return out
}
