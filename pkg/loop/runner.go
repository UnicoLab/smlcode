package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// Logger is an optional progress sink.
type Logger func(format string, args ...interface{})

// AfterWave is called after each execute/review wave with the tasks that were in that wave
// (post-update). Used for dynamic context + learning.
type AfterWave func(ctx context.Context, board *plan.Board, wave []plan.Task)

// AgentEvent reports live agent activity for CLI/GUI streaming.
type AgentEvent func(kind, agent, taskID, message, scope, output string)

// BuildInput optionally builds the worker/corrector prompt for a task.
// When set, packs stay ephemeral and are not persisted into task.Description.
type BuildInput func(t plan.Task) string

// Runner executes parallel workers → review → correct against a live board.
type Runner struct {
	Executor       *ggagent.SubAgentExecutor
	Shared         *ggagent.SharedState
	Store          *plan.LiveStore // optional — reload mid-run for human edits
	Root           string          // workspace root for evidence checks
	Focus          *workspace.FocusGuard
	AfterWave      AfterWave
	OnEvent        AgentEvent
	BuildInput     BuildInput
	MaxRetries     int
	MaxParallel    int
	Timeout        time.Duration
	IdleWait       time.Duration // wait for human to promote to_scope → ready
	Log            Logger
	FailureHandler *EnhancedFailureHandler
}

func NewRunner(exec *ggagent.SubAgentExecutor, shared *ggagent.SharedState) *Runner {
	return &Runner{
		Executor:    exec,
		Shared:      shared,
		MaxRetries:  4,
		MaxParallel: 2,
		Timeout:     12 * time.Minute,
		IdleWait:    2 * time.Second,
		Log:         func(string, ...interface{}) {},
	}
}

func (r *Runner) WithFailureHandler(fh *EnhancedFailureHandler) *Runner {
	r.FailureHandler = fh
	return r
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

		ready := scheduleReady(board.ReadyTasks())
		if len(ready) == 0 {
			if board.AgentWorkRemaining() {
				idleRounds++
				if idleRounds > 30 {
					return nil
				}
				wait := r.IdleWait
				if wait > 500*time.Millisecond {
					wait = 500 * time.Millisecond // less idle spin while in-progress
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			if board.HumanBacklogRemaining() {
				r.Log("waiting: human backlog (to_scope/scoped) — promote tasks to ready_to_dev")
				idleRounds++
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

func (r *Runner) taskInput(t plan.Task) string {
	if r.BuildInput != nil {
		return r.BuildInput(t)
	}
	return formatWorkerPrompt(t)
}

func (r *Runner) runWave(ctx context.Context, board *plan.Board, wave []plan.Task) error {
	for _, t := range wave {
		t.MoveTo(plan.ColInProgress)
		board.UpdateTask(t)
	}
	r.persist(board)

	// Constrain writes to the union of this wave's focus files (+ paths from task text).
	if r.Focus != nil {
		lists := make([][]string, 0, len(wave))
		for _, t := range wave {
			lists = append(lists, expandTaskFocus(t))
		}
		r.Focus.SetWave(lists)
		defer r.Focus.Clear()
	}

	reqs := make([]ggagent.SubAgentRequest, 0, len(wave))
	roles := make([]string, 0, len(wave))
	snapshots := make([]map[string]string, len(wave))
	for i, t := range wave {
		role := normalizeExecRole(t.Role)
		roles = append(roles, role)
		scope := strings.Join(t.Files, ", ")
		r.fire("agent_start", role, t.ID, t.Title, scope, "")
		snapshots[i] = r.snapshotTargets(t)
		reqs = append(reqs, ggagent.SubAgentRequest{
			AgentID:    role,
			Input:      r.taskInput(t),
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
			if r.FailureHandler != nil {
				_ = r.FailureHandler.ReportTaskFailure(board, t, res.Error, 0)
			}
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
					Issues:   []string{"Finish the task and return status JSON. Do not end on a tool call. Use ws_edit/ws_write first."},
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
		mergeFilesChanged(&t)
		// Attach disk evidence hint for reviewer.
		if hint := r.diskEvidenceHint(t, snapshots[i]); hint != "" {
			t.Output = strings.TrimSpace(t.Output) + "\n\n## Disk evidence\n" + hint
		}
		r.fire("agent_end", role, t.ID, "worker finished", strings.Join(t.Files, ", "), truncate(t.Output, 1200))
		t.MoveTo(plan.ColInReview)
		board.UpdateTask(t)
		// Use the pre-wave snapshot so disk evidence survives finalize JSON that
		// omits tool traces (reviewAndCorrect must not re-baseline after writes).
		if err := r.reviewAndCorrect(ctx, board, t, snapshots[i]); err != nil {
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

func (r *Runner) reviewAndCorrect(ctx context.Context, board *plan.Board, t plan.Task, baseline map[string]string) error {
	if baseline == nil {
		baseline = r.snapshotTargets(t)
	}
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if r.Store != nil {
			_ = r.Store.MergeFromDisk()
			if latest, ok := r.Store.GetTask(t.ID); ok {
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

		// Fast path: skip reviewer LLM when disk/acceptance evidence is already clear.
		// Prevents SLM hangs after a successful write (live multi-turn lesson).
		reviewRaw := ""
		review := plan.ReviewResult{}
		satisfied := alreadySatisfied(r.Root, current)
		diskWrite := r.hasRealWriteEvidence(current, baseline)
		toolWrite := hasToolWriteEvidence(current.Output)
		diskSection := hasDiskEvidenceSection(current.Output)
		done := plan.WorkerLooksComplete(current.Output) || workerReportedDone(current.Output)
		hasEvidence := diskWrite || toolWrite || diskSection
		fastPath := (satisfied || diskWrite || diskSection) && r.scopeOK(current) == ""
		if fastPath {
			review.Approved = true
			review.Score = 85
			if satisfied && !hasEvidence {
				review.Summary = "auto-approved: acceptance already satisfied on disk"
			} else {
				review.Summary = "auto-approved: disk write evidence on focus files"
			}
			r.Log("%s review fast-path (skip reviewer LLM): %s", current.ID, review.Summary)
		} else {
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
				if r.FailureHandler != nil {
					_ = r.FailureHandler.ReportTaskFailure(board, current, err, attempt)
				}
				return err
			}
			if len(results) > 0 {
				reviewRaw = outputString(results[0])
				if results[0].Error != nil && reviewRaw == "" {
					reviewRaw = results[0].Error.Error()
				}
			}
			review = plan.ParseReviewJSON(reviewRaw)
			// SLM fallback: trust clear worker completion + tool/disk evidence.
			if !review.Approved {
				if satisfied || diskWrite || diskSection || (done && (looksLikeBrokenReview(reviewRaw) || hasEvidence)) {
					review.Approved = true
					review.Score = 80
					switch {
					case satisfied && !hasEvidence:
						review.Summary = "auto-approved: acceptance already satisfied on disk"
					case diskSection || diskWrite:
						review.Summary = "auto-approved: disk write evidence on focus files"
					default:
						review.Summary = "auto-approved: worker completion + write evidence"
					}
					review.Issues = nil
				}
			}
		}
		// Tester gate: never accept "does not work" / passed:false / empty finalize as done.
		if review.Approved && strings.EqualFold(current.Role, plan.RoleTester) {
			tr := plan.ParseTesterJSON(current.Output)
			if !tr.Passed {
				review.Approved = false
				review.Score = 0
				why := "tester reported failure"
				if len(tr.Failures) > 0 {
					why = tr.Failures[0]
				} else if tr.Summary != "" {
					why = tr.Summary
				}
				review.Summary = "rejected by tester gate: " + why
				review.Issues = append([]string{why}, review.Issues...)
				r.Log("%s tester gate blocked approval: %s", current.ID, why)
			}
		}
		// Evidence gate: never mark done when targets are missing or no write occurred.
		if review.Approved {
			if ok, why := r.evidenceOK(current, baseline); !ok {
				review.Approved = false
				review.Score = 0
				review.Summary = "rejected by evidence gate: " + why
				review.Issues = append([]string{why}, review.Issues...)
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

		endOut := truncate(reviewRaw, 600)
		if endOut == "" {
			endOut = review.Summary
		}
		r.fire("agent_end", plan.RoleReviewer, current.ID,
			fmt.Sprintf("review approved=%v score=%d", review.Approved, review.Score),
			"", endOut)

		if review.Approved {
			mergeFilesChanged(&current)
			current.MoveTo(plan.ColDone)
			current.Error = ""
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s approved → done (score=%d)", current.ID, review.Score)
			return nil
		}

		if attempt == r.MaxRetries {
			// Escalate to human backlog instead of hard-blocking forever.
			current.MoveTo(plan.ColToScope)
			current.Error = "review rejected after max retries — needs human input or smaller scope"
			current.Notes = strings.TrimSpace(current.Notes + "\n" +
				"ESCALATED: review rejected after max retries. " +
				"Fix acceptance/context, then promote back to ready_to_dev.\n" +
				current.Review)
			board.UpdateTask(current)
			r.persist(board)
			r.Log("%s escalated to to_scope after %d retries", current.ID, r.MaxRetries)

			if r.FailureHandler != nil {
				failErr := fmt.Errorf("max retries exceeded: review rejected")
				_ = r.FailureHandler.ReportTaskFailure(board, current, failErr, attempt)
				_ = r.FailureHandler.AddWaveLesson(board, current, failErr)
			}
			return nil
		}

		current.MoveTo(plan.ColInProgress)
		current.Retries = attempt + 1
		board.UpdateTask(current)
		r.persist(board)
		r.Log("%s correcting (attempt %d)", current.ID, attempt+1)
		r.fire("agent_start", plan.RoleCorrector, current.ID, "correction pass", strings.Join(current.Files, ", "), "")

		corrResults, corrErr := r.Executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
			AgentID: plan.RoleCorrector, Input: formatCorrectPrompt(current, review),
			Timeout: r.Timeout, ShareState: true,
		}}, r.Shared)
		if corrErr != nil && len(corrResults) == 0 {
			current.MoveTo(plan.ColBlocked)
			current.Error = corrErr.Error()
			board.UpdateTask(current)
			r.persist(board)
			if r.FailureHandler != nil {
				_ = r.FailureHandler.ReportTaskFailure(board, current, corrErr, attempt)
			}
			return corrErr
		}
		if len(corrResults) > 0 {
			current.Output = outputString(corrResults[0])
			if corrResults[0].Error != nil && current.Output == "" {
				current.Error = corrResults[0].Error.Error()
			}
		}
		mergeFilesChanged(&current)
		if hint := r.diskEvidenceHint(current, baseline); hint != "" {
			current.Output = strings.TrimSpace(current.Output) + "\n\n## Disk evidence\n" + hint
		}
		r.fire("agent_end", plan.RoleCorrector, current.ID, "corrector finished", "", truncate(current.Output, 800))
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
	b.WriteString(StripScopedPack(t.Description))
	b.WriteString("\n")
	if len(t.Files) > 0 {
		b.WriteString("\n## Focus files (HARD SCOPE)\nOnly edit these paths or files in the same package directory:\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
		b.WriteString("\nDo NOT create main.go / index.js / other entrypoints unless listed above.\n")
		b.WriteString("Do NOT add extra helper files or unrelated functions — only what acceptance requires.\n")
		b.WriteString("If ws_patch fails, re-read the file and retry a minimal SEARCH/REPLACE; never invent new root files.\n")
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
	b.WriteString(`
## Required finish
1. Use ws_read / ws_edit / ws_patch / ws_write on focus files only.
2. Prefer small patches; never invent unrelated new files.
3. End with STRICT JSON only:
{"status":"done","summary":"...","files_changed":["real/path.go"],"notes":"..."}
Never claim done without tool edits. Never end on a tool call.
`)
	return b.String()
}

// StripScopedPack removes ephemeral pack headers so TASKS.md stays lean.
func StripScopedPack(desc string) string {
	desc = strings.TrimSpace(desc)
	if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
		desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
	}
	if strings.HasPrefix(desc, "# Scoped context") {
		if idx := strings.Index(desc, "\n# "); idx > 0 {
			// keep looking for task body markers
		}
		if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
			desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
		}
	}
	return strings.TrimSpace(desc)
}

func formatReviewPrompt(t plan.Task) string {
	return fmt.Sprintf(`Review task %s (%s) role=@%s. Reply with JSON only — no tools.

## Acceptance
%s

## Agent output
%s

Rules:
- Approve if worker JSON status is done AND there is real write evidence (ws_write/ws_edit/ws_patch tool result OR Disk evidence section showing changed files).
- Reject if output is only claims/JSON with no tool or disk evidence for edit tasks.
- Reject if files_changed includes paths outside focus scope (especially unwanted main.go).
- @explorer: approve if a real file path was found.
- @tester: approve ONLY when output JSON has "passed":true (or clear command success). Reject if passed:false, failures listed, or "does not work".
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

Use ws_edit/ws_write on real files, then finish with STRICT JSON:
{"status":"done","summary":"...","files_changed":["..."],"notes":"..."}
`, t.ID, StripScopedPack(t.Description), truncate(t.Output, 2500), strings.Join(review.Issues, "\n- "), review.Summary)
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
	return strings.Contains(lower, "<function") || strings.Contains(lower, "<tool_call>") ||
		strings.Contains(lower, "</tool_call>")
}

func looksLikeBrokenReview(raw string) bool {
	lower := strings.ToLower(raw)
	if looksLikeToolJunk(raw) {
		return true
	}
	if strings.Contains(lower, `"approved"`) {
		return false
	}
	return !strings.Contains(raw, "{") || strings.TrimSpace(raw) == ""
}

func workerReportedDone(output string) bool {
	lower := strings.ToLower(output)
	hasDone := strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`)
	hasFiles := strings.Contains(lower, `"files_changed"`) || strings.Contains(lower, "dry-run: would")
	return hasDone && hasFiles
}

func mergeFilesChanged(t *plan.Task) {
	files := parseFilesChanged(t.Output)
	if len(files) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, f := range t.Files {
		seen[f] = true
	}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		t.Files = append(t.Files, f)
	}
}

func parseFilesChanged(output string) []string {
	raw := extractJSONObject(output)
	if raw == "" {
		return nil
	}
	var payload struct {
		FilesChanged []string `json:"files_changed"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload.FilesChanged
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return ""
}

func (r *Runner) evidenceOK(t plan.Task, baseline map[string]string) (bool, string) {
	if r.Root == "" {
		return true, ""
	}
	if why := r.scopeOK(t); why != "" {
		return false, why
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
	// Create/scaffold (or doc-comment) already on disk → accept even without a
	// fresh baseline delta (common when a prior attempt or sibling wrote the file).
	if alreadySatisfied(r.Root, t) {
		return true, ""
	}
	if looksLikeEditTask(t) {
		if r.hasRealWriteEvidence(t, baseline) {
			return true, ""
		}
		if hasToolWriteEvidence(t.Output) {
			return true, ""
		}
		return false, "edit task has no real write evidence (tool result or disk/git change)"
	}
	return true, ""
}

// scopeOK rejects wander: claimed or newly created files outside task focus.
func (r *Runner) scopeOK(t plan.Task) string {
	claimed := parseFilesChanged(t.Output)
	// Build a task-local guard from expanded focus (includes scaffold paths).
	g := workspace.NewFocusGuard()
	focus := expandTaskFocus(t)
	if len(focus) > 0 {
		g.SetWave([][]string{focus})
	}
	if bad := g.OutOfScopeFiles(claimed); len(bad) > 0 {
		return "out-of-scope files_changed: " + strings.Join(bad, ", ")
	}
	// Detect newly created entrypoints on disk that are not in focus.
	if r.Root == "" || len(t.Files) == 0 {
		return ""
	}
	for _, name := range []string{"main.go", "main.py", "index.js", "index.ts", "app.js", "app.ts"} {
		p := filepath.Join(r.Root, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		rel := name
		if g.Allow(rel) {
			continue
		}
		// Only flag if created during this task (not in baseline focus fingerprints)
		// or simply present while not allowed — prefer git/status dirty.
		dirty := false
		for _, c := range r.gitChangedFiles() {
			if c == rel || strings.HasSuffix(c, "/"+rel) {
				dirty = true
				break
			}
		}
		// Also treat as wander if worker claimed it.
		for _, c := range claimed {
			if filepath.Base(c) == name && !g.Allow(c) {
				dirty = true
				break
			}
		}
		if dirty {
			return "out-of-scope entrypoint created/modified: " + rel
		}
	}
	return ""
}

// expandTaskFocus merges declared files with path-like mentions in the task text
// so greenfield scaffolding (src/pkg/…) is not blocked by a single-manifest focus.
func expandTaskFocus(t plan.Task) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, f := range t.Files {
		add(f)
	}
	blob := t.Title + "\n" + StripScopedPack(t.Description) + "\n" + t.Acceptance
	for _, f := range plan.ExtractFilePaths(blob) {
		add(f)
	}
	// Common greenfield directories when the task is clearly creating a project.
	lower := strings.ToLower(blob)
	if strings.Contains(lower, "create") || strings.Contains(lower, "scaffold") ||
		strings.Contains(lower, "pyproject") || strings.Contains(lower, "langgraph") ||
		strings.Contains(lower, "new project") || strings.Contains(lower, "mvp") {
		add("src/")
		add("tests/")
		add("README.md")
		add("pyproject.toml")
		add("main.py")
	}
	return out
}

// scheduleReady orders ready tasks for better parallel utilization:
// focused (has files) + fewer deps-looking + shorter titles first.
func scheduleReady(ready []plan.Task) []plan.Task {
	if len(ready) < 2 {
		return ready
	}
	out := append([]plan.Task(nil), ready...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if taskPriority(out[j]) < taskPriority(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func taskPriority(t plan.Task) int {
	p := 100
	if len(t.Files) > 0 {
		p -= 40
	}
	p += len(t.DependsOn) * 5
	p += len(t.Title) / 20
	switch strings.ToLower(t.Role) {
	case "explorer", "docs":
		p -= 10 // discover first when parallel slots free
	case "tester":
		p += 20 // tests after implementers when possible
	}
	return p
}

func looksLikeEditTask(t plan.Task) bool {
	blob := strings.ToLower(t.Title + " " + StripScopedPack(t.Description) + " " + t.Acceptance)
	for _, k := range []string{"add ", "edit", "fix", "implement", "write", "update", "doc comment", "comment", "refactor", "rename", "create ", "improve", "revamp", "redesign"} {
		if strings.Contains(blob, k) {
			return true
		}
	}
	return false
}

func hasToolWriteEvidence(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "ws_write") || strings.Contains(lower, "ws_edit") ||
		strings.Contains(lower, "ws_patch") ||
		strings.Contains(lower, "wrote ") || strings.Contains(lower, "edited ") ||
		strings.Contains(lower, "updated ") || strings.Contains(lower, "patched ") ||
		strings.Contains(lower, "dry-run: would write") || strings.Contains(lower, "dry-run: would edit") ||
		strings.Contains(lower, "staged write") || strings.Contains(lower, "pending/") ||
		strings.Contains(lower, "## disk evidence")
}

func hasWriteEvidence(output string) bool {
	// Kept for compatibility; prefer hasToolWriteEvidence + disk checks.
	return hasToolWriteEvidence(output) ||
		(strings.Contains(strings.ToLower(output), `"files_changed":[`) &&
			!strings.Contains(strings.ToLower(output), `"files_changed":[]`))
}

func (r *Runner) hasRealWriteEvidence(t plan.Task, baseline map[string]string) bool {
	if hasToolWriteEvidence(t.Output) {
		return true
	}
	if hasDiskEvidenceSection(t.Output) {
		return true
	}
	if r.Root == "" {
		return false
	}
	// Pending review queue counts as a write attempt.
	pending := filepath.Join(r.Root, ".slmcode", "pending")
	if entries, err := os.ReadDir(pending); err == nil && len(entries) > 0 {
		return true
	}
	// Content hash changes vs baseline.
	delta := false
	for _, f := range t.Files {
		cur := fileFingerprint(filepath.Join(r.Root, f))
		prev := baseline[f]
		if cur != "" && prev != "" && cur != prev {
			delta = true
			break
		}
		if prev == "" && cur != "" && plan.FileExists(r.Root, f) {
			// Newly created file.
			delta = true
			break
		}
	}
	if delta {
		return true
	}
	// Ambiguous baseline (empty / missing / matches current after a late snapshot):
	// trust git dirty on focus files or disk evidence section already checked above.
	if baselineAmbiguous(baseline, t) {
		if focusGitDirty(r.gitChangedFiles(), t.Files) {
			return true
		}
		// Focus file present + edit task with any write-looking output → trust disk.
		if looksLikeEditTask(t) && focusFilesPresent(r.Root, t.Files) &&
			(strings.Contains(strings.ToLower(t.Output), "edited ") ||
				strings.Contains(strings.ToLower(t.Output), "wrote ") ||
				strings.Contains(strings.ToLower(t.Output), "patched ") ||
				strings.Contains(strings.ToLower(t.Output), "dry-run: would")) {
			return true
		}
	}
	// Git diff for target files.
	changed := r.gitChangedFiles()
	if len(changed) == 0 {
		return false
	}
	if len(t.Files) == 0 {
		return true
	}
	return focusGitDirty(changed, t.Files)
}

func hasDiskEvidenceSection(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "## disk evidence") {
		return false
	}
	return strings.Contains(lower, "modified:") ||
		strings.Contains(lower, "created/present:") ||
		strings.Contains(lower, "git dirty:") ||
		strings.Contains(lower, "pending:")
}

func baselineAmbiguous(baseline map[string]string, t plan.Task) bool {
	if len(t.Files) == 0 {
		return true
	}
	if baseline == nil || len(baseline) == 0 {
		return true
	}
	missing := 0
	for _, f := range t.Files {
		if _, ok := baseline[f]; !ok {
			missing++
		}
	}
	return missing == len(t.Files)
}

func focusFilesPresent(root string, files []string) bool {
	if root == "" || len(files) == 0 {
		return false
	}
	for _, f := range files {
		if plan.FileExists(root, f) {
			return true
		}
	}
	return false
}

func focusGitDirty(changed, focus []string) bool {
	if len(changed) == 0 {
		return false
	}
	if len(focus) == 0 {
		return true
	}
	for _, f := range focus {
		f = filepath.ToSlash(f)
		base := strings.ToLower(filepath.Base(f))
		for _, c := range changed {
			c = filepath.ToSlash(c)
			if c == f || strings.HasSuffix(c, "/"+f) || strings.HasSuffix(f, "/"+c) {
				return true
			}
			if base != "" && strings.ToLower(filepath.Base(c)) == base {
				return true
			}
		}
	}
	return false
}

func (r *Runner) snapshotTargets(t plan.Task) map[string]string {
	out := map[string]string{}
	if r.Root == "" {
		return out
	}
	for _, f := range t.Files {
		out[f] = fileFingerprint(filepath.Join(r.Root, f))
	}
	return out
}

func (r *Runner) diskEvidenceHint(t plan.Task, baseline map[string]string) string {
	var lines []string
	for _, f := range t.Files {
		cur := fileFingerprint(filepath.Join(r.Root, f))
		prev := baseline[f]
		switch {
		case prev == "" && cur != "":
			lines = append(lines, "- created/present: "+f)
		case prev != "" && cur != "" && prev != cur:
			lines = append(lines, "- modified: "+f)
		}
	}
	for _, c := range r.gitChangedFiles() {
		lines = append(lines, "- git dirty: "+c)
		if len(lines) > 12 {
			break
		}
	}
	pending := filepath.Join(r.Root, ".slmcode", "pending")
	if entries, err := os.ReadDir(pending); err == nil {
		for _, e := range entries {
			lines = append(lines, "- pending: "+e.Name())
			if len(lines) > 16 {
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (r *Runner) gitChangedFiles() []string {
	if r.Root == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", r.Root, "diff", "--name-only", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("git", "-C", r.Root, "status", "--porcelain")
		out, err = cmd.Output()
		if err != nil {
			return nil
		}
		var files []string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// status --porcelain: XY PATH
			if len(line) > 3 {
				files = append(files, filepath.ToSlash(strings.TrimSpace(line[3:])))
			}
		}
		return files
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, filepath.ToSlash(line))
		}
	}
	return files
}

func fileFingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// lightweight fingerprint
	sum := 0
	for i, b := range data {
		sum = (sum*131 + int(b) + i) % 1000000007
	}
	return fmt.Sprintf("%d:%d", len(data), sum)
}

func alreadySatisfied(root string, t plan.Task) bool {
	if root == "" {
		return false
	}
	blob := strings.ToLower(t.Title + " " + StripScopedPack(t.Description) + " " + t.Acceptance)
	// Create/scaffold tasks: acceptance met when the task's declared files exist.
	// Use t.Files only (not expandTaskFocus) so scaffold prefixes don't inflate "needed".
	// Keep this narrow — "implement"/"write"/"edit" still need real write evidence.
	if strings.Contains(blob, "create") || strings.Contains(blob, "scaffold") ||
		strings.Contains(blob, "initialize") || strings.Contains(blob, "greenfield") {
		targets := t.Files
		if len(targets) == 0 {
			targets = plan.InferCreateFiles(blob)
		}
		present, needed := 0, 0
		for _, f := range targets {
			if strings.HasSuffix(f, "/") || f == "src" || f == "tests" {
				continue
			}
			needed++
			if !plan.FileExists(root, f) {
				continue
			}
			info, err := os.Stat(filepath.Join(root, f))
			if err == nil && info.Size() > 0 {
				present++
			}
		}
		if needed > 0 && present >= needed {
			return true
		}
	}
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
			if strings.Contains(body, "\n//") || strings.HasPrefix(strings.TrimSpace(body), "//") {
				return true
			}
		}
	}
	return false
}
