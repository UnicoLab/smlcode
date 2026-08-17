package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/composer"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// composeDynamicPipeline runs the composer specialist to assemble a
// task-specific pipeline, then activates it for the rest of the run.
//
// Failures are non-fatal by design: if the composer cannot be run or its output
// cannot be parsed/validated, the run continues on the static pipeline so a weak
// local SLM never hard-blocks a real coding task.
func (o *Orchestrator) composeDynamicPipeline(ctx context.Context, query string, inventory []string, exploreOut, archOut string) {
	if o == nil || o.cfg == nil || o.factory == nil || o.skills == nil {
		return
	}

	o.emitAgent("compose", composer.RoleID, "", "assembling task-specific pipeline", "", "")

	input := o.buildComposerPrompt(query, inventory, exploreOut, archOut)
	pack := o.skillPackFor(composer.RoleID, query)
	if strings.TrimSpace(pack) != "" {
		input = pack + "\n\n" + input
	}

	out, err := o.runRoleTracked(ctx, composer.RoleID, "", input)
	if err != nil {
		o.emitWarn("compose", "composer failed ("+err.Error()+") — keeping static pipeline", "")
		return
	}
	comp, err := composer.Parse(out)
	if err != nil {
		o.emitWarn("compose", "unparsable composition ("+err.Error()+") — keeping static pipeline", "")
		return
	}
	unknown := o.sanitizeComposition(&comp)
	if len(unknown) > 0 {
		sort.Strings(unknown)
		o.emitWarn("compose", "unknown agents dropped — "+strings.Join(unknown, ", "), "")
	}

	// Deterministic upgrade: when the query clearly targets a language and the
	// composer left execute/test on the generic worker/tester, bind the matching
	// specialist so an SLM that merely *mentions* "web-worker" in its strategy
	// still actually uses it.
	if w, tst := queryLanguageSpecialists(query); w != "" {
		if genericAgent(comp.Execute.DefaultRole) {
			comp.Execute.DefaultRole = w
		}
		for i := range comp.Phases {
			switch comp.Phases[i].ID {
			case "execute":
				if genericAgent(comp.Phases[i].Agent) {
					comp.Phases[i].Agent = w
				}
			case "test":
				if genericAgent(comp.Phases[i].Agent) {
					comp.Phases[i].Agent = tst
				}
			}
		}
	}

	cfg, err := composer.Apply(comp)
	if err != nil {
		o.emitWarn("compose", "invalid composition ("+err.Error()+") — keeping static pipeline", "")
		return
	}

	// Safety net: execute + test are required to implement and verify code. If an
	// SLM composer omitted/disabled them, re-enable with their default binding
	// rather than silently skipping implementation or verification.
	if enforced := ensureCriticalPhases(&cfg); len(enforced) > 0 {
		o.emitWarn("compose", "re-enabled critical phases omitted by composer — "+strings.Join(enforced, ", "), "")
	}

	o.mu.Lock()
	o.pipe = &cfg
	o.dynamicSkills = comp.SkillsByRole()
	o.mu.Unlock()

	// Persist for inspection; pipeline.yaml is intentionally left untouched.
	_ = pipeline.SaveDynamic(o.cfg.SlmDir(), &cfg)

	o.emitFullL("compose", stream.KindOutput, composer.RoleID, "", comp.Summary, "composition", compositionMarkdown(comp), stream.LevelSuccess)
	o.emitSuccess("compose", fmt.Sprintf("dynamic pipeline active · %d phases · %d team members · %d slots",
		len(comp.Phases), len(comp.Team), len(comp.Slots)), "")

	if o.store != nil {
		_ = o.store.Append(contextstore.DocScratch, "Dynamic pipeline", compositionMarkdown(comp))
	}
}

// ensureCriticalPhases re-enables the phases that are required to implement and
// verify code (execute + test) when a composer omitted/disabled them. Returns the
// list of phases it re-enabled. Idempotent and safe on a nil config.
func ensureCriticalPhases(cfg *pipeline.Config) []string {
	if cfg == nil || cfg.Phases == nil {
		return nil
	}
	def := pipeline.Default()
	var reenabled []string
	for _, id := range []string{"execute", "test"} {
		ps, ok := cfg.Phases[id]
		if !ok {
			continue
		}
		if ps.EnabledOrDefault() && !strings.EqualFold(strings.TrimSpace(ps.When), pipeline.WhenNever) {
			continue
		}
		ps.Enabled = nil
		ps.When = pipeline.WhenAlways
		if strings.TrimSpace(ps.Agent) == "" {
			ps.Agent = def.Phases[id].Agent
		}
		cfg.Phases[id] = ps
		reenabled = append(reenabled, id)
	}
	return reenabled
}

// queryLanguageSpecialists maps query keywords to the language-specific
// worker/tester pair, or ("","") when the query language is not clearly known.
// The generic worker/tester + project-language hint remain the safe fallback.
func queryLanguageSpecialists(query string) (worker, tester string) {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "rust") || strings.Contains(q, "cargo"):
		return "rust-worker", "rust-tester"
	case (strings.Contains(q, "java") && !strings.Contains(q, "javascript")) ||
		strings.Contains(q, "maven") || strings.Contains(q, "gradle"):
		return "java-worker", "java-tester"
	case strings.Contains(q, "c++") || strings.Contains(q, "cpp") || strings.Contains(q, "cmake"):
		return "cpp-worker", "cpp-tester"
	case strings.Contains(q, "html") || strings.Contains(q, "browser") ||
		strings.Contains(q, "game") || strings.Contains(q, "website") ||
		strings.Contains(q, "webpage") || strings.Contains(q, "frontend") ||
		strings.Contains(q, "vanilla js"):
		return "web-worker", "web-tester"
	case strings.Contains(q, "react") || strings.Contains(q, "next.js") ||
		strings.Contains(q, "vite") || strings.Contains(q, "typescript"):
		return "react-worker", "react-tester"
	case strings.Contains(q, "bash") || strings.Contains(q, "shell script"):
		return "shell-worker", "shell-tester"
	case strings.Contains(q, "golang") || strings.Contains(q, "go.mod"):
		return "go-worker", "go-tester"
	case strings.Contains(q, "python") || strings.Contains(q, "django") ||
		strings.Contains(q, "flask") || strings.Contains(q, "fastapi") ||
		strings.Contains(q, "langgraph") || strings.Contains(q, "pytest"):
		return "python-worker", "python-tester"
	}
	return "", ""
}

// genericAgent reports whether an agent id is the generic worker/tester (or empty),
// i.e. the composer did not pick a language specialist.
func genericAgent(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return id == "" || id == "worker" || id == "tester" || id == "deep"
}

// sanitizeComposition clears any agent reference not present in the registry and
// returns the list of dropped ids. Unknown roles fall back to engine defaults.
func (o *Orchestrator) sanitizeComposition(comp *composer.Composition) []string {
	if comp == nil {
		return nil
	}
	known := map[string]bool{}
	for _, s := range o.factory.AllSpecs() {
		known[strings.ToLower(s.ID)] = true
	}
	var dropped []string
	note := func(id *string) {
		if id == nil {
			return
		}
		v := strings.ToLower(strings.TrimSpace(*id))
		if v == "" || known[v] {
			return
		}
		dropped = append(dropped, v)
		*id = ""
	}
	for i := range comp.Phases {
		note(&comp.Phases[i].Agent)
	}
	note(&comp.Execute.DefaultRole)
	note(&comp.Execute.Reviewer)
	note(&comp.Execute.Corrector)
	for i := range comp.Slots {
		note(&comp.Slots[i].Agent)
	}
	// Team members with unknown roles are dropped (their skills can't be bound).
	var team []composer.TeamMember
	for _, t := range comp.Team {
		if known[strings.ToLower(strings.TrimSpace(t.Role))] {
			team = append(team, t)
		} else {
			dropped = append(dropped, strings.ToLower(strings.TrimSpace(t.Role)))
		}
	}
	comp.Team = team
	return dropped
}

// buildComposerPrompt renders the full composer context (query + inventory +
// exploration + phases + roster + skills) with the STRICT JSON schema contract.
func (o *Orchestrator) buildComposerPrompt(query string, inventory []string, exploreOut, archOut string) string {
	var b strings.Builder
	b.WriteString("## Query\n")
	b.WriteString(truncate(query, 2000))
	b.WriteString("\n\n")

	if lang := detectProjectLang(o.cfg.Root); lang != "" {
		b.WriteString("## Detected project language\n" + lang + "\n\n")
	} else {
		b.WriteString("## Detected project language\nunknown (greenfield) — infer from the query keywords\n\n")
	}

	if len(inventory) > 0 {
		b.WriteString("## Workspace inventory (authoritative — do NOT invent paths)\n")
		for _, f := range inventory {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	if strings.TrimSpace(exploreOut) != "" {
		b.WriteString("## Exploration\n")
		b.WriteString(truncate(exploreOut, 3000))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(archOut) != "" {
		b.WriteString("## Architecture notes\n")
		b.WriteString(truncate(archOut, 1500))
		b.WriteString("\n\n")
	}

	b.WriteString("## Canonical phases (copy ids exactly)\n")
	def := pipeline.Default()
	for _, id := range def.Order {
		ps := def.Phases[id]
		agent := ps.Agent
		if agent == "" {
			agent = "(engine)"
		}
		b.WriteString(fmt.Sprintf("- %s — label=%q default_agent=%s when=%s\n", id, ps.Label, agent, ps.When))
	}
	b.WriteString("\n")

	b.WriteString("## Available specialists (copy role ids exactly)\n")
	for _, s := range o.factory.AllSpecs() {
		tools := "false"
		if len(s.Tools) > 0 {
			tools = "true"
		}
		b.WriteString(fmt.Sprintf("- %s — %s (tools=%s)\n", s.ID, s.Title, tools))
	}
	b.WriteString("\n")

	b.WriteString("## Available skills (copy skill names exactly)\n")
	skillList, _ := o.skills.List()
	if len(skillList) == 0 {
		b.WriteString("(none)\n")
	}
	for _, s := range skillList {
		b.WriteString(fmt.Sprintf("- %s — %s\n", s.Name, truncate(s.Description, 120)))
	}
	b.WriteString("\n")

	b.WriteString("Return the STRICT JSON composition now. Output JSON only — no prose.\n")
	return b.String()
}

// compositionMarkdown renders a compact, human-readable summary of a composition.
func compositionMarkdown(c composer.Composition) string {
	var b strings.Builder
	b.WriteString("# Dynamic pipeline\n\n")
	b.WriteString("**Summary:** " + c.Summary + "\n\n")
	if strings.TrimSpace(c.Strategy) != "" {
		b.WriteString("**Strategy:** " + c.Strategy + "\n\n")
	}
	b.WriteString("## Phases\n\n")
	for _, p := range c.Phases {
		state := "disabled"
		if p.Enabled {
			state = "enabled"
			if p.When != "" {
				state += " (when=" + p.When + ")"
			}
		}
		agent := p.Agent
		if agent == "" {
			agent = "(default)"
		}
		b.WriteString(fmt.Sprintf("- `%s` — %s — agent=%s\n", p.ID, state, agent))
	}
	b.WriteString("\n## Execute loop\n\n")
	b.WriteString(fmt.Sprintf("- default_role=%s · reviewer=%s · corrector=%s · max_waves=%d\n",
		c.Execute.DefaultRole, c.Execute.Reviewer, c.Execute.Corrector, c.Execute.MaxWaves))
	if len(c.Team) > 0 {
		b.WriteString("\n## Team & skills\n\n")
		for _, t := range c.Team {
			b.WriteString(fmt.Sprintf("- `%s` → %s\n", t.Role, strings.Join(t.Skills, ", ")))
		}
	}
	if len(c.Slots) > 0 {
		b.WriteString("\n## Slots\n\n")
		for _, s := range c.Slots {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", s.ID, s.Agent))
		}
	}
	return b.String()
}
