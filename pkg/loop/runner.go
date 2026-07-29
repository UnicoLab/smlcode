package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
)

// Logger is an optional progress sink.
type Logger func(format string, args ...interface{})

// AfterWave is called after each execute/review wave with the tasks that were in that wave
// (post-update). Used for dynamic context + learning.
type AfterWave func(ctx context.Context, board *plan.Board, wave []plan.Task)

// AgentEvent reports live agent activity for CLI/GUI streaming.
type AgentEvent func(kind, agent, taskID, message, scope, output string)

// Runner executes parallel workers → review → correct against a live board.
type Runner struct {
	Executor    *ggagent.SubAgentExecutor
	Shared      *ggagent.SharedState
	Store       *plan.LiveStore // optional — reload mid-run for human edits
	Root        string         // workspace root for evidence checks
	AfterWave   AfterWave
	OnEvent     AgentEvent
	MaxRetries  int
	MaxParallel int
	Timeout     time.Duration
	IdleWait    time.Duration // wait for human to promote to_scope → ready
	Log         Logger
}

func NewRunner(exec *ggagent.SubAgentExecutor, shared *ggagent.SharedState) *Runner {
	return &Runner{
		Executor:    exec,
		Shared:      shared,
		MaxRetries:  2,
		MaxParallel: 3,
		Timeout:     5 * time.Minute,
		IdleWait:    2 * time.Second,
		Log:         func(string, ...interface{}) {},
	}
}

// RunBoard processes executable tasks; reloads LiveStore each wave so humans
// can add / move / edit tasks while agents work.
func (r *Runner) RunBoard(ctx context.Context, board *plan.Board) error {
	guard := 0
	idleRounds := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		guard++
		if guard > 200 {
			return fmt.Errorf("task loop exceeded safety guard")
		}

		// Pick up CLI/UI edits
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			snap := r.Store.Snapshot()
			*board = snap
		}

		ready := board.ReadyTasks()
		if len(ready) == 0 {
			if board.AgentWorkRemaining() {
				// in_progress / in_review without ready — wait briefly
				idleRounds++
				if idleRounds > 30 {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(r.IdleWait):
				}
				continue
			}
			if board.HumanBacklogRemaining() {
				r.Log("waiting: human backlog (to_scope/scoped) — promote tasks to ready_to_dev")
				idleRounds++
				// Don't fail the run; exit agent loop so human can continue in Studio
				if idleRounds > 3 {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(r.IdleWait):
				}
				continue
			}
			return nil
		}
		idleRounds = 0

		wave := ready
		if len(wave) > r.MaxParallel {
			wave = wave[:r.MaxParallel]
		}
		r.Log("wave: %d ready task(s)", len(wave))
		ids := make([]string, len(wave))
		for i, t := range wave {
			ids[i] = t.ID
		}
		if err := r.runWave(ctx, board, wave); err != nil {
			return err
		}
		r.persist(board)
		if r.AfterWave != nil {
			var finished []plan.Task
			for _, id := range ids {
				if t, ok := board.Get(id); ok {
					finished = append(finished, t)
				}
			}
			r.AfterWave(ctx, board, finished)
			r.persist(board)
		}
	}
}

func (r *Runner) persist(board *plan.Board) {
	if r.Store != nil {
		_ = r.Store.Replace(*board)
	}
}

func (r *Runner) runWave(ctx context.Context, board *plan.Board, wave []plan.Task) error {
	for _, t := range wave {
		t.MoveTo(plan.ColInProgress)
		board.UpdateTask(t)
	}
	r.persist(board)

	reqs := make([]ggagent.SubAgentRequest, 0, len(wave))
	roles := make([]string, 0, len(wave))
	for _, t := range wave {
		role := normalizeExecRole(t.Role)
		roles = append(roles, role)
		scope := strings.Join(t.Files, ", ")
		r.fire("agent_start", role, t.ID, t.Title, scope, "")
		reqs = append(reqs, ggagent.SubAgentRequest{
			AgentID:    role,
			Input:      formatWorkerPrompt(t),
			Timeout:    r.Timeout,
			ShareState: true,
		})
	}

	results, err := r.Executor.ExecuteSubAgents(ctx, reqs, r.Shared)
	if err != nil {
		r.Log("wave execution warning: %v", err)
	}

	for i, res := range results {
		t := wave[i]
		role := roles[i]
		if res.Error != nil {
			t.MoveTo(plan.ColBlocked)
			t.Error = res.Error.Error()
			board.UpdateTask(t)
			r.fire("agent_end", role, t.ID, "error", strings.Join(t.Files, ", "), t.Error)
			continue
		}
		t.Output = outputString(res)
		// SLMs sometimes emit a bare tool call as "final" — nudge one corrective pass.
		if looksLikeToolJunk(t.Output) {
			r.Log("%s produced tool-junk finalize; running corrector once", t.ID)
			r.fire("agent_start", plan.RoleCorrector, t.ID, "fix incomplete finalize", strings.Join(t.Files, ", "), "")
			corr, _ := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
				AgentID: plan.RoleCorrector,
				Input: formatCorrectPrompt(t, plan.ReviewResult{
					Approved: false,
					Issues:   []string{"Finish the task and return status JSON. Do not end on a tool call."},
					Summary:  "incomplete finalize",
				}),
				Timeout: r.Timeout, ShareState: true,
			}}, r.Shared)
			if len(corr) > 0 {
				if out := outputString(corr[0]); strings.TrimSpace(out) != "" {
					t.Output = out
				}
			}
			r.fire("agent_end", plan.RoleCorrector, t.ID, "corrector finished", "", truncate(t.Output, 800))
		}
		r.fire("agent_end", role, t.ID, "worker finished", strings.Join(t.Files, ", "), truncate(t.Output, 1200))
		t.MoveTo(plan.ColInReview)
		board.UpdateTask(t)
		if err := r.reviewAndCorrect(ctx, board, t); err != nil {
			r.Log("review/correct %s: %v", t.ID, err)
		}
	}
	r.persist(board)
	return nil
}

func normalizeExecRole(role string) string {
	if role == "" || role == "implementer" {
		return plan.RoleWorker
	}
	switch role {
	case plan.RolePlanner, "splitter", "orchestrator", "memory", "coordinator", "architect", "context":
		return plan.RoleWorker
	default:
		return role
	}
}

func (r *Runner) fire(kind, agent, taskID, msg, scope, output string) {
	if r.OnEvent != nil {
		r.OnEvent(kind, agent, taskID, msg, scope, output)
	}
}

func (r *Runner) reviewAndCorrect(ctx context.Context, board *plan.Board, t plan.Task) error {
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			if latest, ok := r.Store.GetTask(t.ID); ok {
				// Human may have moved/edited the task
				if latest.Column == plan.ColDone || latest.Column == plan.ColBlocked {
					board.UpdateTask(latest)
					return nil
				}
				if latest.Column == plan.ColToScope || latest.Column == plan.ColScoped {
					board.UpdateTask(latest)
					return nil
				}
				t = latest
			}
		}

		current, ok := board.Get(t.ID)
		if !ok {
			return fmt.Errorf("missing task %s", t.ID)
		}

		r.fire("agent_start", plan.RoleReviewer, current.ID, "self-critic review", strings.Join(current.Files, ", "), "")
		results, err := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
			AgentID: plan.RoleReviewer, Input: formatReviewPrompt(current),
			Timeout: r.Timeout, ShareState: true,
		}}, r.Shared)
		if err != nil && len(results) == 0 {
			current.MoveTo(plan.ColBlocked)
			current.Error = err.Error()
			board.UpdateTask(current)
			r.persist(board)
			return err
		}

		reviewRaw := ""
		if len(results) > 0 {
			reviewRaw = outputString(results[0])
			if results[0].Error != nil && reviewRaw == "" {
				reviewRaw = results[0].Error.Error()
			}
		}
		review := plan.ParseReviewJSON(reviewRaw)
		// SLM fallback: reviewers are often overly skeptical; trust clear worker completion.
		if !review.Approved && plan.WorkerLooksComplete(current.Output) {
			if looksLikeBrokenReview(reviewRaw) || workerReportedDone(current.Output) {
				review.Approved = true
				review.Score = 80
				review.Summary = "auto-approved: worker reported completion"
				review.Issues = nil
			}
		}
		// Evidence gate: never mark done when targets are missing or no write occurred.
		if review.Approved {
			if ok, why := r.evidenceOK(current); !ok {
				review.Approved = false
				review.Score = 0
				review.Summary = "rejected by evidence gate: " + why
				review.Issues = append([]string{why}, review.Issues...)
				// Rewrite task files to real workspace paths for the corrector
				if r.Root != "" {
					real := plan.ReconcileFiles(r.Root, current.Files, plan.DiscoverRelevantFiles(r.Root, current.Title+" "+current.Description, current.Output))
					if len(real) > 0 {
						current.Files = real
					}
				}
				r.Log("%s evidence gate blocked approval: %s", current.ID, why)
			}
		}
		current.Review = review.Summary
		if len(review.Issues) > 0 {
			current.Review += "\nIssues:\n- " + strings.Join(review.Issues, "\n- ")
		}

		r.fire("agent_end", plan.RoleReviewer, current.ID,
			fmt.Sprintf("review approved=%v score=%d", review.Approved, review.Score),
			"", truncate(reviewRaw, 600))

		if review.Approved {
			current.MoveTo(plan.ColDone)
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s approved → done (score=%d)", current.ID, review.Score)
			return nil
		}

		if attempt == r.MaxRetries {
			current.MoveTo(plan.ColBlocked)
			current.Error = "review rejected after max retries"
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s blocked after %d retries", current.ID, r.MaxRetries)
			return nil
		}

		current.MoveTo(plan.ColInProgress)
		current.Retries = attempt + 1
		board.UpdateTask(current)
		r.persist(board)
		r.Log("%s correcting (attempt %d)", current.ID, attempt+1)

		corrResults, corrErr := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
			AgentID: plan.RoleCorrector, Input: formatCorrectPrompt(current, review),
			Timeout: r.Timeout, ShareState: true,
		}}, r.Shared)
		if corrErr != nil && len(corrResults) == 0 {
			current.MoveTo(plan.ColBlocked)
			current.Error = corrErr.Error()
			board.UpdateTask(current)
			r.persist(board)
			return corrErr
		}
		if len(corrResults) > 0 {
			current.Output = outputString(corrResults[0])
			if corrResults[0].Error != nil && current.Output == "" {
				current.Error = corrResults[0].Error.Error()
			}
		}
		current.MoveTo(plan.ColInReview)
		board.UpdateTask(current)
		r.persist(board)
		t = current
	}
	return nil
}

func formatWorkerPrompt(t plan.Task) string {
	var b strings.Builder
	b.WriteString("Atomic task — complete only this:\n\n")
	b.WriteString(fmt.Sprintf("ID: %s\nTitle: %s\nColumn: %s\nRole: %s\n\n", t.ID, t.Title, t.Column, t.Role))
	b.WriteString(t.Description)
	b.WriteString("\n")
	if len(t.Files) > 0 {
		b.WriteString("\nFocus files:\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
		b.WriteString("\n")
	}
	if t.Acceptance != "" {
		b.WriteString("\nAcceptance criteria:\n")
		b.WriteString(t.Acceptance)
		b.WriteString("\n")
	}
	if len(t.Checklist) > 0 {
		b.WriteString("\nChecklist:\n")
		for _, c := range t.Checklist {
			mark := "[ ]"
			if c.Done {
				mark = "[x]"
			}
			b.WriteString(fmt.Sprintf("- %s %s\n", mark, c.Text))
		}
	}
	if t.Notes != "" {
		b.WriteString("\nHuman notes:\n")
		b.WriteString(t.Notes)
		b.WriteString("\n")
	}
	return b.String()
}

func formatReviewPrompt(t plan.Task) string {
	return fmt.Sprintf(`Review task %s (%s) role=@%s. Reply with JSON only — no tools.

## Acceptance
%s

## Agent output
%s

Rules:
- @worker: approve if JSON status is done OR dry-run would edit/write succeeded.
- @explorer: approve if a real file path was found (e.g. *.go).
- @tester: approve if verification looks reasonable.
- Reject only if work is clearly missing or out of scope.
`, t.ID, t.Title, t.Role, t.Acceptance, truncate(t.Output, 3500))
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func formatCorrectPrompt(t plan.Task, review plan.ReviewResult) string {
	return fmt.Sprintf(`Fix task %s after failed review.

## Original task
%s

## Previous output
%s

## Review issues
- %s

## Review summary
%s
`, t.ID, t.Description, t.Output, strings.Join(review.Issues, "\n- "), review.Summary)
}

func outputString(res ggagent.SubAgentResult) string {
	if res.Output == nil {
		return ""
	}
	if s, ok := res.Output.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", res.Output)
}

func looksLikeToolJunk(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "<function") || strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<tool_call>")
}

func looksLikeBrokenReview(raw string) bool {
	lower := strings.ToLower(raw)
	if looksLikeToolJunk(raw) {
		return true
	}
	if strings.Contains(lower, `"approved"`) {
		return false
	}
	// No JSON review shape at all
	return !strings.Contains(raw, "{") || strings.TrimSpace(raw) == ""
}

func workerReportedDone(output string) bool {
	lower := strings.ToLower(output)
	hasDone := strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`)
	hasFiles := strings.Contains(lower, `"files_changed"`) || strings.Contains(lower, "dry-run: would")
	return hasDone && hasFiles
}

func (r *Runner) evidenceOK(t plan.Task) (bool, string) {
	if r.Root == "" {
		return true, ""
	}
	if len(t.Files) > 0 {
		missing := 0
		for _, f := range t.Files {
			if !plan.FileExists(r.Root, f) {
				missing++
			}
		}
		if missing == len(t.Files) {
			return false, "all task target files are missing on disk (hallucinated paths)"
		}
	}
	if looksLikeEditTask(t) && !hasWriteEvidence(t.Output) {
		// Allow approve only if target files already satisfy a trivial doc-comment ask
		if alreadySatisfied(r.Root, t) {
			return true, ""
		}
		return false, "edit task has no ws_write/ws_edit evidence in worker output"
	}
	return true, ""
}

func looksLikeEditTask(t plan.Task) bool {
	blob := strings.ToLower(t.Title + " " + t.Description + " " + t.Acceptance)
	for _, k := range []string{"add ", "edit", "fix", "implement", "write", "update", "doc comment", "comment", "refactor", "rename"} {
		if strings.Contains(blob, k) {
			return true
		}
	}
	return false
}

func hasWriteEvidence(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "ws_write") || strings.Contains(lower, "ws_edit") ||
		strings.Contains(lower, "wrote ") || strings.Contains(lower, "updated ") ||
		strings.Contains(lower, "dry-run: would write") || strings.Contains(lower, "dry-run: would edit") {
		return true
	}
	return strings.Contains(lower, `"files_changed":[`) && !strings.Contains(lower, `"files_changed":[]`)
}

func alreadySatisfied(root string, t plan.Task) bool {
	blob := strings.ToLower(t.Title + " " + t.Description + " " + t.Acceptance)
	if !(strings.Contains(blob, "doc comment") || strings.Contains(blob, "comment")) {
		return false
	}
	for _, f := range t.Files {
		if !plan.FileExists(root, f) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		body := string(data)
		if strings.Contains(body, "//") && (strings.Contains(strings.ToLower(body), "func ") || strings.Contains(body, "def ")) {
			// crude: file already has a comment near code
			if strings.Contains(body, "\n//") || strings.HasPrefix(strings.TrimSpace(body), "//") {
				return true
			}
		}
	}
	return false
}
