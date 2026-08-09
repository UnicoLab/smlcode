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

const (
	// SafetyMarginPercent reserves headroom for system prompts, instruction
	// overhead, and model response space so the pack never crowds out the
	// model's ability to reason and generate (critical for small 7B–30B SLMs).
	SafetyMarginPercent = 80

	// MaxLeanPackBytes is the hard ceiling for a lean (worker/corrector) pack
	// regardless of the configured MaxContextKB. This keeps per-task context
	// below ~12 KB — safe for even 16 K-window models.
	MaxLeanPackBytes = 12 * 1024

	// MinRemainingBytes is the floor below which we stop adding content.
	MinRemainingBytes = 256

	// MaxSkillFraction is the maximum share of remaining budget given to skills
	// when context is tight (15% of remaining after files+docs).
	MaxSkillFraction = 15 // percent
)

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
//
// An 80 % safety margin is applied so the pack never crowds out the system
// prompt, agent instructions, and model response space (critical for SLMs).
// For lean roles (worker, corrector, …) focus files are packed before
// exploration docs because the agent needs code context more than project
// history. Skills are truncated relative to remaining budget rather than a
// hardcoded cap.
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

	// --- budget with safety margin ---
	budget := int(float64(p.MaxBytes) * float64(SafetyMarginPercent) / 100.0)
	used := 0
	lean := isLeanRole(role)
	pack.LeanFiles = lean

	// Lean cap: never exceed MaxLeanPackBytes for worker/corrector roles,
	// so even budget-rich stacks (128 KB) don't drown small SLMs.
	if lean && budget > MaxLeanPackBytes {
		budget = MaxLeanPackBytes
	}

	fileLimit := (budget * 50) / 100 // 50 % of budget per file
	docLimit := 0                    // 0 = only budget (no extra doc cap for non-lean)
	if lean {
		fileLimit = min(fileLimit, 2800)
		docLimit = min(budget/4, 1800)
	}

	// --- helper: take a content blob into a dest map ---
	take := func(label, content string, dest map[string]string, fileCap bool) {
		content = strings.TrimSpace(content)
		if content == "" || budget-used < MinRemainingBytes {
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

	// --- pack ordering ---
	// Lean roles (worker, corrector, etc.): files first — they need code
	// context more than exploration output. Other roles: docs first.
	if lean {
		// Files first: focus code over long exploration docs.
		for _, rel := range filePaths {
			if budget-used < MinRemainingBytes {
				break
			}
			abs := filepath.Join(p.Root, rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			take(rel, string(data), pack.Files, true)
		}
		for _, name := range docNames {
			if budget-used < MinRemainingBytes {
				break
			}
			body, err := p.Store.Read(name)
			if err != nil {
				continue
			}
			take(name, body, pack.Docs, false)
		}
	} else {
		// Docs first: planners, architects, reviewers need project context.
		for _, name := range docNames {
			body, err := p.Store.Read(name)
			if err != nil {
				continue
			}
			take(name, body, pack.Docs, false)
		}
		for _, rel := range filePaths {
			if budget-used < MinRemainingBytes {
				break
			}
			abs := filepath.Join(p.Root, rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			take(rel, string(data), pack.Files, true)
		}
	}

	// --- skills: scale cap by remaining budget ---
	if skillsMarkdown != "" && budget-used > MinRemainingBytes {
		sk := skillsMarkdown
		// Give skills at most MaxSkillFraction % of remaining budget.
		skillCap := (budget - used) * MaxSkillFraction / 100
		if skillCap <= 0 {
			skillCap = budget - used
		}
		if len(sk) > skillCap {
			sk = sk[:skillCap] + "\n...[truncated]"
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
	case "worker", "corrector", "deep", "placeholder":
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
	case "worker", "corrector", "deep", "placeholder":
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
