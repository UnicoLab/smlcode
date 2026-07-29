package instructions

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadProjectInstructions gathers Claude Code / Cursor / AGENTS.md style instructions.
func LoadProjectInstructions(root string) string {
	candidates := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"AGENT.md",
		".cursorrules",
		filepath.Join(".slmcode", "PROJECT.md"),
		filepath.Join(".slmcode", "AGENTS.md"),
		"README.md",
	}
	var parts []string
	seen := map[string]bool{}
	budget := 12000
	used := 0
	for _, rel := range candidates {
		if used >= budget {
			break
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || len(data) == 0 {
			continue
		}
		key := strings.ToLower(filepath.Base(rel))
		if seen[key] && key != "project.md" {
			continue
		}
		seen[key] = true
		body := string(data)
		if len(body) > 4000 {
			body = body[:4000] + "\n…[truncated]"
		}
		parts = append(parts, "## "+rel+"\n\n"+body)
		used += len(body)
	}
	return strings.Join(parts, "\n\n")
}
