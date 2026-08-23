package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// This file is the load-bearing half of "fail once, then never again".
//
// pkg/loop's Runner has always exposed RecordToolEvent (fold a tool call into
// working memory) and ReportToolFailure (fingerprint a failure, consult the
// repair rules, hand back REPAIRED ARGUMENTS when a deterministic fix exists).
// Nothing outside pkg/loop called either, so the engine only ever saw the
// failures the inner loop raised itself — never the tool refusals, which are
// where a small model actually fails.
//
// installToolObserver connects the two: every workspace tool call is recorded,
// every refusal is reported, and a repair that produces new arguments is
// retried IMMEDIATELY, in the tool layer, with no LLM round-trip at all. That
// last part is the whole design: an ws_edit whose old_str carries ws_read's
// `   42|` gutter is repaired and applied within the same tool call, and the
// model never learns it made a mistake.

// installToolObserver wires the workspace tool layer to the self-improvement
// engine. Safe to call when evolve is disabled: the observer degrades to a
// no-op because lastRunner()'s Evolve is nil.
func (o *Orchestrator) installToolObserver() {
	if o == nil || o.workspace == nil {
		return
	}
	o.workspace.SetToolObserver(o.observeToolCall, o.toolRetryOutcome)
}

// observeToolCall is the per-call hook. It runs inside the tool call, so it
// does no I/O beyond what working memory already does (O(1), in-process).
func (o *Orchestrator) observeToolCall(ctx context.Context, call workspace.ToolCall) workspace.ToolAdvice {
	runner := o.lastRunner()
	if runner == nil {
		return workspace.ToolAdvice{}
	}
	ev := o.toolEvent(call)
	msg := call.Failure()
	if msg == "" {
		runner.RecordToolEvent(ev)
		return workspace.ToolAdvice{}
	}
	ev.OK = false
	ev.Error = truncate(msg, 2000)

	// A repaired retry that failed AGAIN is recorded but never repaired a
	// second time: the tool layer allows exactly one deterministic retry, and
	// re-entering the repair path here would credit a rule for a fix it did
	// not make.
	if call.Retried {
		runner.RecordToolEvent(ev)
		return workspace.ToolAdvice{}
	}

	// ReportToolFailure records the event itself (with the failure
	// fingerprint attached), so this path must NOT also call RecordToolEvent.
	newArgs, guidance, retry := runner.ReportToolFailure(ctx, ev, o.toolSignal(call, msg))
	if retry && strings.TrimSpace(newArgs) != "" {
		repaired, err := decodeArgs(newArgs)
		if err != nil || len(repaired) == 0 {
			return workspace.ToolAdvice{Guidance: guidance}
		}
		o.emitFull("execute", stream.KindDebug, "evolve", "",
			"repaired "+call.Tool+" arguments from memory — retrying with no model call", "", "")
		return workspace.ToolAdvice{RetryArgs: repaired}
	}
	return workspace.ToolAdvice{Guidance: guidance}
}

// toolRetryOutcome reports back whether the repaired retry worked, so the rule
// that produced it is credited (or not). Without this the failure would stay
// "unresolved" in the reflection even though the harness fixed it, and
// ResolvedFromMemory would read zero for a run that resolved everything from
// memory.
func (o *Orchestrator) toolRetryOutcome(retryArgs map[string]interface{}, ok bool, result string) {
	runner := o.lastRunner()
	if runner == nil {
		return
	}
	encoded, err := json.Marshal(retryArgs)
	if err != nil {
		return
	}
	runner.ToolRetryOutcome(string(encoded), ok, truncate(firstLine(result), 200))
}

// toolEvent renders one call as the working-memory record.
func (o *Orchestrator) toolEvent(call workspace.ToolCall) memory.ToolEvent {
	ev := memory.ToolEvent{
		Tool:     call.Tool,
		Path:     stringArg(call.Args, "path"),
		Command:  stringArg(call.Args, "command"),
		Args:     encodeArgs(call.Args),
		OK:       true,
		At:       time.Now(),
		Duration: call.Duration,
	}
	return ev
}

// toolSignal fingerprints the failure. Language and model come from config so
// a rule learned on Go with one model is not applied blind to Python with
// another; pkg/loop fills in whatever is left blank.
func (o *Orchestrator) toolSignal(call workspace.ToolCall, msg string) evolve.Signal {
	sig := evolve.Signal{
		Tool:    call.Tool,
		Message: msg,
		Path:    stringArg(call.Args, "path"),
		Command: stringArg(call.Args, "command"),
		Phase:   "execute",
	}
	if o != nil && o.cfg != nil {
		sig.Model = o.cfg.Model
		sig.Language = detectProjectLang(o.cfg.Root)
	}
	return sig
}

// encodeArgs renders tool arguments as the compact JSON the repair transforms
// operate on. A value that will not marshal yields "" — which simply means no
// deterministic argument repair is possible for that call.
func encodeArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeArgs parses repaired arguments back into a tool-call map.
func decodeArgs(raw string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stringArg(args map[string]interface{}, key string) string {
	if len(args) == 0 {
		return ""
	}
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
