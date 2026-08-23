package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// FocusGuard constrains workspace writes to task focus files / packages.
// Safe for concurrent waves: SetWave replaces the active allowlist atomically.
//
// Greenfield / scaffold mode: when focus includes root project files
// (pyproject.toml, package.json, …) or explicit directory prefixes (src/),
// new package trees may be created without treating that as wander.
type FocusGuard struct {
	mu       sync.RWMutex
	enabled  bool
	scaffold bool // allow creating project tree files
	files    map[string]struct{}
	dirs     map[string]struct{}
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
	g.scaffold = false
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
	g.scaffold = false
	for _, list := range focusLists {
		for _, f := range list {
			f = normalizeRel(f)
			if f == "" || f == "." {
				continue
			}
			// Trailing slash or bare dir name → directory allow.
			if strings.HasSuffix(f, "/") || !strings.Contains(filepath.Base(f), ".") {
				g.dirs[strings.TrimSuffix(f, "/")] = struct{}{}
				continue
			}
			g.files[f] = struct{}{}
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir != "" && dir != "." {
				g.dirs[dir] = struct{}{}
			} else if isRootProjectFile(f) {
				// Root manifest ⇒ greenfield scaffold (src/, tests/, README, …).
				g.scaffold = true
				g.dirs["src"] = struct{}{}
				g.dirs["tests"] = struct{}{}
				g.dirs["test"] = struct{}{}
				g.dirs["lib"] = struct{}{}
				g.dirs["app"] = struct{}{}
				g.dirs["pkg"] = struct{}{}
			}
		}
	}
	g.enabled = len(g.files) > 0 || len(g.dirs) > 0
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
	// .slmcode/ is harness state, not agent workspace. It used to be
	// unconditionally writable, which let an agent drop a hooks.json (arbitrary
	// bash on the next run), rewrite config.yaml to disable its own guards, or
	// forge pending/*.patch.json. Only the scratch subtree is agent-writable.
	if IsHarnessStatePath(path) {
		return AllowedScratchPath(path)
	}
	if _, ok := g.files[path]; ok {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if _, ok := g.dirs[dir]; ok {
		return true
	}
	for d := range g.dirs {
		if path == d || strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	if g.scaffold && isScaffoldPath(path) {
		return true
	}
	return false
}

// Check returns an error when a write is out of scope.
func (g *FocusGuard) Check(path string) error {
	if err := CheckHarnessStateWrite(path); err != nil {
		return err
	}
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

func isRootProjectFile(path string) bool {
	if strings.Contains(path, "/") {
		return false
	}
	switch strings.ToLower(path) {
	case "pyproject.toml", "setup.py", "setup.cfg", "requirements.txt",
		"package.json", "cargo.toml", "go.mod", "pom.xml", "build.gradle",
		"readme.md", "makefile", "cmakelists.txt":
		return true
	default:
		return false
	}
}

func isScaffoldPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "readme.md", "pyproject.toml", "requirements.txt", "setup.py",
		"package.json", "go.mod", "makefile", ".env.example", "license",
		"license.md", "conftest.py", "pytest.ini", "tox.ini", ".gitignore":
		return true
	}
	// main.py at project root is OK for Python MVP entrypoints during scaffold.
	if !strings.Contains(path, "/") && base == "main.py" {
		return true
	}
	for _, prefix := range []string{"src/", "tests/", "test/", "lib/", "app/", "pkg/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// HarnessStateDir is the harness's private control directory.
const HarnessStateDir = ".slmcode"

// ScratchDir is the ONLY path under HarnessStateDir that tools may write.
// Agents use it for notes / todo lists; nothing in it is ever executed or
// interpreted as configuration.
const ScratchDir = ".slmcode/scratch"

// IsHarnessStatePath reports whether rel lives under .slmcode/.
func IsHarnessStatePath(rel string) bool {
	rel = normalizeRel(rel)
	return rel == HarnessStateDir || strings.HasPrefix(rel, HarnessStateDir+"/")
}

// AllowedScratchPath reports whether rel is inside the agent scratch subtree.
func AllowedScratchPath(rel string) bool {
	rel = normalizeRel(rel)
	return rel == ScratchDir || strings.HasPrefix(rel, ScratchDir+"/")
}

// CheckHarnessStateWrite refuses tool writes into .slmcode/ outside scratch.
// This holds even when the focus guard is disabled — it is a privilege
// boundary, not an anti-wander heuristic.
func CheckHarnessStateWrite(path string) error {
	rel := normalizeRel(path)
	if !IsHarnessStatePath(rel) || AllowedScratchPath(rel) {
		return nil
	}
	return fmt.Errorf(
		"write refused — %s is harness control state, not project source.\n"+
			"Files under .slmcode/ (hooks.json, config.yaml, pending/, checkpoints/) configure the "+
			"harness itself and are never edited by tools.\n"+
			"If you need scratch space, write under %s/ instead. "+
			"If you meant to change project code, use the real source path.",
		rel, ScratchDir,
	)
}
