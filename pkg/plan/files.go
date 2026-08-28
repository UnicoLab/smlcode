package plan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListWorkspaceFiles returns a shallow inventory of source files (authoritative).
func ListWorkspaceFiles(root string, limit int) []string {
	if root == "" {
		return nil
	}
	if limit <= 0 {
		limit = 48
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if skipInventoryDir(filepath.ToSlash(rel), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, ".") {
			return nil
		}
		if !inventoryFileAllowed(rel) {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := inventoryPathPriority(out[i]), inventoryPathPriority(out[j])
		if pi != pj {
			return pi < pj
		}
		return out[i] < out[j]
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func inventoryFileAllowed(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "go.mod", "go.sum", "makefile", "dockerfile", "package-lock.json", "pnpm-lock.yaml",
		"yarn.lock", "cargo.lock", "requirements.txt":
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".md", ".json", ".yaml", ".yml",
		".toml", ".gradle":
		return true
	default:
		return false
	}
}

func skipInventoryDir(rel, name string) bool {
	switch name {
	case ".git", ".slmcode", "node_modules", "vendor", "bin", "dist", "build",
		"coverage", ".next", "out", "target", ".turbo", ".venv", "__pycache__":
		return true
	}
	switch rel {
	case "cmd/slmcode/ui", "web/dist":
		return true
	default:
		return false
	}
}

func inventoryPathPriority(path string) int {
	lp := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lp)
	switch base {
	case "go.mod", "package.json", "pyproject.toml", "cargo.toml", "pom.xml", "build.gradle",
		"makefile", "dockerfile", "readme.md", "agents.md", "project.md":
		return 0
	case "go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "cargo.lock",
		"requirements.txt", "compose.yaml", "docker-compose.yml":
		return 1
	}
	switch {
	case strings.HasPrefix(lp, "cmd/"):
		return 10
	case strings.HasPrefix(lp, "pkg/"):
		return 11
	case strings.HasPrefix(lp, "internal/"):
		return 12
	case strings.HasPrefix(lp, "web/src/"):
		return 13
	case strings.HasPrefix(lp, "src/"):
		return 14
	case strings.HasPrefix(lp, "test/") || strings.HasPrefix(lp, "tests/"):
		return 20
	case strings.HasPrefix(lp, "docs/"):
		return 30
	default:
		return 40
	}
}

// FileExists reports whether rel exists under root.
func FileExists(root, rel string) bool {
	if root == "" || rel == "" || strings.Contains(rel, "..") {
		return false
	}
	_, err := os.Stat(filepath.Join(root, filepath.Clean(rel)))
	return err == nil
}

// FilterExisting keeps only paths that exist on disk under root.
func FilterExisting(root string, files []string) []string {
	if root == "" {
		return uniq(files)
	}
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if FileExists(root, f) {
			out = append(out, f)
		}
	}
	return uniq(out)
}

// ReconcileFiles keeps claimed paths when they exist or look like intentional
// greenfield creates. Hallucinated missing paths fall back to discovered files.
//
// When nothing resolves it returns nil — NOT an arbitrary slice of the
// workspace. It used to return FilterExisting(root, ListWorkspaceFiles(root, 12)),
// and those twelve unrelated files went verbatim into "## Focus files (HARD
// SCOPE)" and into Focus.SetWave, so the anti-wander guard started PERMITTING
// writes to all twelve and an SLM with a vague task was invited to wander over
// them. A task whose targets cannot be resolved has unknown scope: callers must
// block it (move it to ColToScope for human scoping), not guess for it.
func ReconcileFiles(root string, claimed, discovered []string) []string {
	var planned []string
	for _, f := range claimed {
		f = strings.TrimSpace(f)
		if f == "" || strings.Contains(f, "..") || strings.HasPrefix(f, "/") {
			continue
		}
		planned = append(planned, filepath.ToSlash(filepath.Clean(f)))
	}
	planned = uniq(planned)
	if len(planned) > 0 {
		existing := FilterExisting(root, planned)
		var missing []string
		seen := map[string]bool{}
		for _, f := range existing {
			seen[f] = true
		}
		for _, f := range planned {
			if !seen[f] {
				missing = append(missing, f)
			}
		}
		// A repository with no source files in it cannot tell a real target
		// from an invented one — there is nothing to reconcile against. In
		// THAT state the claimed paths are the scope, because refusing them
		// means the harness can never scaffold a project anywhere the fixed
		// prefix list below does not already bless.
		//
		// This is what blocked "build a Go backend serving a React frontend":
		// cmd/server/main.go and web/src/App.tsx are the conventional layouts
		// for exactly that request, and neither starts with src/, tests/,
		// lib/ or app/, so every task the splitter wrote was parked as
		// unscoped and the run produced nothing.
		//
		// The looser rule is confined to the greenfield state on purpose. Once
		// a repository HAS source files, a claimed path that does not exist is
		// far more likely to be invented than intended, and the conservative
		// behavior below is the right one — which is why the existing
		// "hallucinated falls back to discovered" contract still holds.
		green := isGreenfieldRoot(root)
		out := append([]string{}, existing...)
		for _, f := range missing {
			if isGreenfieldCreatePath(f) || (green && looksLikeSourceTarget(f)) {
				out = append(out, f)
			}
		}
		if len(out) > 0 {
			return uniq(out)
		}
	}
	return FilterExisting(root, discovered)
}

func isGreenfieldCreatePath(f string) bool {
	f = strings.ToLower(strings.TrimSpace(f))
	if f == "" || strings.Contains(f, "path/to") || strings.Contains(f, "placeholder") {
		return false
	}
	base := filepath.Base(f)
	switch base {
	case "pyproject.toml", "requirements.txt", "setup.py", "package.json", "go.mod",
		"cargo.toml", "readme.md", "main.py", "conftest.py":
		return true
	}
	return strings.HasPrefix(f, "src/") || strings.HasPrefix(f, "tests/") ||
		strings.HasPrefix(f, "test/") || strings.HasPrefix(f, "lib/") ||
		strings.HasPrefix(f, "app/")
}

// isGreenfieldRoot reports whether a workspace holds no source files yet.
//
// Manifest-only counts as greenfield: a directory with nothing but go.mod or
// package.json is a project about to be written, not one to reconcile against.
func isGreenfieldRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		return true
	}
	for _, f := range ListWorkspaceFiles(root, 64) {
		if isExistingCode(f) {
			return false
		}
	}
	return true
}

// isExistingCode reports whether a path is CODE — the only kind of file that
// tells you where this project puts things.
//
// Narrower than looksLikeSourceTarget on purpose: those answer different
// questions. "May a task create this?" includes a README and a config file.
// "Does this repository have a layout to reconcile against?" does not — a
// directory holding go.mod and a README is still a project about to be written,
// and treating it as established re-blocks the greenfield scaffolding this
// whole rule exists to allow.
func isExistingCode(f string) bool {
	if isProjectManifest(f) {
		return false
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(f))) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".vue",
		".svelte", ".rs", ".java", ".kt", ".kts", ".rb", ".php", ".swift",
		".cs", ".c", ".h", ".cc", ".cpp", ".hpp":
		return true
	}
	return false
}

// isProjectManifest reports whether a path is a project descriptor rather than
// source. Their presence does not make a repository non-greenfield.
func isProjectManifest(f string) bool {
	switch strings.ToLower(filepath.Base(f)) {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml",
		"yarn.lock", "pyproject.toml", "requirements.txt", "setup.py", "cargo.toml",
		"cargo.lock", "gemfile", "composer.json", "build.gradle", "pom.xml":
		return true
	}
	return false
}

// looksLikeSourceTarget reports whether a path is a plausible file to create.
//
// Deliberately about the SHAPE of the path, not about a blessed directory: a
// real source extension, no placeholder marker, no traversal, and a sane depth.
// An extensionless path or one containing "path/to" is a model describing a
// file rather than naming one.
func looksLikeSourceTarget(f string) bool {
	f = strings.ToLower(strings.TrimSpace(filepath.ToSlash(f)))
	if f == "" || strings.HasPrefix(f, "/") || strings.Contains(f, "..") {
		return false
	}
	for _, marker := range []string{"path/to", "placeholder", "<", ">", "your-", "example/", "foo/bar"} {
		if strings.Contains(f, marker) {
			return false
		}
	}
	// A path nested eight levels deep is a description, not a target.
	if strings.Count(f, "/") > 7 {
		return false
	}
	return greenfieldSourceExts[filepath.Ext(f)]
}

// greenfieldSourceExts are the extensions a scaffolding task may create.
//
// Source and config only: an image, an archive or a compiled object is never
// something a worker should be told to write.
var greenfieldSourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".mjs": true, ".cjs": true, ".vue": true, ".svelte": true, ".rs": true,
	".java": true, ".kt": true, ".kts": true, ".rb": true, ".php": true,
	".swift": true, ".cs": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".hpp": true, ".sh": true, ".bash": true, ".sql": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".md": true,
	".mod": true, ".txt": true, ".env": true, ".gitignore": true, ".dockerfile": true,
}
