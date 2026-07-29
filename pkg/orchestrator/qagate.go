package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// runQAGate iterates a project test command until green or max rounds.
// On failure it asks the tester/corrector specialists to fix, then re-runs.
func (o *Orchestrator) runQAGate(ctx context.Context, query string, board *plan.Board) {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return
	}
	cmd := strings.TrimSpace(o.cfg.QAGateCommand)
	if cmd == "" {
		cmd = detectQACommand(o.cfg.Root)
	}
	if cmd == "" {
		o.emit("test", "qa_gate: no auto test command — set qa_gate_command", "")
		return
	}
	max := o.cfg.QAGateMaxRounds
	if max <= 0 {
		max = 3
	}

	for round := 1; round <= max; round++ {
		if err := ctx.Err(); err != nil {
			return
		}
		o.emitFull("test", stream.KindAgentStart, "qa", "",
			fmt.Sprintf("qa_gate %d/%d: %s", round, max, cmd), "", "")
		out, err := runProjectCommand(ctx, o.cfg.Root, cmd, o.cfg.TaskTimeout)
		if err == nil {
			_ = o.store.Append(contextstore.DocScratch, "QA gate", fmt.Sprintf("GREEN round %d\n\n%s", round, truncate(out, 2000)))
			o.emitFull("test", stream.KindAgentEnd, "qa", "", "qa_gate green", "", truncate(out, 800))
			return
		}
		failText := strings.TrimSpace(out + "\n" + err.Error())
		_ = o.store.Append(contextstore.DocScratch, "QA gate failure",
			fmt.Sprintf("round %d/%d\ncmd: %s\n\n%s", round, max, cmd, truncate(failText, 4000)))
		o.emitFull("test", stream.KindOutput, "qa", "",
			fmt.Sprintf("qa_gate failed round %d/%d", round, max), "", truncate(failText, 1500))

		if round == max {
			o.emitFull("test", stream.KindAgentEnd, "qa", "",
				fmt.Sprintf("qa_gate still red after %d rounds", max), "", "")
			if board != nil {
				// Surface a blocked note so humans/board see the red gate.
				for i := range board.Tasks {
					if board.Tasks[i].Column == plan.ColDone {
						board.Tasks[i].Notes = strings.TrimSpace(board.Tasks[i].Notes + "\nQA gate still failing: " + cmd)
						break
					}
				}
				o.persistBoard(board)
			}
			return
		}

		// Ask tester to diagnose, then corrector-style worker to fix.
		o.emitAgent("test", plan.RoleTester, "", "qa_gate diagnose failures", "", "")
		testPack, _ := o.packer.Build("tester", query, contextstore.DefaultDocsForRole("tester"), nil, o.skillPackFor("tester", query))
		diag, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+
			"\n## QA gate failure\nCommand: "+cmd+"\n\n"+truncate(failText, 6000)+
			"\n\nDiagnose. List concrete file edits needed. Return JSON with status and issues.")
		if strings.TrimSpace(diag) != "" {
			o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "qa diagnose", "", truncate(diag, 1000))
		}

		o.emitAgent("test", plan.RoleCorrector, "", "qa_gate fix iteration", "", "")
		fixPack, _ := o.packer.Build("corrector", query, contextstore.DefaultDocsForRole("corrector"), nil, o.skillPackFor("corrector", query))
		fixPrompt := fixPack.Render() +
			"\n## Goal\nMake this command pass: `" + cmd + "`\n\n## Failure output\n" + truncate(failText, 5000) +
			"\n\n## Diagnosis\n" + truncate(diag, 3000) +
			"\n\nUse ws_edit / ws_patch / ws_write for SMALL fixes. Then return STRICT JSON status."
		fixOut, _ := o.runRoleTracked(ctx, plan.RoleCorrector, "", fixPrompt)
		if strings.TrimSpace(fixOut) != "" {
			o.emitFull("test", stream.KindOutput, plan.RoleCorrector, "", "qa fix output", "", truncate(fixOut, 1000))
		}
	}
}

func detectQACommand(root string) string {
	if root == "" {
		return ""
	}
	if fileExists(filepath.Join(root, "go.mod")) {
		return "go test ./... -short"
	}
	if fileExists(filepath.Join(root, "package.json")) {
		// Prefer test script when present; fall back to build.
		data, _ := os.ReadFile(filepath.Join(root, "package.json"))
		if bytes.Contains(data, []byte(`"test"`)) {
			return "npm test --silent"
		}
		return ""
	}
	if fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "pytest.ini")) {
		return "python -m pytest -q"
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		return "cargo test --quiet"
	}
	if fileExists(filepath.Join(root, "Makefile")) {
		data, _ := os.ReadFile(filepath.Join(root, "Makefile"))
		if bytes.Contains(data, []byte("\ntest:")) || bytes.Contains(data, []byte("\ntest :")) {
			return "make test"
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runProjectCommand(ctx context.Context, root, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	// Cap single QA invocation so a hung test suite cannot eat the whole run.
	if timeout > 8*time.Minute {
		timeout = 8 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-lc", command)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > 20_000 {
		out = out[:20_000] + "\n...[truncated]"
	}
	return out, err
}
