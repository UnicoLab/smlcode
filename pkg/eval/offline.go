package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
	"github.com/UnicoLab/slmcode/pkg/evolve"
)

// Offline eval: proving an improvement without a live model.
//
// The live cases in this package answer "did the model finish the task", which
// is the right question but the wrong instrument for harness work: it needs a
// running model, it takes minutes, and its variance swamps the effect of any
// single harness change. Most of what this harness DOES is not "be smart" — it
// is repair a failed edit, fall back to another edit format, avoid a redundant
// call. All of that is measurable from a RECORDING.
//
// So each fixture below is a trajectory: what a small model really emitted on a
// task, which tool calls failed, and the arguments that eventually worked.
// Replaying it with and without the repair store is a controlled A/B with one
// variable, no network, and no flakiness — and it reports the same
// metrics.Metrics shape a live run does, so the two are comparable.

//go:embed fixtures/*.json
var fixtureFS embed.FS

// FixtureDir is where the embedded trajectories live in the source tree.
const FixtureDir = "fixtures"

// OfflineCase is one fixture-driven eval case.
type OfflineCase struct {
	// ID is the trajectory id (also the fixture's file name).
	ID string `json:"id"`
	// Exercises names the harness behavior this fixture covers.
	Exercises string `json:"exercises"`
	// Trajectory is the recording replayed by RunOffline.
	Trajectory metrics.Trajectory `json:"-"`
}

// offlineExercises documents what each shipped fixture is for. A fixture with
// no entry still runs; it is just undocumented.
var offlineExercises = map[string]string{
	"repair-ladder-go": "repair ladder: a line-number-gutter edit repaired deterministically, " +
		"then an old_str miss that still costs a model round-trip",
	"edit-format-fallback-py": "edit-format fallback: two failed diff hunks followed by the " +
		"search/replace edit that applied",
	"unrepairable-js": "a failure no rule covers — the control arm that must NOT improve",
}

// OfflineCases returns the embedded fixture cases, sorted by id.
func OfflineCases() ([]OfflineCase, error) {
	entries, err := fixtureFS.ReadDir(FixtureDir)
	if err != nil {
		return nil, fmt.Errorf("eval: read fixtures: %w", err)
	}
	var out []OfflineCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fixtureFS.ReadFile(path.Join(FixtureDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("eval: read %s: %w", e.Name(), err)
		}
		var tr metrics.Trajectory
		if err := json.Unmarshal(data, &tr); err != nil {
			return nil, fmt.Errorf("eval: parse %s: %w", e.Name(), err)
		}
		if tr.ID == "" {
			tr.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, OfflineCase{
			ID: tr.ID, Exercises: offlineExercises[tr.ID], Trajectory: tr,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// OfflineReport is the result of an offline A/B.
type OfflineReport struct {
	// Cases is what was replayed.
	Cases []OfflineCase `json:"cases"`
	// Baseline is the arm with no repair store: every failure costs a model
	// round-trip, which is the harness before any of this existed.
	Baseline []metrics.Metrics `json:"baseline"`
	// Current is the arm with the repair store.
	Current []metrics.Metrics `json:"current"`
	// Comparison is the readable delta between the two arms.
	Comparison metrics.Comparison `json:"comparison"`
}

// Improved reports whether the current arm beat the baseline overall.
func (r OfflineReport) Improved() bool { return r.Comparison.Improved() }

// Render is the human-readable A/B table.
func (r OfflineReport) Render() string {
	var b strings.Builder
	b.WriteString("Offline eval (fixture replay, no model called)\n")
	for _, c := range r.Cases {
		b.WriteString("  · " + c.ID)
		if c.Exercises != "" {
			b.WriteString(" — " + c.Exercises)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(r.Comparison.Render())
	return b.String()
}

// OfflineOptions configures RunOffline.
type OfflineOptions struct {
	// Repairer is the store under test. Nil opens the shipped rule set, which
	// is what `slmcode eval --offline` means by "current".
	Repairer metrics.Repairer
	// ModelFamily narrows repair lookup (empty matches family-agnostic rules).
	ModelFamily string
	// Cases overrides the embedded fixtures.
	Cases []OfflineCase
}

// RunOffline replays every fixture twice — without and with the repair store —
// and returns the comparison. It calls no model, touches no network, and writes
// nothing outside the caller's own directories.
func RunOffline(opt OfflineOptions) (OfflineReport, error) {
	cases := opt.Cases
	if len(cases) == 0 {
		var err error
		cases, err = OfflineCases()
		if err != nil {
			return OfflineReport{}, err
		}
	}
	repairer := opt.Repairer
	if repairer == nil {
		rules, err := evolve.OpenRules("", "")
		if err != nil {
			return OfflineReport{}, fmt.Errorf("eval: open repair rules: %w", err)
		}
		repairer = rules
	}

	trajectories := make([]metrics.Trajectory, 0, len(cases))
	for _, c := range cases {
		trajectories = append(trajectories, c.Trajectory)
	}
	baseline := metrics.ReplayAll(trajectories, metrics.ReplayOptions{Label: "baseline"})
	current := metrics.ReplayAll(trajectories, metrics.ReplayOptions{
		Repairer: repairer, ModelFamily: opt.ModelFamily, Label: "with-repairs",
	})
	return OfflineReport{
		Cases:      cases,
		Baseline:   baseline,
		Current:    current,
		Comparison: metrics.Compare(baseline, current),
	}, nil
}
