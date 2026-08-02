package quality

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// PreciseGap is a file-level placeholder/stub finding for HITL or fill agents.
type PreciseGap struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// ScanProjectPlaceholders walks the workspace for stub/placeholder code.
// Skips VCS/cache dirs. Prefers board focus files when provided, then walks.
func ScanProjectPlaceholders(root string, board *plan.Board) []PreciseGap {
	if root == "" {
		return nil
	}
	seen := map[string]bool{}
	var gaps []PreciseGap
	add := func(rel string) {
		rel = strings.TrimSpace(strings.TrimPrefix(rel, "./"))
		if rel == "" || seen[rel] || strings.Contains(rel, "..") {
			return
		}
		ext := strings.ToLower(filepath.Ext(rel))
		switch ext {
		case ".py", ".go", ".js", ".ts", ".tsx", ".jsx":
		default:
			return
		}
		abs := filepath.Join(root, rel)
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			return
		}
		text := string(data)
		if why := staticReason(text, ext); why != "" {
			seen[rel] = true
			gaps = append(gaps, PreciseGap{
				Path:    rel,
				Reason:  why,
				Line:    firstPlaceholderLine(text),
				Snippet: firstPlaceholderSnippet(text),
			})
		}
	}

	if board != nil {
		for _, t := range board.Tasks {
			for _, f := range t.Files {
				add(f)
			}
			for _, f := range parseFilesChangedLoose(t.Output) {
				add(f)
			}
		}
	}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".slmcode", "__pycache__", "node_modules", ".venv", "venv",
				"dist", "build", ".tox", ".mypy_cache", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		add(rel)
		return nil
	})
	return gaps
}

// FormatPlaceholderReport builds a markdown section for agents / Studio.
func FormatPlaceholderReport(gaps []PreciseGap) string {
	if len(gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Placeholder gaps (precise)\n")
	b.WriteString(fmt.Sprintf("%d file(s) still need real implementations:\n", len(gaps)))
	for _, g := range gaps {
		loc := g.Path
		if g.Line > 0 {
			loc = fmt.Sprintf("%s:%d", g.Path, g.Line)
		}
		b.WriteString(fmt.Sprintf("- `%s` — %s\n", loc, g.Reason))
		if s := strings.TrimSpace(g.Snippet); s != "" {
			b.WriteString(fmt.Sprintf("  snippet: `%s`\n", truncateRunes(s, 80)))
		}
	}
	b.WriteString("Fill each gap with working code, or leave a `// TODO(precise): …` " +
		"only if blocked on missing secrets/APIs (not laziness).\n")
	return b.String()
}

func firstPlaceholderLine(text string) int {
	for i, ln := range strings.Split(text, "\n") {
		if rePlaceholder.MatchString(ln) || reStubReturn.MatchString(ln) ||
			reBadLangGraphImport.MatchString(ln) || reNotImplemented.MatchString(ln) ||
			rePassStub.MatchString(ln) || strings.Contains(strings.ToLower(ln), "placeholder") {
			return i + 1
		}
	}
	return 0
}

func firstPlaceholderSnippet(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if rePlaceholder.MatchString(t) || reStubReturn.MatchString(t) ||
			reBadLangGraphImport.MatchString(t) || strings.Contains(strings.ToLower(t), "placeholder") {
			return t
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
