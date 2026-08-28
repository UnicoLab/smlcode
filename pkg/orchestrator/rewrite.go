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
		o.emitFullL("test", stream.KindAgentEnd, plan.RoleTester, "", "tester passed", "",
			truncate(tr.Summary, 400), stream.LevelSuccess)
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
	reason := firstFailureLine(failList, tr.Summary)
	// Reported as work that has been routed, not as an alarm. A defect with an
	// owner and a reproduction is a ticket; the red notification it used to be
	// told the user nothing they could act on and arrived on every gate run.
	o.emitFull("test", stream.KindOutput, plan.RoleTester, "",
		"tester found "+failureCountLabel(failList)+" — raising a correction ticket", "",
		truncate(strings.Join(failList, "; "), 800))
	o.emitLoop("test", LoopEvent{
		Action:   "tester_reject",
		Reason:   reason,
		Failures: trimFailures(failList, 5),
		From:     "test",
		To:       "plan",
	})

	// 1) Deterministic rewrite: reopen, and raise ONE correction ticket routed
	// to the specialist whose language broke, carrying the command and its
	// output as evidence.
	var hasRole func(string) bool
	if o != nil && o.factory != nil {
		hasRole = o.factory.HasRole
	}
	rewritten := rewriteBoardFromTesterWith(board, query, failList, tr.Summary,
		testerCommand(tr), testerOutput(testOut), hasRole)
	*board = rewritten
	// 1b) A defect that has already had a ticket goes past the project manager
	// before it goes back to the specialist that could not fix it last time.
	o.triageRepeatTickets(ctx, board)
	o.persistBoard(board)
	o.emitLoop("plan", LoopEvent{
		Action:   "rewrite",
		Reason:   "reopened/added corrective tasks from tester failures",
		Failures: trimFailures(failList, 5),
		From:     "test",
		To:       "execute",
		Wave:     o.waveCounter,
	})

	// 2) Optional SLM-assisted plan revision (cheap, think_passes-aware).
	if o.cfg.ThinkPasses >= 1 {
		o.emitAgent("plan", plan.RolePlanner, "", "revising plan after tester failure", "", "")
		o.emitLoop("plan", LoopEvent{
			Action: "replan",
			Reason: "planner revising narrow corrective scope",
			From:   "test",
			To:     "plan",
			Wave:   o.waveCounter,
		})
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
	return rewriteBoardFromTesterWith(board, query, failures, summary, "", "", nil)
}

// rewriteBoardFromTesterWith is the full form: cmd/output are the evidence for
// the correction ticket, hasRole reports which language specialists exist.
func rewriteBoardFromTesterWith(board *plan.Board, query string, failures []string, summary, cmd, output string, hasRole func(string) bool) plan.Board {
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
	// A reopened task IS the correction ticket for this defect, so it gets the
	// same treatment a freshly raised one would: the evidence, and the
	// specialist whose language actually broke.
	//
	// It used to be reopened with `Review: "tester feedback: <one sentence>"`
	// and nothing else — the agent that picked it up saw a task it had already
	// marked done, a sentence of verdict, and no way to reproduce the failure.
	// That is what turned corrections into retries.
	reopened := 0
	for i, note := range reopenIdx {
		t := out.Tasks[i]
		t.Normalize()
		t.MoveTo(plan.ColReadyToDev)
		t.Retries = 0
		t.Error = ""
		t.Notes = strings.TrimSpace(t.Notes + "\n" + note)
		t.Review = "tester feedback: " + firstSentence(summary)
		if looksImplementer(t) {
			enrichReopenedTask(&t, plan.CorrectionInput{
				Source:   plan.SourceTester,
				Failures: failures,
				Summary:  summary,
				Command:  cmd,
				Output:   output,
				Files:    firstNonEmptyFiles(t.Files, narrowFiles),
				Squad:    t.Squad,
				Origin:   t.ID,
				Attempt:  countPriorCorrections(out),
			}, hasRole)
			reopened++
		}
		out.Tasks[i] = t
	}

	// One correction ticket, routed to the specialist whose language actually
	// broke and carrying the evidence to fix it. The old version created a
	// generic `worker` task titled "Fix tester failures" whose whole context
	// was the failure lines — see pkg/plan/correction.go for why that produced
	// retries rather than fixes.
	if reopened == 0 && !hasOpenCorrective(out) {
		in := plan.CorrectionInput{
			Source:   plan.SourceTester,
			Failures: failures,
			Summary:  summary,
			Command:  cmd,
			Output:   output,
			Files:    narrowFiles,
			Squad:    correctionSquad(out, narrowFiles),
			Origin:   firstOf(targets.taskIDs),
			Attempt:  countPriorCorrections(out),
		}
		key := plan.CorrectionKey(in)
		// How many times THIS defect has been ticketed, not how many tickets
		// the board carries: a first attempt that announces itself as a third
		// tells its worker that approaches it never tried are already ruled out.
		in.Attempt = out.CorrectionAttempts(key)
		if !out.HasOpenCorrection(key) {
			nt := plan.NewCorrectionTicket(in, hasRole)
			plan.StampCorrectionKey(&nt, key)
			nt.Notes = strings.TrimSpace(nt.Notes + "\nquery scope " + out.QueryID)
			out.AddTask(nt)
		}
	}
	return out
}

// correctionSquad keeps a ticket with the team that owns the broken files, so
// a backend regression does not land on the frontend's board.
func correctionSquad(b plan.Board, files []string) string {
	for _, t := range b.Tasks {
		if t.Squad == "" {
			continue
		}
		for _, f := range files {
			for _, tf := range t.Files {
				if tf == f {
					return t.Squad
				}
			}
		}
	}
	return ""
}

// countPriorCorrections is how many correction tickets this board already
// carries. A repeat correction must know it is one, or it repeats the last
// attempt verbatim.
func countPriorCorrections(b plan.Board) int {
	n := 0
	for _, t := range b.Tasks {
		if strings.Contains(t.Notes, "correction ticket from the") {
			n++
		}
	}
	return n
}

func firstOf(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
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
		if !strings.Contains(lower, "fix") && !strings.Contains(lower, "test") &&
			!strings.Contains(lower, "implement") && !strings.Contains(lower, "add") &&
			!strings.Contains(lower, "address") && !strings.Contains(lower, "verify") {
			continue
		}
		nt := plan.Task{
			Title: firstSentence(step), Description: step,
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
		// AddTask mints the id under the board lock; NextID followed by a bare
		// append is two unsynchronized steps and hands out duplicates when
		// parallel review is appending at the same time.
		board.AddTask(nt)
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
		fmt.Fprintf(&b, "- %s [%s/%s] %s files=%v err=%s\n",
			t.ID, t.Column, t.Role, t.Title, t.Files, firstSentence(t.Error))
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

// testerCommand is the command the tester ran, when it reported one. It becomes
// the ticket's reproduction step and its acceptance.
func testerCommand(tr plan.TesterResult) string {
	for _, c := range tr.Commands {
		if strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

// testerOutput is the evidence to paste into the ticket.
//
// TesterResult carries no raw output field, so the finalize JSON itself is the
// best evidence available here — it holds the failure list and the summary the
// model actually produced, which is what the fixer needs to see verbatim rather
// than re-summarized.
func testerOutput(raw string) string {
	return strings.TrimSpace(raw)
}

// failureCountLabel keeps the event line honest about scale without pasting
// the list into it.
func failureCountLabel(failures []string) string {
	switch len(failures) {
	case 0, 1:
		return "1 failure"
	default:
		return fmt.Sprintf("%d failures", len(failures))
	}
}

// enrichReopenedTask gives a reopened task the context a fresh correction
// ticket would carry, and re-routes it to the specialist for its files.
//
// The original description is kept: the task still has to do what it was
// created to do. The correction is appended, so the agent reads the goal first
// and the defect second.
func enrichReopenedTask(t *plan.Task, in plan.CorrectionInput, hasRole func(string) bool) {
	if t == nil {
		return
	}
	ticket := plan.NewCorrectionTicket(in, hasRole)
	t.Description = strings.TrimRight(t.Description, "\n") + "\n\n---\n\n" + ticket.Description
	// Only re-route a generic worker. A task already held by a specialist was
	// routed deliberately — by the composer, the manager, or a human — and
	// overriding that here would quietly undo their choice.
	if strings.EqualFold(t.Role, plan.RoleWorker) && ticket.Role != plan.RoleWorker {
		t.Role = ticket.Role
	}
	if strings.TrimSpace(in.Command) != "" {
		t.Acceptance = ticket.Acceptance
	}
	plan.StampCorrectionKey(t, plan.CorrectionKey(in))
	t.Notes = strings.TrimSpace(t.Notes + "\ncorrection ticket from the " + string(in.Source) + " gate; assigned to " + t.Role)
}

// firstNonEmptyFiles prefers the task's own files over the board-wide guess.
func firstNonEmptyFiles(own, fallback []string) []string {
	if len(own) > 0 {
		return own
	}
	return fallback
}
