package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// SeedProjectMarkdown builds a useful PROJECT.md from repo metadata.
// Used at init and when PROJECT.md is still an empty scaffold.
func SeedProjectMarkdown(root, projectName string) string {
	if projectName == "" {
		projectName = filepath.Base(root)
	}
	overview := detectOverview(root, projectName)
	conventions := detectConventions(root)
	paths := detectKeyPaths(root)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Project: %s\n\n", projectName))
	b.WriteString("## Overview\n\n")
	b.WriteString(overview)
	b.WriteString("\n\n## Conventions\n\n")
	if conventions == "" {
		b.WriteString("- Prefer small, reviewable edits\n- Follow existing package layout and naming\n")
	} else {
		b.WriteString(conventions)
	}
	b.WriteString("\n## Key paths\n\n")
	b.WriteString("| Path | Role |\n|------|------|\n")
	if len(paths) == 0 {
		b.WriteString("| . | project root |\n")
	} else {
		for _, p := range paths {
			b.WriteString(fmt.Sprintf("| %s | %s |\n", p.path, p.role))
		}
	}
	return b.String()
}

// ProjectNeedsSeed reports whether PROJECT.md is still an empty scaffold.
func ProjectNeedsSeed(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return true
	}
	// Scaffold has empty Overview/Conventions and blank key-path row.
	hasEmptyOverview := strings.Contains(body, "## Overview\n\n\n") || strings.Contains(body, "## Overview\n\n## ")
	hasBlankPath := strings.Contains(body, "| | |") || strings.Contains(body, "|  |  |")
	return hasEmptyOverview || (hasBlankPath && len(body) < 280)
}

type keyPath struct{ path, role string }

func detectOverview(root, name string) string {
	for _, candidate := range []string{"README.md", "readme.md", "STRUCTURE.md"} {
		data, err := os.ReadFile(filepath.Join(root, candidate))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		// Prefer markdown body; skip HTML badge/header noise common in READMEs.
		lines := strings.Split(text, "\n")
		var paras []string
		var cur strings.Builder
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "<") || strings.HasPrefix(trim, "---") ||
				strings.HasPrefix(trim, "![") || strings.HasPrefix(trim, "[!") {
				if cur.Len() > 0 {
					paras = append(paras, strings.TrimSpace(cur.String()))
					if len(paras) >= 2 {
						break
					}
					cur.Reset()
				}
				continue
			}
			if strings.HasPrefix(trim, "#") {
				// Keep heading text if descriptive, else skip.
				title := strings.TrimSpace(strings.TrimLeft(trim, "#"))
				if len(title) > 8 && !strings.EqualFold(title, name) {
					paras = append(paras, title)
				}
				continue
			}
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			cur.WriteString(trim)
		}
		if cur.Len() > 0 && len(paras) < 2 {
			paras = append(paras, strings.TrimSpace(cur.String()))
		}
		if len(paras) > 0 {
			return textutil.Truncate(strings.Join(paras, " — "), 600, "…")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		mod := readGoModule(root)
		if mod != "" {
			return fmt.Sprintf("%s is a Go module (`%s`).", name, mod)
		}
		return fmt.Sprintf("%s is a Go project.", name)
	}
	if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
		return fmt.Sprintf("%s is a Node/JavaScript project.", name)
	}
	return fmt.Sprintf("%s — project conventions and key paths are maintained by SLMCode.", name)
}

func readGoModule(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func detectConventions(root string) string {
	var bullets []string
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", ".slmcode/PROJECT.md"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(data) == 0 {
			continue
		}
		text := string(data)
		// Pull a few short imperative / bullet lines.
		for _, line := range strings.Split(text, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") {
				item := strings.TrimSpace(trim[2:])
				if len(item) > 12 && len(item) < 160 {
					bullets = append(bullets, "- "+item)
				}
			}
			if len(bullets) >= 8 {
				break
			}
		}
		if len(bullets) > 0 {
			break
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		bullets = append(bullets,
			"- Use `go test ./...` for verification",
			"- Prefer focused package-local changes over broad refactors",
		)
	}
	return strings.Join(uniqueStrings(bullets), "\n")
}

func detectKeyPaths(root string) []keyPath {
	candidates := []keyPath{
		{"cmd/", "CLI entrypoints"},
		{"pkg/", "core libraries"},
		{"web/", "web assets"},
		{"docs/", "documentation"},
		{"test/", "tests / e2e"},
		{"skills/", "bundled skill packs"},
		{".slmcode/", "runtime project memory + board"},
		{"Makefile", "build / test targets"},
		{"go.mod", "Go module definition"},
		{"README.md", "project overview"},
		{"TODO.md", "open work / feedback"},
		{"package.json", "JS package manifest"},
		{"src/", "source"},
	}
	var out []keyPath
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(root, c.path)); err == nil {
			out = append(out, c)
		}
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// MergeProjectSections updates Overview / Conventions / Key paths without wiping user edits.
func MergeProjectSections(existing, seeded string) string {
	if strings.TrimSpace(existing) == "" {
		return seeded
	}
	out := existing
	if strings.TrimSpace(sectionBody(out, "Overview")) == "" {
		if ov := strings.TrimSpace(sectionBody(seeded, "Overview")); ov != "" {
			out = replaceSection(out, "Overview", ov)
		}
	}
	if strings.TrimSpace(sectionBody(out, "Conventions")) == "" {
		if conv := strings.TrimSpace(sectionBody(seeded, "Conventions")); conv != "" {
			out = replaceSection(out, "Conventions", conv)
		}
	}
	kp := sectionBody(out, "Key paths")
	if strings.Contains(kp, "| | |") || strings.Contains(kp, "|  |  |") || strings.TrimSpace(kp) == "" {
		if seedPaths := strings.TrimSpace(sectionBody(seeded, "Key paths")); seedPaths != "" {
			out = replaceSection(out, "Key paths", seedPaths)
		}
	}
	return out
}

func sectionBody(md, heading string) string {
	marker := "## " + heading
	i := strings.Index(md, marker)
	if i < 0 {
		return ""
	}
	rest := md[i+len(marker):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

func replaceSection(md, heading, body string) string {
	marker := "## " + heading
	i := strings.Index(md, marker)
	if i < 0 {
		return strings.TrimRight(md, "\n") + "\n\n" + marker + "\n\n" + strings.TrimSpace(body) + "\n"
	}
	rest := md[i+len(marker):]
	end := len(md)
	if j := strings.Index(rest, "\n## "); j >= 0 {
		end = i + len(marker) + j
	}
	return md[:i] + marker + "\n\n" + strings.TrimSpace(body) + "\n" + md[end:]
}
