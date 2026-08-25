package server

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/UnicoLab/slmcode/pkg/calibrate"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// GET /api/calibration — the measured profile for the configured pair, the
// budgets derived from it, and whether it is current.
//
// Studio is where a user most often SWITCHES MODEL, so it is where "what is
// this model actually configured to do, and on what evidence?" gets asked. The
// CLI answers that with `slmcode calibrate`; without this the web UI could only
// show the numbers, never where they came from.
//
// READ-ONLY BY CONSTRUCTION. It never probes: a GET that could spend a minute
// of GPU on a cold model is a GET that will be spammed by a polling UI. The
// probe happens at studio startup, before a run (ensureCalibrated), and on
// demand from the CLI; this endpoint reports what those produced.

// calibrationView is the JSON shape the UI renders.
type calibrationView struct {
	// Present is false when this pair has never been measured — the UI shows a
	// "not calibrated yet" state rather than zeros that look like measurements.
	Present  bool   `json:"present"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`

	// Measured evidence.
	ContextLimit   int                `json:"context_limit,omitempty"`
	ContextSource  string             `json:"context_source,omitempty"`
	MaxParallel    int                `json:"max_parallel,omitempty"`
	P50Ms          int64              `json:"p50_ms,omitempty"`
	P95Ms          int64              `json:"p95_ms,omitempty"`
	TokensPerSec   float64            `json:"tokens_per_sec,omitempty"`
	QueueInflation float64            `json:"queue_inflation,omitempty"`
	Levels         []calibrationLevel `json:"levels,omitempty"`

	// Partial marks a probe that ran out of budget: usable, but the knee may be
	// an underestimate, and it expires in an hour rather than a month.
	Partial    bool   `json:"partial,omitempty"`
	MeasuredAt string `json:"measured_at,omitempty"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
	Current    bool   `json:"current"`

	// Budgets currently in force for this model, derived from the window.
	Budgets calibrationBudgets `json:"budgets"`

	// Report is the rendered human report, so the UI can show exactly what the
	// CLI shows rather than reimplementing the prose.
	Report string `json:"report,omitempty"`
}

type calibrationLevel struct {
	Concurrency int     `json:"concurrency"`
	Efficiency  float64 `json:"efficiency"`
	Throughput  float64 `json:"throughput"`
}

type calibrationBudgets struct {
	ContextLimit         int `json:"context_limit"`
	MaxTokens            int `json:"max_tokens"`
	ThinkingBudgetTokens int `json:"thinking_budget_tokens"`
	SkillTokenBudget     int `json:"skill_token_budget"`
	KnowledgeTokenBudget int `json:"knowledge_token_budget"`
	MaxTurns             int `json:"max_turns"`
}

func (s *Server) handleCalibration(w http.ResponseWriter, r *http.Request) {
	var view calibrationView

	s.withConfigRead(func(c *config.Config) {
		if c == nil {
			return
		}
		view.Model, view.Provider, view.Endpoint = c.Model, c.Provider, c.Endpoint
		prof := config.ResolveModelProfile(c.ModelProfiles, c.Model)
		view.Budgets = calibrationBudgets{
			ContextLimit:         prof.ContextLimit,
			MaxTokens:            prof.MaxTokens,
			ThinkingBudgetTokens: prof.ThinkingBudgetTokens,
			SkillTokenBudget:     prof.SkillTokenBudget,
			KnowledgeTokenBudget: prof.KnowledgeTokenBudget,
			MaxTurns:             prof.MaxTurns,
		}

		home, _ := os.UserHomeDir()
		store := calibrate.Open(calibrate.UserDir(home))
		if store == nil {
			return
		}
		defer func() { _ = store.Close() }()

		p, ok := store.Lookup(c.Model, c.Endpoint)
		if p.ID == "" {
			return
		}
		view.Present = true
		view.Current = ok
		view.ContextLimit, view.ContextSource = p.ContextLimit, p.ContextSource
		view.MaxParallel = p.MaxParallel
		view.P50Ms, view.P95Ms = p.P50Ms, p.P95Ms
		view.TokensPerSec, view.QueueInflation = p.TokensPerSec, p.QueueInflation
		view.Partial = p.Partial
		if !p.MeasuredAt.IsZero() {
			view.MeasuredAt = p.MeasuredAt.UTC().Format("2006-01-02T15:04:05Z")
			view.AgeSeconds = int64(p.Age(time.Now()).Seconds())
		}
		for _, l := range p.Levels {
			view.Levels = append(view.Levels, calibrationLevel{
				Concurrency: l.Concurrency, Efficiency: l.Efficiency, Throughput: l.Throughput,
			})
		}
		// The rendered report shows the derivation as a diff against the STATIC
		// profile for this model, which is what makes "why is skills 4096?"
		// answerable in the UI.
		base := config.ResolveModelProfile(config.DefaultModelProfiles(), c.Model)
		view.Report = calibrate.NewReport(p, nil, base, prof).Render()
	})

	writeJSON(w, view)
}

// ensureCalibrated measures the CURRENT model before a run, if it has not been.
//
// Studio is a long-lived process in which the model can change at any time, so
// "calibrated at startup" is not the same as "calibrated for this run". This
// closes that window: a model switched in the UI is measured on its first run
// rather than inheriting the previous model's concurrency knee, timeouts and
// token budgets.
//
// Everything expensive is cached in the GLOBAL store (~/.slmcode/calibrate),
// keyed by exact model plus endpoint, so this is a map lookup on every run
// after the first for a given pair — across restarts, and across projects.
//
// Progress goes to the EVENT STREAM, which is what makes a cold 42GB model's
// minute of weight-loading legible instead of looking like a hang.
func (s *Server) ensureCalibrated(ctx context.Context) {
	var cfg *config.Config
	s.withConfigRead(func(c *config.Config) { cfg = c })
	if cfg == nil || !cfg.CalibrationEnabled() {
		return
	}

	home, _ := os.UserHomeDir()
	store := calibrate.Open(calibrate.UserDir(home))
	if store == nil {
		return
	}
	defer func() { _ = store.Close() }()

	opts := calibrate.AutoOptions{Store: store}
	opts.Options.OnProgress = func(pr calibrate.Progress) {
		s.emit(orchestrator.Event{
			Phase: "init", Kind: "calibration", Message: pr.String(), Time: time.Now(),
		})
	}
	out := calibrate.EnsureCalibrated(ctx, cfg, opts)
	switch {
	case out.Warning != "":
		s.emit(orchestrator.Event{
			Phase: "init", Kind: "warn", Message: out.Warning, Time: time.Now(),
		})
	case out.Measured && out.Notice != "":
		s.emit(orchestrator.Event{
			Phase: "init", Kind: "calibration", Message: out.Notice, Time: time.Now(),
		})
	}
}
