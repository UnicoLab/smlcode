package squads

import "strings"

// ── Who answers for a team ───────────────────────────────────────────────
//
// A rejected delivery raises one question: who takes it next, and what do they
// need to know that the last attempt did not. That is a staffing decision, and
// staffing decisions belong to somebody who knows the people.
//
// Run-wide, there is nobody who does. The manager sees a flat roster of every
// implementer the factory registered and a task id, and has to guess which of
// them is the team that owns the file. On a two-squad run it guesses wrong in
// the obvious way: a failing Go handler goes to whoever sounds most competent,
// which is as likely to be the React half's worker as the Go one.
//
// A squad knows its own people. These helpers are how the triage step asks it.

// Staffing is one squad's people, as far as reassignment is concerned.
type Staffing struct {
	// Squad is the id these people belong to; "" when the task is unassigned.
	Squad string
	// Manager triages this squad's rejected work. "" falls back to the run's
	// default manager.
	Manager string
	// Members are the agent ids this squad is staffed with, most-specific
	// first: its own worker, then its reviewer.
	Members []string
}

// StaffingFor reports who answers for a squad.
//
// An unknown id is not an error: tasks routed before the squads were assembled,
// and tasks in a single-stream run, legitimately carry no squad. The zero value
// means "the run's defaults decide", which is what the caller wants.
func StaffingFor(p *Plan, squadID string) Staffing {
	squadID = slug(squadID)
	if p == nil || squadID == "" {
		return Staffing{}
	}
	for _, s := range p.Squads {
		if s.ID != squadID {
			continue
		}
		out := Staffing{Squad: s.ID, Manager: strings.TrimSpace(s.Manager)}
		for _, m := range []string{s.Worker, s.Reviewer} {
			if m = strings.TrimSpace(m); m != "" && !containsFold(out.Members, m) {
				out.Members = append(out.Members, m)
			}
		}
		return out
	}
	return Staffing{}
}

// Colleagues orders a roster so the task's own team comes first.
//
// It never REMOVES anybody. A squad's worker is the obvious first choice for
// that squad's failing work, but it is not always the right one — the whole
// reason the delivery was rejected may be that this team lacks the skill the
// fix needs, and a manager forbidden from looking outside its own team could
// only pick between agents that have already failed.
//
// So this is an ordering, not a filter: in-team staff first, in the order the
// squad lists them, then everybody else in their original order. What the
// manager sees first is its own people; what it can still reach is everyone.
func Colleagues(p *Plan, squadID string, roster []string) []string {
	staff := StaffingFor(p, squadID)
	if len(staff.Members) == 0 || len(roster) == 0 {
		return roster
	}
	out := make([]string, 0, len(roster))
	taken := make(map[string]bool, len(roster))
	for _, m := range staff.Members {
		for _, id := range roster {
			if strings.EqualFold(id, m) && !taken[id] {
				taken[id] = true
				out = append(out, id)
			}
		}
	}
	for _, id := range roster {
		if !taken[id] {
			out = append(out, id)
		}
	}
	return out
}

func containsFold(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}

// LaneOf reports the single squad that owns every one of these paths.
//
// It answers a narrower question than Assign: not "who should do this work" but
// "is this defect entirely inside one team's territory". The answer is "" the
// moment the paths straddle two teams or land somewhere nobody owns, because a
// defect on the seam belongs to both halves and one that lands nowhere belongs
// to whoever the board says.
//
// # WHY THIS EXISTS
//
// A tester failure names files. The reopen heuristics that decide which tasks
// to reopen from those files are text matches — acceptance snippets, basenames,
// task ids in the failure blob — and text matches leak across teams. The
// frozen contract makes it worse rather than better: it is attached as
// acceptance criteria to BOTH halves, so one clause of shared text is enough
// for a backend compile error to reopen the frontend's finished work.
//
// The frontend team then re-runs, fails at a defect it does not own and cannot
// see, and the run ends reporting "frontend 0/1 working" over a half that was
// correct and complete. That is the same failure the write deny list exists to
// prevent, arriving through the board instead of through the tool layer.
func LaneOf(p *Plan, paths []string) string {
	if p == nil || len(paths) == 0 {
		return ""
	}
	lane := ""
	for _, path := range paths {
		owner, ok := p.Owner(path)
		if !ok {
			// Unowned: it could belong to anybody, so it cannot narrow the
			// defect to one team.
			return ""
		}
		if lane == "" {
			lane = owner
			continue
		}
		if lane != owner {
			return ""
		}
	}
	return lane
}
