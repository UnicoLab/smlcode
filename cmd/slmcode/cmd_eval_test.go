package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/eval"
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
)

// `slmcode eval --offline` is the mode that can actually show a harness
// improvement: it replays recorded trajectories with and without the repair
// store, so the only variable is the harness. It must call no model and reach
// no network, which is why this test can run at all.
func TestEvalOfflineRunsWithNoModel(t *testing.T) {
	out := filepath.Join(t.TempDir(), "offline.json")
	if err := runOfflineEval(out); err != nil {
		t.Fatalf("runOfflineEval: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the --out report was not written: %v", err)
	}
	var rep eval.OfflineReport
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if len(rep.Cases) == 0 {
		t.Fatal("no fixture trajectories were replayed")
	}
	if len(rep.Baseline) != len(rep.Cases) || len(rep.Current) != len(rep.Cases) {
		t.Fatalf("both arms must cover every case: cases=%d baseline=%d current=%d",
			len(rep.Cases), len(rep.Baseline), len(rep.Current))
	}
}

func TestEvalOfflineWithNoOutPathStillRuns(t *testing.T) {
	if err := runOfflineEval(""); err != nil {
		t.Fatalf("runOfflineEval without --out: %v", err)
	}
}

// --compare loads a previously written report; RecordMetrics is what makes a
// later comparison possible at all.
func TestEvalReportRoundTripsForCompare(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	base := eval.Report{
		Model: "m", Provider: "p", Passed: 1,
		Results: []eval.Result{{
			ID: "c1", OK: true,
			Metrics: metrics.Metrics{RunID: "c1", LLMCalls: 10, EditsAttempted: 4, EditsApplied: 2},
		}},
	}
	if err := eval.WriteReport(path, base); err != nil {
		t.Fatal(err)
	}
	got, err := readEvalReport(path)
	if err != nil {
		t.Fatalf("readEvalReport: %v", err)
	}
	if len(got.Metrics()) != 1 || got.Metrics()[0].LLMCalls != 10 {
		t.Fatalf("metrics did not survive the round trip: %+v", got.Metrics())
	}

	current := base
	current.Results[0].Metrics.EditsApplied = 4
	cmp := current.CompareTo(got)
	if cmp.Render() == "" {
		t.Fatal("CompareTo produced no rendered comparison")
	}
}

// RecordMetrics is the call cmd_eval.go now makes after RunAll — without it a
// future run has no baseline to compare against.
func TestRecordMetricsWritesTheProjectLog(t *testing.T) {
	dir := t.TempDir()
	rep := eval.Report{Results: []eval.Result{{
		ID: "c1", Metrics: metrics.Metrics{RunID: "c1", LLMCalls: 3},
	}}}
	if err := rep.RecordMetrics(dir); err != nil {
		t.Fatalf("RecordMetrics: %v", err)
	}
	if _, err := os.Stat(eval.MetricsPath(dir)); err != nil {
		t.Fatalf("metrics log not written at %s: %v", eval.MetricsPath(dir), err)
	}
	loaded, err := eval.LoadMetrics(dir)
	if err != nil || len(loaded) != 1 || loaded[0].LLMCalls != 3 {
		t.Fatalf("metrics not readable back: %v %+v", err, loaded)
	}
}
