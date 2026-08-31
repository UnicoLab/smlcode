package server

import (
	"encoding/json"
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

// teamView renders one library team for Studio.
func teamView(b *blocks.TeamBlock) map[string]interface{} {
	if b == nil {
		return nil
	}
	t := b.Spec
	return map[string]interface{}{
		"id":         t.ID,
		"name":       t.Name,
		"charter":    t.Charter,
		"owns":       t.Owns,
		"acceptance": t.Acceptance,
		"worker":     t.Worker,
		"reviewer":   t.Reviewer,
		"tester":     t.Tester,
		"manager":    t.Manager,
		"agents":     t.Agents,
		"skills":     t.Skills,
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
	list := make([]map[string]interface{}, 0, len(reg.Teams))
	for _, id := range sortedTeamIDs(reg) {
		list = append(list, teamView(reg.Teams[id]))
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
		"pinned":          cfg.Teams,
		"pipeline_teams":  s.pipelineTeams(),
		"preselect":       preselect,
	})
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
	writeJSON(w, teamView(b))
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
	writeJSON(w, teamView(saved))
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
	return map[string]interface{}{
		"query":    query,
		"selected": sel.IDs(),
		"evidence": evidence,
		// enabled is the fact that matters: fewer than two teams means this
		// request runs as one stream no matter how well any single team scored.
		"enabled":  sel.Enabled(),
		"problems": problems,
		"staffing": notes,
		"pinned":   pinned,
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
