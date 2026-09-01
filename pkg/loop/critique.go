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

	// Disk evidence = what the task DID (diskEvidenceHint) plus what its shell
	// commands did behind the tool guards (shellScopeEvidence). The two halves
	// share one section but not one meaning: only the first carries an
	// evidentialDiskMarker, so an out-of-scope shell write can never be
	// miscounted by hasDiskEvidenceSection as proof the task did its job. Order
	// matters for nothing except reading — the markers are what the gates see.
	hint := r.diskEvidenceHint(*t, snapshot)
	if scope := r.shellScopeEvidence(t.ID); scope != "" {
		if hint != "" {
			hint += "\n"
		}
		hint += scope
	}
	if hint != "" {
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

	// Structured acceptance criteria supersede the prose scan.
	//
	// The two never both run for one task. Task.Normalize synthesizes
	// Acceptance FROM Criteria, so a structured task's prose contains the very
	// same commands — running both would execute the project's test suite
	// twice per correction round, which on a local model is wall-clock the run
	// does not have to spare.
	if acceptanceSmokeRole(role) && len(t.Criteria) > 0 {
		r.runCriteriaGate(ctx, t, role, scope, opt.verbose)
	} else if acceptanceSmokeRole(role) && strings.TrimSpace(t.Acceptance) != "" {
		started := time.Now()
		ar := quality.RunAcceptanceSmokeWithPolicy(ctx, r.Root, t.Acceptance, r.Timeout,
			quality.NormalizeBootstrapPolicy(r.BootstrapDeps))
		r.recordSmokeTool(ar, started)
		// A command that could not be LAUNCHED proves nothing about the code.
		// Treated as a failure it blamed the worker for the machine: measured,
		// `python -m pytest -q` on a host with no pytest rejected a correct
		// task, the corrector rewrote correct code, and the task escalated for
		// human review with the dependency still missing.
		if why := quality.CheckDidNotRun(ar.Command, ar.Output); ar.Ran && why != "" {
			if opt.verbose {
				r.logf("%s acceptance smoke UNVERIFIED — %s: %s", t.ID, why, ar.Command)
				r.fireLevel(stream.KindAgentEnd, "qa", t.ID,
					"acceptance smoke could not run — "+why,
					scope, truncate(ar.Output, 800), stream.LevelWarn)
			}
		} else if ar.Ran {
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
	// Suffix-aware: a routed `go-tester` is a tester, and an exact match here
	// subjected it to a gate testers are deliberately exempt from.
	if r.ClaimsGate && !plan.IsTesterRole(role) && role != plan.RoleExplorer {
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

	// Knowledge grounding. The gates above ask the DISK whether the worker did
	// what it said; this one asks the RECORD whether what it said is true of
	// this project at all. It is last because it is the least authoritative:
	// it never mints a FAILED verdict and nothing in outputWeak reads it — it
	// exists so the reviewer validates claims against stored facts instead of
	// judging whether an answer looks right.
	if sec := r.knowledgeConflictSection(*t); sec != "" {
		t.Output = appendHarnessSection(t.Output, sec)
		if opt.verbose {
			r.logf("%s knowledge conflicts flagged against stored facts", t.ID)
			r.fireLevel(stream.KindAgentEnd, "qa", t.ID, "claims conflict with stored knowledge",
				scope, truncate(sec, 600), stream.LevelWarn)
		}
	}
}

// knowledgeConflictSection reconciles the worker's own claims against semantic
// memory and renders the disagreements, or "" when there are none.
//
// Best-effort in the strict sense: every step that could be absent is a plain
// early return, so a run with --no-evolve, a memory store that failed to open,
// or simply an empty fact file produces the byte-identical output it produced
// before this existed. Memory is an optimization here, never a dependency.
func (r *Runner) knowledgeConflictSection(t plan.Task) string {
	if r == nil || r.Evolve == nil {
		return ""
	}
	mem := r.Evolve.Memory()
	if mem == nil {
		return ""
	}
	facts := mem.Semantic()
	if facts == nil {
		return ""
	}
	// Read the MODEL's text only. The harness's own sections quote the commands
	// it ran ("cmd: go test ./..."), and re-extracting those would have the
	// harness contradicting itself with its own evidence.
	claims := quality.ExtractClaims(stripPostSections(t.Output))
	if len(claims) == 0 {
		return ""
	}
	return quality.RenderContradictions(quality.Reconcile(claims, facts.All()))
}

// acceptanceSmokeRole reports whether a role's output should be acceptance-smoked.
func acceptanceSmokeRole(role string) bool {
	role = baseRole(role)
	// A LANGUAGE SPECIALIST is a worker. Once tasks began being staffed from
	// their own files, a Go task arrives with role "go-worker" and an exact
	// match answered no — silently switching the acceptance-criteria gate off
	// for every routed task. Measured: a board whose criteria were emitted
	// correctly, with runnable bare verify commands, and never verified once,
	// because the role that would have run them had a language in front of it.
	return plan.IsImplementerRole(role) || role == "deep"
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
		// A blocked must-criterion is caught here, one turn BEFORE the review
		// gate would catch it. Same verdict either way; this path fixes it
		// inside the current turn instead of spending a full
		// review → reject → correct round on a failure the harness can already
		// name exactly. Only CriteriaBlockedInOutput — an UNVERIFIED row is
		// not a defect the worker can act on.
		quality.CriteriaBlockedInOutput(t.Output) ||
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
	if quality.CriteriaBlockedInOutput(t.Output) {
		// Naming the exact criterion is the whole point of the structured
		// form: "acceptance failed" tells a small model to re-read a blob,
		// while "AC2's command failed" tells it which condition to fix.
		issues = append(issues, "Fix the "+quality.CriterionFailed+" row(s) in "+
			quality.CriteriaSectionHeader+" — a must-criterion's verify command must exit 0")
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
	if led := r.attemptLogSection(t); led != "" {
		b.WriteString(strings.TrimLeft(led, "\n") + "\n")
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
// runCriteriaGate verifies every structured criterion on a task and appends
// the per-criterion evidence section.
//
// It always appends the section when there are criteria at all — including the
// case where nothing could be run. An all-UNVERIFIED section is the single
// most useful thing this gate produces: it tells the reviewer that every
// stated condition is still an open question, which is the exact state the
// prose scan used to render as silence.
func (r *Runner) runCriteriaGate(ctx context.Context, t *plan.Task, role, scope string, verbose bool) {
	started := time.Now()
	rep := quality.VerifyCriteria(ctx, r.Root, *t, r.Timeout,
		quality.NormalizeBootstrapPolicy(r.BootstrapDeps))
	if sec := quality.FormatCriteriaSection(rep); sec != "" {
		t.Output = appendHarnessSection(t.Output, sec)
	}
	passed, failed, unverified, blocked := rep.Counts()

	// One aggregate tool event, not one per criterion: identical commands were
	// already deduplicated by VerifyCriteria, and the memory layer counts
	// COMMANDS that work in this project, not criteria that named them.
	if rep.Ran {
		agg := quality.SmokeResult{Ran: true, OK: blocked == 0}
		if o, ok := rep.FirstBlocking(); ok {
			agg.Command, agg.Output = o.Command, o.Output
		} else {
			for _, o := range rep.Outcomes {
				if o.Command != "" {
					agg.Command = o.Command
					break
				}
			}
		}
		r.recordSmokeTool(agg, started)
	}

	if blocked > 0 {
		o, _ := rep.FirstBlocking()
		// The command's stdout is the project's own suite talking — defuse it
		// before it joins a string the gates scan.
		t.Output += "\nObservation: exit error: acceptance criterion " + o.Criterion.ID + " failed\n" +
			quality.DefuseHarnessMarkers(truncate(o.Output, 1500))
		if verbose {
			r.logf("%s acceptance criteria FAILED: %s (%s)", t.ID, o.Criterion.ID, o.Command)
			r.fireLevel(stream.KindAgentEnd, "qa", t.ID,
				"acceptance criterion "+o.Criterion.ID+" failed",
				scope, truncate(o.Output, 800), stream.LevelProblem)
		}
		r.noteFailure(evolve.Signal{
			Tool: "ws_shell", Message: o.Output, Command: o.Command, ExitCode: 1,
			Language: detectSignalLanguage(r.Root), Phase: "acceptance", Role: role,
		}, "")
		return
	}
	if verbose {
		r.logf("%s acceptance criteria: %d passed, %d failed, %d unverified",
			t.ID, passed, failed, unverified)
	}
}

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
