package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
)

// Evolved is the result of a knowledge write-back.
type Evolved struct {
	SkillsIndex string // relative path written
	LearnedSkill string
	ProjectNote  string
}

// Evolve persists iterative project knowledge so future agents skip deep dives:
// - .slmcode/SKILLS.md (index of active + learned skills)
// - .slmcode/skills/learned/SKILL.md (auto-grown conventions)
// - append durable bullets into PROJECT.md when useful
func Evolve(slmDir string, query string, board *plan.Board, lessonsMD string, skillList []skills.Skill) (*Evolved, error) {
	if slmDir == "" {
		return nil, fmt.Errorf("empty slm dir")
	}
	out := &Evolved{}

	index := renderSkillsIndex(skillList, lessonsMD)
	indexPath := filepath.Join(slmDir, "SKILLS.md")
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		return nil, err
	}
	out.SkillsIndex = "SKILLS.md"

	learnedDir := filepath.Join(slmDir, "skills", "learned")
	if err := os.MkdirAll(learnedDir, 0o755); err != nil {
		return nil, err
	}
	learnedPath := filepath.Join(learnedDir, "SKILL.md")
	learnedBody := mergeLearnedSkill(learnedPath, query, board, lessonsMD)
	if err := os.WriteFile(learnedPath, []byte(learnedBody), 0o644); err != nil {
		return nil, err
	}
	out.LearnedSkill = "skills/learned/SKILL.md"

	if note := projectNote(query, board); note != "" {
		proj := filepath.Join(slmDir, "PROJECT.md")
		_ = appendSection(proj, "Auto-learned", note)
		out.ProjectNote = note
	}
	return out, nil
}

func renderSkillsIndex(list []skills.Skill, lessons string) string {
	var b strings.Builder
	b.WriteString("# Project Skills\n\n")
	b.WriteString("_Auto-updated by SLMCode. Injected into specialist packs when relevant._\n\n")
	b.WriteString(fmt.Sprintf("Updated: %s\n\n", time.Now().Format(time.RFC3339)))
	b.WriteString("## Catalog\n\n")
	if len(list) == 0 {
		b.WriteString("_No skills discovered yet._\n\n")
	}
	for _, s := range list {
		b.WriteString(fmt.Sprintf("- **%s** — %s\n  `%s`\n", s.Name, s.Description, s.Path))
	}
	if strings.TrimSpace(lessons) != "" {
		b.WriteString("\n## Latest lessons\n\n")
		b.WriteString(lessons)
		b.WriteString("\n")
	}
	b.WriteString("\n## How skills grow\n\n")
	b.WriteString("- `skills/learned/SKILL.md` accumulates conventions from successful runs\n")
	b.WriteString("- Bundled skills live under `skills/_bundled/`\n")
	b.WriteString("- Drop custom `SKILL.md` folders under `.slmcode/skills/`\n")
	return b.String()
}

func mergeLearnedSkill(path, query string, board *plan.Board, lessons string) string {
	prev, _ := os.ReadFile(path)
	body := string(prev)
	if strings.TrimSpace(body) == "" {
		body = "---\nname: learned\ndescription: Auto-evolved project conventions from SLMCode runs\ntriggers: conventions patterns pitfalls\n---\n\n# Learned project skill\n\n"
	}

	var add strings.Builder
	add.WriteString(fmt.Sprintf("\n## Session %s\n\n", time.Now().Format("2006-01-02 15:04")))
	add.WriteString(fmt.Sprintf("Query: %s\n\n", firstLine(query)))
	if board != nil {
		done, fail := 0, 0
		for _, t := range board.Tasks {
			t.Normalize()
			if t.Column == plan.ColDone {
				done++
			}
			if t.Column == plan.ColBlocked {
				fail++
			}
		}
		add.WriteString(fmt.Sprintf("Board: %d done / %d blocked / %d total\n\n", done, fail, len(board.Tasks)))
		for _, t := range board.Tasks {
			t.Normalize()
			if t.Column != plan.ColDone || len(t.Files) == 0 {
				continue
			}
			add.WriteString(fmt.Sprintf("- ✓ %s touched `%s`\n", t.ID, strings.Join(t.Files, "`, `")))
		}
	}
	if strings.TrimSpace(lessons) != "" {
		add.WriteString("\n### Lessons\n\n")
		add.WriteString(lessons)
		add.WriteString("\n")
	}

	merged := body + add.String()
	// Keep learned skill bounded for SLM packs
	if len(merged) > 12000 {
		merged = merged[:4000] + "\n\n…\n\n" + merged[len(merged)-7000:]
	}
	return merged
}

func projectNote(query string, board *plan.Board) string {
	if board == nil {
		return ""
	}
	var files []string
	seen := map[string]bool{}
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column != plan.ColDone {
			continue
		}
		for _, f := range t.Files {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf("- %s → active files: `%s`", firstLine(query), strings.Join(files, "`, `"))
}

func appendSection(path, heading, body string) error {
	prev, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	section := fmt.Sprintf("\n\n## %s\n\n%s\n", heading, strings.TrimSpace(body))
	return os.WriteFile(path, append(prev, []byte(section)...), 0o644)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
