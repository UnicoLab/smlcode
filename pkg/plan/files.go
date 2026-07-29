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

// ReconcileFiles drops hallucinated paths and falls back to discovered real files.
func ReconcileFiles(root string, claimed, discovered []string) []string {
	real := FilterExisting(root, claimed)
	if len(real) > 0 {
		return real
	}
	fallback := FilterExisting(root, discovered)
	if len(fallback) > 0 {
		return fallback
	}
	return FilterExisting(root, ListWorkspaceFiles(root, 12))
}
