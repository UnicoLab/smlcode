package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// ── W7: agent_end must carry a level ────────────────────────────────────────

func TestInferEndLevel(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"worker finished", stream.LevelSuccess},
		{"review approved=true score=90", stream.LevelSuccess},
		{"review approved=false score=20", stream.LevelProblem},
		{"deterministic smoke failed", stream.LevelProblem},
		{"claims gate failed", stream.LevelProblem},
		{"error", stream.LevelError},
		{"timed out — task needs retry/re-scope", stream.LevelError},
		{"interrupted — react checkpointed", stream.LevelWarn},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			if got := inferEndLevel(tc.msg); got != tc.want {
				t.Fatalf("inferEndLevel(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestAgentEndAlwaysCarriesALevel drives a full failing wave and asserts no
// agent_end reaches the UI with an empty Level — an empty level renders the
// icon green and counts the agent as done.
func TestAgentEndAlwaysCarriesALevel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":false,"score":5,"summary":"nope","issues":["do it"]}`
		}
		return `{"status":"done","summary":"claimed nothing","files_changed":["a.go"]}`
	}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 1

	var mu sync.Mutex
	var levels []string
	var problems int
	r.OnEventFull = func(ev LoopEvent) {
		if ev.Kind != stream.KindAgentEnd {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		levels = append(levels, ev.Level)
		if ev.Level == stream.LevelProblem || ev.Level == stream.LevelError {
			problems++
		}
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	if len(levels) == 0 {
		t.Fatal("no agent_end events were emitted")
	}
	for i, lv := range levels {
		if lv == "" {
			t.Fatalf("agent_end #%d has an empty Level — it would render as a success", i)
		}
	}
	if problems == 0 {
		t.Fatalf("a rejected task must produce at least one problem/error agent_end, got %v", levels)
	}
}

func TestEmitTokenCarriesTypedPayload(t *testing.T) {
	var got []LoopEvent
	r := &Runner{OnEventFull: func(ev LoopEvent) { got = append(got, ev) }}
	sink := r.TokenSink("worker", "T1")
	sink("hel", 1)
	sink("lo", 2)
	sink("", 3) // empty deltas are dropped
	if len(got) != 2 {
		t.Fatalf("emitted %d token events, want 2", len(got))
	}
	for i, ev := range got {
		if ev.Kind != stream.KindToken {
			t.Fatalf("event %d kind=%q", i, ev.Kind)
		}
		if ev.Agent != "worker" || ev.TaskID != "T1" {
			t.Fatalf("event %d missing agent/task: %+v", i, ev)
		}
		tok, ok := ev.Data.(stream.Token)
		if !ok {
			t.Fatalf("event %d data is %T, want stream.Token", i, ev.Data)
		}
		if tok.Delta == "" || tok.Tokens == 0 {
			t.Fatalf("event %d payload = %+v", i, tok)
		}
	}
}

// TestLegacyEventSinkStillReceivesEverything guards the orchestrator contract:
// OnEvent must keep working unchanged when OnEventFull is not set.
func TestLegacyEventSinkStillReceivesEverything(t *testing.T) {
	var legacy, full int
	r := &Runner{
		OnEvent:     func(kind, agent, taskID, msg, scope, output string) { legacy++ },
		OnEventFull: func(ev LoopEvent) { full++ },
	}
	r.fire(stream.KindAgentStart, "worker", "T1", "start", "", "")
	r.fireLevel(stream.KindAgentEnd, "worker", "T1", "worker finished", "", "", stream.LevelSuccess)
	if legacy != 2 || full != 2 {
		t.Fatalf("legacy=%d full=%d, want 2 and 2", legacy, full)
	}
}

// ── W1: per-task tool isolation ─────────────────────────────────────────────

// taskIDExec records the workspace task id carried by the ctx of each call.
type taskIDExec struct {
	mu  sync.Mutex
	ids []string
}

func (e *taskIDExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	e.ids = append(e.ids, workspace.TaskIDFrom(ctx))
	e.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		body := `{"approved":true,"score":90,"summary":"ok"}`
		if req.AgentID != plan.RoleReviewer {
			body = "Observation: ws_edit edited a.go (1 replacement(s))\n" +
				`{"status":"done","summary":"done","files_changed":["a.go"]}`
		}
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: body}
	}
	return out, nil
}

// TestPerTaskToolIsolation asserts OnTaskStart fires once per task and that a
// single-task call carries its task id in ctx, so the tool layer's loop guard
// keeps a per-task history instead of one shared "" bucket.
func TestPerTaskToolIsolation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &taskIDExec{}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0

	var mu sync.Mutex
	var started []string
	r.OnTaskStart = func(id string) {
		mu.Lock()
		started = append(started, id)
		mu.Unlock()
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update a.go", Acceptance: "a.go changed", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	if len(started) != 1 || started[0] != "T1" {
		t.Fatalf("OnTaskStart calls = %v, want [T1]", started)
	}
	exec.mu.Lock()
	ids := append([]string(nil), exec.ids...)
	exec.mu.Unlock()
	if len(ids) == 0 {
		t.Fatal("executor was never called")
	}
	if ids[0] != "T1" {
		t.Fatalf("worker call ctx task id = %q, want T1 — every parallel task would share one loop-guard bucket", ids[0])
	}
}

// TestBatchedWaveCarriesOneTaskIDPerCall is the batched half of per-task tool
// isolation.
//
// workspace.WithTaskID keys the tool layer's loop guard, and a batched call has
// ONE ctx for N tasks — so every task in a multi-task wave landed in the shared
// "" bucket and tripped its neighbours' loop detection. GoLangGraph's executor
// hands the same ctx to every subagent goroutine and has no per-request hook,
// so the loop dispatches one request per task instead.
func TestBatchedWaveCarriesOneTaskIDPerCall(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &taskIDExec{}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0
	r.MaxParallel = 3

	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update a.go", Acceptance: "a.go changed", Files: []string{"a.go"}},
		{ID: "T2", Title: "Edit b", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update b.go", Acceptance: "b.go changed", Files: []string{"b.go"}},
		{ID: "T3", Title: "Edit c", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update c.go", Acceptance: "c.go changed", Files: []string{"c.go"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	exec.mu.Lock()
	ids := append([]string(nil), exec.ids...)
	exec.mu.Unlock()

	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatalf("a wave call carried NO task id (%v) — every task shares the loop-guard bucket", ids)
		}
		seen[id] = true
	}
	for _, want := range []string{"T1", "T2", "T3"} {
		if !seen[want] {
			t.Fatalf("no call carried task id %s; saw %v", want, ids)
		}
	}
}

// TestDispatchWavePreservesResultOrder pins the contract collectWaveResults
// depends on: results[j] must correspond to reqs[j], however the calls were
// scheduled.
func TestDispatchWavePreservesResultOrder(t *testing.T) {
	exec := &echoTaskExec{}
	r := &Runner{Executor: exec, Log: func(string, ...interface{}) {}}
	reqs := []ggagent.SubAgentRequest{
		{AgentID: "worker", TaskID: "T1"},
		{AgentID: "worker", TaskID: "T2"},
		{AgentID: "worker", TaskID: "T3"},
	}
	results, err := r.dispatchWave(context.Background(), reqs)
	if err != nil {
		t.Fatalf("dispatchWave: %v", err)
	}
	if len(results) != len(reqs) {
		t.Fatalf("got %d results for %d requests", len(results), len(reqs))
	}
	for i, res := range results {
		if res.TaskID != reqs[i].TaskID {
			t.Fatalf("results[%d].TaskID = %q, want %q", i, res.TaskID, reqs[i].TaskID)
		}
		if res.Output != "ctx="+reqs[i].TaskID {
			t.Fatalf("results[%d] ran under ctx %q, want the request's own task id %q",
				i, res.Output, reqs[i].TaskID)
		}
	}
}

// echoTaskExec echoes the workspace task id its ctx carried, so a mismatch
// between request and ctx is visible in the result.
type echoTaskExec struct{}

func (echoTaskExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: "ctx=" + workspace.TaskIDFrom(ctx),
		}
	}
	return out, nil
}

// TestStartTaskResetsBudget covers the budget half of the per-task reset.
func TestStartTaskResetsBudget(t *testing.T) {
	r := &Runner{MaxTaskCalls: 2}
	if !r.spend("T1", "worker") || !r.spend("T1", "review") {
		t.Fatal("first two calls must be within budget")
	}
	if r.spend("T1", "correct") {
		t.Fatal("third call must be refused")
	}
	if !r.budgetExhausted("T1") {
		t.Fatal("budget should report exhausted")
	}
	r.startTask(context.Background(), "T1")
	if r.budgetExhausted("T1") {
		t.Fatal("starting the task again must reset its budget")
	}
	// A different task has its own bucket.
	if r.budget().spent("T2") != 0 {
		t.Fatal("budgets must be per task")
	}
}

// ── W5: role resolution ─────────────────────────────────────────────────────

func TestResolveRoleAndBuiltinSlot(t *testing.T) {
	r := &Runner{Log: func(string, ...interface{}) {}}
	if got := r.resolveRole("reviewer"); got != "reviewer" {
		t.Fatalf("nil ResolveRole must be identity, got %q", got)
	}
	if _, ok := r.resolveBuiltinSlot(roleReviewerStrict); !ok {
		t.Fatal("reviewer-strict must now be a registered agent — the second-reviewer path never ran while it was not")
	}
	r.ResolveRole = func(id string) string {
		if id == roleReviewerStrict {
			return "go-reviewer-strict" // a pipeline-specific override that is not registered
		}
		return id
	}
	var warned bool
	r.OnEventFull = func(ev LoopEvent) {
		if ev.Level == stream.LevelWarn && strings.Contains(ev.Message, "unknown agent role") {
			warned = true
		}
	}
	got, ok := r.resolveBuiltinSlot(roleReviewerStrict)
	if ok {
		t.Fatalf("an unregistered role must not be used, got %q", got)
	}
	if !warned {
		t.Fatal("an unknown slot role must be reported loudly")
	}
}

// TestStrictReviewerSlotIsOfferedAtCapacity asserts the second reviewer is now
// really part of the race. speculate.go referenced "reviewer-strict" while no
// such agent was registered, so every strict slot returned
// "subagent 'reviewer-strict' not found" and the path never once ran.
func TestStrictReviewerSlotIsOfferedAtCapacity(t *testing.T) {
	r := &Runner{Log: func(string, ...interface{}) {}}
	cases := []struct {
		name        string
		maxParallel int
		resolve     func(string) string
		wantRoles   []string
	}{
		{"two slots below capacity", 2, nil, []string{"acceptance", plan.RoleReviewer}},
		{"strict joins at capacity", 3, nil,
			[]string{"acceptance", plan.RoleReviewer, roleReviewerStrict}},
		{"unregistered override is dropped, not dispatched", 4,
			func(id string) string {
				if id == roleReviewerStrict {
					return "no-such-agent"
				}
				return id
			},
			[]string{"acceptance", plan.RoleReviewer}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.MaxParallel = tc.maxParallel
			r.ResolveRole = tc.resolve
			slots := r.reviewSlots(plan.Task{ID: "T1"}, gateState{}, nil, "prompt", plan.RoleReviewer)
			var roles []string
			for _, s := range slots {
				roles = append(roles, s.Role)
			}
			if strings.Join(roles, ",") != strings.Join(tc.wantRoles, ",") {
				t.Fatalf("slot roles = %v, want %v", roles, tc.wantRoles)
			}
		})
	}
}

// ── W6: evolve accessors are nil-safe and drainable ─────────────────────────

func TestEvolveAccessorsAreNilSafe(t *testing.T) {
	r := &Runner{Root: t.TempDir(), Log: func(string, ...interface{}) {}} // Evolve is nil

	// Nothing must panic, and the memory block must be empty.
	if got := r.memorySection(plan.RoleWorker); got != "" {
		t.Fatalf("memorySection with a nil engine = %q, want empty", got)
	}
	if got := r.editFormatSection(); got != "" {
		t.Fatalf("editFormatSection with a nil engine = %q, want empty", got)
	}
	if got := r.chooseEditFormat(); got != "search_replace" {
		t.Fatalf("chooseEditFormat fallback = %q", got)
	}
	r.RecordToolEvent(memory.ToolEvent{Tool: "ws_edit", Path: "a.go", OK: true})

	adv := r.noteFailure(evolve.Signal{
		Tool: "ws_edit", Message: "old_str not found in a.go", Language: "go",
	}, `{"path":"a.go"}`)
	if adv.Fingerprint.Zero() {
		t.Fatal("a failure must always be fingerprinted, even with no engine")
	}
	if adv.Found {
		t.Fatal("no rule store means nothing can be found")
	}

	events := r.FailureEvents()
	if len(events) != 1 {
		t.Fatalf("FailureEvents = %d, want 1", len(events))
	}
	if events[0].Resolved() {
		t.Fatal("the failure is not resolved yet")
	}
	r.noteResolved(adv, "re-read then retried")
	if !r.FailureEvents()[0].Resolved() {
		t.Fatal("noteResolved must mark the accumulated event")
	}

	drained := r.DrainFailureEvents()
	if len(drained) != 1 {
		t.Fatalf("DrainFailureEvents = %d, want 1", len(drained))
	}
	if len(r.FailureEvents()) != 0 {
		t.Fatal("draining must clear the accumulator")
	}
	if r.DrainDecisionRecords() != nil && len(r.DrainDecisionRecords()) != 0 {
		t.Fatal("no decisions were recorded")
	}

	// ReportToolFailure with no engine advises nothing but still records.
	newArgs, guidance, retry := r.ReportToolFailure(context.Background(),
		memory.ToolEvent{Tool: "ws_edit", Path: "a.go", Error: "old_str not found"},
		evolve.Signal{})
	if newArgs != "" || guidance != "" || retry {
		t.Fatalf("nil engine must advise nothing, got (%q,%q,%v)", newArgs, guidance, retry)
	}
	if len(r.FailureEvents()) != 1 {
		t.Fatal("ReportToolFailure must still accumulate the failure")
	}
}

// TestFailureEventsAccumulateFromTheWave asserts a real run feeds the
// accumulator the orchestrator drains.
func TestFailureEventsAccumulateFromTheWave(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer {
			return `{"approved":false,"score":5,"summary":"no evidence","issues":["prove it"]}`
		}
		return `{"status":"done","summary":"claimed","files_changed":["a.go"]}`
	}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 1

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	events := r.DrainFailureEvents()
	if len(events) == 0 {
		t.Fatal("a rejected + escalated task must produce failure events for reflection")
	}
	var phases []string
	for _, e := range events {
		phases = append(phases, e.Signal.Phase)
	}
	sort.Strings(phases)
	if !containsString(phases, "review") {
		t.Fatalf("review rejections must be recorded, got phases %v", phases)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ── gate-signal struct ──────────────────────────────────────────────────────

func TestGateStateFastPathAndRejectReason(t *testing.T) {
	cases := []struct {
		name       string
		g          gateState
		role       string
		wantFast   bool
		wantReject string
	}{
		{"rename on disk always wins", gateState{renameDisk: true, smokeFail: true}, plan.RoleWorker, true, ""},
		{"disk write approves", gateState{diskWrite: true}, plan.RoleWorker, true, ""},
		{"tester never fast-paths", gateState{diskWrite: true}, plan.RoleTester, false, ""},
		{"smoke failure blocks", gateState{diskWrite: true, smokeFail: true}, plan.RoleWorker, false,
			"rejected: deterministic smoke failed"},
		{"claims failure has priority", gateState{diskWrite: true, claimsFail: true, smokeFail: true},
			plan.RoleWorker, false, "rejected: hallucinated files_changed paths"},
		{"out-of-scope claim blocks", gateState{diskWrite: true, scopeWhy: "out-of-scope files_changed: main.go"},
			plan.RoleWorker, false, ""},
		{"no evidence at all", gateState{}, plan.RoleWorker, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.g.fastPath(tc.role); got != tc.wantFast {
				t.Fatalf("fastPath = %v, want %v", got, tc.wantFast)
			}
			if tc.wantReject != "" {
				summary, issue := tc.g.rejectReason()
				if summary != tc.wantReject {
					t.Fatalf("rejectReason summary = %q, want %q", summary, tc.wantReject)
				}
				if issue == "" {
					t.Fatal("rejectReason must always produce an issue for the corrector")
				}
			}
		})
	}
}

// ── W7b: the structured sink is the ONLY sink the bridge needs ──────────────

// failingExec makes the worker call fail outright.
type failingExec struct{}

func (failingExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Error: errors.New("provider refused the request"),
		}
	}
	return out, errors.New("provider refused the request")
}

// bridgedEvent mirrors what pkg/orchestrator's buildRunner does with a
// LoopEvent: it forwards Kind, Level and Data into a stream.Event. Anything
// this conversion cannot carry never reaches the CLI.
func bridgedEvent(ev LoopEvent) stream.Event {
	return stream.Event{
		Phase: "execute", Kind: ev.Kind, Level: ev.Level, Message: ev.Message,
		TaskID: ev.TaskID, Agent: ev.Agent, Scope: ev.Scope, Output: ev.Output,
		Data: ev.Data,
	}
}

// TestFailingAgentReachesTheUIAsAnError drives a wave whose agent fails and
// asserts the resulting stream.Event carries an error-class Level.
//
// Routing the bridge through the legacy six-string AgentEvent sink dropped
// Level entirely, so a failed agent arrived at the CLI with Level == "" and was
// rendered — and counted — as a success.
func TestFailingAgentReachesTheUIAsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := defaultRunner(t, root, failingExec{})
	r.MaxRetries = 0

	var mu sync.Mutex
	var events []stream.Event
	r.OnEventFull = func(ev LoopEvent) {
		mu.Lock()
		events = append(events, bridgedEvent(ev))
		mu.Unlock()
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Edit a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "implement a.go", Acceptance: "a.go updated", Files: []string{"a.go"},
	}}}
	_ = r.RunBoard(context.Background(), board)

	mu.Lock()
	defer mu.Unlock()
	var ends int
	var errored bool
	for _, e := range events {
		if e.Kind != stream.KindAgentEnd {
			continue
		}
		ends++
		if e.Level == "" {
			t.Fatalf("agent_end reached the UI with no level: %+v", e)
		}
		if e.Level == stream.LevelError || e.Level == stream.LevelProblem {
			errored = true
		}
	}
	if ends == 0 {
		t.Fatal("a failing agent must still emit agent_end")
	}
	if !errored {
		t.Fatal("a failing agent must reach the UI with an error/problem level, not a success")
	}
}

// TestTokenDeltaSurvivesTheBridge asserts the typed payload — not just the
// message string — survives the LoopEvent → stream.Event conversion the
// orchestrator performs. stream.Token is what pkg/cli type-asserts on to
// render live token text and keep a running token count.
func TestTokenDeltaSurvivesTheBridge(t *testing.T) {
	var got []stream.Event
	r := &Runner{OnEventFull: func(ev LoopEvent) { got = append(got, bridgedEvent(ev)) }}
	r.TokenSink("worker", "T1")("func Add(", 7)

	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1", len(got))
	}
	e := got[0]
	if e.Kind != stream.KindToken || e.Agent != "worker" || e.TaskID != "T1" {
		t.Fatalf("token event lost its identity: %+v", e)
	}
	tok, ok := e.Data.(stream.Token)
	if !ok {
		t.Fatalf("Data is %T, want stream.Token — the CLI type-asserts on this", e.Data)
	}
	if tok.Delta != "func Add(" || tok.Tokens != 7 {
		t.Fatalf("token payload = %+v, want {func Add( 7}", tok)
	}
	if e.Level == "" {
		t.Fatal("every event must carry a level")
	}
}

// TestOnlyOneSinkIsInstalledByTheBridge documents why the orchestrator must set
// OnEventFull ALONE: fireEvent mirrors to the legacy sink too, so installing
// both would emit every event twice.
func TestOnlyOneSinkIsInstalledByTheBridge(t *testing.T) {
	var full, legacy int
	r := &Runner{OnEventFull: func(LoopEvent) { full++ }}
	r.fire(stream.KindAgentEnd, "worker", "T1", "worker finished", "", "")
	if full != 1 || legacy != 0 {
		t.Fatalf("full=%d legacy=%d, want 1 and 0", full, legacy)
	}
	r.OnEvent = func(string, string, string, string, string, string) { legacy++ }
	r.fire(stream.KindAgentEnd, "worker", "T1", "worker finished", "", "")
	if full != 2 || legacy != 1 {
		t.Fatalf("with both sinks set every event is delivered twice: full=%d legacy=%d", full, legacy)
	}
}
