package quality

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Node counts a comment-only file as a successful file-level test. Reject that
// specific false positive without requiring a particular assertion library or
// confusing top-level assertions with an empty suite. Inspect only explicit,
// sanitized file arguments; this is not a general JavaScript test-quality judge.
func emptyNodeTestFile(root, command string) string {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	for _, part := range strings.Split(command, "&&") {
		cmd := SanitizeAcceptanceCommand(strings.TrimSpace(part), "node --test")
		if cmd == "" {
			continue
		}
		for _, arg := range strings.Fields(cmd)[2:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(base, arg))
			for i, path := range matches {
				if i >= 256 {
					break
				}
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					continue
				}
				rel, err := filepath.Rel(base, resolved)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				f, err := os.Open(resolved)
				if err != nil {
					continue
				}
				data, readErr := io.ReadAll(io.LimitReader(f, 65537))
				_ = f.Close()
				if readErr == nil && len(data) <= 65536 && jsCommentsOnly(string(data)) {
					return filepath.ToSlash(rel)
				}
			}
		}
	}
	return ""
}

// Stop at the first actual token. Strings, regexps and template literals are
// therefore never parsed as comments; their opening token already means code.
func jsCommentsOnly(source string) bool {
	source = strings.TrimPrefix(source, "\ufeff")
	if strings.HasPrefix(source, "#!") {
		_, source, _ = strings.Cut(source, "\n")
	}
	for {
		source = strings.TrimSpace(source)
		switch {
		case source == "":
			return true
		case strings.HasPrefix(source, "//"):
			_, source, _ = strings.Cut(source, "\n")
		case strings.HasPrefix(source, "/*"):
			var closed bool
			_, source, closed = strings.Cut(source[2:], "*/")
			if !closed {
				return true
			}
		default:
			return false
		}
	}
}
