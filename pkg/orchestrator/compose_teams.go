package orchestrator

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/schema"
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
// So the team decision is made ONCE, deterministically, from the library, and
// stamped onto the composition:
//
//   - two or more teams → they run in parallel behind a frozen contract, and
//     the composition says so, with each team's staffing and manager, in the
//     same words the charter phase will act on;
//   - exactly one team → it STAFFS the run: its worker, reviewer and tester
//     take the execute loop, its skills are pinned, and its charter and
//     territory ride in the handoff. That is what "send this request to the
//     backend team" means when the request has no second half;
//   - none → said plainly, with the evidence, so "why did my teams not run" has
//     an answer on the panel rather than in a log.
//
// The composer MODEL never picks teams. It is told which teams are on the run
// so the roles it picks agree with them, and anything it says about teams is
// discarded — the same rule fillContract applies to the squad list.

// teamChoices computes the library teams for this request as the composition
// will carry them: chosen teams in rank order, the mode they put the run in,
// and a one-line note explaining the decision.
func (o *Orchestrator) teamChoices(query string, inventory []string) ([]composer.TeamChoice, string, string) {
	if o == nil || o.cfg == nil {
		return nil, "", ""
	}
	if !o.cfg.TeamLibrary {
		if o.cfg.Squads {
			return nil, "", "the team library is not used for this project (team_library: false) — the manager agent assembles teams at charter"
		}
		return nil, "", ""
	}
	sel, roster := o.preselectTeams(query, inventory)
	if len(roster) == 0 {
		return nil, "", "no teams in the library — create one on the Teams page to staff runs by team"
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
		if len(sel.Evidence) == 0 {
			return nil, "", "no team matched this request — it runs as one stream"
		}
		return nil, "", "no team matched this request (" + evidenceLine(sel) + ") — it runs as one stream"
	case len(choices) == 1:
		c := choices[0]
		return choices, composer.TeamModeSingle, fmt.Sprintf(
			"team %s staffs this run as one stream (%s) — manager %s", c.ID, c.Reason, c.Manager)
	case !o.cfg.Squads:
		return choices, "", fmt.Sprintf(
			"%d teams matched (%s) but teams are turned off for this project (squads: false) — it runs as one stream",
			len(choices), strings.Join(idsOf(choices), ", "))
	default:
		return choices, composer.TeamModeParallel, fmt.Sprintf(
			"%d teams build in parallel behind a frozen contract: %s",
			len(choices), strings.Join(idsOf(choices), ", "))
	}
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

// effectiveManager resolves a team's manager to the agent that will actually
// triage its rejected work.
func (o *Orchestrator) effectiveManager(named string) (string, bool) {
	named = strings.ToLower(strings.TrimSpace(named))
	if named == "" {
		return agents.RoleTriage, true
	}
	if o != nil && o.factory != nil && !o.factory.EmitsSchema(named, schema.RoleTriage) {
		return agents.RoleTriage, true
	}
	return named, false
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
func (o *Orchestrator) composeTeams(comp *composer.Composition, query string, inventory []string) {
	if comp == nil {
		return
	}
	choices, mode, note := o.teamChoices(query, inventory)
	comp.Teams, comp.TeamMode, comp.TeamNote = choices, mode, note
	if mode != composer.TeamModeSingle || len(choices) == 0 {
		return
	}
	adoptTeamStaffing(comp, choices[0])
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
		for _, s := range []struct{ role, id string }{
			{"worker", t.Worker}, {"reviewer", t.Reviewer}, {"tester", t.Tester}, {"manager", t.Manager},
		} {
			if s.id != "" {
				staff = append(staff, s.role+"="+s.id)
			}
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
func teamsPromptSection(choices []composer.TeamChoice, mode, note string) string {
	var b strings.Builder
	b.WriteString("## Teams on this run (decided from the library — do NOT change them)\n")
	if note != "" {
		b.WriteString(note + "\n")
	}
	for _, t := range choices {
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
	switch mode {
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
	}
	b.WriteString("\n")
	return b.String()
}
