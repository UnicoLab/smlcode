package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/teams"
)

// ── The team library ─────────────────────────────────────────────────────
//
// GET /api/squads answers "how are the teams on this run doing", which is only
// answerable while a run has teams — that is why the Teams page used to be
// empty most of the time. It was reporting on a thing that only exists for the
// duration of one run.
//
// These endpoints are the other half: the LIBRARY, which exists whether or not
// anything is running and is where a team is authored, edited, deleted and
// attached to a pipeline. A library team is a block (kind "team"), so it gets
// the same discovery a pack does — builtin, then user, then project, project
// wins — and editing a builtin writes a project override rather than mutating
// something shipped inside the binary.

// teamPayload is the wire shape for authoring one team.
//
// The block Meta fields the UI actually surfaces travel alongside the spec
// rather than nested, because "description" and "icon" are properties of the
// team as far as anyone editing one is concerned, and a two-level form for a
// one-level concept is a form people fill in wrong.
type teamPayload struct {
	teams.Team
	Description string   `json:"description,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Language    string   `json:"language,omitempty"`
}

// runDefaultManager is the agent that triages a team's rejected work when the
// team names nobody, or names someone who cannot answer the triage contract.
const runDefaultManager = agents.RoleTriage

// effectiveManager resolves the manager a team will actually get.
//
// The same rule the run applies (orchestrator.effectiveManager): a manager has
// to answer the triage contract, not merely exist, and the run default always
// can. Reported alongside the team so the page never shows a manager the run
// would then silently replace.
func effectiveManager(named string, managers map[string]bool) (string, bool) {
	return agents.ResolveManager(named, func(id string) bool { return managers[id] })
}

// teamView renders one library team for Studio.
func teamView(b *blocks.TeamBlock, managers map[string]bool) map[string]interface{} {
	if b == nil {
		return nil
	}
	t := b.Spec
	manager, isDefault := effectiveManager(t.Manager, managers)
	return map[string]interface{}{
		"effective_manager": manager,
		"manager_default":   isDefault,
		"id":                t.ID,
		"name":              t.Name,
		"charter":           t.Charter,
		"owns":              t.Owns,
		"acceptance":        t.Acceptance,
		"worker":            t.Worker,
		"reviewer":          t.Reviewer,
		"tester":            t.Tester,
		"manager":           t.Manager,
		"agents":            t.Agents,
		"skills":            t.Skills,
		"match": map[string]interface{}{
			"keywords":   t.Match.Keywords,
			"files":      t.Match.Files,
			"extensions": t.Match.Extensions,
			"priority":   t.Match.Priority,
		},
		"description": b.Description,
		"icon":        b.Icon,
		"tags":        b.Tags,
		"language":    b.Language,
		"source":      b.Source,
		"path":        b.Path,
		"builtin":     b.Source == blocks.SourceBuiltin,
	}
}

// handleListTeams serves the library plus everything needed to edit it.
//
// The agent rosters ride along on purpose. A team names a worker, a reviewer
// and a manager, and a picker offering an agent this harness cannot dispatch
// produces a team that looks fine and staffs nothing — the roster is the only
// place that knows which ids are real.
func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	managers := s.managerSet()
	list := make([]map[string]interface{}, 0, len(reg.Teams))
	for _, id := range sortedTeamIDs(reg) {
		list = append(list, teamView(reg.Teams[id], managers))
	}

	// An optional query previews what WOULD be selected, so the page can show
	// the same evidence the run will act on before anything is started.
	var preselect interface{}
	if q := strings.TrimSpace(r.URL.Query().Get("query")); q != "" {
		preselect = s.preselectView(reg, q, nil)
	}

	cfg := s.cfg()
	writeJSON(w, map[string]interface{}{
		"ok":              true,
		"teams":           list,
		"agents":          s.staffableAgentIDs(),
		"managers":        s.triageCapableAgents(),
		"library_enabled": cfg.TeamLibrary,
		"squads_enabled":  cfg.Squads,
		"dynamic_enabled": cfg.DynamicPipeline,
		"default_manager": runDefaultManager,
		"pinned":          cfg.Teams,
		"pipeline_teams":  s.pipelineTeams(),
		"preselect":       preselect,
		"running":         s.isRunning(),
	})
}

func (s *Server) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// managerSet is the triage-capable roster as a lookup.
func (s *Server) managerSet() map[string]bool {
	out := map[string]bool{}
	for _, id := range s.triageCapableAgents() {
		out[strings.ToLower(id)] = true
	}
	return out
}

func sortedTeamIDs(reg *blocks.Registry) []string {
	out := make([]string, 0, len(reg.Teams))
	for id := range reg.Teams {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, ok := reg.GetTeam(strings.TrimSpace(r.PathValue("id")))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, teamView(b, s.managerSet()))
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	s.writeTeam(w, r, "")
}

func (s *Server) handlePutTeam(w http.ResponseWriter, r *http.Request) {
	s.writeTeam(w, r, strings.ToLower(strings.TrimSpace(r.PathValue("id"))))
}

// writeTeam is create and update, which differ only in where the id comes from.
func (s *Server) writeTeam(w http.ResponseWriter, r *http.Request, pathID string) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	var body teamPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if pathID != "" {
		if strings.TrimSpace(body.ID) == "" {
			body.ID = pathID
		}
		if !strings.EqualFold(strings.TrimSpace(body.ID), pathID) {
			http.Error(w, "id mismatch", http.StatusBadRequest)
			return
		}
	}
	body.Normalize()

	block := &blocks.TeamBlock{
		Meta: blocks.Meta{
			Kind:        blocks.KindTeam,
			ID:          body.ID,
			Name:        body.Name,
			Description: body.Description,
			Icon:        body.Icon,
			Tags:        body.Tags,
			Language:    body.Language,
		},
		Spec: body.Team,
	}
	if err := block.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Provenance is discovery's answer, not the client's. Saving it would
	// persist "builtin" into a project override and make the UI offer to delete
	// a file it then could not find.
	block.Spec.Source, block.Spec.Path = "", ""
	if _, err := blocks.Save(s.cfg().Root, block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, "saved but reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	saved, ok := reg.GetTeam(block.ID)
	if !ok {
		http.Error(w, "saved but not discoverable — check .slmcode/blocks/teams/", http.StatusInternalServerError)
		return
	}
	writeJSON(w, teamView(saved, s.managerSet()))
}

// handleDeleteTeam removes a project-level team.
//
// A builtin has no file to remove, and deleting one would mean deleting it from
// inside the binary. blocks.Delete says so in the error, which is the honest
// answer: edit it instead and the override shadows it.
func (s *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	found, err := blocks.Delete(s.cfg().Root, blocks.KindTeam, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "deleted": id, "removed_file": found})
}

// handlePreselectTeams previews the teams a query would run with.
//
// Same code path the run takes, so what the page shows is what will happen —
// a preview computed a second way is a preview that eventually lies.
func (s *Server) handlePreselectTeams(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query  string   `json:"query"`
		Pinned []string `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.preselectView(reg, body.Query, body.Pinned))
}

// preselectView scores the library against a query and reports the outcome.
func (s *Server) preselectView(reg *blocks.Registry, query string, pinned []string) map[string]interface{} {
	cfg := s.cfg()
	if len(pinned) == 0 {
		pinned = append(append([]string(nil), cfg.Teams...), s.pipelineTeams()...)
	}
	sel := teams.Select(reg.TeamRoster(),
		teams.Signals{Query: query, Files: plan.ListWorkspaceFiles(cfg.Root, 2000)},
		teams.Options{Pinned: pinned})

	p := teams.Compose(sel, "")
	notes := teams.StaffCheck(&p, s.agentRegistered())
	problems := []string{}
	if sel.Enabled() {
		for _, pr := range p.Validate() {
			problems = append(problems, pr.String())
		}
	}

	evidence := make([]map[string]interface{}, 0, len(sel.Evidence))
	for _, ev := range sel.Evidence {
		evidence = append(evidence, map[string]interface{}{
			"team_id": ev.TeamID, "score": ev.Score, "reasons": ev.Reasons,
			"selected": ev.Selected, "conflict": ev.Conflict, "pinned": ev.Pinned,
		})
	}

	// Who will actually work, per selected team, after the staff check — the
	// same answer the composition carries (composer.TeamChoice), so the page
	// and the run setup panel never disagree about a team's manager.
	managers := s.managerSet()
	staffed := make([]map[string]interface{}, 0, len(p.Squads))
	for _, sq := range p.Squads {
		manager, isDefault := effectiveManager(sq.Manager, managers)
		staffed = append(staffed, map[string]interface{}{
			"id": sq.ID, "name": sq.Name, "worker": sq.Worker, "reviewer": sq.Reviewer,
			"tester": sq.Tester, "manager": manager, "manager_default": isDefault,
			"agents": sq.Agents, "skills": sq.Skills, "owns": sq.Owns, "acceptance": sq.Acceptance,
		})
	}
	mode, note := teamModeNote(sel, cfg.Squads)
	return map[string]interface{}{
		"query":    query,
		"selected": sel.IDs(),
		"evidence": evidence,
		// enabled is the fact that matters: fewer than two teams means this
		// request runs as one stream no matter how well any single team scored.
		"enabled":  sel.Enabled() && cfg.Squads,
		"mode":     mode,
		"note":     note,
		"teams":    staffed,
		"problems": problems,
		"staffing": notes,
		"pinned":   pinned,
	}
}

// teamModeNote says what a selection does to a run, in the composition's
// vocabulary (composer.TeamMode*): parallel teams, one team staffing the run,
// or none.
func teamModeNote(sel teams.Selection, squadsOn bool) (string, string) {
	ids := sel.IDs()
	switch {
	case len(ids) == 0:
		return "", "no team matched — the request runs as one stream"
	case len(ids) == 1:
		return "single", "team " + ids[0] + " staffs the run as one stream: its worker, reviewer, tester and skills take the pipeline"
	case !squadsOn:
		return "", strings.Join(ids, ", ") + " matched, but teams are turned off for this project (squads: false) — the request runs as one stream"
	default:
		return "parallel", strings.Join(ids, " + ") + " build in parallel behind a frozen contract"
	}
}

// handleActivateTeams writes a squad plan from library teams, outside a run.
//
// This is what makes the Teams page usable when nothing is running. Composing
// an org chart, seeing the ownership check pass, and having the next run pick it
// up is the whole workflow the page was missing; before this, the only way to
// get a squad plan on disk was to start a run and hope the manager produced one.
func (s *Server) handleActivateTeams(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	var body struct {
		Teams   []string `json:"teams"`
		Summary string   `json:"summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Teams) < 2 {
		http.Error(w, "a team plan needs at least 2 teams — one team is the single-stream pipeline", http.StatusBadRequest)
		return
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Pinned-only selection: an explicit list is an instruction, not a
	// hypothesis, so nothing here is scored. Max is raised to the list length
	// because the default cap exists to stop AUTOMATIC selection from running
	// away, and the user asking for five teams has already made that call.
	sel := teams.Select(reg.TeamRoster(), teams.Signals{}, teams.Options{
		Pinned: body.Teams,
		Max:    len(body.Teams),
	})
	if len(sel.Teams) < 2 {
		writeProblems(w, problemsFromEvidence(sel, body.Teams))
		return
	}
	p := teams.Compose(sel, body.Summary)
	notes := teams.StaffCheck(&p, s.agentRegistered())

	// An id that resolved to nothing is dropped rather than failing the
	// request — but it is REPORTED. The user typed it; silently activating two
	// of the three teams they asked for is the kind of near-miss nobody
	// notices until the run is short a team.
	on := map[string]bool{}
	for _, id := range p.IDs() {
		on[id] = true
	}
	var unknown []string
	for _, id := range body.Teams {
		if slug := strings.ToLower(strings.TrimSpace(id)); slug != "" && !on[slug] {
			unknown = append(unknown, id)
		}
	}

	// The saved contract is preserved across a team change when the teams it
	// names still exist. Losing a frozen interface because someone added a
	// third team would silently un-freeze the seam both halves already built
	// against.
	if prev, ok, _ := squads.Load(s.slmDir()); ok {
		p.Contract = keepKnownClauses(prev.Contract, p.IDs())
		if p.Integration.Acceptance == "" {
			p.Integration = prev.Integration
		}
	}
	p.Normalize()
	if probs := p.Validate(); probs.Errors() {
		writeProblems(w, probs.Strings())
		return
	}
	if err := squads.Save(s.slmDir(), p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Pinning the same teams in config is what makes Activate mean anything.
	//
	// Without it the saved org chart is overwritten by the next run's own
	// preselection, and "Activate" is a button that appears to work and changes
	// nothing — the worst possible outcome for a control whose entire purpose
	// is to say "these teams, not whatever you would have picked".
	var saveErr error
	s.withConfigWrite(func(c *config.Config) {
		if c == nil {
			return
		}
		c.Teams = p.IDs()
		saveErr = c.Save()
	})
	if saveErr != nil {
		http.Error(w, "teams activated but the pin could not be saved: "+saveErr.Error(),
			http.StatusInternalServerError)
		return
	}

	s.emit(orchestrator.Event{
		Phase: "charter", Kind: "output",
		Message: "teams activated from the library: " + p.Summarize(), Time: time.Now(),
	})
	writeJSON(w, map[string]interface{}{
		"ok": true, "summary": p.Summarize(), "teams": p.IDs(), "staffing": notes,
		"pinned": p.IDs(), "unknown": unknown,
	})
}

// keepKnownClauses drops contract clauses naming a team that is no longer here.
func keepKnownClauses(c squads.Contract, ids []string) squads.Contract {
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	out := squads.Contract{Summary: c.Summary}
	for _, in := range c.Interfaces {
		if !known[in.Provider] {
			continue
		}
		cons := make([]string, 0, len(in.Consumers))
		for _, id := range in.Consumers {
			if known[id] {
				cons = append(cons, id)
			}
		}
		in.Consumers = cons
		out.Interfaces = append(out.Interfaces, in)
	}
	return out
}

// problemsFromEvidence explains a selection that came back too small.
//
// Two different failures land here and they need different sentences: a team
// that does not exist (the client named a stale id) and a team that exists and
// lost a contested path. Reporting the second for the first sends the user
// looking for an overlap that is not there.
func problemsFromEvidence(sel teams.Selection, asked []string) []string {
	scored := map[string]bool{}
	out := make([]string, 0, len(sel.Evidence))
	for _, ev := range sel.Evidence {
		scored[ev.TeamID] = true
		if ev.Selected {
			continue
		}
		reason := strings.Join(ev.Reasons, "; ")
		if reason == "" {
			reason = "not selected"
		}
		out = append(out, ev.TeamID+": "+reason)
	}
	for _, id := range asked {
		if !scored[strings.ToLower(strings.TrimSpace(id))] {
			out = append(out, id+": no such team in the library")
		}
	}
	if len(out) == 0 {
		out = append(out, "none of those teams are in the library")
	}
	sort.Strings(out)
	return out
}

// writeProblems reports an understood request that produced an unrunnable plan.
//
// 422 rather than 400 for the same reason PATCH /api/squads uses it: the client
// sent something well-formed and the harness understood it — what failed is the
// resulting org chart, and the client needs the reasons to show the user.
func writeProblems(w http.ResponseWriter, problems []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "problems": problems})
}

// agentRegistered reports whether an agent id can actually be dispatched.
func (s *Server) agentRegistered() func(string) bool {
	known := map[string]bool{}
	for _, id := range s.staffableAgentIDs() {
		known[strings.ToLower(id)] = true
	}
	return func(id string) bool { return known[strings.ToLower(strings.TrimSpace(id))] }
}

// staffableAgentIDs lists every agent id this harness can dispatch.
func (s *Server) staffableAgentIDs() []string {
	specs := agents.PublicSpecsWithCustom(s.loadCustomAgents())
	out := make([]string, 0, len(specs))
	for _, m := range specs {
		if id, ok := m["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// pipelineTeams are the teams the active pipeline attaches.
func (s *Server) pipelineTeams() []string {
	o := s.orch()
	if o == nil {
		return nil
	}
	return o.Pipeline().Teams
}

// ── A dedicated project manager for one team ─────────────────────────────

// handleCreateTeamManager gives a team its own project manager.
//
// A manager is an ordinary custom agent whose id ends in "-triage" — that
// suffix is what maps it to the triage contract (agents.NormalizeDecoding), so
// the loop can read its verdicts. Writing one by hand means knowing that rule,
// the prompt the builtin manager runs with, and that a manager must have no
// tools; this endpoint does all three, seeds the prompt with the team's charter
// and roster so the manager knows whose work it answers for, and points the
// team at it in one step. Idempotent: an existing agent of that id is kept and
// only the team's manager seat is written.
func (s *Server) handleCreateTeamManager(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	var body struct {
		Title        string `json:"title"`
		SystemPrompt string `json:"system_prompt"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
	}
	reg, err := blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, ok := reg.GetTeam(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	team := b.Spec

	managerID := team.ID + "-triage"
	created := false
	if !s.agentRegistered()(managerID) {
		title := strings.TrimSpace(body.Title)
		if title == "" {
			title = team.Name + " project manager"
		}
		prompt := strings.TrimSpace(body.SystemPrompt)
		if prompt == "" {
			prompt = managerPrompt(team)
		}
		spec := agents.CustomSpec{
			ID:           managerID,
			Title:        title,
			Description:  "Decides who on the " + team.Name + " team takes a rejected delivery next, and what they need to know.",
			SystemPrompt: prompt,
			Tools:        agents.BoolPtr(false),
			MaxIter:      2,
			Temperature:  0.15,
			MaxTokens:    640,
		}
		if _, err := agents.WriteCustom(s.cfg().AgentsDir(), spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.rebuildOrchestrator(); err != nil {
			http.Error(w, "manager saved but rebuild failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		created = true
	}

	// The team now names its manager. Written as a project override, which is
	// what editing any team does — a builtin is shadowed, not mutated.
	block := &blocks.TeamBlock{Meta: b.Meta, Spec: team}
	block.Spec.Manager = managerID
	block.Spec.Source, block.Spec.Path = "", ""
	if err := block.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := blocks.Save(s.cfg().Root, block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reg, err = blocks.Load(s.cfg().Root)
	if err != nil {
		http.Error(w, "saved but reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	saved, ok := reg.GetTeam(team.ID)
	if !ok {
		http.Error(w, "saved but not discoverable", http.StatusInternalServerError)
		return
	}
	s.emit(orchestrator.Event{
		Phase: "charter", Kind: "output",
		Message: "team " + team.ID + " now has its own project manager: " + managerID, Time: time.Now(),
	})
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"manager":  managerID,
		"created":  created,
		"team":     teamView(saved, s.managerSet()),
		"managers": s.triageCapableAgents(),
	})
}

// managerPrompt is the builtin triage prompt with the team written in: whose
// work this manager answers for, who is on the team, and where its territory
// ends. The rules and the output shape are the builtin's verbatim, because the
// decoding grammar is derived from them.
func managerPrompt(t teams.Team) string {
	var b strings.Builder
	b.WriteString("You are the project manager of the " + t.Name + " team (" + t.ID + ").\n")
	if t.Charter != "" {
		b.WriteString("Team charter: " + t.Charter + "\n")
	}
	var people []string
	for _, id := range append([]string{t.Worker, t.Reviewer, t.Tester}, t.Agents...) {
		if id = strings.TrimSpace(id); id != "" {
			people = append(people, id)
		}
	}
	if len(people) > 0 {
		b.WriteString("Your people: " + strings.Join(people, ", ") + ". Prefer them; reach outside the team only when the fix needs a skill they lack.\n")
	}
	if len(t.Owns) > 0 {
		b.WriteString("Team territory: " + strings.Join(t.Owns, ", ") + ". A fix that needs files outside it belongs to another team or to integration — say so in guidance.\n")
	}
	b.WriteString("\n")
	b.WriteString(agents.PromptTriage)
	return b.String()
}

// ── Assigning a task to a team by hand ───────────────────────────────────

// teamAssignmentProblem refuses a team that is not on the org chart.
func teamAssignmentProblem(p *squads.Plan, team string) string {
	team = strings.ToLower(strings.TrimSpace(team))
	if team == "" {
		return ""
	}
	if p == nil {
		return "no org chart — activate teams on the Teams page before assigning tasks to one"
	}
	if _, ok := p.Squad(team); !ok {
		return "no team " + team + " on the org chart (" + strings.Join(p.IDs(), ", ") + ")"
	}
	return ""
}

// ownershipRefusal is why a hand assignment would not stick.
//
// A stamp is a write permission at the wave fence, and the fence is derived
// from the task's FILES: a task whose files all sit in the backend's territory
// is re-stamped backend on the next save whatever the board says
// (squads.RetargetAssignments). Refusing here, with the reason, beats
// accepting an assignment the next wave silently undoes — and that covers
// un-assigning too: "no team" for a task the backend's territory owns is
// undone just as silently. A task with no files, or files no single team
// owns, keeps whatever a human puts there.
func ownershipRefusal(p *squads.Plan, t plan.Task, team string) string {
	team = strings.ToLower(strings.TrimSpace(team))
	if p == nil || len(t.Files) == 0 {
		return ""
	}
	a := p.Assign(t.Files)
	if a.Squad == "" || a.Squad == team {
		return ""
	}
	verb := "move to " + team
	if team == "" {
		verb = "be un-assigned"
	}
	return fmt.Sprintf("%s cannot %s: its files (%s) are owned by %s, and ownership decides the stamp — "+
		"change the task's files or the team's paths first", t.ID, verb, strings.Join(limitPaths(t.Files, 3), ", "), a.Squad)
}

func limitPaths(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(append([]string{}, in[:n]...), fmt.Sprintf("+%d more", len(in)-n))
}
