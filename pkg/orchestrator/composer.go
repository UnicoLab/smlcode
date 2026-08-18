package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

func clearDynamicRunArtifacts(slmDir string) {
	if strings.TrimSpace(slmDir) == "" {
		return
	}
	_ = os.Remove(filepath.Join(slmDir, pipeline.DynamicFileName))
	_ = os.Remove(filepath.Join(slmDir, composer.DynamicFileName))
}

// composeDynamicPipeline runs the composer specialist to assemble a
// task-specific pipeline, then activates it for the rest of the run.
//
// Failures are non-fatal by design: if the composer cannot be run or its output
// cannot be parsed/validated, a deterministic heuristic composition is activated
// so a weak local SLM still gets an adaptive run instead of a static fallback.
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
		if ctx.Err() != nil {
			o.emitWarn("compose", "composer cancelled — keeping static pipeline", "")
			return
		}
		o.emitWarn("compose", "composer failed ("+err.Error()+") — using deterministic composition", "")
		o.composeHeuristicDynamicPipeline(query, inventory, exploreOut, archOut)
		return
	}
	comp, err := composer.Parse(out)
	if err != nil {
		o.emitWarn("compose", "unparsable composition ("+err.Error()+") — using deterministic composition", "")
		o.composeHeuristicDynamicPipeline(query, inventory, exploreOut, archOut)
		return
	}
	if err := o.activateDynamicComposition(&comp, query, inventory, false); err != nil {
		o.emitWarn("compose", "invalid composition ("+err.Error()+") — using deterministic composition", "")
		o.composeHeuristicDynamicPipeline(query, inventory, exploreOut, archOut)
	}
}

func (o *Orchestrator) composeHeuristicDynamicPipeline(query string, inventory []string, exploreOut, archOut string) {
	if o == nil || o.cfg == nil {
		return
	}
	comp := heuristicComposition(query, inventory, detectProjectLang(o.cfg.Root), exploreOut, archOut)
	if err := o.activateDynamicComposition(&comp, query, inventory, true); err != nil {
		o.emitWarn("compose", "deterministic composition failed ("+err.Error()+") — keeping static pipeline", "")
	}
}

// PreviewComposition returns the deterministic dynamic composition that would
// be used as the local-model fallback for this query. It does not call an LLM,
// mutate pipeline state, write files, or emit events.
func (o *Orchestrator) PreviewComposition(query string) composer.Composition {
	if o == nil {
		return PreviewCompositionForConfig(nil, query)
	}
	inventory := []string{}
	lang := ""
	if o.cfg != nil {
		inventory = plan.ListWorkspaceFiles(o.cfg.Root, 48)
		lang = detectProjectLang(o.cfg.Root)
	}
	comp := heuristicComposition(query, inventory, lang, "", "")
	_ = o.prepareDynamicComposition(&comp, query, inventory)
	return comp
}

// PreviewCompositionForConfig returns a deterministic composition preview using
// only filesystem/config context. It intentionally avoids constructing the full
// orchestrator so CLI previews and JSON output stay quiet and model-free.
func PreviewCompositionForConfig(cfg *config.Config, query string) composer.Composition {
	inventory := []string{}
	lang := ""
	if cfg != nil {
		inventory = plan.ListWorkspaceFiles(cfg.Root, 48)
		lang = detectProjectLang(cfg.Root)
	}
	comp := heuristicComposition(query, inventory, lang, "", "")
	o := &Orchestrator{cfg: cfg}
	_ = o.prepareDynamicComposition(&comp, query, inventory)
	return comp
}

func (o *Orchestrator) activateDynamicComposition(comp *composer.Composition, query string, inventory []string, heuristic bool) error {
	if o == nil || o.cfg == nil || o.factory == nil || comp == nil {
		return fmt.Errorf("missing dynamic composition dependencies")
	}
	unknown := o.prepareDynamicComposition(comp, query, inventory)
	if len(unknown) > 0 {
		sort.Strings(unknown)
		o.emitWarn("compose", "unknown agents dropped — "+strings.Join(unknown, ", "), "")
	}
	cfg, err := composer.Apply(*comp)
	if err != nil {
		return err
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
	o.dynamicBrief = compositionBrief(*comp)
	cp := *comp
	o.dynamicComposition = &cp
	o.mu.Unlock()

	// Persist for inspection; pipeline.yaml is intentionally left untouched.
	_ = pipeline.SaveDynamic(o.cfg.SlmDir(), &cfg)
	_ = composer.SaveDynamic(o.cfg.SlmDir(), comp)
	if o.currentTurn != nil {
		_ = composer.SaveDynamic(session.TurnDir(o.cfg.SlmDir(), o.currentTurn.ID), comp)
	}

	o.emitFullDataL("compose", stream.KindComposition, composer.RoleID, "", comp.Summary, "composition", compositionMarkdown(*comp), stream.LevelSuccess, *comp)
	mode := "dynamic pipeline active"
	if heuristic {
		mode = "deterministic dynamic pipeline active"
	}
	o.emitSuccess("compose", fmt.Sprintf("%s · %d phases · %d team members · %d slots",
		mode, len(comp.Phases), len(comp.Team), len(comp.Slots)), "")

	if o.store != nil {
		_ = o.store.Append(contextstore.DocScratch, "Dynamic pipeline", compositionMarkdown(*comp))
	}
	return nil
}

func (o *Orchestrator) prepareDynamicComposition(comp *composer.Composition, query string, inventory []string) []string {
	if comp == nil {
		return nil
	}
	var unknown []string
	if o != nil && o.factory != nil {
		unknown = o.sanitizeComposition(comp)
	}
	workerHint, testerHint := queryLanguageSpecialists(query)
	if workerHint == "" && o.cfg != nil {
		workerHint, testerHint = projectLanguageSpecialists(detectProjectLang(o.cfg.Root))
	}
	workerHint, testerHint = filterKnownSpecialistPair(workerHint, testerHint, o.knownAgentIDs())
	if workerHint != "" {
		if genericAgent(comp.Execute.DefaultRole) {
			comp.Execute.DefaultRole = workerHint
		}
		for i := range comp.Phases {
			switch comp.Phases[i].ID {
			case "execute":
				if genericAgent(comp.Phases[i].Agent) {
					comp.Phases[i].Agent = workerHint
				}
			case "test":
				if genericAgent(comp.Phases[i].Agent) {
					comp.Phases[i].Agent = testerHint
				}
			}
		}
	}
	ensureCriticalComposition(comp, workerHint, testerHint)
	orderCompositionPhases(comp)
	lang := ""
	if o != nil && o.cfg != nil {
		lang = detectProjectLang(o.cfg.Root)
	}
	ensureCompositionHandoff(comp, query, inventory, lang, workerHint, testerHint)
	if o != nil {
		ensureCompositionTeam(comp, o.availableSkillNames())
	}
	return unknown
}

// ensureCriticalComposition repairs weak composer output before activation so
// the persisted dynamic pipeline, streamed composition, and executed config
// agree on mandatory implementation/verification phases.
func ensureCriticalComposition(comp *composer.Composition, workerHint, testerHint string) []string {
	if comp == nil {
		return nil
	}
	def := pipeline.Default()
	byID := map[string]int{}
	for i, p := range comp.Phases {
		byID[p.ID] = i
	}
	var reenabled []string
	ensure := func(id, agent string) {
		if agent == "" {
			agent = def.Phases[id].Agent
		}
		if idx, ok := byID[id]; ok {
			p := comp.Phases[idx]
			if p.Enabled && p.When != pipeline.WhenNever {
				if p.Agent == "" {
					p.Agent = agent
					comp.Phases[idx] = p
				}
				return
			}
			p.Enabled = true
			p.When = pipeline.WhenAlways
			p.Agent = agent
			comp.Phases[idx] = p
			reenabled = append(reenabled, id)
			return
		}
		comp.Phases = append(comp.Phases, composer.PhaseChoice{
			ID: id, Agent: agent, Enabled: true, When: pipeline.WhenAlways,
		})
		reenabled = append(reenabled, id)
	}
	ensure("execute", workerHint)
	ensure("test", testerHint)
	if comp.Execute.DefaultRole == "" {
		if workerHint != "" {
			comp.Execute.DefaultRole = workerHint
		} else {
			comp.Execute.DefaultRole = def.Execute.DefaultRole
		}
	}
	if comp.Execute.Reviewer == "" {
		comp.Execute.Reviewer = def.Execute.Reviewer
	}
	if comp.Execute.Corrector == "" {
		comp.Execute.Corrector = def.Execute.Corrector
	}
	if comp.Execute.MaxWaves <= 0 {
		comp.Execute.MaxWaves = def.Execute.MaxWaves
	}
	return reenabled
}

func orderCompositionPhases(comp *composer.Composition) {
	if comp == nil || len(comp.Phases) == 0 {
		return
	}
	rank := map[string]int{}
	for i, id := range pipeline.Default().Order {
		rank[id] = i
	}
	sort.SliceStable(comp.Phases, func(i, j int) bool {
		ri, okI := rank[comp.Phases[i].ID]
		rj, okJ := rank[comp.Phases[j].ID]
		switch {
		case okI && okJ:
			return ri < rj
		case okI:
			return true
		case okJ:
			return false
		default:
			return comp.Phases[i].ID < comp.Phases[j].ID
		}
	})
}

func ensureCompositionHandoff(comp *composer.Composition, query string, inventory []string, lang, workerHint, testerHint string) {
	if comp == nil || len(comp.Handoff) > 0 {
		return
	}
	var handoff []string
	if strings.TrimSpace(lang) != "" {
		handoff = append(handoff, "Detected project language: "+lang)
	}
	if workerHint != "" || testerHint != "" {
		handoff = append(handoff, "Use "+valueOr(workerHint, "worker")+" for implementation and "+valueOr(testerHint, "tester")+" for verification")
	}
	if len(inventory) > 0 {
		handoff = append(handoff, "Use only authoritative workspace paths; do not invent files")
	}
	if cmd := verificationHintForLang(lang); cmd != "" {
		handoff = append(handoff, "Verify with "+cmd)
	}
	if strings.TrimSpace(query) != "" {
		handoff = append(handoff, "Keep changes scoped to this user request")
	}
	comp.Handoff = handoff
}

func ensureCompositionTeam(comp *composer.Composition, skills map[string]bool) {
	if comp == nil {
		return
	}
	def := pipeline.Default()
	seen := map[string]int{}
	for i, member := range comp.Team {
		seen[strings.ToLower(strings.TrimSpace(member.Role))] = i
	}
	add := func(role string) {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			return
		}
		if _, ok := seen[role]; ok {
			return
		}
		comp.Team = append(comp.Team, composer.TeamMember{Role: role, Skills: defaultSkillsForRole(role, skills)})
		seen[role] = len(comp.Team) - 1
	}
	for _, p := range comp.Phases {
		if !p.Enabled || p.When == pipeline.WhenNever {
			continue
		}
		role := p.Agent
		if role == "" {
			role = def.Phases[p.ID].Agent
		}
		add(role)
	}
	add(comp.Execute.DefaultRole)
	add(comp.Execute.Reviewer)
	add(comp.Execute.Corrector)
	for i := range comp.Team {
		comp.Team[i].Skills = filterKnownSkills(comp.Team[i].Skills, skills)
		if len(comp.Team[i].Skills) == 0 {
			comp.Team[i].Skills = defaultSkillsForRole(comp.Team[i].Role, skills)
		}
	}
}

func defaultSkillsForRole(role string, skills map[string]bool) []string {
	candidates := []string{}
	switch {
	case strings.Contains(role, "worker") || role == "deep":
		candidates = []string{"atomic-coding", "specialist-worker"}
	case strings.Contains(role, "tester"):
		candidates = []string{"multipass-quality", "specialist-tester"}
	case strings.Contains(role, "reviewer"):
		candidates = []string{"multipass-quality", "specialist-reviewer"}
	case strings.Contains(role, "corrector"):
		candidates = []string{"atomic-coding", "specialist-corrector"}
	case strings.Contains(role, "planner"):
		candidates = []string{"specialist-planner"}
	case strings.Contains(role, "splitter"):
		candidates = []string{"specialist-splitter"}
	case strings.Contains(role, "explorer"):
		candidates = []string{"specialist-explorer"}
	case strings.Contains(role, "architect"):
		candidates = []string{"specialist-architect"}
	case strings.Contains(role, "coordinator"):
		candidates = []string{"specialist-coordinator"}
	case strings.Contains(role, "memory"):
		candidates = []string{"markdown-memory", "specialist-memory"}
	}
	return filterKnownSkills(candidates, skills)
}

func filterKnownSkills(in []string, skills map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		if len(skills) > 0 && !skills[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func verificationHintForLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go":
		return "go test ./... -count=1"
	case "python":
		return "python -m pytest -q"
	case "typescript", "javascript":
		return "npm test, npx tsc --noEmit, or npm run build"
	case "html":
		return "node --check for JavaScript files and browser smoke"
	case "rust":
		return "cargo test --quiet"
	case "java":
		return "mvn -q test or ./gradlew test"
	case "c++", "c/make":
		return "cmake --build build or make, then ctest when present"
	default:
		return ""
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

func heuristicComposition(query string, inventory []string, lang, exploreOut, archOut string) composer.Composition {
	worker, tester := queryLanguageSpecialists(query)
	if worker == "" {
		worker, tester = projectLanguageSpecialists(lang)
	}
	if worker == "" {
		worker = "worker"
	}
	if tester == "" {
		tester = "tester"
	}

	targets := heuristicTargetFiles(query, inventory, 5)
	q := strings.ToLower(query)
	broad := len(targets) == 0 || containsAny(q, "project", "repo", "harness", "architecture", "refactor", "production", "end to end", "full")
	needsDocs := wantsDocsExplorer(query) || containsAny(q, "readme", "docs", "documentation", "guide", "api")
	needsArch := wantsArchitect(query) || containsAny(q, "architecture", "design", "security", "migration", "production", "scal")
	needsPolish := containsAny(q, "ui", "frontend", "studio", "polish", "production", "review", "security")
	needsCoord := broad || len(targets) > 2 || containsAny(q, "multi", "parallel", "pipeline", "agents", "orchestrat")
	needsClarify := len(strings.Fields(q)) <= 4 && len(targets) == 0 && !containsAny(q, "fix", "add", "implement", "test", "build")

	phases := []composer.PhaseChoice{
		{ID: "context", Agent: "context", Enabled: true, When: pipeline.WhenAlways},
	}
	if broad || len(targets) == 0 || strings.TrimSpace(exploreOut) == "" {
		phases = append(phases, composer.PhaseChoice{ID: "explore", Agent: "explorer", Enabled: true, When: pipeline.WhenAlways})
	} else {
		phases = append(phases, composer.PhaseChoice{ID: "explore", Agent: "explorer", Enabled: true, When: pipeline.WhenAuto})
	}
	if needsDocs {
		phases = append(phases, composer.PhaseChoice{ID: "docs", Agent: "docs", Enabled: true, When: pipeline.WhenAlways})
	}
	if needsArch || strings.TrimSpace(archOut) != "" {
		phases = append(phases, composer.PhaseChoice{ID: "architect", Agent: "architect", Enabled: true, When: pipeline.WhenAlways})
	}
	if needsClarify {
		phases = append(phases, composer.PhaseChoice{ID: "clarify", Enabled: true, When: pipeline.WhenAuto})
	}
	phases = append(phases,
		composer.PhaseChoice{ID: "plan", Agent: "planner", Enabled: true, When: pipeline.WhenAlways},
		composer.PhaseChoice{ID: "split", Agent: "splitter", Enabled: true, When: pipeline.WhenAlways},
	)
	if needsCoord {
		phases = append(phases, composer.PhaseChoice{ID: "coord", Agent: "coordinator", Enabled: true, When: pipeline.WhenAlways})
	}
	phases = append(phases,
		composer.PhaseChoice{ID: "execute", Agent: worker, Enabled: true, When: pipeline.WhenAlways},
		composer.PhaseChoice{ID: "test", Agent: tester, Enabled: true, When: pipeline.WhenAlways},
	)
	if broad {
		phases = append(phases, composer.PhaseChoice{ID: "learn", Agent: "memory", Enabled: true, When: pipeline.WhenAuto})
	}
	if needsPolish {
		phases = append(phases, composer.PhaseChoice{ID: "polish", Agent: "reviewer", Enabled: true, When: pipeline.WhenAuto})
	}
	phases = append(phases, composer.PhaseChoice{ID: "memory", Agent: "memory", Enabled: true, When: pipeline.WhenAlways})

	comp := composer.Composition{
		Summary:  "Deterministic dynamic pipeline for this task",
		Strategy: "Heuristic fallback selected from query, detected language, workspace inventory, and exploration availability.",
		Handoff:  heuristicHandoff(query, targets, lang, worker, tester),
		Phases:   phases,
		Execute: composer.ExecuteChoice{
			DefaultRole: worker,
			Reviewer:    "reviewer",
			Corrector:   "corrector",
			MaxWaves:    2,
		},
	}
	comp.Normalize()
	orderCompositionPhases(&comp)
	return comp
}

func heuristicTargetFiles(query string, inventory []string, max int) []string {
	if max <= 0 {
		return nil
	}
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var exact, fuzzy []string
	for _, f := range inventory {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		lf := strings.ToLower(f)
		base := filepath.Base(lf)
		switch {
		case strings.Contains(q, lf) || strings.Contains(q, base):
			exact = append(exact, f)
			seen[f] = true
		case pathTokenMatchesQuery(base, q):
			fuzzy = append(fuzzy, f)
			seen[f] = true
		}
	}
	out := append([]string{}, exact...)
	out = append(out, fuzzy...)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func (o *Orchestrator) composerInventoryLimit() int {
	limit := 48
	if o == nil || o.cfg == nil {
		return limit
	}
	prof := o.resolvedProfile()
	switch {
	case prof.ContextLimit > 0 && prof.ContextLimit <= 8192:
		limit = 24
	case prof.ContextLimit > 0 && prof.ContextLimit <= 16384:
		limit = 36
	}
	if o.cfg.MaxContextKB > 0 && o.cfg.MaxContextKB <= 8 && limit > 24 {
		limit = 24
	}
	return limit
}

func rankComposerInventory(query string, inventory []string, max int) []string {
	if max <= 0 || max > len(inventory) {
		max = len(inventory)
	}
	q := strings.ToLower(query)
	targets := map[string]bool{}
	for _, f := range heuristicTargetFiles(query, inventory, len(inventory)) {
		targets[f] = true
	}
	type candidate struct {
		path  string
		score int
		index int
	}
	seen := map[string]bool{}
	var ranked []candidate
	for i, f := range inventory {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		score := 0
		lf := strings.ToLower(f)
		if targets[f] {
			score += 100
		}
		if importantProjectPath(lf) {
			score += 40
		}
		if pathTokenMatchesQuery(filepath.Base(lf), q) {
			score += 25
		}
		if strings.Contains(q, strings.TrimSuffix(filepath.Base(lf), filepath.Ext(lf))) {
			score += 15
		}
		ranked = append(ranked, candidate{path: f, score: score, index: i})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})
	out := make([]string, 0, max)
	for i := 0; i < len(ranked) && len(out) < max; i++ {
		out = append(out, ranked[i].path)
	}
	return out
}

func importantProjectPath(path string) bool {
	base := filepath.Base(path)
	switch base {
	case "go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json",
		"pyproject.toml", "requirements.txt", "cargo.toml", "cargo.lock", "pom.xml", "build.gradle",
		"settings.gradle", "makefile", "dockerfile", "compose.yaml", "docker-compose.yml",
		"readme.md", "agents.md", "project.md":
		return true
	default:
		return strings.HasPrefix(path, ".slmcode/")
	}
}

func pathTokenMatchesQuery(base, query string) bool {
	name := strings.TrimSuffix(base, filepath.Ext(base))
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	}) {
		part = strings.TrimSpace(part)
		if len(part) >= 4 && strings.Contains(query, part) {
			return true
		}
	}
	return false
}

func heuristicHandoff(query string, targets []string, lang, worker, tester string) []string {
	var out []string
	if strings.TrimSpace(lang) != "" {
		out = append(out, "Detected project language: "+lang)
	}
	if len(targets) > 0 {
		out = append(out, "Likely target files: "+strings.Join(targets, ", "))
	} else {
		out = append(out, "No exact target files found; explorer/splitter must use existing paths only")
	}
	out = append(out, "Use "+valueOr(worker, "worker")+" for implementation and "+valueOr(tester, "tester")+" for verification")
	if cmd := verificationHintForLang(lang); cmd != "" {
		out = append(out, "Verify with "+cmd)
	}
	if strings.TrimSpace(query) != "" {
		out = append(out, "Keep edits scoped to this request; do not broaden the task")
	}
	return out
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
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
	case strings.Contains(q, "react") || strings.Contains(q, "next.js") ||
		strings.Contains(q, "nextjs") || strings.Contains(q, "vite") ||
		strings.Contains(q, "typescript") || strings.Contains(q, "tsx"):
		return "react-worker", "react-tester"
	case strings.Contains(q, "html") || strings.Contains(q, "browser") ||
		strings.Contains(q, "game") || strings.Contains(q, "website") ||
		strings.Contains(q, "webpage") || strings.Contains(q, "frontend") ||
		strings.Contains(q, "vanilla js") || strings.Contains(q, "javascript") ||
		strings.Contains(q, "css"):
		return "web-worker", "web-tester"
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

func projectLanguageSpecialists(lang string) (worker, tester string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go":
		return "go-worker", "go-tester"
	case "python":
		return "python-worker", "python-tester"
	case "typescript":
		return "react-worker", "react-tester"
	case "javascript", "html":
		return "web-worker", "web-tester"
	case "rust":
		return "rust-worker", "rust-tester"
	case "java":
		return "java-worker", "java-tester"
	case "c++", "c/make":
		return "cpp-worker", "cpp-tester"
	default:
		return "", ""
	}
}

// genericAgent reports whether an agent id is the generic worker/tester (or empty),
// i.e. the composer did not pick a language specialist.
func genericAgent(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return id == "" || id == "worker" || id == "tester" || id == "deep"
}

func (o *Orchestrator) knownAgentIDs() map[string]bool {
	known := map[string]bool{}
	if o == nil || o.factory == nil {
		return known
	}
	for _, s := range o.factory.AllSpecs() {
		known[strings.ToLower(strings.TrimSpace(s.ID))] = true
	}
	return known
}

func filterKnownSpecialistPair(worker, tester string, known map[string]bool) (string, string) {
	if len(known) == 0 {
		return worker, tester
	}
	worker = strings.ToLower(strings.TrimSpace(worker))
	tester = strings.ToLower(strings.TrimSpace(tester))
	if worker != "" && !known[worker] {
		worker = ""
	}
	if tester != "" && !known[tester] {
		tester = ""
	}
	return worker, tester
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
	knownPhases := map[string]bool{}
	for id := range pipeline.Default().Phases {
		knownPhases[id] = true
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
	var phases []composer.PhaseChoice
	for i := range comp.Phases {
		if !knownPhases[comp.Phases[i].ID] {
			dropped = append(dropped, "phase:"+comp.Phases[i].ID)
			continue
		}
		note(&comp.Phases[i].Agent)
		phases = append(phases, comp.Phases[i])
	}
	comp.Phases = phases
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

func (o *Orchestrator) availableSkillNames() map[string]bool {
	out := map[string]bool{}
	if o == nil || o.skills == nil {
		return out
	}
	list, _ := o.skills.List()
	for _, s := range list {
		out[strings.ToLower(strings.TrimSpace(s.Name))] = true
	}
	return out
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

	targets := heuristicTargetFiles(query, inventory, 8)
	if len(targets) > 0 {
		b.WriteString("## Likely target files (authoritative)\n")
		for _, f := range targets {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}
	if len(inventory) > 0 {
		ranked := rankComposerInventory(query, inventory, o.composerInventoryLimit())
		b.WriteString(fmt.Sprintf("## Workspace inventory (top %d of %d authoritative paths — do NOT invent paths)\n",
			len(ranked), len(inventory)))
		for _, f := range ranked {
			b.WriteString("- " + f + "\n")
		}
		if len(ranked) < len(inventory) {
			b.WriteString(fmt.Sprintf("- ... %d more paths omitted for context budget\n", len(inventory)-len(ranked)))
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

	b.WriteString("## Handoff contract guidance\n")
	b.WriteString("- Keep handoff short: 2-6 bullets, each under 140 characters.\n")
	b.WriteString("- Include authoritative target paths when known, verification command(s), non-goals, and role coordination constraints.\n")
	b.WriteString("- Later specialists will receive this handoff verbatim, so write it as operational instructions, not reasoning.\n\n")

	b.WriteString("Return the STRICT JSON composition now. Output JSON only — no prose.\n")
	return b.String()
}

// compositionBrief is injected into later scoped packs so the dynamic team
// shares the same compact run contract without carrying the full composer log.
func compositionBrief(c composer.Composition) string {
	var b strings.Builder
	b.WriteString("## Run collaboration contract\n\n")
	if c.Summary != "" {
		b.WriteString("Summary: " + c.Summary + "\n")
	}
	if len(c.Handoff) > 0 {
		b.WriteString("\nHandoff:\n")
		for _, h := range c.Handoff {
			b.WriteString("- " + h + "\n")
		}
	}
	if c.Execute.DefaultRole != "" || c.Execute.Reviewer != "" || c.Execute.Corrector != "" {
		b.WriteString("\nExecute loop: worker=" + valueOr(c.Execute.DefaultRole, "worker") +
			", reviewer=" + valueOr(c.Execute.Reviewer, "reviewer") +
			", corrector=" + valueOr(c.Execute.Corrector, "corrector") + "\n")
	}
	if len(c.Phases) > 0 {
		b.WriteString("\nSelected phases:\n")
		for _, p := range c.Phases {
			if p.Enabled && p.When != pipeline.WhenNever {
				b.WriteString("- " + p.ID + " @" + valueOr(p.Agent, "default") + "\n")
			}
		}
	}
	if len(c.Team) > 0 {
		b.WriteString("\nSelected team:\n")
		for _, t := range c.Team {
			if len(t.Skills) > 0 {
				b.WriteString("- " + t.Role + " skills=" + strings.Join(t.Skills, ", ") + "\n")
			} else {
				b.WriteString("- " + t.Role + "\n")
			}
		}
	}
	return b.String()
}

func valueOr(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

// compositionMarkdown renders a compact, human-readable summary of a composition.
func compositionMarkdown(c composer.Composition) string {
	var b strings.Builder
	b.WriteString("# Dynamic pipeline\n\n")
	b.WriteString("**Summary:** " + c.Summary + "\n\n")
	if strings.TrimSpace(c.Strategy) != "" {
		b.WriteString("**Strategy:** " + c.Strategy + "\n\n")
	}
	if len(c.Handoff) > 0 {
		b.WriteString("## Handoff\n\n")
		for _, h := range c.Handoff {
			b.WriteString("- " + h + "\n")
		}
		b.WriteString("\n")
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
