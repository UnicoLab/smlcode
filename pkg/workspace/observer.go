package workspace

import (
	"context"
	"strings"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// The tool layer is where the harness finds out what actually happened: a tool
// either did the thing or refused, and the refusal text is the single richest
// failure signal in the system. Nothing upstream could see it — pkg/loop's
// Runner exposed RecordToolEvent and ReportToolFailure and no caller existed,
// so "fail once, then never again" only worked for failures the inner loop
// happened to raise itself.
//
// ToolObserver is that missing seam. It is deliberately free of any
// pkg/evolve or pkg/memory dependency: this package must stay a leaf, so the
// observer receives the raw call/result and decides for itself whether it was
// a failure and what to do about it. pkg/orchestrator supplies the
// implementation that talks to the self-improvement engine.

// ToolCall is one completed tool invocation, as the observer sees it.
type ToolCall struct {
	// Tool is the registered tool name ("ws_edit").
	Tool string
	// Args are the arguments the call was made with. The observer must treat
	// them as read-only; a repaired retry returns a NEW map.
	Args map[string]interface{}
	// Result is the tool's string result ("" when the tool returned a
	// non-string value or an error).
	Result string
	// Err is the hard error, when the tool returned one. Most workspace tools
	// report a refusal as a successful call whose RESULT explains the refusal,
	// so an observer that only looks at Err sees almost nothing.
	Err error
	// Duration is how long the call took.
	Duration time.Duration
	// Retried reports that this call is itself a harness-repaired retry, so an
	// observer cannot recurse or double-count.
	Retried bool
}

// ToolAdvice is what an observer sends back.
type ToolAdvice struct {
	// RetryArgs, when non-nil, are repaired arguments for the SAME tool. The
	// wrapper retries immediately with them and no LLM round-trip — that
	// immediate retry is the whole point of the seam.
	RetryArgs map[string]interface{}
	// Guidance is text to append to the result the agent sees when no
	// deterministic repair was available.
	Guidance string
}

// ToolObserver observes one completed tool call and may repair it.
//
// It runs on the tool call's own goroutine, inside the call, so it must be
// cheap and must not block. It may be called concurrently for parallel agents.
type ToolObserver func(context.Context, ToolCall) ToolAdvice

// ToolRetrySink is told whether a repaired retry actually worked, so the
// observer can credit (or blame) whatever produced the repair. It is called
// only for calls the observer repaired.
type ToolRetrySink func(retryArgs map[string]interface{}, ok bool, result string)

// SetToolObserver installs (or clears, with nil) the per-call observer.
//
// Safe to call after registration: the wrapper reads the observer at call
// time, so the orchestrator can build its workspace first and connect the
// inner loop's Runner later, which is the order those two are constructed in.
func (w *Workspace) SetToolObserver(fn ToolObserver, done ToolRetrySink) {
	if w == nil {
		return
	}
	w.obsMu.Lock()
	w.observer = fn
	w.retrySink = done
	w.obsMu.Unlock()
}

func (w *Workspace) toolObserver() (ToolObserver, ToolRetrySink) {
	if w == nil {
		return nil, nil
	}
	w.obsMu.RLock()
	defer w.obsMu.RUnlock()
	return w.observer, w.retrySink
}

// observed wraps one tool executor so every completed call reaches the
// observer, and a repaired call is retried in place.
//
// Placement matters. This sits INSIDE the CallTracker's loop guard and outside
// the hooks/cap layers, so:
//   - the observer sees the final, capped result text the agent would have
//     seen, which is what the failure classifier is written against;
//   - a harness-repaired retry does not spend one of the agent's tool-call
//     budget entries, and does not read as a repeated call to the loop guard.
func (w *Workspace) observed(name string, fn tools.ToolExecutor) tools.ToolExecutor {
	if fn == nil {
		return fn
	}
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		start := time.Now()
		out, err := fn(ctx, args)
		obs, sink := w.toolObserver()
		if obs == nil {
			return out, err
		}
		call := ToolCall{
			Tool:     name,
			Args:     args,
			Result:   resultString(out),
			Err:      err,
			Duration: time.Since(start),
		}
		adv := obs(ctx, call)
		if len(adv.RetryArgs) == 0 {
			if adv.Guidance != "" && err == nil {
				return appendGuidance(call.Result, adv.Guidance), nil
			}
			return out, err
		}

		// ONE deterministic retry. A repair that does not work on the first
		// try is a repair that does not work; looping here would turn a bad
		// rule into an infinite tool loop.
		start = time.Now()
		out2, err2 := fn(ctx, adv.RetryArgs)
		retry := ToolCall{
			Tool:     name,
			Args:     adv.RetryArgs,
			Result:   resultString(out2),
			Err:      err2,
			Duration: time.Since(start),
			Retried:  true,
		}
		adv2 := obs(ctx, retry)
		if sink != nil {
			// The verdict comes from the RETRY's own outcome, never from the
			// observer's response to it: an observer that has already used its
			// one repair returns empty advice for a retried call whether that
			// call worked or refused again, so reading the verdict off the
			// advice would report every retry as a success.
			sink(adv.RetryArgs, err2 == nil && retry.Failure() == "", retry.Result)
		}
		if err2 != nil {
			// The repair made it worse: hand back the ORIGINAL outcome, which
			// at least carries the tool's own explanation of what to fix.
			return out, err
		}
		if adv2.Guidance != "" {
			return appendGuidance(retry.Result, adv2.Guidance), nil
		}
		return out2, nil
	}
}

// resultString renders a tool result as the agent would see it.
func resultString(out interface{}) string {
	if s, ok := out.(string); ok {
		return s
	}
	return ""
}

// appendGuidance attaches a repair hint to a refusal the agent is about to
// read, keeping it inside the same tool result so it cannot be dropped by a
// context compaction that keeps results but not commentary.
func appendGuidance(result, guidance string) string {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return result
	}
	if strings.TrimSpace(result) == "" {
		return guidance
	}
	return result + "\n\nHOW TO FIX (from harness memory):\n" + guidance
}

// ---------------------------------------------------------------------------
// Refusal detection
// ---------------------------------------------------------------------------

// Most tools in this package report a refusal as a SUCCESSFUL call whose result
// explains what to fix — deliberately, because a small model handles a readable
// tool result far better than a transport error. That makes `err != nil` an
// almost useless failure signal: the line-numbered old_str, the unread file,
// the ambiguous anchor and the over-edit are all "successful" calls.
//
// refusalMarkers is therefore the authoritative list of phrases THIS package
// emits when it declines to do something. Adding a new refusal message means
// adding its distinctive phrase here — TestEveryRefusalReasonIsDetected asserts
// that every exported reason constructor is covered.
//
// Matching is on lowercased text and deliberately narrow: a ws_grep hit that
// merely quotes "no such file or directory" out of a source file must not be
// mistaken for a failure of the grep.
var refusalMarkers = []string{
	"refused —", // Write/Edit/No-op/Over-edit/Ambiguous/shell refused (em dash)
	"refused -", // ASCII fallback for the same family
	"file must be read first before edit",
	"has not been read in this session",
	"old_str not found in ",
	"old_str found ",
	"path is required",
	"quality monitor",
	"does not exist. use ws_glob",
	"cannot be read (permission denied)",
	"could not be read: ",
}

// ToolResultFailure reports the failure text of a completed tool call, or ""
// when the call did what it was asked.
//
// A hard error always counts. Otherwise the result is matched against the
// refusal phrases this package emits.
func ToolResultFailure(result string, err error) string {
	if err != nil {
		return err.Error()
	}
	low := strings.ToLower(result)
	for _, m := range refusalMarkers {
		if strings.Contains(low, m) {
			return result
		}
	}
	return ""
}

// Failure is ToolResultFailure for a completed call.
func (c ToolCall) Failure() string { return ToolResultFailure(c.Result, c.Err) }
