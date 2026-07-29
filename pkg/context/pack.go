package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TaskPack is the minimal context handed to one specialist — never the whole repo.
type TaskPack struct {
	Query      string            `json:"query"`
	Role       string            `json:"role"`
	TaskID     string            `json:"task_id,omitempty"`
	TaskTitle  string            `json:"task_title,omitempty"`
	Docs       map[string]string `json:"docs"`
	Files      map[string]string `json:"files"`
	Skills     string            `json:"skills,omitempty"`
	BudgetUsed int               `json:"budget_used"`
}

// Packer builds incremental, budgeted context packs from markdown + file excerpts.
type Packer struct {
	Store    *Store
	Root     string
	MaxBytes int
}

func NewPacker(store *Store, root string, maxKB int) *Packer {
	if maxKB <= 0 {
		maxKB = 16
	}
	return &Packer{Store: store, Root: root, MaxBytes: maxKB * 1024}
}

// Build creates a role-specific pack. docNames select .slmcode markdown slices;
// filePaths are optional workspace files (truncated per file).
func (p *Packer) Build(role, query string, docNames []string, filePaths []string, skillsMarkdown string) (*TaskPack, error) {
	pack := &TaskPack{
		Query:  query,
		Role:   role,
		Docs:   map[string]string{},
		Files:  map[string]string{},
		Skills: skillsMarkdown,
	}
	budget := p.MaxBytes
	used := 0

	take := func(label, content string, dest map[string]string, fileCap bool) {
		content = strings.TrimSpace(content)
		if content == "" || budget-used < 256 {
			return
		}
		max := budget - used
		if len(content) > max {
			content = content[:max] + "\n...[truncated]"
		}
		if fileCap && len(content) > 8000 {
			content = content[:8000] + "\n...[truncated]"
		}
		dest[label] = content
		used += len(content)
	}

	for _, name := range docNames {
		body, err := p.Store.Read(name)
		if err != nil {
			continue
		}
		take(name, body, pack.Docs, false)
	}

	for _, rel := range filePaths {
		if budget-used < 256 {
			break
		}
		abs := filepath.Join(p.Root, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		take(rel, string(data), pack.Files, true)
	}

	if skillsMarkdown != "" && budget-used > 256 {
		sk := skillsMarkdown
		if len(sk) > budget-used {
			sk = sk[:budget-used]
		}
		pack.Skills = sk
		used += len(sk)
	}

	pack.BudgetUsed = used
	return pack, nil
}

// Render turns a pack into a prompt section for a specialist.
func (p *TaskPack) Render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Scoped context for role=%s\n\n", p.Role))
	if p.TaskID != "" {
		b.WriteString(fmt.Sprintf("Task: %s — %s\n\n", p.TaskID, p.TaskTitle))
	}
	if p.Query != "" {
		b.WriteString("## User query\n\n")
		b.WriteString(p.Query)
		b.WriteString("\n\n")
	}
	if p.Skills != "" {
		b.WriteString(p.Skills)
		b.WriteString("\n")
	}
	for name, body := range p.Docs {
		b.WriteString(fmt.Sprintf("## Doc: %s\n\n%s\n\n", name, body))
	}
	for name, body := range p.Files {
		b.WriteString(fmt.Sprintf("## File: %s\n\n```\n%s\n```\n\n", name, body))
	}
	b.WriteString(fmt.Sprintf("\n(context budget used: %d bytes)\n", p.BudgetUsed))
	return b.String()
}

// DefaultDocsForRole picks which markdown docs a specialist typically needs.
func DefaultDocsForRole(role string) []string {
	switch role {
	case "context":
		return []string{DocProject, DocContext, DocMemory, DocQuery}
	case "explorer":
		return []string{DocProject, DocQuery, DocContext}
	case "docs":
		return []string{DocProject, DocQuery, DocContext, DocMemory}
	case "architect", "coordinator":
		return []string{DocQuery, DocContext, DocScratch, DocPlan, DocProject}
	case "planner":
		return []string{DocQuery, DocContext, DocScratch, DocProject}
	case "splitter":
		return []string{DocPlan, DocQuery, DocScratch}
	case "worker", "corrector", "deep":
		return []string{DocQuery, DocContext, DocPlan, DocMemory}
	case "reviewer":
		return []string{DocQuery, DocTasks}
	case "tester":
		return []string{DocProject, DocTasks}
	case "memory":
		return []string{DocMemory, DocPlan, DocTasks}
	default:
		return []string{DocQuery, DocContext}
	}
}
