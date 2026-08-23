package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// Case is one coding eval scenario.
type Case struct {
	ID           string
	Query        string
	SeedFiles    map[string]string // rel path → content
	ExpectFiles  []string          // must exist after run
	ExpectSubstr map[string]string // path → substring that must appear
	Timeout      time.Duration
}

// Result is the outcome of one case.
type Result struct {
	ID            string        `json:"id"`
	OK            bool          `json:"ok"`
	Duration      time.Duration `json:"duration"`
	TasksDone     int           `json:"tasks_done"`
	TasksTotal    int           `json:"tasks_total"`
	SmokeOK       bool          `json:"smoke_ok"`
	FilesOK       bool          `json:"files_ok"`
	Error         string        `json:"error,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	Interventions int           `json:"interventions"`
	// Metrics is this case's harness record: edit-apply rate, tool error rate,
	// LLM calls and tokens per task. Pass/fail alone cannot say whether a
	// change improved the harness; these numbers can.
	Metrics metrics.Metrics `json:"metrics"`
}

// Report aggregates many results.
type Report struct {
	Started  string   `json:"started"`
	Model    string   `json:"model"`
	Provider string   `json:"provider"`
	Results  []Result `json:"results"`
	Passed   int      `json:"passed"`
	Failed   int      `json:"failed"`
}

// Metrics returns one record per case, in case order — the input to Compare.
func (r Report) Metrics() []metrics.Metrics {
	out := make([]metrics.Metrics, 0, len(r.Results))
	for _, res := range r.Results {
		out = append(out, res.Metrics)
	}
	return out
}

// Summary pools every case's metrics into one aggregate.
func (r Report) Summary() metrics.Summary { return metrics.Aggregate(r.Metrics()) }

// CompareTo produces the baseline→this delta, so `slmcode eval` can answer
// "did this change improve the harness" rather than only "did it pass".
func (r Report) CompareTo(baseline Report) metrics.Comparison {
	return metrics.Compare(baseline.Metrics(), r.Metrics())
}

// RecordMetrics appends every case's record to the project's metrics log, so a
// later run can Compare against it.
func (r Report) RecordMetrics(projectDir string) error {
	for _, m := range r.Metrics() {
		if err := metrics.AppendTo(projectDir, m); err != nil {
			return err
		}
	}
	return nil
}

// DefaultCases returns offline-friendly coding checks (no network).
func DefaultCases() []Case {
	return []Case{
		{
			ID:          "py-hello",
			Query:       "Create hello.py that defines greet(name) returning 'Hello, {name}!'. Add a tiny pytest in test_hello.py.",
			ExpectFiles: []string{"hello.py", "test_hello.py"},
			ExpectSubstr: map[string]string{
				"hello.py": "def greet",
			},
			Timeout: 3 * time.Minute,
		},
		{
			ID:    "go-add",
			Query: "In add.go create package main with func Add(a, b int) int. Keep it minimal.",
			SeedFiles: map[string]string{
				"go.mod": "module evaltmp\n\ngo 1.22\n",
			},
			ExpectFiles: []string{"add.go"},
			ExpectSubstr: map[string]string{
				"add.go": "func Add",
			},
			Timeout: 3 * time.Minute,
		},
	}
}

// RunCase executes one eval case in an isolated temp workspace.
func RunCase(ctx context.Context, c Case, baseCfg *config.Config) Result {
	start := time.Now()
	res := Result{ID: c.ID}
	root, err := os.MkdirTemp("", "slmcode-eval-"+c.ID+"-*")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(root)

	for path, body := range c.SeedFiles {
		abs := filepath.Join(root, filepath.FromSlash(path))
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			res.Error = err.Error()
			return res
		}
	}

	cfg := config.Default(root)
	if baseCfg != nil {
		cfg.Provider = baseCfg.Provider
		cfg.Model = baseCfg.Model
		cfg.Endpoint = baseCfg.Endpoint
		cfg.APIKey = baseCfg.APIKey
		cfg.ThinkPasses = baseCfg.ThinkPasses
	}
	cfg.Root = root
	cfg.MaxParallel = 1
	cfg.PlanApprove = "auto"
	cfg.ClarifyMode = "off"
	cfg.TaskTimeout = c.Timeout
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 3 * time.Minute
	}

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		res.Error = err.Error()
		return res
	}
	h, err := harness.New(root)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	interventions := 0
	collector := newMetricsCollector()
	orch.OnEvent(func(e orchestrator.Event) {
		if e.Kind == "intervention" {
			interventions++
		}
		collector.observe(e)
	})
	// SetOrchestrator, not a bare assignment: harness.New already built one,
	// and dropping that pointer leaked its MCP subprocesses and evolve engine
	// for every eval case.
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		res.Error = cerr.Error()
		return res
	}
	defer func() { _ = h.Close() }()

	runCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	out, err := h.Run(runCtx, c.Query)
	res.Duration = time.Since(start)
	res.Interventions = interventions
	if out != nil {
		res.Summary = out.Summary
		if b := orch.Board(); b != nil {
			_ = b.Load()
			snap := b.Snapshot()
			res.TasksTotal = len(snap.Tasks)
			for _, t := range snap.Tasks {
				t.Normalize()
				if t.Column == plan.ColDone {
					res.TasksDone++
				}
				if strings.Contains(t.Output, "Deterministic smoke") && strings.Contains(t.Output, "PASSED") {
					res.SmokeOK = true
				}
			}
		}
	}
	if err != nil && (out == nil || !strings.Contains(strings.ToLower(err.Error()), "canceled")) {
		res.Error = err.Error()
		res.Metrics = collector.snapshot(res, out, cfg.Model, cfg.Provider, start)
		return res
	}

	filesOK := true
	for _, f := range c.ExpectFiles {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			filesOK = false
			res.Error = fmt.Sprintf("missing file %s", f)
			break
		}
	}
	for path, sub := range c.ExpectSubstr {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || !strings.Contains(string(data), sub) {
			filesOK = false
			res.Error = fmt.Sprintf("file %s missing %q", path, sub)
			break
		}
	}
	res.FilesOK = filesOK
	// Score against expert reference bar (completeness), not just file existence.
	if filesOK {
		if gaps := quality.CheckProjectCompleteness(root, c.Query); len(gaps) > 0 {
			filesOK = false
			res.FilesOK = false
			res.Error = fmt.Sprintf("below reference bar: %s", gaps[0].Reason)
			if len(gaps) > 1 {
				res.Error += fmt.Sprintf(" (+%d more)", len(gaps)-1)
			}
		}
	}
	res.OK = filesOK && res.Error == "" && res.TasksDone > 0
	if res.OK && res.Error != "" {
		res.Error = ""
	}
	if !res.OK && res.Error == "" {
		res.Error = "tasks not done or files incomplete"
	}
	res.Metrics = collector.snapshot(res, out, cfg.Model, cfg.Provider, start)
	return res
}

// RunAll runs cases and returns a report.
func RunAll(ctx context.Context, cases []Case, baseCfg *config.Config) Report {
	rep := Report{
		Started: time.Now().UTC().Format(time.RFC3339),
	}
	if baseCfg != nil {
		rep.Model = baseCfg.Model
		rep.Provider = baseCfg.Provider
	}
	for _, c := range cases {
		if err := ctx.Err(); err != nil {
			break
		}
		r := RunCase(ctx, c, baseCfg)
		rep.Results = append(rep.Results, r)
		if r.OK {
			rep.Passed++
		} else {
			rep.Failed++
		}
	}
	return rep
}

// WriteReport saves JSON under path.
func WriteReport(path string, rep Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
