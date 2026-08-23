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
		out := append([]string{}, existing...)
		for _, f := range missing {
			if isGreenfieldCreatePath(f) {
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
