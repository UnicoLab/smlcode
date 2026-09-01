package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/teams"
)

// ── The team library, ahead of the model ─────────────────────────────────
//
// assembleSquads used to have one path: ask the manager specialist to invent an
// org chart, validate it, and fall back to a single stream when it did not hold
// up. On a frontier model that is fine. On the 7–32B models this harness exists
// for it is the single most expensive gamble in the run, because the org chart
// is assembled BEFORE the split and everything downstream — routing, ownership,
// per-team acceptance, the write deny list — is derived from it. When it comes
// back wrong the run does not fail, it silently downgrades: one stream, one
// worker, no contract, and a user watching a "parallel teams" feature do
// nothing.
//
// The library removes the gamble from the part that never needed a model. Which
// teams a request involves is answerable from evidence that is already on disk,
// and teams.Select answers it deterministically. What is left for the model is
// the CONTRACT — the routes and shapes where the halves meet — which is genuine
// judgment about THIS request and cannot be stored in a library.
//
// So: library first, model for the seam, model-assembled teams only when the
// library has nothing to say.

// teamRoster reads the discovered team library for this project.
//
// Never fatal. A malformed team block is one team missing from a menu, not a
// reason to refuse to run.
func (o *Orchestrator) teamRoster() []teams.Team {
	if o == nil || o.cfg == nil {
		return nil
	}
	reg, err := blocks.Load(o.cfg.Root)
	if err != nil {
		o.emitWarn("charter", "could not read the team library ("+err.Error()+")", "")
		return nil
	}
	return reg.TeamRoster()
}

// pinnedTeams are the team ids this run was told to use, in precedence order.
//
// A run-level choice (Studio's run setup, `--team`) beats the pipeline's own
// attachment, because the pipeline says what this SHAPE of work usually needs
// and the run-level flag says what THIS request needs. Both are explicit
// choices, so neither is scored — they are simply selected.
func (o *Orchestrator) pinnedTeams() []string {
	if o == nil || o.cfg == nil {
		return nil
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
	add(o.cfg.Teams)
	add(o.Pipeline().Teams)
	return out
}

// teamInventoryLimit is how much of the workspace preselection gets to see.
//
// Far larger than the 48-file inventory the planning phases are handed, and for
// the opposite reason: those are pasted into a prompt and every entry costs
// tokens, while this list is walked by a matcher and costs nothing. A marker
// that fell outside the top 48 — a nested web/package.json in a monorepo — is a
// team that silently never gets selected.
const teamInventoryLimit = 2000

// preselectTeams picks the teams for this request without a model call.
func (o *Orchestrator) preselectTeams(query string, inventory []string) (teams.Selection, []teams.Team) {
	roster := o.teamRoster()
	if len(roster) == 0 {
		return teams.Selection{}, nil
	}
	files := plan.ListWorkspaceFiles(o.cfg.Root, teamInventoryLimit)
	if len(files) == 0 {
		files = inventory
	}
	sel := teams.Select(roster, teams.Signals{Query: query, Files: files}, teams.Options{
		Pinned: o.pinnedTeams(),
	})
	return sel, roster
}

// teamsFromLibrary builds and activates a squad plan from the library.
//
// Returns nil when the library cannot answer — no teams, or fewer than two
// selected — which is the signal for assembleSquads to fall back to the model.
func (o *Orchestrator) teamsFromLibrary(ctx context.Context, query string, inventory []string, exploreOut, archOut string) *squads.Plan {
	sel, roster := o.preselectTeams(query, inventory)
	if len(roster) == 0 {
		return nil
	}
	if !sel.Enabled() {
		// Reported, not warned: one team (or none) is the correct answer for a
		// single-domain request, which is most of them. Saying WHY is what
		// stops "why did my teams not run" from being unanswerable.
		o.emit("charter", "team library: "+selectionLine(sel), "")
		return nil
	}

	p := teams.Compose(sel, "teams preselected from the library: "+strings.Join(sel.IDs(), " + "))
	if o.factory != nil {
		for _, note := range teams.StaffCheck(&p, o.factory.HasRole) {
			o.emitWarn("charter", note, "")
		}
	}

	// Report the evidence before the contract call, so a preselection the user
	// disagrees with is visible even if the model call then fails.
	o.emit("charter", "team library: "+selectionLine(sel), "")
	for _, ev := range sel.Evidence {
		if !ev.Selected {
			continue
		}
		o.emitAgent("charter", "manager", "", fmt.Sprintf("team %s — %s",
			ev.TeamID, strings.Join(ev.Reasons, "; ")), "", "")
	}

	// The seam is the model's job. A failure here costs the contract, not the
	// teams: a plan with no interfaces warns and runs, where a plan with the
	// wrong teams corrupts work.
	o.fillContract(ctx, &p, query, inventory, exploreOut, archOut)

	problems := p.Validate()
	for _, pr := range problems {
		if pr.Severity == squads.SeverityWarn {
			o.emitWarn("charter", "team plan: "+pr.Message, "")
		}
	}
	if problems.Errors() {
		// A library that produces an invalid plan is a bug in the library, and
		// the user is the only one who can fix it — so say exactly what is
		// wrong rather than falling through to the model, which would hide it.
		for _, pr := range problems {
			if pr.Severity == squads.SeverityError {
				o.emitWarn("charter", "team plan rejected: "+pr.Message, "")
			}
		}
		return nil
	}
	if err := squads.Save(o.cfg.SlmDir(), p); err != nil {
		o.emitWarn("charter", "could not save the team plan ("+err.Error()+") — running as a single stream", "")
		return nil
	}
	o.emit("charter", p.Summarize(), "")
	for _, s := range p.Squads {
		o.emitAgent("charter", "manager", "", fmt.Sprintf("team %s owns %s",
			s.ID, strings.Join(s.Owns, ", ")), "", "")
	}
	return &p
}

// fillContract asks the manager for the seam between teams that already exist.
//
// This is the same specialist and the same schema as full assembly, given a far
// narrower job: the teams, their globs and their acceptance commands are handed
// to it as FIXED, and only `contract` and `integration` are read back. That
// matters for a small model in two ways — the answer it has to get right is a
// third of the size, and the two thirds it no longer owns are the two thirds
// that could corrupt a run if it got them wrong.
//
// Whatever it says about squads is discarded. Not merged: a model that renames
// a team mid-answer would otherwise produce interfaces naming a provider that
// is not in the plan, and Validate would reject the whole thing over a field
// the user never asked the model to touch.
func (o *Orchestrator) fillContract(ctx context.Context, p *squads.Plan, query string, inventory []string, exploreOut, archOut string) {
	if o == nil || p == nil || o.factory == nil || len(p.Squads) < 2 {
		return
	}
	var b strings.Builder
	b.WriteString("## Query\n" + strings.TrimSpace(query) + "\n")
	b.WriteString("\n## Teams — FIXED, do not change\n")
	b.WriteString("These teams are already assembled. Reproduce them EXACTLY in `squads`;\n")
	b.WriteString("your job is `contract` and `integration`.\n\n")
	for _, s := range p.Squads {
		fmt.Fprintf(&b, "- id=%s owns=%s acceptance=%q\n",
			s.ID, strings.Join(s.Owns, ","), s.Acceptance)
		if s.Charter != "" {
			b.WriteString("  charter: " + s.Charter + "\n")
		}
	}
	b.WriteString("\nName every place these teams meet — a route, an exported symbol, a file\n")
	b.WriteString("format — say which team provides it and which consume it, and put the exact\n")
	b.WriteString("shape in `spec`. Provider and consumer ids MUST be from the list above.\n")
	if len(inventory) > 0 {
		b.WriteString("\n## Workspace inventory\n")
		for _, f := range limitList(inventory, 40) {
			b.WriteString("- " + f + "\n")
		}
	}
	if s := strings.TrimSpace(archOut); s != "" {
		b.WriteString("\n## Approach\n" + truncate(s, 1200) + "\n")
	}
	if s := strings.TrimSpace(exploreOut); s != "" {
		b.WriteString("\n## Exploration notes\n" + truncate(s, 900) + "\n")
	}

	o.emitAgent("charter", squadRoleID, "", "freezing the interface contract", "", "")
	out, err := o.runRoleTracked(ctx, squadRoleID, "", b.String())
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		o.emitWarn("charter", "no contract frozen ("+err.Error()+") — the teams will each invent their own seam", "")
		return
	}
	got, err := squads.Parse(out)
	if err != nil {
		o.emitWarn("charter", "unparsable contract ("+err.Error()+") — the teams will each invent their own seam", "")
		return
	}

	// The model was handed the real team ids and asked to reuse them, and on a
	// 7–32B model it will still write `backend` where the team is `backend-go`.
	// Resolving the reference costs nothing; refusing it costs the frozen seam
	// this whole feature exists to protect.
	kept, dropped, implied := resolveInterfaces(p, got.Contract.Interfaces)
	for _, note := range implied {
		o.emit("charter", "interface "+note+": named no consumer, and with two teams "+
			"on this run there is only one it can be", "")
	}
	if len(dropped) > 0 {
		o.emitWarn("charter", fmt.Sprintf(
			"dropped %d contract clause(s) naming a team that is not on this run: %s",
			len(dropped), strings.Join(limitList(dropped, 5), ", ")), "")
	}
	p.Contract.Interfaces = kept
	// Name each frozen clause. Without this the run reports a COUNT, and a
	// contract that froze the wrong seam, or froze it between the wrong two
	// teams, is indistinguishable from one that got it right.
	for _, in := range kept {
		line := "frozen: " + in.ID + " — provided by " + in.Provider
		if len(in.Consumers) > 0 {
			line += ", consumed by " + strings.Join(in.Consumers, ", ")
		} else {
			line += ", consumed by nobody named"
		}
		o.emit("charter", line, "")
	}
	p.Contract.Summary = strings.TrimSpace(got.Contract.Summary)
	p.Integration = got.Integration
	if strings.TrimSpace(got.Summary) != "" {
		p.Summary = strings.TrimSpace(got.Summary)
	}
	p.Normalize()
}

// selectionLine renders a preselection as one deterministic event line.
func selectionLine(sel teams.Selection) string {
	if len(sel.Teams) == 0 {
		if len(sel.Evidence) == 0 {
			return "no team matched this request — running as a single stream"
		}
		return "no team matched this request (" + evidenceLine(sel) + ") — running as a single stream"
	}
	if len(sel.Teams) == 1 {
		return "only " + sel.Teams[0].ID + " matched — one team is the single-stream pipeline wearing a hat, " +
			"so it runs as one stream"
	}
	return fmt.Sprintf("%d teams selected: %s", len(sel.Teams), strings.Join(sel.IDs(), ", "))
}

func evidenceLine(sel teams.Selection) string {
	parts := make([]string, 0, len(sel.Evidence))
	for _, ev := range sel.Evidence {
		parts = append(parts, fmt.Sprintf("%s=%d", ev.TeamID, ev.Score))
	}
	return strings.Join(limitList(parts, 6), " ")
}

// libraryAskView offers the saved library to the approval card.
//
// Teams already on the run are marked rather than dropped, so the UI can show
// the whole library and grey out what is in play — a menu that silently omits
// entries reads as a library that lost them.
func (o *Orchestrator) libraryAskView() []plan.PlanLibraryTeam {
	roster := o.teamRoster()
	if len(roster) == 0 {
		return nil
	}
	onRun := map[string]bool{}
	if o.squadPlan != nil {
		for _, s := range o.squadPlan.Squads {
			onRun[s.ID] = true
		}
	}
	out := make([]plan.PlanLibraryTeam, 0, len(roster))
	for _, t := range roster {
		out = append(out, plan.PlanLibraryTeam{
			ID: t.ID, Name: t.Name, Charter: t.Charter, Owns: t.Owns,
			Acceptance: t.Acceptance, Worker: t.Worker, Reviewer: t.Reviewer,
			Tester: t.Tester, Manager: t.Manager, Agents: t.Agents, Skills: t.Skills,
			OnRun: onRun[t.ID],
		})
	}
	return out
}

// splitGuidance tells the splitter where the ownership boundaries are.
//
// # WHY THIS EXISTS
//
// The org chart is assembled BEFORE the split, and the splitter was never told
// about it. Measured against a live 30B: it produced two tasks, each naming
// `cmd/server/main.go` AND `web/src/App.tsx`. Every one of those straddles both
// teams, so — correctly, by the rule that a seam task handed to one half is how
// a frontend task acquires permission to rewrite the API — every one stayed
// unassigned. The run worked. The teams did nothing: no parallel waves, no
// per-team acceptance, no ownership fence, on a plan whose org chart and frozen
// contract were both right.
//
// A model cannot respect a boundary it was not shown. This is four lines of
// prompt against a whole feature going quietly inert, and it is the cheapest
// SLM-quality fix in the phase: the constraint is concrete (these paths, that
// team), it needs no judgment, and the failure it prevents is invisible in the
// output — the run looks successful either way.
func (o *Orchestrator) splitGuidance() string {
	if o == nil || o.squadPlan == nil || len(o.squadPlan.Squads) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Teams — every task's files must stay inside ONE team\n\n")
	for _, s := range o.squadPlan.Squads {
		fmt.Fprintf(&b, "- `%s` owns %s\n", s.ID, strings.Join(s.Owns, ", "))
	}
	b.WriteString("\nA task whose `files` span two teams belongs to neither: it runs alone, " +
		"outside both lanes, and the parallel build you were asked for does not happen. " +
		"Split it into one task per team, and use `depends_on` when the second genuinely " +
		"needs the first.\n")
	return b.String()
}

// resolveInterfaces maps each clause's team references onto real team ids,
// drops the clauses that name nobody, and fills in the consumer a two-team run
// leaves implicit.
//
// Returns the surviving clauses, the ids of those dropped, and one note per
// implied consumer. Pure: every caller-visible decision is in the return value,
// so the reporting lives with the orchestrator and the rule lives here.
func resolveInterfaces(p *squads.Plan, in []squads.Interface) (kept []squads.Interface, dropped, implied []string) {
	if p == nil {
		return nil, nil, nil
	}
	for _, iface := range in {
		provider, ok := p.ResolveRef(iface.Provider)
		if !ok {
			// An interface whose provider is no team is owed by nobody. Kept,
			// it fails Validate and costs the whole contract; dropped, it costs
			// one clause and says so.
			dropped = append(dropped, iface.ID)
			continue
		}
		iface.Provider = provider
		cons := make([]string, 0, len(iface.Consumers))
		for _, c := range iface.Consumers {
			if id, ok := p.ResolveRef(c); ok && id != provider {
				cons = append(cons, id)
			}
		}
		// A clause naming a provider and NO consumer is the common shape from a
		// small model, and it half-freezes the seam: the spec is agreed, and
		// nothing records who agreed to it. With exactly two teams that is not
		// a guess — the only team that can consume what one provides is the
		// other one. Left empty it costs the consumer its reason to stop
		// waiting on the provider, which is what freezing the seam buys.
		if len(cons) == 0 && len(p.Squads) == 2 {
			for _, s := range p.Squads {
				if s.ID != provider {
					cons = append(cons, s.ID)
					implied = append(implied, iface.ID+" → "+s.ID)
				}
			}
		}
		iface.Consumers = cons
		kept = append(kept, iface)
	}
	sort.Strings(dropped)
	return kept, dropped, implied
}
