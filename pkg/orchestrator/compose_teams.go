package orchestrator

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/teams"
)

// ── Teams in the dynamic composition ─────────────────────────────────────
//
// The composer decided phases, loop roles and skills, and the charter phase
// decided teams, several phases later and from a different input. The two
// never met: the run setup panel showed a composition staffed with go-worker
// while the org chart the charter built put the same half under someone else,
// the composer prompt never mentioned that a React team existed, and a user
// who pinned "the backend team" for a request that only involved that team got
// a single stream staffed by whoever language routing picked — the team's
// worker, reviewer, tester, manager and skills all ignored.
//
// So the team decision is made ONCE per run, deterministically, from the
// library, and stamped onto the composition:
//
//   - two or more teams → they run in parallel behind a frozen contract, and
//     the composition says so, with each team's staffing and manager, in the
//     same words the charter phase will act on;
//   - exactly one team → it STAFFS the run: its worker, reviewer and tester
//     take the execute loop, its skills are pinned, its charter and territory
//     ride in the handoff, and its manager triages its rejected work. That is
//     what "send this request to the backend team" means when the request has
//     no second half — and the charter phase honors it (assembleSquads does
//     not ask the model to invent a second team);
//   - none → said plainly, with the evidence, so "why did my teams not run" has
//     an answer on the panel rather than in a log.
//
// The composer MODEL never picks teams. It is told which teams are on the run
// so the roles it picks agree with them, and anything it says about teams is
// discarded — the same rule fillContract applies to the squad list.

// teamDecision is the team answer for one request, computed once and handed
// to every consumer in the run — the composer prompt, the composition, and
// the charter phase — so none of them can compute a different one.
type teamDecision struct {
	Choices []composer.TeamChoice
	Mode    string
	Note    string
}

// decideTeams computes the library teams for this request as the composition
// will carry them: chosen teams in rank order, the mode they put the run in,
// and a one-line note explaining the decision.
//
// pins overrides the run-level pin (cfg.Teams) when non-nil, which is how a
// preview of "this request, sent to these teams" sees exactly what that run
// would; nil reads the configured pins.
func (o *Orchestrator) decideTeams(query string, inventory []string, pins []string) teamDecision {
	if o == nil || o.cfg == nil {
		return teamDecision{}
	}
	if !o.cfg.TeamLibrary {
		if o.cfg.Squads {
			return teamDecision{Note: "the team library is not used for this project (team_library: false) — the manager agent assembles teams at charter"}
		}
		return teamDecision{}
	}
	sel, roster := o.preselectTeamsWith(query, inventory, pins)
	if len(roster) == 0 {
		return teamDecision{Note: "no teams in the library — create one on the Teams page to staff runs by team"}
	}

	evidence := map[string]teams.Evidence{}
	for _, ev := range sel.Evidence {
		evidence[ev.TeamID] = ev
	}
	choices := make([]composer.TeamChoice, 0, len(sel.Teams))
	for _, t := range sel.Teams {
		c := o.teamChoice(t)
		if ev, ok := evidence[t.ID]; ok {
			c.Pinned, c.Score = ev.Pinned, ev.Score
			switch {
			case ev.Pinned:
				c.Reason = "pinned by hand"
			default:
				c.Reason = strings.Join(ev.Reasons, "; ")
			}
		}
		choices = append(choices, c)
	}

	switch {
	case len(choices) == 0:
		why := "no team matched this request"
		if len(sel.Evidence) > 0 {
			why += " (" + evidenceLine(sel) + ")"
		}
		if o.cfg.Squads {
			// With nothing from the library the charter phase still asks the
			// manager agent, so the honest answer is "one stream unless…".
			return teamDecision{Note: why + " — it runs as one stream unless the manager agent assembles teams at charter"}
		}
		return teamDecision{Note: why + " — it runs as one stream"}
	case len(choices) == 1:
		c := choices[0]
		return teamDecision{Choices: choices, Mode: composer.TeamModeSingle, Note: fmt.Sprintf(
			"team %s staffs this run as one stream (%s) — manager %s", c.ID, c.Reason, c.Manager)}
	case !o.cfg.Squads:
		return teamDecision{Choices: choices, Note: fmt.Sprintf(
			"%d teams matched (%s) but teams are turned off for this project (squads: false) — it runs as one stream",
			len(choices), strings.Join(idsOf(choices), ", "))}
	default:
		return teamDecision{Choices: choices, Mode: composer.TeamModeParallel, Note: fmt.Sprintf(
			"%d teams build in parallel behind a frozen contract: %s",
			len(choices), strings.Join(idsOf(choices), ", "))}
	}
}

// teamPick is one run's library team selection, computed once.
//
// decideTeams (the composer) and teamsFromLibrary (the charter phase) both
// used to call preselectTeams, and each call listed up to 2000 workspace
// files and reloaded the block library to reach the same deterministic
// answer. The selection is a pure function of the query, the tree and the
// pins, none of which change between those two phases, so it is memoized
// for the run and cleared with the rest of the team state.
type teamPick struct {
	query  string
	sel    teams.Selection
	roster []teams.Team
}

// preselectTeamsWith is preselectTeams with the run-level pin overridden.
//
// Only the run path (pins == nil) is memoized: a preview asks about pins the
// run does not have, and must not be answered from the run's cache nor
// pollute it.
func (o *Orchestrator) preselectTeamsWith(query string, inventory []string, pins []string) (teams.Selection, []teams.Team) {
	if pins == nil {
		o.mu.Lock()
		cached := o.teamPick
		o.mu.Unlock()
		if cached != nil && cached.query == query {
			return cached.sel, cached.roster
		}
	}
	roster := o.teamRoster()
	if len(roster) == 0 {
		return teams.Selection{}, nil
	}
	files := plan.ListWorkspaceFiles(o.cfg.Root, teamInventoryLimit)
	if len(files) == 0 {
		files = inventory
	}
	sel := teams.Select(roster, teams.Signals{Query: query, Files: files}, teams.Options{
		Pinned: o.pinnedTeamsWith(pins),
	})
	if pins == nil {
		o.mu.Lock()
		o.teamPick = &teamPick{query: query, sel: sel, roster: roster}
		o.mu.Unlock()
	}
	return sel, roster
}

// pinnedTeamsWith is pinnedTeams with cfg.Teams replaced by pins when pins is
// non-nil — the same precedence a run started with those teams would have
// (server.applyRunOptions swaps them into cfg.Teams for the run's duration),
// so a preview and the run it previews read identical pins.
func (o *Orchestrator) pinnedTeamsWith(pins []string) []string {
	if o == nil || o.cfg == nil {
		return nil
	}
	if pins == nil {
		return o.pinnedTeams()
	}
	var out []string
	seen := map[string]bool{}
	add := func(ids []string) {
		for _, id := range ids {
			id = strings.ToLower(strings.TrimSpace(id))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	add(pins)
	add(o.Pipeline().Teams)
	return out
}

// teamChoice renders one library team with the staffing the run will dispatch.
//
// A seat naming an agent this harness cannot dispatch is cleared here for the
// same reason teams.StaffCheck clears it at charter: the composition must
// describe what WILL run, and "go-worker" on a machine without the Go pack is
// a promise the loop then breaks silently. The manager gets the stricter
// check — it must answer the triage contract, not merely exist — and falls
// back to the run default, which always can.
func (o *Orchestrator) teamChoice(t teams.Team) composer.TeamChoice {
	registered := func(id string) bool {
		if o == nil || o.factory == nil {
			return true
		}
		return o.factory.HasRole(id)
	}
	seat := func(id string) string {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || !registered(id) {
			return ""
		}
		return id
	}
	c := composer.TeamChoice{
		ID:         t.ID,
		Name:       t.Name,
		Charter:    t.Charter,
		Owns:       append([]string(nil), t.Owns...),
		Acceptance: t.Acceptance,
		Worker:     seat(t.Worker),
		Reviewer:   seat(t.Reviewer),
		Tester:     seat(t.Tester),
		Skills:     append([]string(nil), t.Skills...),
	}
	for _, a := range t.Agents {
		if id := seat(a); id != "" {
			c.Agents = append(c.Agents, id)
		}
	}
	c.Manager, c.ManagerDefault = o.effectiveManager(t.Manager)
	return c
}

// canTriage reports whether an agent answers the triage contract. With no
// factory (a config-only preview) every nominee is taken at its word.
func (o *Orchestrator) canTriage(id string) bool {
	if o == nil || o.factory == nil {
		return true
	}
	return o.factory.EmitsSchema(id, schema.RoleTriage)
}

// effectiveManager resolves a team's manager to the agent that will actually
// triage its rejected work — the one rule, from pkg/agents.
func (o *Orchestrator) effectiveManager(named string) (string, bool) {
	return agents.ResolveManager(named, o.canTriage)
}

func idsOf(choices []composer.TeamChoice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.ID)
	}
	return out
}

// composeTeams stamps the team decision onto a composition and, when one team
// staffs the run, adopts its staffing.
//
// Adoption follows the same rule the language hint does — a generic role is
// replaced, a specific one the composer chose is kept — with one exception: a
// PINNED team was chosen by hand, and "run this with the backend team" means
// the backend team's people, whatever the composer preferred.
func composeTeams(comp *composer.Composition, td teamDecision) {
	if comp == nil {
		return
	}
	comp.Teams, comp.TeamMode, comp.TeamNote = td.Choices, td.Mode, td.Note
	if td.Mode == composer.TeamModeSingle && len(td.Choices) > 0 {
		adoptTeamStaffing(comp, td.Choices[0])
	}
	fillTeamSeats(comp)
}

// fillTeamSeats writes, on every team, who actually sits in each seat once
// the pipeline fills what the team left empty — and the gaps in words. Done
// after adoption and the critical-phase repair so the pipeline roles it reads
// are the ones the run will bind.
func fillTeamSeats(comp *composer.Composition) {
	if comp == nil {
		return
	}
	d := comp.SeatDefaultsOf()
	for i := range comp.Teams {
		t := &comp.Teams[i]
		t.Seats = composer.FillSeats(t.Worker, t.Reviewer, t.Tester, t.Manager, t.ManagerDefault, d)
		t.Gaps = composer.Gaps(t.ID, t.Seats)
	}
}

func adoptTeamStaffing(comp *composer.Composition, t composer.TeamChoice) {
	if comp == nil {
		return
	}
	take := func(current *string, want string) {
		if want == "" || current == nil {
			return
		}
		if t.Pinned || genericAgent(*current) {
			*current = want
		}
	}
	take(&comp.Execute.DefaultRole, t.Worker)
	if t.Reviewer != "" && (t.Pinned || comp.Execute.Reviewer == "" || comp.Execute.Reviewer == "reviewer") {
		comp.Execute.Reviewer = t.Reviewer
	}
	for i := range comp.Phases {
		switch comp.Phases[i].ID {
		case "execute":
			take(&comp.Phases[i].Agent, t.Worker)
		case "test":
			take(&comp.Phases[i].Agent, t.Tester)
		}
	}
}

// ── The single team, at run time ─────────────────────────────────────────
//
// With two or more teams the squad plan carries each team's manager and
// roster to the loop. With ONE team there is no plan — the plan is the
// contract between halves, and one half has no seam — so the team's manager
// used to be reported on the composition and then never asked: triage fell to
// the run default, and the dedicated manager the user had just created for
// the team was ignored on exactly the runs that were sent to that team.
//
// The composition's single team is therefore kept on the run and rendered as
// a one-squad staffing wherever the loop asks "who manages this task": the
// manager, and the team's own people first in the roster.

// singleTeamPlan renders the run's single team as a one-squad plan, for the
// staffing helpers that read one. Nil when the run has no single team.
func (o *Orchestrator) singleTeamPlan() *squads.Plan {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	t := o.singleTeam
	o.mu.Unlock()
	if t == nil {
		return nil
	}
	return &squads.Plan{Squads: []squads.Squad{{
		ID: t.ID, Name: t.Name, Charter: t.Charter, Owns: append([]string(nil), t.Owns...),
		Acceptance: t.Acceptance, Worker: t.Worker, Reviewer: t.Reviewer, Tester: t.Tester,
		Manager: t.Manager, Agents: append([]string(nil), t.Agents...), Skills: append([]string(nil), t.Skills...),
	}}}
}

// staffingPlan is the plan the loop's staffing questions are answered from:
// the squad plan when the run has one, else the single team's.
func (o *Orchestrator) staffingPlan() *squads.Plan {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	p := o.squadPlan
	o.mu.Unlock()
	if p != nil {
		return p
	}
	return o.singleTeamPlan()
}

// singleTeamStaffing is the staffing a task gets on a single-team run: the
// whole run is that team's, so every task is.
func (o *Orchestrator) singleTeamStaffing() squads.Staffing {
	p := o.singleTeamPlan()
	if p == nil {
		return squads.Staffing{}
	}
	return squads.StaffingFor(p, p.Squads[0].ID)
}

// teamHandoff is what later specialists are told about the teams, appended to
// the handoff after the generic lines and kept to two bullets: the handoff is
// pasted into every task pack, and a team's whole charter is already carried
// by the squad brief when teams run in parallel.
func teamHandoff(comp *composer.Composition) {
	if comp == nil || len(comp.Teams) == 0 {
		return
	}
	add := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || handoffContains(comp.Handoff, line) {
			return
		}
		comp.Handoff = append(comp.Handoff, line)
	}
	switch comp.TeamMode {
	case composer.TeamModeSingle:
		t := comp.Teams[0]
		var staff []string
		for _, s := range t.Seats {
			if s.Agent == "" {
				continue
			}
			entry := s.Role + "=" + s.Agent
			if s.Borrowed() && s.Role != "manager" {
				entry += " (" + s.Source + ")"
			}
			staff = append(staff, entry)
		}
		line := "Team " + t.ID + " staffs this run"
		if len(staff) > 0 {
			line += ": " + strings.Join(staff, ", ")
		}
		if t.Charter != "" {
			line += ". " + strings.TrimSuffix(t.Charter, ".")
		}
		add(line)
		if len(t.Owns) > 0 {
			add("Team territory: " + strings.Join(limitList(t.Owns, 6), ", ") +
				" — touch files outside it only when the request requires it")
		}
	case composer.TeamModeParallel:
		parts := make([]string, 0, len(comp.Teams))
		for _, t := range comp.Teams {
			part := t.ID
			if len(t.Owns) > 0 {
				part += " (" + strings.Join(limitList(t.Owns, 3), ", ") + ")"
			}
			parts = append(parts, part)
		}
		add("Teams build in parallel: " + strings.Join(parts, " · ") +
			" — every task's files stay inside ONE team; the seam is frozen in CONTRACT.md")
	}
}

// teamSeats folds each chosen team's people into the composition's team
// roster, carrying the team's skills, so the "Team" panel and the skill packs
// describe the people who will actually work.
func teamSeats(comp *composer.Composition, skills map[string]bool) {
	if comp == nil || len(comp.Teams) == 0 {
		return
	}
	index := map[string]int{}
	for i, m := range comp.Team {
		index[m.Role] = i
	}
	for _, t := range comp.Teams {
		want := filterKnownSkills(t.Skills, skills)
		ids := []string{t.Worker, t.Reviewer, t.Tester, t.Manager}
		ids = append(ids, t.Agents...)
		// A borrowed seat is on the team for this run: whoever the pipeline
		// lends as tester loads the team's skills like the team's own would.
		for _, seat := range t.Seats {
			if seat.Borrowed() && seat.Role != "manager" {
				ids = append(ids, seat.Agent)
			}
		}
		for _, id := range ids {
			id = strings.ToLower(strings.TrimSpace(id))
			if id == "" {
				continue
			}
			if i, ok := index[id]; ok {
				comp.Team[i].Skills = mergeSkills(comp.Team[i].Skills, want)
				continue
			}
			member := composer.TeamMember{Role: id, Skills: defaultSkillsForRole(id, skills)}
			member.Skills = mergeSkills(member.Skills, want)
			comp.Team = append(comp.Team, member)
			index[id] = len(comp.Team) - 1
		}
	}
}

func mergeSkills(have, more []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(have)+len(more))
	for _, s := range append(append([]string{}, have...), more...) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// teamsPromptSection tells the composer model which teams are on the run, so
// the roles it picks agree with them. It is guidance, not a menu: the model
// cannot add or remove a team, and the section says so.
func teamsPromptSection(td teamDecision) string {
	if len(td.Choices) == 0 && td.Note == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Teams on this run (decided from the library — do NOT change them)\n")
	if td.Note != "" {
		b.WriteString(td.Note + "\n")
	}
	for _, t := range td.Choices {
		fmt.Fprintf(&b, "- %s", t.ID)
		if t.Name != "" && t.Name != t.ID {
			fmt.Fprintf(&b, " (%s)", t.Name)
		}
		var staff []string
		for _, s := range []struct{ role, id string }{
			{"worker", t.Worker}, {"reviewer", t.Reviewer}, {"tester", t.Tester}, {"manager", t.Manager},
		} {
			if s.id != "" {
				staff = append(staff, s.role+"="+s.id)
			}
		}
		if len(staff) > 0 {
			b.WriteString(" — " + strings.Join(staff, ", "))
		}
		if len(t.Owns) > 0 {
			b.WriteString(" — owns " + strings.Join(limitList(t.Owns, 4), ", "))
		}
		b.WriteString("\n")
	}
	switch td.Mode {
	case composer.TeamModeParallel:
		b.WriteString("Bind execute/test to specialists these teams can dispatch; each team's own seats take its tasks.\n")
	case composer.TeamModeSingle:
		b.WriteString("Bind execute/test to this team's worker and tester unless the request plainly needs another language.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// teamsMarkdown renders the team decision for the composition summary.
func teamsMarkdown(c composer.Composition) string {
	if len(c.Teams) == 0 && c.TeamNote == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Teams\n\n")
	if c.TeamNote != "" {
		b.WriteString(c.TeamNote + "\n\n")
	}
	for _, t := range c.Teams {
		manager := t.Manager
		if t.ManagerDefault && manager != "" {
			manager += " (run default)"
		}
		fmt.Fprintf(&b, "- `%s` — worker=%s · reviewer=%s · tester=%s · manager=%s",
			t.ID, valueOr(t.Worker, "default"), valueOr(t.Reviewer, "default"),
			valueOr(t.Tester, "default"), valueOr(manager, "default"))
		if len(t.Owns) > 0 {
			b.WriteString(" · owns " + strings.Join(t.Owns, ", "))
		}
		if t.Reason != "" {
			b.WriteString(" — " + t.Reason)
		}
		b.WriteString("\n")
		for _, g := range t.Gaps {
			b.WriteString("  - " + g + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}
