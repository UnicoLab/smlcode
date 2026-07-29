package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// FocusGuard constrains workspace writes to task focus files / packages.
// Safe for concurrent waves: SetWave replaces the active allowlist atomically.
type FocusGuard struct {
	mu      sync.RWMutex
	enabled bool
	files   map[string]struct{} // exact relative paths
	dirs    map[string]struct{} // package/dir prefixes derived from files
}

// NewFocusGuard returns an inactive guard (all writes allowed until SetWave).
func NewFocusGuard() *FocusGuard {
	return &FocusGuard{
		files: map[string]struct{}{},
		dirs:  map[string]struct{}{},
	}
}

// Clear disables focus enforcement (explore / docs / unrestricted phases).
func (g *FocusGuard) Clear() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = false
	g.files = map[string]struct{}{}
	g.dirs = map[string]struct{}{}
}

// SetWave activates an allowlist from the union of task focus file lists.
// When every list is empty, the guard stays disabled.
func (g *FocusGuard) SetWave(focusLists [][]string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files = map[string]struct{}{}
	g.dirs = map[string]struct{}{}
	for _, list := range focusLists {
		for _, f := range list {
			f = normalizeRel(f)
			if f == "" || f == "." {
				continue
			}
			g.files[f] = struct{}{}
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir != "" && dir != "." {
				g.dirs[dir] = struct{}{}
			}
		}
	}
	g.enabled = len(g.files) > 0
}

// Enabled reports whether writes are constrained.
func (g *FocusGuard) Enabled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// Allow reports whether a relative path may be written.
func (g *FocusGuard) Allow(path string) bool {
	if g == nil {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.enabled {
		return true
	}
	path = normalizeRel(path)
	if path == "" {
		return false
	}
	// Always allow .slmcode memory / pending.
	if path == ".slmcode" || strings.HasPrefix(path, ".slmcode/") {
		return true
	}
	if _, ok := g.files[path]; ok {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if _, ok := g.dirs[dir]; ok {
		return true
	}
	// Nested under an allowed package prefix.
	for d := range g.dirs {
		if path == d || strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}

// Check returns an error when a write is out of scope.
func (g *FocusGuard) Check(path string) error {
	if g == nil || g.Allow(path) {
		return nil
	}
	path = normalizeRel(path)
	base := filepath.Base(path)
	if isEntrypointName(base) && !strings.Contains(path, "/") {
		return fmt.Errorf("out-of-scope write blocked: %s (not in task focus files; do not create root entrypoints)", path)
	}
	return fmt.Errorf("out-of-scope write blocked: %s (stay within focus files / their packages)", path)
}

// OutOfScopeFiles filters claimed/changed paths that violate the allowlist.
func (g *FocusGuard) OutOfScopeFiles(paths []string) []string {
	if g == nil || !g.Enabled() {
		return nil
	}
	var bad []string
	for _, p := range paths {
		p = normalizeRel(p)
		if p == "" || strings.HasPrefix(p, ".slmcode/") {
			continue
		}
		if !g.Allow(p) {
			bad = append(bad, p)
		}
	}
	return bad
}

func normalizeRel(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." {
		return ""
	}
	return p
}

func isEntrypointName(base string) bool {
	switch strings.ToLower(base) {
	case "main.go", "main.py", "main.ts", "main.js", "main.rs",
		"index.js", "index.ts", "index.tsx", "app.js", "app.ts", "app.tsx",
		"program.cs", "__main__.py":
		return true
	default:
		return false
	}
}
