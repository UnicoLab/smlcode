package workspace

import (
	"fmt"
	"os"
	"strings"
)

// BuiltinSafePrefixes is little-coder's SAFE_PREFIXES plus common coding
// smoke/test commands used by slmcode workers.
var BuiltinSafePrefixes = []string{
	"ls", "cat", "head", "tail", "wc", "pwd", "echo", "printf", "date",
	"which", "type", "env", "printenv", "uname", "whoami", "id",
	"git log", "git status", "git diff", "git show", "git branch",
	"git remote", "git stash list", "git tag",
	"find ", "grep ", "rg ", "ag ", "fd ", "sed ",
	"python ", "python3 ", "node ", "ruby ", "perl ",
	"pip show", "pip list", "npm list", "npm test", "npm run", "npx ",
	"cargo metadata", "cargo test", "cargo build",
	"df ", "du ", "free ", "top -bn", "ps ",
	"curl -I", "curl --head",
	"cp ", "mv ", "mkdir ", "touch ",
	// slmcode coding smoke
	"go test", "go build", "go vet", "go run", "go fmt", "gofmt ",
	"pytest", "python -m ", "python3 -m ",
	"uv run", "uv pip", "make ", "cmake ",
	"node --check", "tsc ", "eslint ",
	"true", "false", "test ", "[",
}

// ParseExtraPrefixes splits a comma-separated allow list (trailing spaces kept
// for word-boundary matching, like little-coder).
func ParseExtraPrefixes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimLeft(part, " \t")
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SafePrefixes merges builtins with extras (env SLMCODE_BASH_ALLOW + config).
func SafePrefixes(extra []string) []string {
	out := append([]string{}, BuiltinSafePrefixes...)
	out = append(out, ParseExtraPrefixes(os.Getenv("SLMCODE_BASH_ALLOW"))...)
	out = append(out, extra...)
	return out
}

// IsSafeBash reports whether every chain segment is whitelisted and none write
// via redirect/tee/dd (little-coder permission-gate #70).
func IsSafeBash(command string, prefixes []string) bool {
	if HasWriteRedirection(command) {
		return false
	}
	segs := splitCommandChain(command)
	if len(segs) == 0 {
		return false
	}
	if len(prefixes) == 0 {
		prefixes = BuiltinSafePrefixes
	}
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if !segmentHasSafePrefix(seg, prefixes) {
			return false
		}
	}
	return true
}

func segmentHasSafePrefix(segment string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(segment, p) {
			return true
		}
	}
	return false
}

// GuardShellWhitelist refuses non-whitelisted shell in auto mode.
// Returns a model-facing refusal string when blocked.
func GuardShellWhitelist(command string, extra []string) (refuse string, blocked bool) {
	prefixes := SafePrefixes(extra)
	if IsSafeBash(command, prefixes) {
		return "", false
	}
	writes := DetectWriteTargets(command)
	if len(writes) > 0 {
		var paths []string
		for _, w := range writes {
			paths = append(paths, `"`+w.Path+`"`)
		}
		return fmt.Sprintf(
			"shell whitelist: this command writes to %s via shell redirection. "+
				"Use ws_write for a new file, or ws_edit/ws_patch for an existing one — "+
				"do not redirect into files.",
			strings.Join(paths, ", "),
		), true
	}
	offender := command
	for _, seg := range splitCommandChain(command) {
		seg = strings.TrimSpace(seg)
		if seg != "" && !segmentHasSafePrefix(seg, prefixes) {
			offender = seg
			break
		}
	}
	bin := strings.Fields(offender)
	name := offender
	if len(bin) > 0 {
		name = bin[0]
	}
	return fmt.Sprintf(
		`shell whitelist: "%s" is not in SAFE_PREFIXES. Use ws_edit/ws_write/ws_shell `+
			`with an allowed smoke command (go test / pytest / python -m py_compile / …).`,
		name,
	), true
}
