package plan

import (
	"os"
	"path/filepath"
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
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".slmcode" || name == "node_modules" || name == "vendor" ||
				name == "bin" || name == "dist" || name == ".venv" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".md", ".json", ".yaml", ".yml":
		default:
			return nil
		}
		out = append(out, rel)
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out
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
	fallback := FilterExisting(root, discovered)
	if len(fallback) > 0 {
		return fallback
	}
	return FilterExisting(root, ListWorkspaceFiles(root, 12))
}

// Keep testable helper name used by greenfield sanitize paths.
func looksLikeCreateTarget(f string) bool { return isGreenfieldCreatePath(f) }

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
