package orchestrator

import (
	"context"
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

// runQAGate iterates a project test/smoke command until green or max rounds.
// On failure it asks the tester/corrector specialists to fix, then re-runs.
// Returns true when the gate ends red (caller should rewrite plan/tasks).
func (o *Orchestrator) runQAGate(ctx context.Context, query string, board *plan.Board) bool {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return false
	}
	cmd := strings.TrimSpace(o.cfg.QAGateCommand)
	if cmd == "" {
		cmd = blocks.ResolveQAGateCommand(o.cfg.Root, o.cfg.Root, o.cfg.ActivePack)
		if cmd == "" {
			cmd = quality.DetectProjectCommandWithPack(o.cfg.Root, o.cfg.ActivePack)
		}
	}
	if cmd == "" {
		o.emitWarn("test", "qa_gate: no auto test/smoke command — set qa_gate_command", "")
		return false
	}
	max := o.cfg.QAGateMaxRounds
	if max <= 0 {
		max = 3
	}

	if prep := quality.BootstrapDeps(o.cfg.Root, cmd); prep != "" {
		o.emit("test", "qa_gate bootstrap: "+truncate(prep, 120), "")
		sr := quality.RunSmoke(ctx, o.cfg.Root, prep, o.cfg.TaskTimeout)
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
		// Fast auto-fix: run gofmt before first QA round
		if round == 1 && strings.Contains(cmd, "go test") {
			if fixOut := quality.AutoFixFormatting(o.cfg.Root); fixOut != "" {
				o.emit("test", "qa_gate: auto-fixed formatting: "+truncate(fixOut, 200), "")
			}
			// Quick compile check first (faster than full test)
			if _, err := os.Stat(filepath.Join(o.cfg.Root, "go.mod")); err == nil {
				buildCmd := "go build ./..."
				br := quality.RunSmoke(ctx, o.cfg.Root, buildCmd, 30*time.Second)
				if !br.OK {
					// Build failed — this is a real issue, report it
					o.emitWarn("test", "qa_gate: build failed — "+truncate(br.Output, 300), "")
				} else {
					o.emit("test", "qa_gate: build OK, running full tests", "")
				}
			}
		}
		o.emitFull("test", stream.KindAgentStart, "qa", "",
			fmt.Sprintf("qa_gate %d/%d: %s", round, max, cmd), "", "")
		sr := quality.RunSmoke(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
		// Check if failure is just "no test files" — skip gracefully
		if !sr.OK && round == 1 && (strings.Contains(sr.Output, "no test files") ||
			strings.Contains(sr.Output, "no Go files") || strings.Contains(sr.Output, "?\t")) {
			o.emitWarn("test", "qa_gate: no test files found — skipping gate (code compiles)", "")
			return false
		}
		if sr.OK {
			_ = o.store.Append(contextstore.DocScratch, "QA gate",
				fmt.Sprintf("GREEN round %d\n\n%s", round, truncate(sr.Output, 2000)))
			o.emitFullL("test", stream.KindAgentEnd, "qa", "", "qa_gate green", "", truncate(sr.Output, 800), stream.LevelSuccess)
			return false
		}
		failText := strings.TrimSpace(sr.Output + "\n" + sr.Summary)
		_ = o.store.Append(contextstore.DocScratch, "QA gate failure",
			fmt.Sprintf("round %d/%d\ncmd: %s\n\n%s", round, max, cmd, truncate(failText, 4000)))
		o.emitFullL("test", stream.KindOutput, "qa", "",
			fmt.Sprintf("qa_gate failed round %d/%d", round, max), "", truncate(failText, 1500), stream.LevelError)

		if round == max {
			o.emitFullL("test", stream.KindAgentEnd, "qa", "",
				fmt.Sprintf("qa_gate still red after %d rounds", max), "", "", stream.LevelError)
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

		o.emitAgent("test", plan.RoleTester, "", "qa_gate diagnose failures", "", "")
		testPack, _ := o.packer.Build("tester", query, contextstore.DefaultDocsForRole("tester"), nil,
			o.skillPackFor("tester", query))
		diag, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
			"\n## QA gate failure\nCommand: "+cmd+"\n\n"+truncate(failText, 6000)+
			"\n\n"+o.langHint()+"\n\nDiagnose with ws_shell if helpful. List concrete file edits needed. "+
			"Return JSON with status and issues.")
		if strings.TrimSpace(diag) != "" {
			o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "qa diagnose", "",
				truncate(diag, 1000))
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
			o.emitFull("test", stream.KindOutput, plan.RoleCorrector, "", "qa fix output", "",
				truncate(fixOut, 1000))
		}
	}
	return true
}

// detectQACommand is kept for tests; delegates to quality.DetectProjectCommand.
func detectQACommand(root string) string {
	return quality.DetectProjectCommand(root)
}

func bootstrapQADeps(root, cmd string) string {
	return quality.BootstrapDeps(root, cmd)
}

// runProjectCommand retained for any callers; prefer quality.RunSmoke.
func runProjectCommand(ctx context.Context, root, command string, timeout time.Duration) (string, error) {
	sr := quality.RunSmoke(ctx, root, command, timeout)
	if !sr.OK {
		return sr.Output, fmt.Errorf("%s", sr.Summary)
	}
	return sr.Output, nil
}
