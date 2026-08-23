package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// skillLoaderRef lets the registered tool follow o.skills, which completeRun
// replaces after knowledge.Evolve writes a new learned skill.
type skillLoaderRef struct {
	mu     sync.RWMutex
	loader *skills.Loader
}

func (r *skillLoaderRef) set(l *skills.Loader) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.loader = l
	r.mu.Unlock()
}

func (r *skillLoaderRef) get() *skills.Loader {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loader
}

// activeSkills is the registry-visible pointer to the live loader. The tool
// registry outlives individual Loader instances, so the tool closes over this
// indirection rather than over one Loader.
var activeSkills = &skillLoaderRef{}

// registerSkillTool registers `ws_skill`, the read side of progressive
// disclosure.
//
// pkg/skills renders a CARD for every match and only expands a full body for an
// explicit `@skill:` reference or a specialist default. Without a way to ask
// for a body, disclosure is one-way: the agent sees a card describing a skill
// it can never open. ws_skill closes the loop — list what matched, then pull
// exactly the one body that is worth its tokens.
func registerSkillTool(reg *tools.ToolRegistry, loader *skills.Loader) error {
	if reg == nil {
		return nil
	}
	activeSkills.set(loader)
	tool := tools.NewGenericTool(
		"ws_skill",
		"Open a skill by name, or list the skills available. Skills are shown to you as "+
			"one-line cards; call this with {\"name\":\"<skill>\"} to read the full body of one, "+
			"or with no arguments to list every skill you can open.",
		func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			l := activeSkills.get()
			if l == nil {
				return "", fmt.Errorf("ws_skill: no skill loader configured")
			}
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return skillToolIndex(l), nil
			}
			if body, ok := l.ExpandBody(name); ok {
				return body, nil
			}
			return "", fmt.Errorf("ws_skill: no skill named %q. Available:\n%s", name, skillToolIndex(l))
		},
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Skill name exactly as shown on its card. Omit to list all skills.",
				},
			},
		},
	)
	if err := reg.RegisterTool(tool); err != nil {
		if !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("ws_skill: %w", err)
		}
	}
	return nil
}

// skillToolIndex renders the one-line card index used for listing and for the
// not-found error, so a wrong name self-corrects in one turn.
func skillToolIndex(l *skills.Loader) string {
	list, err := l.List()
	if err != nil || len(list) == 0 {
		return "(no skills available)"
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var b strings.Builder
	b.WriteString("## Skills you can open with ws_skill\n\n")
	for _, s := range list {
		desc := strings.TrimSpace(s.Description)
		if len(desc) > 140 {
			desc = desc[:140] + "…"
		}
		b.WriteString("- **" + s.Name + "** — " + desc + "\n")
	}
	return b.String()
}
