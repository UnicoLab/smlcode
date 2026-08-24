package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// groundWorkerOutput is a finalize that claims a command this project has never
// been observed using. Everything else about it is well-formed, which is the
// point: no other gate has anything to say about it.
const groundWorkerOutput = "Implemented the parser.\n\n" +
	"```bash\n" +
	"$ npm test\n" +
	"PASS  src/parser.test.js\n" +
	"```\n\n" +
	`{"status":"done","summary":"parser","files_changed":["parser.go"]}`

// groundRunner builds a Runner with every disk-backed gate OFF, so anything
// that lands in t.Output came from the knowledge grounding and nothing else.
func groundRunner(t *testing.T, eng *evolve.Engine) *Runner {
	t.Helper()
	return &Runner{Root: t.TempDir(), Evolve: eng, Log: func(string, ...interface{}) {}}
}

// groundEngine opens a real evolve engine over a temp dir and records `subject`
// as a command that works here, `support` times over.
func groundEngine(t *testing.T, subject string, support int) *evolve.Engine {
	t.Helper()
	dir := t.TempDir()
	eng, err := evolve.OpenWith(dir, dir, evolve.EngineOptions{
		Deterministic: true, ProjectPolicy: true, NoSeedRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if subject == "" {
		return eng
	}
	facts := eng.Memory().Semantic()
	for i := 0; i < support; i++ {
		facts.Observe(memory.Fact{
			Kind:    memory.FactCommand,
			Subject: subject,
			Text:    "`" + subject + "` works here",
		})
	}
	if f, ok := facts.Get(memory.FactCommand, subject); !ok || f.Confidence < 0.6 {
		t.Fatalf("fixture fact is not confident enough: %+v", f)
	}
	return eng
}

// TestRunGatesGroundsClaimsAgainstStoredKnowledge is the wiring proof: a claim
// that disagrees with semantic memory reaches the reviewer as a bounded,
// harness-stamped section, and it does so without disturbing the model's own
// answer.
func TestRunGatesGroundsClaimsAgainstStoredKnowledge(t *testing.T) {
	r := groundRunner(t, groundEngine(t, "go test ./...", 4))
	task := &plan.Task{ID: "T1", Role: plan.RoleWorker, Output: groundWorkerOutput}

	r.runGates(context.Background(), task, plan.RoleWorker, nil, gateOpts{})

	if !strings.Contains(task.Output, quality.KnowledgeSectionHeader) {
		t.Fatalf("knowledge conflicts never reached the reviewer:\n%s", task.Output)
	}
	for _, want := range []string{"npm test", "go test ./...", "decision: " + quality.DecisionRevise, "required_evidence:"} {
		if !strings.Contains(task.Output, want) {
			t.Fatalf("section is missing %q:\n%s", want, task.Output)
		}
	}
	// Appended, never spliced: the model's answer must still come first.
	if strings.Index(task.Output, quality.KnowledgeSectionHeader) < strings.Index(task.Output, `"status":"done"`) {
		t.Fatalf("section was inserted before the model answer:\n%s", task.Output)
	}
	// And it must strip cleanly, so completeness is still judged on the answer.
	core := stripPostSections(task.Output)
	if strings.Contains(core, quality.KnowledgeSectionHeader) {
		t.Fatalf("harness section leaked into the model-answer view:\n%q", core)
	}
	if !multipass.LooksCompleteJSON(core) {
		t.Fatalf("grounding broke completeness detection:\n%q", core)
	}
	// It is evidence for the reviewer, never a verdict: the section must not
	// trip any of the gates that today only fire on disk-backed evidence.
	if quality.SmokeFailedInOutput(task.Output) || quality.StaticFailedInOutput(task.Output) ||
		quality.ClaimsFailedInOutput(task.Output) || quality.AcceptanceFailedInOutput(task.Output) {
		t.Fatalf("knowledge grounding tripped a disk-backed gate:\n%s", task.Output)
	}
}

// TestRunGatesKnowledgeGroundingIsANoRegressionWhenMemoryIsDisabled is the
// no-regression guarantee. With Evolve nil — `--no-evolve`, a store that failed
// to open, any harness that never wired memory up — runGates must produce the
// byte-identical output it produced before this feature existed.
func TestRunGatesKnowledgeGroundingIsANoRegressionWhenMemoryIsDisabled(t *testing.T) {
	cases := []struct {
		name string
		eng  *evolve.Engine
	}{
		{"memory disabled", nil},
		{"memory enabled but empty", groundEngine(t, "", 0)},
		{"memory enabled, claim matches the recorded command", groundEngine(t, "npm test", 4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := groundRunner(t, tc.eng)
			task := &plan.Task{ID: "T1", Role: plan.RoleWorker, Output: groundWorkerOutput}

			r.runGates(context.Background(), task, plan.RoleWorker, nil, gateOpts{})

			if task.Output != groundWorkerOutput {
				t.Fatalf("output is not byte-identical to the pre-feature behavior:\nwant %q\ngot  %q",
					groundWorkerOutput, task.Output)
			}
		})
	}
}

// A nil Runner and a Runner with no store must both be silent rather than panic:
// this path runs on every worker answer, and it may never fail a task.
func TestKnowledgeConflictSectionIsBestEffort(t *testing.T) {
	var nilRunner *Runner
	if got := nilRunner.knowledgeConflictSection(plan.Task{Output: groundWorkerOutput}); got != "" {
		t.Fatalf("nil runner produced %q", got)
	}
	if got := (&Runner{}).knowledgeConflictSection(plan.Task{Output: groundWorkerOutput}); got != "" {
		t.Fatalf("runner without an engine produced %q", got)
	}
	r := groundRunner(t, groundEngine(t, "go test ./...", 4))
	if got := r.knowledgeConflictSection(plan.Task{Output: ""}); got != "" {
		t.Fatalf("empty output produced %q", got)
	}
}

// The harness must not contradict itself with its own evidence: the commands
// quoted inside a harness-authored smoke section are the HARNESS's, not claims
// the model made.
func TestKnowledgeGroundingIgnoresHarnessAuthoredSections(t *testing.T) {
	r := groundRunner(t, groundEngine(t, "go test ./...", 4))
	out := appendHarnessSection(
		`{"status":"done","summary":"ok","files_changed":["a.go"]}`,
		quality.FormatAcceptanceSection(quality.SmokeResult{
			Ran: true, OK: true, Command: "npm test", Output: "",
		}),
	)
	if got := r.knowledgeConflictSection(plan.Task{Output: out}); got != "" {
		t.Fatalf("the harness contradicted its own evidence:\n%s", got)
	}
}
