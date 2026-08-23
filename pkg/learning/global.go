package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// GlobalMemoryPaths returns user-level memory locations. The first path is the
// write target; the others are read-only fallbacks kept for config-dir parity.
func GlobalMemoryPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".slmcode", "MEMORY.md"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "slmcode", "MEMORY.md"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".config", "slmcode", "MEMORY.md"))
	}
	return uniqPaths(paths)
}

func ReadGlobalMemory() string {
	var b strings.Builder
	for _, path := range GlobalMemoryPaths() {
		data, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(string(data))
	}
	return b.String()
}

func AppendGlobalMemory(sectionTitle, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	paths := GlobalMemoryPaths()
	if len(paths) == 0 {
		return nil
	}
	path := paths[0]
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { // ~/.slmcode global memory, owner-only
		return err
	}
	existing, _ := os.ReadFile(path)
	if len(existing) == 0 {
		existing = []byte("# SLMCode Global Memory\n\n## Lessons\n\n")
	}
	stamp := time.Now().Format(time.RFC3339)
	block := fmt.Sprintf("\n\n## %s (%s)\n\n%s\n", sectionTitle, stamp, body)
	return atomicfile.Write(path, append(existing, []byte(block)...), 0o644)
}

// RecentAdaptiveMemory extracts compact high-signal lessons from project and
// global memory for injection into lean worker/corrector packs.
func RecentAdaptiveMemory(projectMemory, globalMemory string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 1600
	}
	lines := appendAdaptiveLines(nil, globalMemory, "global")
	lines = appendAdaptiveLines(lines, projectMemory, "project")
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	out := strings.Join(dedupeStrings(lines), "\n")
	if len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
		if i := strings.Index(out, "\n"); i >= 0 {
			out = out[i+1:]
		}
	}
	return strings.TrimSpace(out)
}

func appendAdaptiveLines(out []string, raw, source string) []string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "timeout") &&
			!strings.Contains(lower, "timed out") &&
			!strings.Contains(lower, "deadline") &&
			!strings.Contains(lower, "max_parallel") &&
			!strings.Contains(lower, "contention") &&
			!strings.Contains(lower, "smoke") &&
			!strings.Contains(lower, "qa_gate") &&
			!strings.Contains(lower, "acceptance") &&
			!strings.Contains(lower, "placeholder") &&
			!strings.Contains(lower, "stub") &&
			!strings.Contains(lower, "max retries") {
			continue
		}
		line = strings.TrimLeft(line, "-* !+>~")
		if line == "" {
			continue
		}
		out = append(out, fmt.Sprintf("- %s memory: %s", source, line))
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func uniqPaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "." || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
