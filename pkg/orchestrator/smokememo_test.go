package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/quality"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// The memo: a command asked of a tree nothing has written to since it last
// answered is answered from memory; any write — a NEW file or a REWRITE of one
// already changed — or a non-agent mutation invalidates it.
func TestSmokeMemoAnswersAnUnchangedTreeOnce(t *testing.T) {
	o := testOrch(t, nil)
	var mu sync.Mutex
	runs := map[string]int{}
	o.qaSmoke = func(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
		mu.Lock()
		runs[cmd]++
		mu.Unlock()
		return quality.SmokeResult{Ran: true, OK: true, Command: cmd, Output: "ok", Duration: time.Second}
	}
	count := func(cmd string) int {
		mu.Lock()
		defer mu.Unlock()
		return runs[cmd]
	}
	ctx := context.Background()
	const cmd = "go test ./..."

	o.runSmoke(ctx, cmd)
	o.runSmoke(ctx, cmd)
	o.runSmokeIn(ctx, cmd, 30*time.Second) // a different timeout is the same question
	if n := count(cmd); n != 1 {
		t.Fatalf("unchanged tree: command ran %d time(s), want 1", n)
	}

	o.noteChangedFiles("x.go")
	o.runSmoke(ctx, cmd)
	if n := count(cmd); n != 2 {
		t.Fatalf("after a write: %d run(s), want 2", n)
	}

	// A rewrite of the SAME file is a change: the path set is unchanged, the
	// write sequence is not.
	o.noteChangedFiles("x.go")
	o.runSmoke(ctx, cmd)
	if n := count(cmd); n != 3 {
		t.Fatalf("after a rewrite of an already-changed file: %d run(s), want 3", n)
	}

	// A mutation outside the tool layer (formatter, dependency install).
	o.noteTreeMutation()
	o.runSmoke(ctx, cmd)
	if n := count(cmd); n != 4 {
		t.Fatalf("after a tree mutation: %d run(s), want 4", n)
	}

	// Another command is another question.
	o.runSmoke(ctx, "npm test")
	o.runSmoke(ctx, "npm test")
	if n := count("npm test"); n != 1 {
		t.Fatalf("second command ran %d time(s), want 1", n)
	}
	if n := count(cmd); n != 4 {
		t.Fatalf("the first command's memo was disturbed by the second: %d", n)
	}

	// A new run forgets everything.
	o.resetObjectiveProbes()
	o.runSmoke(ctx, cmd)
	if n := count(cmd); n != 5 {
		t.Fatalf("after a run reset: %d run(s), want 5", n)
	}
}

// What is NOT remembered: a command that could not start, and one that hit
// its timeout — the second answers "at least this long", not "it fails".
func TestSmokeMemoRefusesUnusableResults(t *testing.T) {
	o := testOrch(t, nil)
	var mu sync.Mutex
	n := 0
	var result quality.SmokeResult
	o.qaSmoke = func(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
		mu.Lock()
		n++
		mu.Unlock()
		r := result
		r.Command = cmd
		return r
	}
	ctx := context.Background()

	result = quality.SmokeResult{Ran: false, Summary: "npm: not found"}
	o.runSmoke(ctx, "npm test")
	o.runSmoke(ctx, "npm test")
	if n != 2 {
		t.Fatalf("a result that never ran was remembered: %d run(s)", n)
	}

	result = quality.SmokeResult{Ran: true, OK: false, Duration: 30 * time.Second, Summary: "killed"}
	o.runSmokeIn(ctx, "slow", 30*time.Second)
	o.runSmokeIn(ctx, "slow", 30*time.Second)
	if n != 4 {
		t.Fatalf("a timed-out result was remembered: %d run(s)", n)
	}
}

// passingExec is a countingExec whose tester PASSES with shell evidence, so a
// run can travel the whole finish path without a corrective wave.
type passingExec struct{ countingExec }

func (p *passingExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	shared *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out, err := p.countingExec.ExecuteSubAgents(ctx, reqs, shared)
	for i, r := range reqs {
		if strings.Contains(r.AgentID, "tester") {
			out[i].Output = "Observation: ws_shell `" + objectiveCmd + "` exit status 0\nok\tstats\t0.2s\n" +
				`{"passed":true,"commands":["` + objectiveCmd + `"],"summary":"green","failures":[]}`
		}
	}
	return out, err
}

// The finish path used to run the objective command up to four times on an
// unchanged tree: the pre-test, the team gate, QA round 1, the final check.
// With nothing written after the pre-test, it runs ONCE.
//
// The baseline is marked green so the early finish does not take the short
// way out (a green that was green before verifies nothing): the run has to go
// through the tester, the quality gates and the QA gate's first round, which
// is exactly the stretch where the repeated runs used to happen.
func TestFinishPathRunsTheObjectiveCommandOnceOnAnUnchangedTree(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tstats\t0.2s\n"}
	exec := &passingExec{}
	o := objectiveOrch(t, gate, exec, nil)
	o.mu.Lock()
	o.objective.baselineKnown, o.objective.baselineGreen = true, true
	o.mu.Unlock()

	res := finalize(t, o, doneBoard())

	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("objective command ran %d time(s) through the finish path on an unchanged tree, want exactly 1",
			n)
	}
	if o.lastRunner().CorrectiveRuns() != 0 {
		t.Fatalf("a passing tester still scheduled corrective waves: %s", res.Summary)
	}
}

// The QA gate's first round says, in its own events, that it reused the
// pre-test — and re-runs when the tree moved in between.
func TestQAGateFirstRoundReusesThePreTest(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tstats\t0.2s\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	rec := &recorder{}
	o.onEvent = rec.handle

	pre := o.runDeterministicPreTest(context.Background())
	if !pre.Ran || pre.Fingerprint == "" {
		t.Fatalf("pre-test did not run or carries no fingerprint: %+v", pre)
	}
	if red := o.runQAGate(context.Background(), "implement the thing", doneBoard(), pre); red {
		t.Fatal("a green pre-test made the QA gate red")
	}
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("QA round 1 re-ran the command (%d total) instead of reusing the pre-test", n)
	}
	if !strings.Contains(rec.text(), "reusing the pre-test") {
		t.Fatal("the reuse is not visible in the gate's events")
	}

	// The tree moved: the pre-test no longer describes it.
	o.noteChangedFiles("stats.go")
	_ = o.runQAGate(context.Background(), "implement the thing", doneBoard(), pre)
	if n := gate.count(objectiveCmd); n != 2 {
		t.Fatalf("after a write the gate still reused the stale pre-test (%d runs)", n)
	}
}
