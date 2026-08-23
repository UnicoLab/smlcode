package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetricsRates(t *testing.T) {
	tests := []struct {
		name string
		m    Metrics
		want map[string]float64
	}{
		{
			name: "full record",
			m: Metrics{
				Tasks: 4, TasksPassed: 3,
				EditsAttempted: 10, EditsApplied: 9,
				ToolCalls: 40, ToolErrors: 4, RedundantCalls: 2,
				LLMCalls: 20, TokensIn: 4000, TokensOut: 400, WallMS: 8000,
				Gates:              []Gate{{Passed: true}, {Passed: false}},
				Failures:           5,
				RepairHits:         3,
				ResolvedFromMemory: 3, ResolvedFromLLM: 1, Unresolved: 1,
			},
			want: map[string]float64{
				"pass": 0.75, "apply": 0.9, "toolerr": 0.1, "redundant": 0.05,
				"gate": 0.5, "repair": 0.6, "memres": 0.75,
				"llmpertask": 5, "tokpertask": 1100, "wallpertask": 2,
			},
		},
		{
			name: "empty record reports no data, not zero",
			m:    Metrics{},
			want: map[string]float64{
				"pass": -1, "apply": -1, "toolerr": -1, "redundant": -1,
				"gate": -1, "repair": -1, "memres": -1,
				"llmpertask": -1, "tokpertask": -1, "wallpertask": -1,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]float64{
				"pass": tc.m.TaskPassRate(), "apply": tc.m.EditApplyRate(),
				"toolerr": tc.m.ToolErrorRate(), "redundant": tc.m.RedundantCallRate(),
				"gate": tc.m.GatePassRate(), "repair": tc.m.RepairHitRate(),
				"memres": tc.m.MemoryResolutionRate(), "llmpertask": tc.m.LLMCallsPerTask(),
				"tokpertask": tc.m.TokensPerTask(), "wallpertask": tc.m.WallSecondsPerTask(),
			}
			for k, want := range tc.want {
				if diff := got[k] - want; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("%s = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

func TestMetricsNormalizeClampsNonsense(t *testing.T) {
	m := Metrics{
		Tasks: 1, TasksPassed: 5,
		EditsAttempted: 2, EditsApplied: 9,
		ToolCalls: -3, WallMS: -1,
		Language: " GO ", Model: " m ",
	}
	for i := 0; i < 50; i++ {
		m.Gates = append(m.Gates, Gate{Name: strings.Repeat("g", 200)})
	}
	m.Normalize(time.Now())
	if m.Tasks < m.TasksPassed || m.EditsAttempted < m.EditsApplied {
		t.Errorf("impossible ratios survived: %+v", m)
	}
	if m.ToolCalls != 0 || m.WallMS != 0 {
		t.Errorf("negatives survived: %+v", m)
	}
	if m.Language != "go" || m.Model != "m" {
		t.Errorf("fields not normalized: %q %q", m.Language, m.Model)
	}
	if len(m.Gates) > MaxGates {
		t.Errorf("gates = %d, cap is %d", len(m.Gates), MaxGates)
	}
	if len(m.Gates[0].Name) > 80 {
		t.Error("gate name not clipped")
	}
	if m.At.IsZero() {
		t.Error("timestamp not filled")
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	proj := t.TempDir()
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		if err := AppendTo(proj, Metrics{
			RunID: "run_" + string(rune('a'+i)), At: base.Add(time.Duration(i) * time.Minute),
			Tasks: 1, TasksPassed: 1, EditsAttempted: 2, EditsApplied: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := Path(proj)
	if !strings.HasSuffix(filepath.ToSlash(path), DefaultPath) {
		t.Errorf("path = %q", path)
	}
	got, err := LoadFrom(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("loaded %d records", len(got))
	}
	if got[0].RunID != "run_a" || got[4].RunID != "run_e" {
		t.Errorf("records out of order: %s … %s", got[0].RunID, got[4].RunID)
	}
	// The file must be inspectable line-by-line JSON.
	data, _ := os.ReadFile(path) //nolint:gosec
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("line %d is not JSON: %v", i, err)
		}
	}
}

func TestLoadSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.jsonl")
	body := strings.Join([]string{
		`{"run_id":"ok1","tasks":1,"tasks_passed":1}`,
		`{"run_id":"broken",`,
		``,
		`garbage`,
		`{"run_id":"ok2","tasks":1,"tasks_passed":0}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load must tolerate corruption: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d usable records, want 2", len(got))
	}
}

func TestLoadMissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil || got != nil {
		t.Errorf("missing file: got %v, %v", got, err)
	}
	if got, err := Load(""); err != nil || got != nil {
		t.Errorf("empty path: got %v, %v", got, err)
	}
}

func TestPruneBoundsTheLog(t *testing.T) {
	proj := t.TempDir()
	for i := 0; i < 60; i++ {
		if err := AppendTo(proj, Metrics{RunID: "r" + itoa(i), At: time.Unix(int64(i), 0), Tasks: 1}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(Path(proj), 10)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 50 {
		t.Errorf("removed %d, want 50", removed)
	}
	got, _ := LoadFrom(proj)
	if len(got) != 10 {
		t.Fatalf("after prune %d records remain", len(got))
	}
	if got[0].RunID != "r50" {
		t.Errorf("prune kept the wrong end: first is %s", got[0].RunID)
	}
	if n, _ := Prune(Path(proj), 10); n != 0 {
		t.Errorf("pruning an already-bounded log removed %d", n)
	}
}

func TestSelect(t *testing.T) {
	base := time.Unix(1000, 0).UTC()
	in := []Metrics{
		{RunID: "a", At: base, Label: "baseline", Model: "m1", Language: "go"},
		{RunID: "b", At: base.Add(time.Hour), Label: "current", Model: "m1", Language: "go"},
		{RunID: "c", At: base.Add(2 * time.Hour), Label: "current", Model: "m2", Language: "python"},
	}
	tests := []struct {
		name string
		f    Filter
		want []string
	}{
		{"all", Filter{}, []string{"a", "b", "c"}},
		{"label", Filter{Label: "current"}, []string{"b", "c"}},
		{"model", Filter{Model: "m1"}, []string{"a", "b"}},
		{"language", Filter{Language: "python"}, []string{"c"}},
		{"since", Filter{Since: base.Add(90 * time.Minute)}, []string{"c"}},
		{"until", Filter{Until: base.Add(30 * time.Minute)}, []string{"a"}},
		{"last", Filter{Last: 1}, []string{"c"}},
		{"none", Filter{Label: "nope"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Select(in, tc.f)
			var ids []string
			for _, m := range got {
				ids = append(ids, m.RunID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Select = %v, want %v", ids, tc.want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
