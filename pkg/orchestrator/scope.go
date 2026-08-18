package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// AskHandler collects structured clarify answers (Studio / TUI / tests).
// Return UseAllRec=true or empty answers to apply recommended defaults.
type AskHandler func(ctx context.Context, ask plan.ScopeAsk) (plan.ScopeAnswers, error)

// OnAsk registers a synchronous ask callback for clarify_mode=ask.
func (o *Orchestrator) OnAsk(h AskHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onAsk = h
}

// runScopeInterview runs the clarifier/interviewer and resolves ask|auto|off.
func (o *Orchestrator) runScopeInterview(ctx context.Context, query, exploreOut string) plan.ScopeInterview {
	mode := plan.NormalizeClarifyMode(o.cfg.ClarifyMode)
	if o.cfg.AutoApprove && mode == plan.ClarifyAsk {
		mode = plan.ClarifyAuto // never block HITL when auto_approve
	}
	if mode == plan.ClarifyOff {
		return plan.ScopeInterview{}
	}
	// When explicitly set to ask, ALWAYS run the interview (user wants to review).
	// When auto, skip for concrete queries that don't need clarification.
	if mode != plan.ClarifyAsk {
		if !plan.NeedsClarification(query) && !forceClarifyGreenfield(query) {
			return plan.ScopeInterview{}
		}
	}

	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseClarify)
	o.emitAgent("clarify", "interviewer", "", "scope interview", "", "")

	clarifyPrompt := agents.PromptClarifier + "\n\n## Query\n" + truncate(query, 1200) +
		"\n\n## Exploration\n" + truncate(exploreOut, 2000) +
		"\n\nMode hint: clarify_mode=" + mode +
		". If mode=auto, still emit recommended options but set needs_user=false." +
		"\n\n## Project language\n" + o.langHint() +
		"\nReturn STRICT JSON interview object."
	clarifyOut, err := o.runRoleTracked(ctx, plan.RolePlanner, "", clarifyPrompt)
	if err != nil || strings.TrimSpace(clarifyOut) == "" {
		o.emit("clarify", "interviewer skipped (empty/error) — using query as-is", "")
		return plan.ScopeInterview{}
	}
	interview := plan.ParseScopeInterview(clarifyOut)
	o.emitFull("clarify", stream.KindOutput, "interviewer", "",
		"interview draft ready", "", truncate(clarifyOut, 1200))

	resolved := interview
	switch mode {
	case plan.ClarifyAsk:
		if interview.NeedsUserDecision() {
			resolved = o.resolveAsk(ctx, query, interview)
		} else {
			resolved = plan.ApplyScopeAnswers(interview, plan.ResolveWithDefaults(interview))
			o.emit("clarify", "no blocking forks — applied recommended defaults", "")
		}
	default: // auto
		resolved = plan.ApplyScopeAnswers(interview, plan.ResolveWithDefaults(interview))
		o.emit("clarify", "auto: locked recommended decisions", "")
	}

	md := plan.FormatPRDMarkdown(resolved.PRD, resolved.Assumptions)
	if strings.TrimSpace(md) != "" {
		_ = o.store.Append(contextstore.DocContext, "Locked PRD", md)
	}
	o.emitFull("clarify", stream.KindOutput, "interviewer", "",
		"PRD locked", "", truncate(md, 1000))
	plan.ClearScopeAsk(o.cfg.SlmDir())
	return resolved
}

func forceClarifyGreenfield(query string) bool {
	q := strings.ToLower(query)
	green := strings.Contains(q, "create") || strings.Contains(q, "scaffold") ||
		strings.Contains(q, "build") || strings.Contains(q, "new project") ||
		strings.Contains(q, "mvp")
	vagueStack := !strings.Contains(q, ".py") && !strings.Contains(q, ".go") &&
		!strings.Contains(q, "fastapi") && !strings.Contains(q, "django") &&
		!strings.Contains(q, "langgraph") && !strings.Contains(q, "pytest") &&
		!strings.Contains(q, "go.mod")
	return green && vagueStack && len(strings.Fields(query)) <= 24
}

func (o *Orchestrator) resolveAsk(ctx context.Context, query string, interview plan.ScopeInterview) plan.ScopeInterview {
	timeout := o.cfg.ClarifyTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ask := plan.ScopeAsk{
		ID:        fmt.Sprintf("ask-%d", time.Now().UnixNano()),
		Kind:      "clarify",
		Query:     query,
		Questions: interview.Questions,
		PRDDraft:  interview.PRD,
		TimeoutS:  int(timeout.Seconds()),
		OnTimeout: "use_recommended",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_ = plan.WriteScopeAsk(o.cfg.SlmDir(), ask)
	payload := plan.MarshalAskJSON(ask)
	o.emitFull("clarify", stream.KindAsk, "interviewer", "",
		fmt.Sprintf("waiting for answers (%d questions) — POST /api/clarify/answer or use recommended",
			len(ask.Questions)),
		"", payload)

	var ans plan.ScopeAnswers
	got := false

	o.mu.Lock()
	h := o.onAsk
	o.mu.Unlock()
	if h != nil {
		if a, err := h(ctx, ask); err == nil {
			a.AskID = ask.ID
			ans, got = a, true
		}
	}
	if !got {
		o.emit("clarify", fmt.Sprintf("polling .slmcode/clarify/answers.json (timeout %s)", timeout), "")
		if a, ok, err := plan.WaitScopeAnswersForID(ctx, o.cfg.SlmDir(), ask.ID, timeout); err == nil && ok {
			ans, got = a, true
		} else if err != nil && ctx.Err() != nil {
			// cancelled
			return plan.ApplyScopeAnswers(interview, plan.ResolveWithDefaults(interview))
		}
	}
	if !got {
		o.emit("clarify", "ask timeout — applying recommended defaults", "")
		ans = plan.ResolveWithDefaults(interview)
	} else {
		o.emit("clarify", "user answers received — locking PRD", "")
	}
	return plan.ApplyScopeAnswers(interview, ans)
}

// runScopeJudgeGate ensures every task has a concrete PRD/acceptance before execute.
func (o *Orchestrator) runScopeJudgeGate(ctx context.Context, query string, board *plan.Board, prd plan.ScopePRD) *plan.ScopeJudgeResult {
	if o.cfg == nil || !o.cfg.ScopeJudge || board == nil {
		return nil
	}
	o.emitAgent("split", "scope-judge", "", "PRD completeness check", "", "")

	// Always enrich from locked PRD first.
	board.Tasks = plan.EnsureTaskPRDs(board.Tasks, prd, query)
	judge := plan.JudgeTaskScopeHeuristics(board.Tasks, prd)

	// Optional LLM judge when think_passes≥2 or heuristics found gaps.
	if o.executor != nil && o.store != nil && ((!judge.OK && o.cfg.ThinkPasses >= 1) || o.cfg.ThinkPasses >= 2) {
		_, tasksMD := board.ToMarkdown()
		prompt := agents.PromptScopeJudge +
			"\n\n## Query\n" + truncate(query, 800) +
			"\n\n## Locked PRD\n" + truncate(plan.FormatPRDMarkdown(prd, nil), 2000) +
			"\n\n## Tasks\n" + truncate(tasksMD, 4000)
		if out, err := o.runRoleTracked(ctx, plan.RoleReviewer, "", prompt); err == nil && strings.TrimSpace(out) != "" {
			lj := plan.ParseScopeJudgeJSON(out)
			_ = o.store.Append(contextstore.DocScratch, "Scope judge", out)
			o.emitFull("split", stream.KindOutput, "scope-judge", "",
				fmt.Sprintf("scope judge ok=%v issues=%d", lj.OK, len(lj.Issues)),
				"", truncate(out, 800))
			if !lj.OK {
				judge.OK = false
				judge.Issues = appendUniqueIssues(judge.Issues, lj.Issues)
				judge.WeakIDs = appendUniqueIssues(judge.WeakIDs, lj.WeakIDs)
				judge.Hints = appendUniqueIssues(judge.Hints, lj.Hints)
			} else if len(judge.Issues) == 0 {
				judge.OK = true
			}
		}
	}

	if judge.OK {
		o.emit("split", "scope judge green — tasks PRD-complete", "")
		return &judge
	}

	o.emitFull("split", stream.KindOutput, "scope-judge", "",
		fmt.Sprintf("scope gaps (%d) — enriching weak tasks", len(judge.Issues)),
		"", strings.Join(judge.Issues, "\n"))

	// Deterministic enrich again after judge, then LLM rewrite for weak tasks.
	board.Tasks = plan.EnsureTaskPRDs(board.Tasks, prd, query)
	if o.executor != nil && len(judge.WeakIDs) > 0 && o.cfg.ThinkPasses >= 1 {
		o.rewriteWeakTaskScopes(ctx, query, board, prd, judge)
	}
	// Re-check; remaining weak tasks stay but with enriched acceptance.
	board.Tasks = plan.EnsureTaskPRDs(board.Tasks, prd, query)
	final := plan.JudgeTaskScopeHeuristics(board.Tasks, prd)
	if final.OK {
		o.emit("split", "scope judge green after enrich", "")
	} else {
		o.emit("split", fmt.Sprintf("scope still soft-gaps (%d) — proceeding with enriched PRD", len(final.Issues)), "")
		if o.store != nil {
			_ = o.store.Append(contextstore.DocScratch, "Scope gaps", strings.Join(final.Issues, "\n"))
		}
	}
	return &final
}

func (o *Orchestrator) rewriteWeakTaskScopes(ctx context.Context, query string, board *plan.Board, prd plan.ScopePRD, judge plan.ScopeJudgeResult) {
	weakSet := map[string]bool{}
	for _, id := range judge.WeakIDs {
		weakSet[id] = true
	}
	var weak []plan.Task
	for _, t := range board.Tasks {
		if weakSet[t.ID] {
			weak = append(weak, t)
		}
	}
	if len(weak) == 0 {
		return
	}
	blob, _ := json.Marshal(weak)
	prompt := agents.PromptTaskSplitter +
		"\n\n## Goal\nRewrite ONLY these weak tasks so each has concrete title, description, files, acceptance from the Locked PRD.\n" +
		"Keep the same ids and roles. Return STRICT JSON {\"tasks\":[…]} with ONLY the rewritten tasks.\n\n" +
		"## Locked PRD\n" + truncate(plan.FormatPRDMarkdown(prd, nil), 2000) +
		"\n\n## Query\n" + truncate(query, 800) +
		"\n\n## Issues\n- " + strings.Join(judge.Issues, "\n- ") +
		"\n\n## Weak tasks\n" + string(blob)
	o.emitAgent("split", "splitter", "", "rewrite weak task PRDs", "", "")
	out, err := o.runRoleTracked(ctx, "splitter", "", prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	rewritten, err := plan.ParseTasksJSON(out)
	if err != nil || len(rewritten) == 0 {
		return
	}
	byID := map[string]plan.Task{}
	for _, t := range rewritten {
		byID[t.ID] = t
	}
	for i := range board.Tasks {
		if rt, ok := byID[board.Tasks[i].ID]; ok {
			// Preserve column/deps; take scoped fields.
			board.Tasks[i].Title = firstNonEmpty(rt.Title, board.Tasks[i].Title)
			board.Tasks[i].Description = firstNonEmpty(rt.Description, board.Tasks[i].Description)
			board.Tasks[i].Acceptance = firstNonEmpty(rt.Acceptance, board.Tasks[i].Acceptance)
			if len(rt.Files) > 0 {
				board.Tasks[i].Files = rt.Files
			}
			if len(rt.Checklist) > 0 {
				board.Tasks[i].Checklist = rt.Checklist
			}
		}
	}
	o.emit("split", fmt.Sprintf("rewrote %d weak task scopes", len(rewritten)), "")
}

func appendUniqueIssues(list, add []string) []string {
	seen := map[string]bool{}
	for _, x := range list {
		seen[strings.ToLower(strings.TrimSpace(x))] = true
	}
	for _, x := range add {
		x = strings.TrimSpace(x)
		if x == "" || seen[strings.ToLower(x)] {
			continue
		}
		list = append(list, x)
		seen[strings.ToLower(x)] = true
	}
	return list
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
