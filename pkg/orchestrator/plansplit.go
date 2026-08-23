package orchestrator

import (
	"context"
	"fmt"
	"strings"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// planSplitInput is everything the plan → split → approve stretch needs from
// the phases before it.
type planSplitInput struct {
	RunID      string
	Query      string
	ExploreOut string
	ArchOut    string
	Inventory  []string
	Discovered []string
	PRD        plan.ScopePRD
	Clarify    plan.ClarifyResult
}

// maxPlanApprovalReplans bounds the user-driven replan loop.
const maxPlanApprovalReplans = 2

// unscopedTaskNote marks a task whose target files could not be resolved.
// A task with unknown targets must BLOCK, not guess: the splitter's
// hallucinated paths used to be silently replaced with the first twelve files
// in the workspace, which then became "## Focus files (HARD SCOPE)" and a
// twelve-file write allowlist on the anti-wander guard.
const unscopedTaskNote = "no resolvable target files — needs human scoping"

// runPlanSplitApprove runs plan → split → scope judge → coordinator → approval,
// looping when the user asks for a bounded replan.
func (o *Orchestrator) runPlanSplitApprove(ctx context.Context, in planSplitInput) (*plan.Board, plan.Plan, string, error) {
	var (
		planOut     string
		pl          plan.Plan
		board       *plan.Board
		replanNotes []string
		err         error
	)
	for planAttempt := 0; ; planAttempt++ {
		if planAttempt > 0 {
			o.emit("plan", fmt.Sprintf("replanning from user feedback (%d/%d)", planAttempt, maxPlanApprovalReplans), "")
		}

		planOut, pl, err = o.runPlanPhase(ctx, in, replanNotes)
		if err != nil {
			return nil, pl, planOut, err
		}

		board, err = o.runSplitPhase(ctx, in, pl, planOut, replanNotes)
		if err != nil {
			return nil, pl, planOut, err
		}

		// 4a Scope / PRD judge gate — enrich or rewrite weak tasks before execute.
		scopeValidation := o.runScopeJudgeGate(ctx, in.Query, board, in.PRD)
		o.persistBoard(board)
		o.emit("split", fmt.Sprintf("TASKS.md + board: %d agent tasks", len(board.Tasks)), "")

		// 4b Coordinator reviews the board before execute
		o.coordinate(ctx, in.Query, board, "pre-execute")

		// 4c Plan approval gate (Claude Code Plan Mode)
		decision, aerr := o.runPlanApprovalDecision(ctx, in.Query, board, scopeValidation)
		if aerr != nil {
			return nil, pl, planOut, aerr
		}
		if decision.Approved {
			return board, pl, planOut, nil
		}
		if !decision.Replan {
			return nil, pl, planOut, fmt.Errorf("plan not approved")
		}
		if planAttempt >= maxPlanApprovalReplans {
			return nil, pl, planOut, fmt.Errorf("plan replan limit reached after %d revision(s): %s",
				maxPlanApprovalReplans, decision.Notes)
		}
		note := strings.TrimSpace(decision.Notes)
		if note == "" {
			note = "Revise the plan for smaller, safer, better-scoped execution."
		}
		replanNotes = append(replanNotes, note)
		_ = o.store.Append(contextstore.DocScratch, "Plan replan request", "- "+note)
	}
}

// runPlanPhase is phase 3: planner (multipass when think_passes>1), optional
// critique/refine, then parse + PRD merge.
func (o *Orchestrator) runPlanPhase(ctx context.Context, in planSplitInput, replanNotes []string) (string, plan.Plan, error) {
	var pl plan.Plan
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhasePlan)
	if err := o.runPipelineSlots(ctx, "plan", "before", in.Query, in.ExploreOut, ""); err != nil {
		return "", pl, err
	}
	planAgent := o.phaseAgent("plan", plan.RolePlanner)
	planOut := ""
	if o.Pipeline().HasReplace("plan") {
		if err := o.runPipelineSlots(ctx, "plan", "replace", in.Query, in.ExploreOut, ""); err != nil {
			return "", pl, err
		}
		planOut = `{"summary":"pipeline slot replaced plan","goals":[],"steps":["Execute board tasks"]}`
	} else if o.phaseEnabled("plan") {
		o.emitAgent("plan", planAgent, "", "creating plan", "", "")
		planPrompt := o.buildPlannerPrompt(in.Query, in.RunID, planAgent, in.ExploreOut, in.ArchOut, in.PRD, in.Clarify, replanNotes)
		out, err := o.runRoleMultipassTracked(ctx, planAgent, "", planPrompt)
		if err != nil {
			if isCancelErr(err) {
				_, cerr := o.checkpointInterrupt(&plan.Board{QueryID: in.RunID, Query: in.Query}, session.PhasePlan, err)
				return "", pl, cerr
			}
			return "", pl, fmt.Errorf("planner: %w", err)
		}
		planOut = out
		planOut = o.maybeCritiquePlan(ctx, in.Query, planAgent, planPrompt, planOut)
	}
	if err := o.runPipelineSlots(ctx, "plan", "after", in.Query, in.ExploreOut, planOut); err != nil {
		return planOut, pl, err
	}

	pl, _ = plan.ParsePlanJSON(planOut)
	if strings.TrimSpace(pl.Summary) == "" || looksLikeJSONBlob(pl.Summary) {
		pl.Summary = firstSentence(stripJSONNoise(planOut))
		if strings.TrimSpace(pl.Summary) == "" || looksLikeJSONBlob(pl.Summary) {
			pl.Summary = "Implement request with locked PRD"
		}
	}
	pl = plan.MergeClarifyIntoPlan(pl, in.Clarify)
	pl = plan.MergePRDIntoPlan(pl, in.PRD)
	if len(replanNotes) > 0 {
		pl.Assumptions = append(pl.Assumptions, "User replan notes: "+strings.Join(replanNotes, " | "))
	}
	// Persist agent plan immediately so Studio PLAN.md / board update live mid-run.
	o.persistBoard(&plan.Board{QueryID: in.RunID, Query: in.Query, Plan: pl, Tasks: nil})
	o.emit("plan", "PLAN.md rewritten for this query", "")
	return planOut, pl, nil
}

// maybeCritiquePlan runs the extra reviewer critique+refine pass (think_passes≥3).
func (o *Orchestrator) maybeCritiquePlan(ctx context.Context, query, planAgent, planPrompt, planOut string) string {
	if o.cfg.ThinkPasses < 3 || multipass.LooksCompleteJSON(planOut) {
		return planOut
	}
	revAgent := o.Pipeline().Execute.Reviewer
	if revAgent == "" {
		revAgent = plan.RoleReviewer
	}
	o.emitAgent("plan", revAgent, "", "plan critique pass", "", "")
	critiquePrompt := "Critique this SLM plan. Check missing files, oversized tasks, unclear acceptance, wrong order.\n" +
		"Query:\n" + truncate(query, 800) + "\n\nPlan:\n" + truncate(planOut, 3500) +
		"\n\nSTRICT JSON: {\"ok\":bool,\"issues\":[string],\"hints\":[string]}"
	critique, _ := o.runRoleTracked(ctx, revAgent, "", critiquePrompt)
	if strings.TrimSpace(critique) == "" {
		return planOut
	}
	_ = o.store.Append(contextstore.DocScratch, "Plan critique", critique)
	o.emitFull("plan", stream.KindOutput, revAgent, "", "plan critique", "", truncate(critique, 800))
	lower := strings.ToLower(critique)
	if strings.Contains(lower, `"ok": true`) || strings.Contains(lower, `"ok":true`) {
		return planOut
	}
	o.emitAgent("plan", planAgent, "", "refining plan from critique", "", "")
	refinePrompt := planPrompt + "\n\n## Critique\n" + truncate(critique, 2000) +
		"\n\nRevise. Atomic for SLM. STRICT JSON plan."
	if refined, rerr := o.runRoleTracked(ctx, planAgent, "", refinePrompt); rerr == nil && strings.TrimSpace(refined) != "" {
		return refined
	}
	return planOut
}

// runSplitPhase is phase 4: splitter, sanitize, reconcile, cap, board build.
func (o *Orchestrator) runSplitPhase(ctx context.Context, in planSplitInput, pl plan.Plan, planOut string, replanNotes []string) (*plan.Board, error) {
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseSplit)
	if err := o.runPipelineSlots(ctx, "split", "before", in.Query, in.ExploreOut, planOut); err != nil {
		return nil, err
	}
	splitAgent := o.phaseAgent("split", "splitter")
	var tasksOut string
	if o.Pipeline().HasReplace("split") {
		if err := o.runPipelineSlots(ctx, "split", "replace", in.Query, in.ExploreOut, planOut); err != nil {
			return nil, err
		}
		tasksOut = `{"tasks":[]}`
	} else if o.phaseEnabled("split") {
		o.emitAgent("split", splitAgent, "", "atomic task split", "", "")
		splitPrompt := o.buildSplitterPrompt(in.Query, splitAgent, planOut, in.PRD, in.Clarify, replanNotes)
		out, err := o.runRoleMultipassTracked(ctx, splitAgent, "", splitPrompt)
		if err != nil {
			if isCancelErr(err) {
				_, cerr := o.checkpointInterrupt(&plan.Board{QueryID: in.RunID, Query: in.Query, Plan: pl}, session.PhaseSplit, err)
				return nil, cerr
			}
			return nil, fmt.Errorf("splitter: %w", err)
		}
		tasksOut = out
	}
	if err := o.runPipelineSlots(ctx, "split", "after", in.Query, in.ExploreOut, planOut); err != nil {
		return nil, err
	}

	tasks, err := plan.ParseTasksJSON(tasksOut)
	if err != nil || len(tasks) == 0 {
		tasks = fallbackTasks(pl)
	}
	discovered := plan.DiscoverRelevantFiles(o.cfg.Root, in.Query, in.ExploreOut)
	if len(in.Discovered) > 0 {
		discovered = plan.ReconcileFiles(o.cfg.Root, append(discovered, in.Discovered...), in.Inventory)
	}
	if rm := o.repoMapNow(); rm != nil {
		// The ranked symbol index finds targets that filename matching misses.
		discovered = plan.FilterExisting(o.cfg.Root,
			mergeUnique(discovered, rm.RankFilesFor(in.Query, 8)))
	}
	tasks = plan.SanitizeTasksIn(tasks, in.ExploreOut+"\n"+strings.Join(discovered, "\n"), in.Query, o.cfg.Root)
	tasks = plan.EnsureTaskPRDs(tasks, in.PRD, in.Query)
	if len(discovered) > 0 {
		_ = o.store.ReplaceSection(contextstore.DocContext, "Discovered files", "- "+strings.Join(discovered, "\n- "))
	}

	unscoped := 0
	for i := range tasks {
		resolved := plan.ReconcileFiles(o.cfg.Root, tasks[i].Files, discovered)
		if len(resolved) == 0 && len(tasks[i].Files) > 0 {
			// The splitter named files and none of them resolve. Block the task
			// instead of handing it an invented scope.
			blockUnscopedTask(&tasks[i])
			unscoped++
		}
		tasks[i].Files = resolved
		// Keep persisted descriptions lean — scoped packs are injected at execute time.
		tasks[i].Description = loop.StripScopedPack(tasks[i].Description)
	}
	if unscoped > 0 {
		o.emitWarn("split", fmt.Sprintf("%d task(s) moved to to_scope — %s", unscoped, unscopedTaskNote), "")
	}

	if len(tasks) > 8 {
		o.emit("split", fmt.Sprintf("capping tasks %d -> 8 for SLM efficiency (preserving harness/tester)", len(tasks)), "")
		tasks = plan.CapTasksPreserveHarness(tasks, 8)
	}

	board := &plan.Board{QueryID: in.RunID, Query: in.Query, Plan: pl, Tasks: tasks}
	for i := range board.Tasks {
		t := board.Tasks[i]
		if t.Column == "" {
			t.Column = plan.ColReadyToDev
		}
		t.Normalize()
		board.Tasks[i] = t
	}
	return board, nil
}

// blockUnscopedTask parks a task whose targets could not be resolved.
func blockUnscopedTask(t *plan.Task) {
	if t == nil {
		return
	}
	t.MoveTo(plan.ColToScope)
	t.Notes = strings.TrimSpace(t.Notes + "\n" + unscopedTaskNote)
	if strings.TrimSpace(t.Error) == "" {
		t.Error = unscopedTaskNote
	}
}
