package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/blocks"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// realFailureTokens are the substrings that mean a test/build command actually
// FAILED, as opposed to merely reporting packages that have no tests.
//
// `go test ./...` prints `?   pkg/foo  [no test files]` for every untested
// package WHILE ALSO printing FAIL for the failing ones, so in any mixed repo
// (i.e. almost all of them, this one included) the old
// `strings.Contains(sr.Output, "?\t")` clause turned a real reproduced failure
// into "no test files found — skipping gate (code compiles)" and returned
// green. finalizeAfterExecute then promoted the board and cleared the tester
// rejection on the strength of it.
var realFailureTokens = []string{
	"FAIL",
	"build failed",
	"cannot find",
	"undefined:",
	"Error:",
	"error:",
	"panic:",
	"Traceback (most recent call last)",
}

// noTestsTokens indicate the toolchain found nothing to run.
var noTestsTokens = []string{
	"no test files",
	"no Go files",
	"no tests ran",
	"collected 0 items",
}

// qaLooksLikeNoTests reports whether a non-zero exit is only "there is nothing
// to test" and not a real failure hiding among the [no test files] lines.
func qaLooksLikeNoTests(output string) bool {
	if strings.TrimSpace(output) == "" {
		return false
	}
	for _, tok := range realFailureTokens {
		if strings.Contains(output, tok) {
			return false
		}
	}
	for _, tok := range noTestsTokens {
		if strings.Contains(output, tok) {
			return true
		}
	}
	return false
}

// runQAGate iterates a project test/smoke command until green or max rounds.
// On failure it asks the tester/corrector specialists to fix, then re-runs.
// Returns true when the gate ends red (caller should rewrite plan/tasks).
func (o *Orchestrator) runQAGate(ctx context.Context, query string, board *plan.Board) bool {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return false
	}
	cmd := o.qaCommand()
	if cmd == "" {
		o.emitWarn("test", "qa_gate: no auto test/smoke command — set qa_gate_command", "")
		return false
	}
	// qaGateRounds floors config's shipped default of 1: with max==1,
	// `round == max` was true on the FIRST iteration, so the gate annotated the
	// board and returned before the tester-diagnose and corrector-fix blocks
	// ever ran — the documented "iterates until green" repair loop was
	// unreachable under the shipped default.
	max := o.qaGateRounds()

	o.runRegressionChecks(ctx, "pre-gate")

	o.runQABootstrap(ctx, cmd)

	for round := 1; round <= max; round++ {
		if err := ctx.Err(); err != nil {
			return true
		}
		o.qaPreflight(ctx, round, cmd)

		o.emitFull("test", stream.KindAgentStart, "qa", "",
			fmt.Sprintf("qa_gate %d/%d: %s", round, max, cmd), "", "")
		sr := quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)

		if !sr.OK && round == 1 && qaLooksLikeNoTests(sr.Output) {
			o.emitWarn("test", "qa_gate: no test files found — skipping gate (code compiles)", "")
			o.recordGate("qa_gate", true, "no tests to run")
			return false
		}
		if sr.OK {
			_ = o.store.Append(contextstore.DocScratch, "QA gate",
				fmt.Sprintf("GREEN round %d\n\n%s", round, truncate(sr.Output, 2000)))
			o.emitFullL("test", stream.KindAgentEnd, "qa", "", "qa_gate green", "",
				truncate(sr.Output, 800), stream.LevelSuccess)
			o.recordGate("qa_gate", true, cmd)
			o.runRegressionChecks(ctx, "post-gate")
			return false
		}

		failText := strings.TrimSpace(sr.Output + "\n" + sr.Summary)
		// FailureExcerpt, never a head cut: every runner this harness drives
		// prints its verdict LAST, so head-only truncation handed the reader
		// collection noise with the assertion removed.
		_ = o.store.Append(contextstore.DocScratch, "QA gate failure",
			fmt.Sprintf("round %d/%d\ncmd: %s\n\n%s", round, max, cmd,
				quality.FailureExcerpt(failText, 4000)))
		o.emitFullL("test", stream.KindOutput, "qa", "",
			fmt.Sprintf("qa_gate failed round %d/%d", round, max), "",
			quality.FailureExcerpt(failText, 1500), stream.LevelError)

		// The fix pass now runs on EVERY round, the last one included. It used
		// to be skipped whenever round == max, which with max==1 meant always.
		o.qaDiagnoseAndFix(ctx, query, cmd, failText)
	}

	// One final verification of the last fix pass before declaring red.
	if err := ctx.Err(); err == nil {
		final := quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
		if final.OK {
			_ = o.store.Append(contextstore.DocScratch, "QA gate",
				"GREEN after final fix pass\n\n"+truncate(final.Output, 2000))
			o.emitFullL("test", stream.KindAgentEnd, "qa", "", "qa_gate green after final fix pass", "",
				truncate(final.Output, 800), stream.LevelSuccess)
			o.recordGate("qa_gate", true, cmd)
			o.runRegressionChecks(ctx, "post-gate")
			return false
		}
	}

	o.emitFullL("test", stream.KindAgentEnd, "qa", "",
		fmt.Sprintf("qa_gate still red after %d rounds", max), "", "", stream.LevelError)
	o.recordGate("qa_gate", false, cmd)
	if board != nil {
		for i := range board.Tasks {
			if board.Tasks[i].Column == plan.ColDone {
				board.Tasks[i].Notes = strings.TrimSpace(
					board.Tasks[i].Notes + "\nQA gate still failing: " + cmd)
				break
			}
		}
		o.persistBoard(board)
	}
	return true
}

// qaCommand resolves the project's test/smoke command.
func (o *Orchestrator) qaCommand() string {
	if o == nil || o.cfg == nil {
		return ""
	}
	cmd := strings.TrimSpace(o.cfg.QAGateCommand)
	if cmd != "" {
		return cmd
	}
	if cmd = blocks.ResolveQAGateCommand(o.cfg.Root, o.cfg.Root, o.cfg.ActivePack); cmd != "" {
		return cmd
	}
	return quality.DetectProjectCommandWithPack(o.cfg.Root, o.cfg.ActivePack)
}

// qaPreflight runs the cheap deterministic fixes before round 1.
func (o *Orchestrator) qaPreflight(ctx context.Context, round int, cmd string) {
	if round != 1 {
		return
	}
	o.formatWaveChanges(ctx)
	if !strings.Contains(cmd, "go test") {
		return
	}
	if _, err := os.Stat(filepath.Join(o.cfg.Root, "go.mod")); err != nil {
		return
	}
	br := quality.RunSmoke(ctx, o.cfg.Root, "go build ./...", 30*time.Second)
	if !br.OK {
		o.emitWarn("test", "qa_gate: build failed — "+quality.FailureExcerpt(br.Output, 300), "")
		return
	}
	o.emit("test", "qa_gate: build OK, running full tests", "")
}

// formatWaveChanges formats the files THIS RUN changed, and nothing else.
//
// The pre-QA formatter used to run `gofmt -w .` / `goimports -w .` over the
// project root (quality.AutoFixFormatting, since deleted), so a repo that was not
// already gofmt-clean got an enormous unrelated diff attributed to the agent,
// with no checkpoint and no timeout. Its replacement is scoped to the changed
// set and snapshots every file first, so the pass stays undoable.
func (o *Orchestrator) formatWaveChanges(ctx context.Context) {
	if o == nil || o.cfg == nil {
		return
	}
	changed := o.changedFilesSnapshot()
	if len(changed) == 0 {
		return
	}
	var snapshot func(string)
	if o.workspace != nil && o.workspace.Checkpointer != nil {
		snapshot = o.workspace.Checkpointer.BackupIfNeeded
	}
	fixOut := quality.FormatChangedFiles(ctx, quality.FormatRequest{
		Root:  o.cfg.Root,
		Files: changed,
		// goimports stays opt-in: it rewrites the import block from the file it
		// can see and will delete an import only a build-tagged sibling needs.
		Goimports: false,
		Timeout:   quality.DefaultFormatTimeout,
		Snapshot:  snapshot,
	})
	if fixOut != "" {
		o.emit("test", "qa_gate: formatted changed files: "+truncate(fixOut, 200), "")
	}
}

// runQABootstrap applies the qa_bootstrap policy to the dependency install the
// QA command implies.
//
// BootstrapDeps proposes `pip install` / `npm install` / `go mod tidy` derived
// from an AGENT-AUTHORED manifest, which is arbitrary code execution from model
// output. quality.PlanBootstrap states the decision explicitly instead of
// assuming consent: off refuses and says so, ask routes the command through the
// ws_shell permission layer (shell mode, whitelist, approval flow — the same
// HITL every other command gets), auto runs it unattended.
func (o *Orchestrator) runQABootstrap(ctx context.Context, cmd string) {
	if o == nil || o.cfg == nil {
		return
	}
	policy := quality.NormalizeBootstrapPolicy(o.QABootstrapMode())
	bp := quality.PlanBootstrap(o.cfg.Root, cmd, policy)
	if bp.Command == "" {
		if bp.Reason != "" {
			// policy=off with a real candidate: say what was skipped, or the
			// run ends in "it just did not install anything" with no trace.
			o.emitWarn("test", "qa_gate bootstrap: "+truncate(bp.Reason, 200), "")
		}
		return
	}
	o.emit("test", "qa_gate bootstrap: "+truncate(bp.Command, 120)+
		" (policy="+string(bp.Policy)+")", "")

	var sr quality.SmokeResult
	if bp.NeedsApproval {
		o.emitWarn("test", truncate(bp.Reason, 240), "")
		sr = o.runGatedCommand(ctx, bp.Command, "qa bootstrap")
	} else {
		// policy=auto: the operator opted in explicitly, so it runs unattended,
		// exactly as quality.RunAcceptanceSmokeWithPolicy does for Run plans.
		sr = quality.RunSmoke(ctx, o.cfg.Root, bp.Command, o.cfg.TaskTimeout)
	}
	_ = o.store.Append(contextstore.DocScratch, "QA bootstrap",
		fmt.Sprintf("cmd: %s\npolicy: %s\nran=%v ok=%v\n\n%s",
			bp.Command, bp.Policy, sr.Ran, sr.OK, quality.FailureExcerpt(sr.Output, 2000)))
	if !sr.OK {
		o.emitFullL("test", stream.KindOutput, "qa", "", "qa_gate bootstrap warning", "",
			quality.FailureExcerpt(sr.Output, 800), stream.LevelWarn)
	}
}

// qaDiagnoseAndFix runs the tester (diagnose) then the corrector (fix).
func (o *Orchestrator) qaDiagnoseAndFix(ctx context.Context, query, cmd, failText string) {
	o.emitAgent("test", plan.RoleTester, "", "qa_gate diagnose failures", "", "")
	testPack, _ := o.packBuild("tester", query, contextstore.DefaultDocsForRole("tester"), nil,
		o.skillPackFor("tester", query))
	diag, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
		"\n## QA gate failure\nCommand: "+cmd+"\n\n"+quality.FailureExcerpt(failText, 6000)+
		"\n\n"+o.langHint()+"\n\nDiagnose with ws_shell if helpful. List concrete file edits needed. "+
		"Return JSON with status and issues.")
	if strings.TrimSpace(diag) != "" {
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "qa diagnose", "", truncate(diag, 1000))
	}

	o.emitAgent("test", plan.RoleCorrector, "", "qa_gate fix iteration", "", "")
	fixPack, _ := o.packBuild("corrector", query, contextstore.DefaultDocsForRole("corrector"), nil,
		o.skillPackFor("corrector", query))
	fixPrompt := fixPack.Render() +
		"\n## Goal\nMake this command pass: `" + cmd + "`\n\n" +
		o.langHint() + "\n\n## Failure output\n" +
		quality.FailureExcerpt(failText, 5000) +
		"\n\n## Diagnosis\n" + truncate(diag, 3000) +
		"\n\nUse ws_edit / ws_patch / ws_write for SMALL fixes. Then return STRICT JSON status."
	fixOut, _ := o.runRoleTracked(ctx, plan.RoleCorrector, "", fixPrompt)
	if strings.TrimSpace(fixOut) != "" {
		o.emitFull("test", stream.KindOutput, plan.RoleCorrector, "", "qa fix output", "", truncate(fixOut, 1000))
	}
}

// runGatedCommand executes a command through the registered ws_shell tool, so
// it inherits the whole permission layer: shell mode (allow/ask/deny), the
// whitelist gate, the write guard and the approval flow. Falls back to a direct
// smoke run only when the tool is not registered (tests, bare orchestrators).
func (o *Orchestrator) runGatedCommand(ctx context.Context, cmd, label string) quality.SmokeResult {
	if strings.TrimSpace(cmd) == "" {
		return quality.SmokeResult{OK: true}
	}
	if o.tools != nil {
		if tool, ok := o.tools.GetTool("ws_shell"); ok {
			args, _ := json.Marshal(map[string]any{"command": cmd})
			out, err := tool.Execute(taskContext(ctx, "qa"), string(args))
			text := fmt.Sprintf("%v", out)
			if err != nil {
				return quality.SmokeResult{Ran: true, Command: cmd, Output: text, Summary: err.Error()}
			}
			// ws_shell answers refusals in prose rather than as an error.
			if refused := shellRefusal(text); refused != "" {
				o.emitWarn("test", label+" not permitted: "+truncate(refused, 200), "")
				return quality.SmokeResult{Ran: false, Command: cmd, Output: text, Summary: refused}
			}
			return quality.SmokeResult{OK: true, Ran: true, Command: cmd, Output: text}
		}
	}
	return quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
}

// shellRefusal detects the permission layer's prose refusals.
func shellRefusal(out string) string {
	lower := strings.ToLower(out)
	for _, marker := range []string{
		"shell denied by permission mode",
		"shell denied by user",
		"shell approval unavailable",
		"not in the allowed command list",
		"refused:",
	} {
		if strings.Contains(lower, marker) {
			return firstSentence(out)
		}
	}
	return ""
}

// runRegressionChecks replays what previous runs already fixed.
//
// This is what makes "fail once, then never again" real: evolve stores a cheap
// re-check for every failure it saw resolved, and a check that starts failing
// again means a regression, not a new bug. Command checks go through the
// permission layer (evolve deliberately never executes anything itself); the
// file assertions are safe and run offline.
func (o *Orchestrator) runRegressionChecks(ctx context.Context, when string) {
	if o == nil || o.evolve == nil || o.cfg == nil || !o.regressionChecksEnabled() {
		return
	}
	regs := o.evolve.Regressions()
	if regs == nil {
		return
	}

	offline := regs.RunOffline(o.cfg.Root)
	failed := 0
	for _, r := range offline {
		if !r.OK {
			failed++
		}
	}
	if n := len(offline); n > 0 {
		level := stream.LevelSuccess
		msg := fmt.Sprintf("regressions %s: %d/%d file checks pass", when, n-failed, n)
		if failed > 0 {
			level = stream.LevelProblem
		}
		o.emitFullL("test", stream.KindOutput, "regressions", "", msg, "", "", level)
		o.recordGate("regressions_offline", failed == 0, msg)
	}

	for _, chk := range regs.Runnable() {
		if err := ctx.Err(); err != nil {
			return
		}
		cmd := strings.TrimSpace(chk.Command)
		if cmd == "" {
			continue
		}
		sr := o.runGatedCommand(ctx, cmd, "regression check")
		if !sr.Ran {
			// Refused by the permission layer — not evidence either way.
			continue
		}
		regs.Record(chk.ID, sr.OK)
		if !sr.OK {
			o.emitFullL("test", stream.KindIntervention, "regressions", "",
				"regression returned: "+truncate(cmd, 120), quality.InterventionReview,
				quality.FailureExcerpt(sr.Output, 800), stream.LevelProblem)
			o.recordGate("regression:"+chk.ID, false, cmd)
		}
	}
}

// detectQACommand is kept for tests; delegates to quality.DetectProjectCommand.
func detectQACommand(root string) string {
	return quality.DetectProjectCommand(root)
}

// bootstrapQADeps reports the install command a QA command WOULD imply.
//
// It grants no permission and is not the production path — runQABootstrap is,
// and it goes through quality.PlanBootstrap so the qa_bootstrap policy decides.
// Kept for the detection tests, which assert what a project shape implies.
func bootstrapQADeps(root, cmd string) string {
	return quality.BootstrapDeps(root, cmd)
}

// shellNoticeProbe is one claim the operator-facing notice makes, paired with
// the command that PROVES it. The claim is only printed when the workspace
// package actually behaves that way for the sample.
type shellNoticeProbe struct {
	label  string
	sample string
}

// shellRefusedProbes name the classes ws_shell refuses under the whitelist.
//
// The notice used to be a hand-written sentence — "python, node, make, npx,
// `go run`, cp, mv and sed" — and it went stale the moment the shell guard
// grew the exec-flag and out-of-jail audits (`env <prog>`, `find -exec`,
// `go test -exec`, `go generate`, `cmake -P`, `mkdir` outside the root). An
// operator reading it concluded those were allowed. Each entry below is
// verified against workspace.GuardShellWhitelist at build time, so a refusal
// that changes shape drops out of the notice instead of lying in it.
var shellRefusedProbes = []shellNoticeProbe{
	{"interpreters (python, node, npx, make, `go run`)", "python script.py"},
	{"file movers (cp, mv, sed -i)", "cp a.go b.go"},
	{"`env <prog>` (runs a program the whitelist never sees)", "env python -c 'print(1)'"},
	{"`find -exec/-execdir/-ok/-delete/-fprintf`", "find . -name '*.go' -exec rm {} ;"},
	{"`go test -exec` / `-toolexec` / `-vettool` / `-ldflags` / `-gcflags`", "go test -exec ./runner ./..."},
	{"`go generate` (runs directives the repository chose)", "go generate ./..."},
	{"`cmake -P` / `-E` / `-C` / `--install`", "cmake -P script.cmake"},
	{"`mkdir` / `touch` outside the project root", "mkdir /tmp/outside"},
}

// shellAllowedProbes name what still runs untouched, so the notice cannot read
// as "verification is blocked".
var shellAllowedProbes = []shellNoticeProbe{
	{"go test", "go test ./... -short"},
	{"go build", "go build ./..."},
	{"pytest", "pytest -q"},
	{"npm test", "npm test"},
	{"cargo test", "cargo test"},
}

// ShellWhitelistNotice is the operator-facing summary of what `ws_shell` in
// whitelist mode refuses. It is DERIVED from pkg/workspace rather than restated:
// every clause is a claim the shell guard is asked to confirm.
//
// Test and build runners remain auto-allowed, so the ordinary verification path
// is unaffected. What changes is the unattended dependency bootstrap:
// pkg/quality's BootstrapDeps proposes `pip install` / `npm install` /
// `go mod tidy` derived from an AGENT-AUTHORED manifest, and that routes
// through the permission layer like any other command instead of running on its
// own authority.
var ShellWhitelistNotice = buildShellWhitelistNotice()

func buildShellWhitelistNotice() string {
	var refused, allowed []string
	for _, p := range shellRefusedProbes {
		if _, blocked := workspace.GuardShellWhitelist(p.sample, nil); blocked {
			refused = append(refused, p.label)
		}
	}
	for _, p := range shellAllowedProbes {
		if _, blocked := workspace.GuardShellWhitelist(p.sample, nil); !blocked {
			allowed = append(allowed, p.label)
		}
	}
	var b strings.Builder
	b.WriteString("ws_shell whitelist is ON. Refused unless listed in shell_allow " +
		"(or SLMCODE_BASH_ALLOW): ")
	if len(refused) == 0 {
		b.WriteString("nothing — the guard reported no refusals")
	} else {
		b.WriteString(strings.Join(refused, "; "))
	}
	b.WriteString(".")
	if len(allowed) > 0 {
		b.WriteString(" Still allowed: " + strings.Join(allowed, ", ") + ".")
	}
	b.WriteString(" Dependency bootstrap (pip install / npm install / go mod tidy) " +
		"asks for approval instead of running unattended.")
	return b.String()
}

// emitShellPolicyNotice tells the operator, once per run, what the shell policy
// will and will not let the agents do. Silent policy is how a run ends in
// "it just did not install anything" with nothing in the log to explain it.
func (o *Orchestrator) emitShellPolicyNotice() {
	if o == nil || o.cfg == nil || !o.cfg.ShellWhitelist {
		return
	}
	msg := ShellWhitelistNotice
	if len(o.cfg.ShellAllow) > 0 {
		msg += " Currently allowed: " + strings.Join(o.cfg.ShellAllow, ", ") + "."
	}
	o.emitWarn("init", msg, "")
}
