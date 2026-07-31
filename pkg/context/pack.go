package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	LeanFiles  bool              `json:"-"` // tighter per-file caps for workers
}

// Packer builds incremental, budgeted context packs from markdown + file excerpts.
type Packer struct {
	Store    *Store
	Root     string
	MaxBytes int

	cacheMu sync.Mutex
	cache   map[string]*TaskPack // reuse identical packs within a run
}

func NewPacker(store *Store, root string, maxKB int) *Packer {
	if maxKB <= 0 {
		maxKB = 16
	}
	return &Packer{Store: store, Root: root, MaxBytes: maxKB * 1024, cache: map[string]*TaskPack{}}
}

// ClearCache drops reused packs (call at the start of each orchestrator Run).
func (p *Packer) ClearCache() {
	if p == nil {
		return
	}
	p.cacheMu.Lock()
	p.cache = map[string]*TaskPack{}
	p.cacheMu.Unlock()
}

// Build creates a role-specific pack. docNames select .slmcode markdown slices;
// filePaths are optional workspace files (truncated per file).
func (p *Packer) Build(role, query string, docNames []string, filePaths []string, skillsMarkdown string) (*TaskPack, error) {
	cacheKey := role + "\x00" + query + "\x00" + strings.Join(docNames, ",") + "\x00" +
		strings.Join(filePaths, ",") + "\x00" + skillsMarkdown
	if p != nil {
		p.cacheMu.Lock()
		if cached, ok := p.cache[cacheKey]; ok && cached != nil {
			p.cacheMu.Unlock()
			cp := *cached
			cp.Docs = copyStringMap(cached.Docs)
			cp.Files = copyStringMap(cached.Files)
			return &cp, nil
		}
		p.cacheMu.Unlock()
	}

	pack := &TaskPack{
		Query:  query,
		Role:   role,
		Docs:   map[string]string{},
		Files:  map[string]string{},
		Skills: skillsMarkdown,
	}
	budget := p.MaxBytes
	used := 0
	lean := isLeanRole(role)
	pack.LeanFiles = lean

	fileLimit := 8000
	docLimit := 0 // 0 = only budget
	skillCap := budget
	if lean {
		// Faster SLM inference: tighter excerpts + smaller docs/skills.
		if budget > 16*1024 {
			budget = 16 * 1024
		}
		fileLimit = 2800
		docLimit = 1800
		skillCap = 900
	}
	take := func(label, content string, dest map[string]string, fileCap bool) {
		content = strings.TrimSpace(content)
		if content == "" || budget-used < 256 {
			return
		}
		max := budget - used
		if len(content) > max {
			content = content[:max] + "\n...[truncated]"
		}
		if fileCap && len(content) > fileLimit {
			content = content[:fileLimit] + "\n...[truncated]"
		}
		if !fileCap && docLimit > 0 && len(content) > docLimit {
			content = content[:docLimit] + "\n...[truncated]"
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
		cap := budget - used
		if skillCap > 0 && skillCap < cap {
			cap = skillCap
		}
		if len(sk) > cap {
			sk = sk[:cap] + "\n...[truncated]"
		}
		pack.Skills = sk
		used += len(sk)
	}

	pack.BudgetUsed = used
	if p != nil {
		p.cacheMu.Lock()
		if p.cache == nil {
			p.cache = map[string]*TaskPack{}
		}
		stored := *pack
		stored.Docs = copyStringMap(pack.Docs)
		stored.Files = copyStringMap(pack.Files)
		p.cache[cacheKey] = &stored
		p.cacheMu.Unlock()
	}
	return pack, nil
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func isLeanRole(role string) bool {
	switch role {
	case "worker", "corrector", "deep", "reviewer", "tester",
		"planner", "splitter", "coordinator", "architect", "context", "memory":
		return true
	default:
		return false
	}
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
		return []string{DocProject, DocContext, DocQuery}
	case "explorer":
		return []string{DocProject, DocQuery, DocContext}
	case "docs":
		return []string{DocProject, DocQuery, DocContext}
	case "architect", "coordinator":
		return []string{DocQuery, DocContext, DocPlan}
	case "planner":
		return []string{DocQuery, DocContext, DocProject}
	case "splitter":
		return []string{DocPlan, DocQuery}
	case "worker", "corrector", "deep":
		return []string{DocQuery, DocContext}
	case "reviewer":
		return []string{DocQuery}
	case "tester":
		return []string{DocProject, DocTasks}
	case "memory":
		return []string{DocMemory, DocPlan}
	default:
		return []string{DocQuery, DocContext}
	}
}

// LeanDocsForRole returns a minimal doc set for execute-time / multi-turn packs.
func LeanDocsForRole(role string) []string {
	switch role {
	case "worker", "corrector", "deep":
		return []string{DocQuery, DocContext}
	case "planner", "splitter", "architect":
		return []string{DocQuery, DocContext}
	case "reviewer", "coordinator":
		return []string{DocQuery}
	case "tester":
		return []string{DocProject}
	default:
		return DefaultDocsForRole(role)
	}
}
