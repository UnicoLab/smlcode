package plan

import (
	"os"
	"path/filepath"
	"strings"
)

// DiscoverRelevantFiles finds likely target files when the SLM explorer is vague.
// Combines exploration text, query tokens, and a shallow workspace walk.
func DiscoverRelevantFiles(root, query, exploration string) []string {
	found := ExtractFilePaths(exploration + "\n" + query)
	if root == "" {
		return found
	}

	tokens := queryTokens(query)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			name := ""
			if d != nil {
				name = d.Name()
			}
			if name == ".git" || name == ".slmcode" || name == "node_modules" || name == "vendor" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		base := strings.ToLower(filepath.Base(rel))
		ext := strings.ToLower(filepath.Ext(rel))
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".md":
		default:
			return nil
		}
		match := false
		for _, tok := range tokens {
			if strings.Contains(base, tok) || strings.Contains(strings.ToLower(rel), tok) {
				match = true
				break
			}
		}
		// Always keep root-level tiny sources for trivial workspaces
		if !match && strings.Count(rel, string(os.PathSeparator)) == 0 && ext == ".go" {
			match = true
		}
		if match {
			found = append(found, rel)
		}
		if len(found) > 24 {
			return filepath.SkipAll
		}
		return nil
	})
	// Never advertise paths that are not on disk (SLMs otherwise invent main.go).
	return FilterExisting(root, uniq(found))
}

func queryTokens(query string) []string {
	q := strings.ToLower(query)
	repl := strings.NewReplacer("(", " ", ")", " ", ",", " ", ".", " ", "/", " ", "_", " ", "-", " ")
	parts := strings.Fields(repl.Replace(q))
	var out []string
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		switch p {
		case "the", "and", "add", "for", "with", "from", "that", "this", "keep", "tiny", "change", "function", "returns", "comment", "doc":
			continue
		}
		out = append(out, p)
	}
	return out
}
