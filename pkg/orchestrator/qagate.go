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

	if prep := quality.BootstrapDeps(o.cfg.Root, cmd); prep != "" {
		o.emit("test", "qa_gate bootstrap: "+truncate(prep, 120), "")
		// BootstrapDeps proposes `pip install` / `npm install` / `go mod tidy`
		// against an AGENT-AUTHORED manifest. That is arbitrary code execution
		// from model output, so it goes through the same permission layer as
		// any other command rather than running unattended.
		sr := o.runGatedCommand(ctx, prep, "qa bootstrap")
		_ = o.store.Append(contextstore.DocScratch, "QA bootstrap",
			fmt.Sprintf("cmd: %s\nok=%v\n\n%s", prep, sr.OK, truncate(sr.Output, 2000)))
		if !sr.OK {
			o.emitFullL("test", stream.KindOutput, "qa", "", "qa_gate bootstrap warning", "",
				truncate(sr.Output, 800), stream.LevelWarn)
		}
	}

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
		_ = o.store.Append(contextstore.DocScratch, "QA gate failure",
			fmt.Sprintf("round %d/%d\ncmd: %s\n\n%s", round, max, cmd, truncate(failText, 4000)))
		o.emitFullL("test", stream.KindOutput, "qa", "",
			fmt.Sprintf("qa_gate failed round %d/%d", round, max), "", truncate(failText, 1500), stream.LevelError)

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
	if round != 1 || !strings.Contains(cmd, "go test") {
		return
	}
	if fixOut := quality.AutoFixFormatting(o.cfg.Root); fixOut != "" {
		o.emit("test", "qa_gate: auto-fixed formatting: "+truncate(fixOut, 200), "")
	}
	if _, err := os.Stat(filepath.Join(o.cfg.Root, "go.mod")); err != nil {
		return
	}
	br := quality.RunSmoke(ctx, o.cfg.Root, "go build ./...", 30*time.Second)
	if !br.OK {
		o.emitWarn("test", "qa_gate: build failed — "+truncate(br.Output, 300), "")
		return
	}
	o.emit("test", "qa_gate: build OK, running full tests", "")
}

// qaDiagnoseAndFix runs the tester (diagnose) then the corrector (fix).
func (o *Orchestrator) qaDiagnoseAndFix(ctx context.Context, query, cmd, failText string) {
	o.emitAgent("test", plan.RoleTester, "", "qa_gate diagnose failures", "", "")
	testPack, _ := o.packer.Build("tester", query, contextstore.DefaultDocsForRole("tester"), nil,
		o.skillPackFor("tester", query))
	diag, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
		"\n## QA gate failure\nCommand: "+cmd+"\n\n"+truncate(failText, 6000)+
		"\n\n"+o.langHint()+"\n\nDiagnose with ws_shell if helpful. List concrete file edits needed. "+
		"Return JSON with status and issues.")
	if strings.TrimSpace(diag) != "" {
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "qa diagnose", "", truncate(diag, 1000))
	}

	o.emitAgent("test", plan.RoleCorrector, "", "qa_gate fix iteration", "", "")
	fixPack, _ := o.packer.Build("corrector", query, contextstore.DefaultDocsForRole("corrector"), nil,
		o.skillPackFor("corrector", query))
	fixPrompt := fixPack.Render() +
		"\n## Goal\nMake this command pass: `" + cmd + "`\n\n" +
		o.langHint() + "\n\n## Failure output\n" +
		truncate(failText, 5000) +
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
				truncate(sr.Output, 800), stream.LevelProblem)
			o.recordGate("regression:"+chk.ID, false, cmd)
		}
	}
}

// detectQACommand is kept for tests; delegates to quality.DetectProjectCommand.
func detectQACommand(root string) string {
	return quality.DetectProjectCommand(root)
}

func bootstrapQADeps(root, cmd string) string {
	return quality.BootstrapDeps(root, cmd)
}

// ShellWhitelistNotice is the operator-facing summary of a behavior change in
// the tool layer: `ws_shell` in whitelist mode now REFUSES the general-purpose
// interpreters and file movers unless they are explicitly allowed.
//
// Test and build runners (go test, go build, pytest, npm test, cargo test, …)
// remain auto-allowed, so the ordinary verification path is unaffected. What
// changes is the unattended dependency bootstrap: pkg/quality's BootstrapDeps
// proposes `pip install` / `npm install` / `go mod tidy` derived from an
// AGENT-AUTHORED manifest, and that now routes through the permission layer
// like any other command instead of running on its own authority.
const ShellWhitelistNotice = "ws_shell whitelist is ON: python, node, make, npx, `go run`, cp, mv and sed " +
	"are refused unless listed in shell_allow (or SLMCODE_BASH_ALLOW). Test/build runners " +
	"(go test, go build, pytest, npm test, cargo test) stay allowed. Dependency bootstrap " +
	"(pip install / npm install / go mod tidy) now asks for approval instead of running unattended."

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
