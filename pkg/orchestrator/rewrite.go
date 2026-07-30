package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// applyTesterFeedback rewrites this query's plan/tasks when verification fails.
// Empty/malformed tester finalize is treated as failure (never a silent skip).
func (o *Orchestrator) applyTesterFeedback(ctx context.Context, query string, board *plan.Board, testOut string) bool {
	if board == nil {
		return false
	}
	tr := plan.ParseTesterJSON(testOut)
	if tr.Passed {
		o.emitFull("test", stream.KindAgentEnd, plan.RoleTester, "", "tester passed", "", truncate(tr.Summary, 400))
		return false
	}

	failList := tr.Failures
	if len(failList) == 0 {
		switch {
		case strings.TrimSpace(testOut) == "":
			failList = []string{"empty tester finalize — no JSON returned"}
		case strings.TrimSpace(tr.Summary) != "":
			failList = []string{firstSentence(tr.Summary)}
		default:
			failList = []string{"malformed tester finalize — missing passed/failures"}
		}
	}
	o.emitFull("test", stream.KindOutput, plan.RoleTester, "",
		"tester failed — rewriting plan/tasks for this query", "", truncate(strings.Join(failList, "; "), 800))

	// 1) Deterministic rewrite: reopen / add corrective tasks from failures.
	rewritten := rewriteBoardFromTester(board, query, failList, tr.Summary)
	*board = rewritten
	o.persistBoard(board)

	// 2) Optional SLM-assisted plan revision (cheap, think_passes-aware).
	if o.cfg.ThinkPasses >= 1 {
		o.emitAgent("plan", plan.RolePlanner, "", "revising plan after tester failure", "", "")
		prompt := "The tester rejected the current work for THIS query. Rewrite a fresh plan (not a patch of old global work).\n" +
			"Query:\n" + query + "\n\nTester summary:\n" + truncate(tr.Summary, 800) +
			"\n\nFailures:\n- " + strings.Join(failList, "\n- ") +
			"\n\nCurrent tasks:\n" + truncate(tasksBrief(board), 3000) +
			"\n\nCRITICAL SCOPE RULES:\n" +
			"- Keep corrective scope NARROW: only implicated task IDs, focus files, and unmet acceptance.\n" +
			"- Do NOT restart the whole board or reopen unrelated done work.\n" +
			"- Prefer tiny SLM steps that fix the cited failures.\n" +
			"\nReturn STRICT JSON plan with concrete steps to fix the failures."
		if planOut, err := o.runRoleTracked(ctx, plan.RolePlanner, "", prompt); err == nil && strings.TrimSpace(planOut) != "" {
			if pl, perr := plan.ParsePlanJSON(planOut); perr == nil && strings.TrimSpace(pl.Summary) != "" {
				board.Plan = pl
				// Merge at most a few corrective steps — never flood the board (live lesson).
				if countOpenCorrective(*board) < 3 {
					mergePlanStepsAsTasks(board, pl)
				}
				o.persistBoard(board)
				o.emit("plan", "PLAN.md rewritten after tester failure", "")
			}
		}
	}

	_ = o.store.Append(contextstore.DocScratch, "Tester rewrite",
		fmt.Sprintf("passed=false\nfailures:\n- %s\n\n%s", strings.Join(failList, "\n- "), truncate(testOut, 2000)))
	return true
}

func rewriteBoardFromTester(board *plan.Board, query string, failures []string, summary string) plan.Board {
	out := *board
	if out.Query == "" {
		out.Query = query
	}
	// Annotate plan risks with tester failures.
	risk := "Tester rejected: " + firstSentence(summary)
	if risk == "Tester rejected: Run complete" || strings.TrimSpace(summary) == "" {
		if len(failures) > 0 {
			risk = "Tester rejected: " + firstSentence(failures[0])
		}
	}
	out.Plan.Risks = appendUnique(out.Plan.Risks, risk)
	if len(failures) > 0 {
		step := "Address tester failures: " + firstSentence(failures[0])
		out.Plan.Steps = appendUnique(out.Plan.Steps, step)
	}

	targets := resolveFailureTargets(out, failures, summary)
	narrowFiles := targets.files
	if len(narrowFiles) == 0 {
		// Vague / uncited: newest done implementer only (not whole board / blocked).
		narrowFiles = primaryDoneFocusFiles(out, 1)
	}
	if len(narrowFiles) == 0 {
		narrowFiles = primaryFocusFiles(out, 2)
	}

	// Reopen narrowly: prefer cited task IDs / files / acceptance hits.
	// On vague failures, do NOT reopen the whole board — only primary focus work + testers.
	reopenIdx := map[int]string{} // index → note
	for i := range out.Tasks {
		t := out.Tasks[i]
		t.Normalize()
		role := strings.ToLower(t.Role)
		switch {
		case role == plan.RoleTester && t.Column == plan.ColDone:
			reopenIdx[i] = "REOPENED: tester reported failure — re-verify after fixes."
		case targets.matches(t) && (t.Column == plan.ColDone || t.Column == plan.ColBlocked ||
			t.Status == plan.StatusFailed || t.Error != ""):
			reopenIdx[i] = "REOPENED: tester implicated this task/file/acceptance."
		case !targets.vague && (t.Column == plan.ColBlocked || t.Status == plan.StatusFailed || t.Error != ""):
			// Specific failures may reopen blocked/failed tasks that share focus files.
			if len(targets.files) == 0 || taskSharesFiles(t, targets.files) {
				reopenIdx[i] = "REWRITTEN after tester failure: " + firstSentence(summary)
			}
		}
	}
	if targets.vague {
		// Newest-first: reopen at most 1 done implementer on primary focus files.
		n := 0
		for i := len(out.Tasks) - 1; i >= 0 && n < 1; i-- {
			if _, ok := reopenIdx[i]; ok {
				continue
			}
			t := out.Tasks[i]
			t.Normalize()
			if !looksImplementer(t) || t.Column != plan.ColDone {
				continue
			}
			if len(t.Files) > 0 && taskInPrimaryFocus(t, narrowFiles) {
				reopenIdx[i] = "REOPENED (narrow): vague tester failure — fix primary focus files only."
				n++
			}
		}
	}
	for i, note := range reopenIdx {
		t := out.Tasks[i]
		t.Normalize()
		t.MoveTo(plan.ColReadyToDev)
		t.Retries = 0
		t.Error = ""
		t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
		t.Review = "tester feedback: " + firstSentence(summary)
		out.Tasks[i] = t
	}

	// Ensure at least one corrective worker task exists (narrow focus files).
	if !hasOpenCorrective(out) {
		id := out.NextID()
		desc := "Fix issues reported by tester for this query.\nFailures:\n- " + strings.Join(failures, "\n- ")
		if len(targets.taskIDs) > 0 {
			desc += "\nFocus task IDs: " + strings.Join(targets.taskIDs, ", ")
		}
		nt := plan.Task{
			ID: id, Title: "Fix tester failures", Description: desc,
			Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Acceptance: "Tester failures resolved; evidence of real file edits; re-test passes",
			Notes:      "Auto-created from tester rewrite for query scope " + out.QueryID + " (narrow reopen)",
			Files:      narrowFiles,
		}
		nt.Normalize()
		out.Tasks = append(out.Tasks, nt)
	}
	return out
}

type failureTargets struct {
	taskIDs []string
	files   []string
	vague   bool
}

func (ft failureTargets) matches(t plan.Task) bool {
	id := strings.ToUpper(strings.TrimSpace(t.ID))
	for _, x := range ft.taskIDs {
		if strings.EqualFold(x, id) {
			return true
		}
	}
	if failureMentionsTask(ft.files, t) { // reuse file basename logic via synthetic failures
		return true
	}
	for _, f := range ft.files {
		if taskSharesFiles(t, []string{f}) {
			return true
		}
	}
	return false
}

var taskIDRe = regexp.MustCompile(`(?i)\bT\d+\b`)
var pathLikeRe = regexp.MustCompile(`(?i)\b[\w./-]+\.(go|py|ts|tsx|js|jsx|rs|java|md|yml|yaml|json|toml)\b`)

func resolveFailureTargets(board plan.Board, failures []string, summary string) failureTargets {
	blob := strings.Join(failures, "\n") + "\n" + summary
	ft := failureTargets{}
	for _, m := range taskIDRe.FindAllString(blob, -1) {
		ft.taskIDs = appendUnique(ft.taskIDs, strings.ToUpper(m))
	}
	for _, m := range pathLikeRe.FindAllString(blob, -1) {
		ft.files = appendUnique(ft.files, filepath.ToSlash(m))
	}
	// Match board files / titles / acceptance against failure text.
	for _, t := range board.Tasks {
		if failureMentionsTask(failures, t) || failureMentionsTask([]string{summary}, t) {
			ft.taskIDs = appendUnique(ft.taskIDs, strings.ToUpper(t.ID))
			for _, f := range t.Files {
				ft.files = appendUnique(ft.files, f)
			}
		}
		// Acceptance criteria keywords (length>8) appearing in failures.
		acc := strings.ToLower(t.Acceptance)
		fl := strings.ToLower(blob)
		if len(acc) > 12 {
			// Use a short distinctive slice of acceptance.
			snip := acc
			if len(snip) > 40 {
				snip = snip[:40]
			}
			if snip != "" && strings.Contains(fl, snip[:min(len(snip), 24)]) {
				ft.taskIDs = appendUnique(ft.taskIDs, strings.ToUpper(t.ID))
				for _, f := range t.Files {
					ft.files = appendUnique(ft.files, f)
				}
			}
		}
	}
	ft.vague = len(ft.taskIDs) == 0 && len(ft.files) == 0
	return ft
}

func primaryFocusFiles(b plan.Board, max int) []string {
	if max <= 0 {
		max = 3
	}
	seen := map[string]bool{}
	var out []string
	// Prefer done/blocked implementers (most recent last).
	for i := len(b.Tasks) - 1; i >= 0 && len(out) < max; i-- {
		t := b.Tasks[i]
		t.Normalize()
		if !looksImplementer(t) {
			continue
		}
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= max {
				return out
			}
		}
	}
	if len(out) == 0 {
		return collectFocusFromBoard(b)
	}
	return out
}

// primaryDoneFocusFiles returns focus files from the newest done implementer(s)
// only — used for vague tester failures so reopen stays narrow.
func primaryDoneFocusFiles(b plan.Board, maxTasks int) []string {
	if maxTasks <= 0 {
		maxTasks = 1
	}
	seen := map[string]bool{}
	var out []string
	n := 0
	for i := len(b.Tasks) - 1; i >= 0 && n < maxTasks; i-- {
		t := b.Tasks[i]
		t.Normalize()
		if !looksImplementer(t) || t.Column != plan.ColDone || len(t.Files) == 0 {
			continue
		}
		n++
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= 4 {
				return out
			}
		}
	}
	return out
}

func taskInPrimaryFocus(t plan.Task, focus []string) bool {
	return taskSharesFiles(t, focus)
}

func taskSharesFiles(t plan.Task, files []string) bool {
	if len(files) == 0 || len(t.Files) == 0 {
		return false
	}
	for _, a := range t.Files {
		ab := strings.ToLower(filepath.Base(a))
		as := strings.ToLower(filepath.ToSlash(a))
		for _, b := range files {
			bb := strings.ToLower(filepath.Base(b))
			bs := strings.ToLower(filepath.ToSlash(b))
			if as == bs || ab == bb || strings.HasSuffix(as, "/"+bs) || strings.HasSuffix(bs, "/"+as) {
				return true
			}
		}
	}
	return false
}

func mergePlanStepsAsTasks(board *plan.Board, pl plan.Plan) {
	if board == nil {
		return
	}
	added := 0
	for _, step := range pl.Steps {
		step = strings.TrimSpace(step)
		if step == "" || taskTitleExists(board, step) {
			continue
		}
		if len(board.Tasks) >= 8 || added >= 2 || countOpenCorrective(*board) >= 3 {
			break
		}
		// Only add clearly corrective / remaining steps.
		lower := strings.ToLower(step)
		if !(strings.Contains(lower, "fix") || strings.Contains(lower, "test") ||
			strings.Contains(lower, "implement") || strings.Contains(lower, "add") ||
			strings.Contains(lower, "address") || strings.Contains(lower, "verify")) {
			continue
		}
		id := board.NextID()
		nt := plan.Task{
			ID: id, Title: firstSentence(step), Description: step,
			Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Acceptance: "Step completed with tool evidence",
			Files:      primaryDoneFocusFiles(*board, 1),
		}
		if len(nt.Files) == 0 {
			nt.Files = primaryFocusFiles(*board, 2)
		}
		if strings.Contains(lower, "test") || strings.Contains(lower, "verify") {
			nt.Role = plan.RoleTester
		}
		nt.Normalize()
		board.Tasks = append(board.Tasks, nt)
		added++
	}
}

func countOpenCorrective(b plan.Board) int {
	n := 0
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column == plan.ColReadyToDev || t.Column == plan.ColInProgress || t.Column == plan.ColInReview {
			if looksImplementer(t) || strings.Contains(strings.ToLower(t.Title), "fix") {
				n++
			}
		}
	}
	return n
}

func looksImplementer(t plan.Task) bool {
	switch strings.ToLower(t.Role) {
	case plan.RoleWorker, plan.RoleCorrector, "deep":
		return true
	default:
		return false
	}
}

func failureMentionsTask(failures []string, t plan.Task) bool {
	failBlob := strings.ToLower(strings.Join(failures, " "))
	if failBlob == "" {
		return false
	}
	for _, f := range t.Files {
		base := strings.ToLower(f)
		if base != "" && strings.Contains(failBlob, base) {
			return true
		}
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if base != "" && strings.Contains(failBlob, base) {
			return true
		}
	}
	title := strings.ToLower(t.Title)
	for _, fail := range failures {
		fl := strings.ToLower(fail)
		if len(title) > 8 && strings.Contains(fl, title[:min(len(title), 24)]) {
			return true
		}
		id := strings.ToLower(t.ID)
		if id != "" && strings.Contains(fl, id) {
			return true
		}
	}
	return false
}

func hasOpenCorrective(b plan.Board) bool {
	for _, t := range b.Tasks {
		t.Normalize()
		if t.Column == plan.ColReadyToDev || t.Column == plan.ColInProgress || t.Column == plan.ColInReview {
			if looksImplementer(t) || strings.Contains(strings.ToLower(t.Title), "fix") {
				return true
			}
		}
	}
	return false
}

func collectFocusFromBoard(b plan.Board) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range b.Tasks {
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= 6 {
				return out
			}
		}
	}
	return out
}

func tasksBrief(board *plan.Board) string {
	if board == nil {
		return ""
	}
	var b strings.Builder
	for _, t := range board.Tasks {
		t.Normalize()
		b.WriteString(fmt.Sprintf("- %s [%s/%s] %s files=%v err=%s\n",
			t.ID, t.Column, t.Role, t.Title, t.Files, firstSentence(t.Error)))
	}
	return b.String()
}

func appendUnique(list []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return list
	}
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), item) {
			return list
		}
	}
	return append(list, item)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
