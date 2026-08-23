package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Trajectory replay exists so an improvement can be A/B'd offline. A stored
// trajectory is a recording of what a model actually emitted on a real task —
// tool calls, arguments, results — plus, for each failed step, the arguments
// that eventually worked. Replaying it against a Repairer answers one precise
// question with no live model involved:
//
//	how many of these failures would this repair store have fixed
//	deterministically, and how many would still have cost a round-trip?
//
// That is the whole A/B. It cannot tell you whether a model got smarter; it
// can tell you whether the harness around it got better, which is the part
// this subsystem is responsible for.

// StepKind classifies a recorded step.
type StepKind string

const (
	StepTool      StepKind = "tool"
	StepAssistant StepKind = "assistant"
)

// Step is one recorded action.
type Step struct {
	Kind StepKind `json:"kind"`
	Tool string   `json:"tool,omitempty"`
	Args string   `json:"args,omitempty"`
	// OK says whether the step succeeded as recorded.
	OK bool `json:"ok"`
	// Error is the failure message the tool returned.
	Error string `json:"error,omitempty"`
	// FixedArgs is the arguments that DID work, when the recording captured a
	// successful retry. A repair that reproduces these is a genuine save.
	FixedArgs string `json:"fixed_args,omitempty"`
	// EditAttempt marks a step that counts toward the edit-apply rate.
	EditAttempt bool `json:"edit_attempt,omitempty"`
}

// Trajectory is one recorded task.
type Trajectory struct {
	ID         string `json:"id"`
	Query      string `json:"query,omitempty"`
	Language   string `json:"language,omitempty"`
	Model      string `json:"model,omitempty"`
	EditFormat string `json:"edit_format,omitempty"`
	// TaskPassed is the recorded ground truth for the task.
	TaskPassed bool   `json:"task_passed,omitempty"`
	Steps      []Step `json:"steps"`
}

// Repairer is the minimal view of a repair store that replay needs. It is
// satisfied by *evolve.Rules without this package importing evolve — the
// dependency runs the other way in production, and a narrow interface keeps
// the offline harness usable with a stub.
type Repairer interface {
	// SuggestRepair returns guidance, rewritten arguments (empty when the
	// repair is not a deterministic argument transform) and whether anything
	// matched.
	SuggestRepair(tool, message, language, modelFamily, args string) (guidance, newArgs string, ok bool)
}

// LoadTrajectory reads one trajectory from a JSON file.
func LoadTrajectory(path string) (Trajectory, error) {
	data, err := os.ReadFile(path) //nolint:gosec // caller-supplied fixture path
	if err != nil {
		return Trajectory{}, err
	}
	var t Trajectory
	if err := json.Unmarshal(data, &t); err != nil {
		return Trajectory{}, err
	}
	if t.ID == "" {
		t.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return t, nil
}

// LoadTrajectories reads every *.json trajectory in a directory, sorted by
// name so replays are reproducible. Unreadable files are skipped.
func LoadTrajectories(dir string) ([]Trajectory, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []Trajectory
	for _, n := range names {
		t, err := LoadTrajectory(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// SaveTrajectory writes a trajectory fixture.
func SaveTrajectory(path string, t Trajectory) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// ReplayOptions configures a replay.
type ReplayOptions struct {
	// Repairer is consulted on every failed step. Nil replays the trajectory
	// as recorded, which is exactly the baseline arm of an A/B.
	Repairer Repairer
	// ModelFamily narrows repair lookup; empty derives nothing.
	ModelFamily string
	// Label is copied onto the resulting Metrics record.
	Label string
	// At overrides the record timestamp (tests).
	At time.Time
}

// Replay re-scores one trajectory and returns the metrics it would have
// produced. It performs no I/O beyond what the Repairer does and calls no
// model.
func Replay(t Trajectory, opt ReplayOptions) Metrics {
	at := opt.At
	if at.IsZero() {
		at = time.Now()
	}
	m := Metrics{
		RunID:      t.ID,
		At:         at,
		Model:      t.Model,
		Language:   t.Language,
		EditFormat: t.EditFormat,
		Label:      opt.Label,
		Tasks:      1,
	}
	seen := map[string]bool{}
	repairedAll := true

	for _, s := range t.Steps {
		if s.Kind == StepAssistant {
			m.LLMCalls++
			continue
		}
		m.ToolCalls++
		sig := s.Tool + "\x00" + s.Args
		if seen[sig] {
			m.RedundantCalls++
		}
		seen[sig] = true
		if s.EditAttempt {
			m.EditsAttempted++
		}
		if s.OK {
			if s.EditAttempt {
				m.EditsApplied++
			}
			continue
		}

		m.ToolErrors++
		m.Failures++
		if opt.Repairer == nil {
			// Baseline: every failure costs a fresh round-trip.
			m.LLMCalls++
			m.ResolvedFromLLM++
			if s.FixedArgs == "" {
				m.Unresolved++
				m.ResolvedFromLLM--
				repairedAll = false
			} else if s.EditAttempt {
				m.EditsApplied++
			}
			continue
		}

		_, newArgs, ok := opt.Repairer.SuggestRepair(s.Tool, s.Error, t.Language, opt.ModelFamily, s.Args)
		if ok {
			m.RepairHits++
		}
		switch {
		case ok && newArgs != "" && s.FixedArgs != "" && sameJSON(newArgs, s.FixedArgs):
			// The stored repair reproduced the arguments that really worked:
			// a saved round-trip, and the edit lands.
			m.ResolvedFromMemory++
			if s.EditAttempt {
				m.EditsApplied++
			}
		case s.FixedArgs != "":
			m.LLMCalls++
			m.ResolvedFromLLM++
			if s.EditAttempt {
				m.EditsApplied++
			}
		default:
			m.Unresolved++
			repairedAll = false
		}
	}
	if t.TaskPassed && repairedAll {
		m.TasksPassed = 1
	}
	m.Normalize(at)
	return m
}

// ReplayAll replays a set of trajectories and returns one record per
// trajectory, in input order.
func ReplayAll(ts []Trajectory, opt ReplayOptions) []Metrics {
	out := make([]Metrics, 0, len(ts))
	for i, t := range ts {
		o := opt
		if o.At.IsZero() {
			// Keep records ordered and deterministic without a real clock.
			o.At = time.Unix(0, 0).UTC().Add(time.Duration(i) * time.Second)
		}
		out = append(out, Replay(t, o))
	}
	return out
}

// ABTest replays the same trajectories with and without a repair store and
// returns the comparison. This is the offline A/B: identical inputs, one
// variable.
func ABTest(ts []Trajectory, r Repairer, modelFamily string) Comparison {
	baseline := ReplayAll(ts, ReplayOptions{Label: "baseline"})
	current := ReplayAll(ts, ReplayOptions{Repairer: r, ModelFamily: modelFamily, Label: "with-repairs"})
	return Compare(baseline, current)
}

// sameJSON compares two JSON blobs semantically, so key ordering and
// whitespace do not decide whether a repair counts.
func sameJSON(a, b string) bool {
	var pa, pb any
	ea := json.Unmarshal([]byte(a), &pa)
	eb := json.Unmarshal([]byte(b), &pb)
	if ea != nil || eb != nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	na, err1 := json.Marshal(pa)
	nb, err2 := json.Marshal(pb)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(na) == string(nb)
}
