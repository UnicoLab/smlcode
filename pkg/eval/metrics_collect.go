package eval

import (
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Per-run metrics for a live eval case.
//
// An eval that reports only pass/fail cannot tell you whether a change made the
// harness better — a run can fail for reasons that have nothing to do with the
// thing you changed, and a run can pass while taking twice the round-trips. The
// numbers that DO move with harness work are edit-apply rate, tool error rate,
// LLM calls per task and tokens per task, so every case now emits a
// metrics.Metrics record alongside its verdict, and Compare turns two eval runs
// into a delta.
//
// Everything here is derived from the live event stream plus the run result,
// because those are the only signals the orchestrator exposes to an embedder.
// Where a field is a lower bound rather than an exact count, it says so.
type metricsCollector struct {
	mu sync.Mutex

	fileChanges  int
	editFailures int
	// firstApplies counts file changes that did NOT follow an unrecovered edit
	// failure, i.e. edits that landed as the model emitted them. pendingEdit
	// carries the failures still waiting for their recovery.
	firstApplies  int
	pendingEdit   int
	toolErrors    int
	redundant     int
	interventions int
	agentCalls    int
	gates         []metrics.Gate
	gateSeen      map[string]bool
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{gateSeen: map[string]bool{}}
}

// observe folds one live event into the counters.
func (c *metricsCollector) observe(e orchestrator.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Kind {
	case stream.KindFileChange:
		// The workspace fired OnFileChange, i.e. an edit really landed on disk.
		c.fileChanges++
		// Attribute it: a change that follows an edit failure the harness has
		// not yet seen recovered is that failure's recovery, not a
		// first-attempt apply. A LOWER BOUND on first-attempt applies, matching
		// the rest of this collector — the pairing is by order, not by path,
		// because the event stream does not expose which failure a change fixes.
		if c.pendingEdit > 0 {
			c.pendingEdit--
			break
		}
		c.firstApplies++
	case stream.KindAgentStart:
		// One agent turn ≈ one model round-trip. It is a LOWER BOUND: a ReAct
		// agent that loops on tools makes several calls per start.
		c.agentCalls++
	case stream.KindAgentEnd:
		if e.Level == stream.LevelError || e.Level == stream.LevelProblem {
			c.gates = append(c.gates, metrics.Gate{Name: gateNameFor(e), Passed: false})
		}
	case stream.KindIntervention:
		c.interventions++
		switch e.Scope {
		case quality.InterventionLoop:
			c.redundant++
			c.toolErrors++
		case quality.InterventionMalformed, quality.InterventionWhitelist:
			c.toolErrors++
		}
		if looksLikeEditFailure(e) {
			c.editFailures++
			c.pendingEdit++
			c.toolErrors++
		}
	}
}

// gateNameFor labels a failing agent_end so the gate list is readable.
func gateNameFor(e orchestrator.Event) string {
	name := strings.TrimSpace(e.Agent)
	if name == "" {
		name = strings.TrimSpace(e.Phase)
	}
	if name == "" {
		name = "agent"
	}
	return name
}

func looksLikeEditFailure(e orchestrator.Event) bool {
	blob := strings.ToLower(e.Message + " " + e.Output)
	return strings.Contains(blob, "ws_edit") || strings.Contains(blob, "ws_patch") ||
		strings.Contains(blob, "old_str")
}

// snapshot builds the metrics record for one finished case.
func (c *metricsCollector) snapshot(res Result, out *orchestrator.Result, cfgModel, cfgProvider string,
	started time.Time) metrics.Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	m := metrics.Metrics{
		RunID:    res.ID,
		At:       started.UTC(),
		Model:    cfgModel,
		Provider: cfgProvider,
		Label:    "eval",

		Tasks:       res.TasksTotal,
		TasksPassed: res.TasksDone,

		// Applied edits are exact (the workspace reports each one). Attempts
		// are a LOWER BOUND: only failures the harness surfaced as an
		// intervention are visible from out here.
		EditsAttempted: c.fileChanges + c.editFailures,
		EditsApplied:   c.fileChanges,
		// "% of responses using the correct edit format" — the Aider number.
		// Unlike EditsApplied it does not credit an edit that only landed after
		// the harness surfaced a failure and the model tried again.
		EditsFirstAttempt: c.firstApplies,

		ToolCalls:      c.fileChanges + c.toolErrors,
		ToolErrors:     c.toolErrors,
		RedundantCalls: c.redundant,

		LLMCalls: c.agentCalls,
		WallMS:   res.Duration.Milliseconds(),

		Gates: c.gates,
	}
	if res.SmokeOK {
		m.Gates = append(m.Gates, metrics.Gate{Name: "smoke", Passed: true})
	}
	m.Gates = append(m.Gates, metrics.Gate{Name: "files", Passed: res.FilesOK})

	if out != nil && out.Usage != nil {
		m.TokensIn = out.Usage.PromptTokens
		m.TokensOut = out.Usage.CompletionTokens
	}
	// Failure accounting: without the engine's own failure events, an
	// intervention is the only visible failure. Resolution is attributed to the
	// model, never to memory, so this metric can only ever UNDER-report the
	// harness — it will not flatter a change that did nothing.
	m.Failures = c.interventions
	if res.OK {
		m.ResolvedFromLLM = c.interventions
	} else {
		m.Unresolved = c.interventions
	}
	m.Normalize(time.Now())
	return m
}
