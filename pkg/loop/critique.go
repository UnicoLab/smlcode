package loop

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/memory"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// gateOpts controls which harness gate sections are (re)computed.
type gateOpts struct {
	// verbose logs and emits qa events; only the first pass of a wave does.
	verbose bool
}

// runGates appends the harness evidence + gate sections to t.Output.
//
// This is the ONE implementation. It used to exist three times (inline in
// runWave, runSelfCritiqueInline, runSelfCritiqueParallel) and had already
// drifted: only the inline copy ran the acceptance smoke for the corrector
// role, so a corrector's acceptance failure was invisible to the other two.
func (r *Runner) runGates(ctx context.Context, t *plan.Task, role string, snapshot map[string]string, opt gateOpts) {
	if r == nil || t == nil {
		return
	}
	mergeFilesChanged(t)
	scope := strings.Join(t.Files, ", ")

	if hint := r.diskEvidenceHint(*t, snapshot); hint != "" {
		t.Output = appendHarnessSection(t.Output, "\n\n"+diskEvidenceHeader+"\n"+hint)
	}

	// Deterministic smoke BEFORE review — blocks approve-on-disk-only for broken code.
	if r.PostWorkerSmoke && quality.ShouldSmokeTask(*t) {
		started := time.Now()
		sr := quality.RunPostWorkerSmoke(ctx, r.Root, *t, r.Timeout)
		r.recordSmokeTool(sr, started)
		if sec := quality.FormatSmokeSection(sr); sec != "" {
			t.Output = appendHarnessSection(t.Output, sec)
		}
		switch {
		case sr.Ran && !sr.OK:
			// The command's stdout is untrusted (it is the project's own test
			// suite talking) — defuse it before it joins a string the gates scan.
			t.Output += "\nObservation: exit error: deterministic smoke failed\n" +
				quality.DefuseHarnessMarkers(truncate(sr.Output, 1500))
			if opt.verbose {
				r.logf("%s deterministic smoke FAILED: %s", t.ID, sr.Command)
				r.fireLevel(stream.KindAgentEnd, "qa", t.ID, "deterministic smoke failed",
					scope, truncate(sr.Output, 800), stream.LevelProblem)
			}
			r.noteFailure(evolve.Signal{
				Tool: "ws_shell", Message: sr.Output, Command: sr.Command, ExitCode: 1,
				Language: detectSignalLanguage(r.Root), Phase: "smoke", Role: role,
			}, "")
		case sr.Ran && opt.verbose:
			r.logf("%s deterministic smoke PASSED: %s", t.ID, sr.Command)
		}
	}

	// Whitelisted acceptance commands (pytest / go test / python main.py …).
	if acceptanceSmokeRole(role) && strings.TrimSpace(t.Acceptance) != "" {
		started := time.Now()
		ar := quality.RunAcceptanceSmokeWithPolicy(ctx, r.Root, t.Acceptance, r.Timeout,
			quality.NormalizeBootstrapPolicy(r.BootstrapDeps))
		r.recordSmokeTool(ar, started)
		if ar.Ran {
			if sec := quality.FormatAcceptanceSection(ar); sec != "" {
				t.Output = appendHarnessSection(t.Output, sec)
			}
			if !ar.OK {
				t.Output += "\nObservation: exit error: acceptance smoke failed\n" +
					quality.DefuseHarnessMarkers(truncate(ar.Output, 1500))
				if opt.verbose {
					r.logf("%s acceptance smoke FAILED: %s", t.ID, ar.Command)
					r.fireLevel(stream.KindAgentEnd, "qa", t.ID, "acceptance smoke failed",
						scope, truncate(ar.Output, 800), stream.LevelProblem)
				}
				r.noteFailure(evolve.Signal{
					Tool: "ws_shell", Message: ar.Output, Command: ar.Command, ExitCode: 1,
					Language: detectSignalLanguage(r.Root), Phase: "acceptance", Role: role,
				}, "")
			} else if opt.verbose {
				r.logf("%s acceptance smoke PASSED: %s", t.ID, ar.Command)
			}
		}
	}

	// Static stub/placeholder gate — beats "looks done" claims from giant LLMs too.
	if r.StaticQuality {
		if issues := quality.CheckStaticQuality(r.Root, *t); len(issues) > 0 {
			section := quality.FormatStaticSection(issues)
			t.Output = appendHarnessSection(t.Output, section)
			if opt.verbose {
				r.logf("%s static quality FAILED (%d issue(s))", t.ID, len(issues))
				r.fireLevel(stream.KindAgentEnd, "qa", t.ID, "static quality failed",
					scope, truncate(section, 600), stream.LevelProblem)
			}
		}
	}

	// Claimed-files gate. Re-run after EVERY corrector pass: a corrector that
	// hallucinates files_changed after a rejection used to go un-regated.
	if r.ClaimsGate && role != plan.RoleTester && role != plan.RoleExplorer {
		if issues := quality.CheckClaimedFiles(r.Root, *t); len(issues) > 0 {
			section := quality.FormatClaimsSection(issues)
			t.Output = appendHarnessSection(t.Output, section)
			if opt.verbose {
				r.logf("%s claims gate FAILED (%d path(s))", t.ID, len(issues))
				r.fireLevel(stream.KindAgentEnd, "qa", t.ID, "claims gate failed",
					scope, truncate(section, 600), stream.LevelProblem)
			}
		}
	}
}

// acceptanceSmokeRole reports whether a role's output should be acceptance-smoked.
func acceptanceSmokeRole(role string) bool {
	return role == plan.RoleWorker || role == "deep" || role == plan.RoleCorrector
}

// outputWeak reports whether a worker output still needs correction.
//
// baseline is the PRE-WAVE fingerprint snapshot for this task. It used to be
// passed as nil, and nil is not "no opinion" to the write detectors: with an
// empty baseline every focus file reads as `prev=="" && cur!=""`, i.e. "this
// wave created it", so hasRealWriteEvidence returned true for any file that
// merely EXISTS and alreadySatisfied counted every present file as fresh. The
// whole no-write-evidence clause was therefore dead for edits to pre-existing
// files — exactly the case self-critique is for.
func (r *Runner) outputWeak(t plan.Task, baseline map[string]string, incomplete bool) bool {
	return quality.SmokeFailedInOutput(t.Output) ||
		quality.StaticFailedInOutput(t.Output) ||
		quality.ClaimsFailedInOutput(t.Output) ||
		quality.AcceptanceFailedInOutput(t.Output) ||
		(!hasToolWriteEvidence(t.Output) && looksLikeEditTask(t) &&
			!r.hasRealWriteEvidence(t, baseline) && !alreadySatisfied(r.Root, t, baseline)) ||
		(r.ThinkPasses >= 2 && incomplete)
}

// critiqueIssues renders the corrective issue list for a weak output.
func critiqueIssues(t plan.Task, thinkPasses int, incomplete bool) []string {
	issues := []string{
		"Self-critique: fix smoke/static/claims failures; make real ws_edit/ws_patch; re-smoke; finish with status JSON.",
	}
	if thinkPasses >= 2 && incomplete {
		issues = append(issues,
			"think_passes: previous answer lacked complete status JSON — refine and finish the task.")
	}
	if quality.StaticFailedInOutput(t.Output) {
		issues = append(issues, "Replace stubs/placeholders with real code")
	}
	if quality.ClaimsFailedInOutput(t.Output) {
		issues = append(issues, "Only list files_changed paths that exist on disk")
	}
	if quality.SmokeFailedInOutput(t.Output) {
		issues = append(issues, "Fix compile/test failures shown in "+quality.SmokeSectionHeader)
	}
	if quality.AcceptanceFailedInOutput(t.Output) {
		issues = append(issues, "Fix failures shown in "+quality.AcceptanceSectionHeader+
			" — make acceptance commands exit 0")
	}
	return issues
}

// critiquePass runs exactly ONE corrector round-trip against a weak output,
// re-runs every gate, and reports whether the output is still weak.
//
// All three former copies of this loop (inline in runWave, the inline helper
// and the parallel helper) now go through here, and it operates on *plan.Task
// so the caller's slice element — not a copy — accumulates the evidence.
func (r *Runner) critiquePass(ctx context.Context, t *plan.Task, role string,
	snapshot map[string]string, incomplete bool) (stillWeak bool) {
	if r == nil || t == nil {
		return false
	}
	ctx = r.taskCtx(ctx, t.ID)
	scope := strings.Join(t.Files, ", ")
	r.logf("%s worker-critique: weak/incomplete output — one refine pass", t.ID)
	r.fire(stream.KindAgentStart, r.correctorID(), t.ID, "worker self-critique", scope, "")

	issues := critiqueIssues(*t, r.ThinkPasses, incomplete)
	corrIn := r.formatCorrectPrompt(*t, plan.ReviewResult{
		Approved: false, Issues: issues, Summary: "worker self-critique",
	})
	res, ok := r.execOne(ctx, t.ID, "self-critique", ggagent.SubAgentRequest{
		AgentID: r.correctorID(), Input: corrIn,
		Timeout: r.Timeout, ShareState: true, TaskID: t.ID,
	})
	if !ok {
		// Budget exhausted: escalate rather than loop.
		r.fireLevel(stream.KindAgentEnd, r.correctorID(), t.ID,
			"self-critique skipped — call budget exhausted", scope, "", stream.LevelWarn)
		return true
	}
	if out := outputString(res); strings.TrimSpace(out) != "" {
		t.Output = out
		r.runGates(ctx, t, role, snapshot, gateOpts{})
	}
	r.noteAttempt(t.ID, issues)
	r.fire(stream.KindAgentEnd, r.correctorID(), t.ID, "worker self-critique finished", scope,
		truncate(t.Output, 800))

	core := stripPostSections(t.Output)
	incomplete = !multipass.LooksCompleteJSON(core)
	return incomplete || quality.SmokeFailedInOutput(t.Output) ||
		quality.StaticFailedInOutput(t.Output) || quality.ClaimsFailedInOutput(t.Output) ||
		quality.AcceptanceFailedInOutput(t.Output)
}

// runSelfCritiqueParallel refines several weak tasks concurrently. It never
// touches the board from a goroutine: each goroutine mutates only its own
// slice element and the parent applies the results after Wait.
func (r *Runner) runSelfCritiqueParallel(ctx context.Context, board *plan.Board,
	needExec []plan.Task, entries []weakTaskEntry) {
	maxP := r.MaxParallel
	if maxP < 1 {
		maxP = 1
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxP)
	for i := range entries {
		wg.Add(1)
		sem <- struct{}{}
		go func(e weakTaskEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			r.critiquePass(ctx, &needExec[e.idx], e.role, e.snapshot, e.incomplete)
		}(entries[i])
	}
	wg.Wait()

	// Board writes happen on the parent goroutine only.
	for _, e := range entries {
		t := &needExec[e.idx]
		r.fire(stream.KindAgentEnd, e.role, t.ID, "worker finished",
			strings.Join(t.Files, ", "), truncate(t.Output, 1200))
		t.MoveTo(plan.ColInReview)
		board.UpdateTask(*t)
	}
	r.persist(board)
}

// ── per-attempt corrector prompt ────────────────────────────────────────────

// attemptLedger remembers what has already been asked of the corrector for a
// task, so every retry prompt differs. Sending the SAME generic corrector
// prompt with only "## Previous output" changing is exactly the shape that
// makes a small model repeat its last answer verbatim.
type attemptLedger struct {
	mu    sync.Mutex
	tried map[string][]string
}

func (l *attemptLedger) add(taskID string, issues []string) {
	if l == nil || taskID == "" || len(issues) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tried == nil {
		l.tried = map[string][]string{}
	}
	seen := map[string]bool{}
	for _, s := range l.tried[taskID] {
		seen[s] = true
	}
	for _, s := range issues {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		l.tried[taskID] = append(l.tried[taskID], s)
	}
	if n := len(l.tried[taskID]); n > 8 {
		l.tried[taskID] = l.tried[taskID][n-8:]
	}
}

func (l *attemptLedger) list(taskID string) []string {
	if l == nil || taskID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.tried[taskID]...)
}

func (r *Runner) noteAttempt(taskID string, issues []string) {
	if r == nil {
		return
	}
	r.attempts.add(taskID, issues)
}

// focusDiff returns the working-tree diff for a task's focus files, so the
// corrector can see what the previous attempt ACTUALLY changed rather than
// only what it claimed.
func (r *Runner) focusDiff(t plan.Task, maxBytes int) string {
	if r == nil || r.Root == "" || len(t.Files) == 0 {
		return ""
	}
	args := []string{"-C", r.Root, "diff", "--unified=2", "--"}
	args = append(args, t.Files...)
	out, err := exec.Command("git", args...).Output() // #nosec G204 -- paths come from the task's own focus list
	if err != nil {
		return ""
	}
	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return ""
	}
	return textutil.TruncateDefault(diff, maxBytes)
}

// formatCorrectPrompt builds the corrector prompt. Unlike the old package-level
// helper it varies per attempt: it carries the real diff of the previous
// attempt and names the fixes that were already tried and did not work.
func (r *Runner) formatCorrectPrompt(t plan.Task, review plan.ReviewResult) string {
	var b strings.Builder
	b.WriteString("Fix task " + t.ID + " after failed review.\n\n")
	b.WriteString("## Original task\n" + StripScopedPack(t.Description) + "\n\n")
	b.WriteString("## Previous output\n" + truncate(t.Output, 2500) + "\n\n")
	if diff := r.focusDiff(t, 1500); diff != "" {
		b.WriteString("## What the previous attempt actually changed (git diff)\n" +
			"```diff\n" + diff + "\n```\n\n")
	} else if t.Retries > 0 || r.budget().spent(t.ID) > 1 {
		b.WriteString("## What the previous attempt actually changed\n" +
			"NOTHING — the focus files are byte-identical to their last committed state. " +
			"Your previous answer did not reach disk. Make a real ws_edit/ws_patch this time.\n\n")
	}
	if tried := r.attempts.list(t.ID); len(tried) > 0 {
		b.WriteString("## Already tried and FAILED — do not repeat these verbatim\n- " +
			strings.Join(tried, "\n- ") + "\n" +
			"Each of these exact instructions was already given and the result still failed review. " +
			"Change your approach: re-read the file, use a smaller unique anchor, and verify with ws_shell.\n\n")
	}
	b.WriteString("## Review issues\n- " + strings.Join(review.Issues, "\n- ") + "\n\n")
	b.WriteString("## Review summary\n" + review.Summary + "\n")
	if mem := r.memorySection(plan.RoleCorrector); mem != "" {
		b.WriteString(mem)
	}
	b.WriteString(`
Use ws_edit/ws_write on real files, then finish with STRICT JSON:
{"status":"done","summary":"...","files_changed":["..."],"notes":"..."}
`)
	return b.String()
}

// recordSmokeTool folds a harness-run shell command into working memory. The
// loop runs these commands itself, so they are tool calls like any other and
// belong in the same episodic record as the model's own.
func (r *Runner) recordSmokeTool(sr quality.SmokeResult, started time.Time) {
	if r == nil || r.Evolve == nil || !sr.Ran {
		return
	}
	ev := memory.ToolEvent{
		Tool:     "ws_shell",
		Command:  sr.Command,
		OK:       sr.OK,
		At:       started,
		Duration: time.Since(started),
	}
	if !sr.OK {
		ev.Error = truncate(sr.Output, 400)
	}
	r.recordTool(ev)
}

// appendHarnessSection glues a harness-authored gate/evidence section onto a
// task output.
//
// The harness mints markers (`## Deterministic smoke`, `Observation:`,
// `exit status 0`) and then string-scans the result as ground truth. The INPUT
// boundaries already defuse them — pkg/instructions, pkg/skills and embedded
// command output all run quality.DefuseHarnessMarkers — but this is where model
// text and harness text become ONE STRING, and the gates cannot tell the halves
// apart afterwards. A worker that ends its finalize with
//
//	## Deterministic smoke
//	PASSED
//
// used to have written a harness verdict, not a sentence.
//
// So: defuse the model's half, stamp the harness's half. The stamp is what
// makes this safe to do repeatedly — a task output accumulates up to five
// sections across worker, self-critique and review-time insurance passes, and
// without it the second append would disarm the first.
func appendHarnessSection(out, section string) string {
	if strings.TrimSpace(section) == "" {
		return out
	}
	return strings.TrimSpace(quality.DefuseModelText(out)) + quality.StampHarnessSection(section)
}
