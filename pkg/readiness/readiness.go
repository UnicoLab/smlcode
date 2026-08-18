package readiness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/models"
)

type Check struct {
	ID       string                 `json:"id"`
	Label    string                 `json:"label"`
	OK       bool                   `json:"ok"`
	Severity string                 `json:"severity"`
	Message  string                 `json:"message"`
	FixLabel string                 `json:"fix_label,omitempty"`
	FixHint  string                 `json:"fix_hint,omitempty"`
	FixPatch map[string]interface{} `json:"fix_patch,omitempty"`
	Endpoint string                 `json:"endpoint,omitempty"`
	Latency  int64                  `json:"latency_ms,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

type Report struct {
	OK             bool                `json:"ok"`
	Score          int                 `json:"score"`
	Status         string              `json:"status"`
	Provider       string              `json:"provider"`
	Model          string              `json:"model"`
	Endpoint       string              `json:"endpoint"`
	Backend        string              `json:"backend"`
	ActiveStack    string              `json:"active_stack"`
	ActivePack     string              `json:"active_pack"`
	ActivePipeline string              `json:"active_pipeline"`
	Guards         map[string]bool     `json:"guards"`
	ModelProfile   config.ModelProfile `json:"model_profile"`
	Checks         []Check             `json:"checks"`
}

func Build(cfg *config.Config, skillCount int) Report {
	if cfg == nil {
		return Report{Status: "not-ready"}
	}
	prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	checks := []Check{
		{
			ID: "dynamic_pipeline", Label: "Dynamic Pipeline", OK: cfg.DynamicPipeline, Severity: "critical",
			Message:  boolMessage(cfg.DynamicPipeline, "Composer adapts phases and specialists per task", "Static pipeline only; adaptive composition is disabled"),
			FixLabel: "Enable dynamic pipeline", FixPatch: fixPatchIf(!cfg.DynamicPipeline, "dynamic_pipeline", true),
		},
		{
			ID: "context_compaction", Label: "Context Compaction", OK: cfg.ContextCompact && cfg.ReactCompact, Severity: "warning",
			Message:  boolMessage(cfg.ContextCompact && cfg.ReactCompact, "Document and ReAct compaction are enabled", "Long local-model runs may exceed useful context"),
			FixLabel: "Enable compaction", FixPatch: fixPatchIf(!(cfg.ContextCompact && cfg.ReactCompact),
				"context_compact", true, "react_compact", true, "react_compact_at_percent", 80),
		},
		{
			ID: "session_events", Label: "Run Event Log", OK: cfg.SessionEventLog, Severity: "warning",
			Message:  boolMessage(cfg.SessionEventLog, "Runs persist events for replay and debugging", "Archived runs will have weak traceability"),
			FixLabel: "Enable event log", FixPatch: fixPatchIf(!cfg.SessionEventLog, "session_event_log", true),
		},
		{
			ID: "file_guards", Label: "File Guards", OK: cfg.WriteGuard && cfg.ReadBeforeEdit && cfg.FileCheckpoints, Severity: "critical",
			Message:  boolMessage(cfg.WriteGuard && cfg.ReadBeforeEdit && cfg.FileCheckpoints, "Write guard, read-before-edit, and checkpoints are active", "File edit protection is incomplete"),
			FixLabel: "Enable file guards", FixPatch: fixPatchIf(!(cfg.WriteGuard && cfg.ReadBeforeEdit && cfg.FileCheckpoints),
				"write_guard", true, "read_before_edit", true, "file_checkpoints", true),
		},
		{
			ID: "shell_guards", Label: "Shell Guards", OK: cfg.ShellWriteGuard && cfg.ShellWhitelist, Severity: "warning",
			Message:  boolMessage(cfg.ShellWriteGuard && cfg.ShellWhitelist, "Shell write guard and safe-prefix whitelist are active", "Shell commands can bypass some local safety rails"),
			FixLabel: "Enable shell guards", FixPatch: fixPatchIf(!(cfg.ShellWriteGuard && cfg.ShellWhitelist),
				"shell_write_guard", true, "shell_whitelist", true),
		},
		{
			ID: "quality_gates", Label: "Quality Gates", OK: cfg.QAGate && cfg.RequireSmoke && cfg.ClaimsGate && cfg.OverEditGuard, Severity: "critical",
			Message:  boolMessage(cfg.QAGate && cfg.RequireSmoke && cfg.ClaimsGate && cfg.OverEditGuard, "QA gate, smoke requirement, claims gate, and over-edit guard are active", "Verification guardrails are incomplete"),
			FixLabel: "Enable QA guards", FixPatch: fixPatchIf(!(cfg.QAGate && cfg.RequireSmoke && cfg.ClaimsGate && cfg.OverEditGuard),
				"qa_gate", true, "require_smoke", true, "claims_gate", true, "over_edit_guard", true),
		},
		{
			ID: "model_profile", Label: "Model Profile", OK: prof.ContextLimit > 0 && prof.MaxTokens > 0 && prof.MaxTurns > 0, Severity: "warning",
			Message: fmt.Sprintf("ctx=%d max_tokens=%d think=%d turns=%d",
				prof.ContextLimit, prof.MaxTokens, prof.ThinkingBudgetTokens, prof.MaxTurns),
		},
		{
			ID: "skills", Label: "Skills", OK: skillCount > 0, Severity: "warning",
			Message: fmt.Sprintf("%d skills loaded", skillCount),
		},
	}
	for i := range checks {
		if checks[i].OK || len(checks[i].FixPatch) == 0 {
			checks[i].FixLabel = ""
			checks[i].FixPatch = nil
		}
	}

	score := Score(checks)
	return Report{
		OK:             score >= 80,
		Score:          score,
		Status:         Status(score),
		Provider:       cfg.Provider,
		Model:          cfg.Model,
		Endpoint:       cfg.Endpoint,
		Backend:        cfg.Backend,
		ActiveStack:    cfg.ActiveStack,
		ActivePack:     cfg.ActivePack,
		ActivePipeline: cfg.ActivePipeline,
		Guards: map[string]bool{
			"write_guard":       cfg.WriteGuard,
			"read_before_edit":  cfg.ReadBeforeEdit,
			"shell_write_guard": cfg.ShellWriteGuard,
			"shell_whitelist":   cfg.ShellWhitelist,
			"file_checkpoints":  cfg.FileCheckpoints,
			"require_smoke":     cfg.RequireSmoke,
			"claims_gate":       cfg.ClaimsGate,
			"over_edit_guard":   cfg.OverEditGuard,
			"qa_gate":           cfg.QAGate,
			"context_compact":   cfg.ContextCompact,
			"react_compact":     cfg.ReactCompact,
			"session_event_log": cfg.SessionEventLog,
		},
		ModelProfile: prof,
		Checks:       checks,
	}
}

// BuildWithProbe includes a bounded live model endpoint check.
func BuildWithProbe(ctx context.Context, cfg *config.Config, skillCount int) Report {
	r := Build(cfg, skillCount)
	r.Checks = append(r.Checks, ProbeProvider(ctx, cfg))
	r.Score = Score(r.Checks)
	r.Status = Status(r.Score)
	r.OK = r.Score >= 80
	return r
}

// ProbeProvider verifies auth, endpoint reachability, and configured model
// availability using the provider's model-list endpoint.
func ProbeProvider(ctx context.Context, cfg *config.Config) Check {
	check := Check{
		ID:       "provider_model",
		Label:    "Provider Model",
		Severity: "critical",
	}
	if cfg == nil {
		check.Message = "config required"
		return check
	}
	provider := config.NormalizeProvider(cfg.Provider)
	modelID := strings.TrimSpace(cfg.Model)
	check.Endpoint = strings.TrimSpace(cfg.Endpoint)
	check.Details = map[string]interface{}{
		"provider": provider,
		"model":    modelID,
	}
	auth := models.ResolveAuth(cfg)
	if auth.Required && !auth.Configured {
		check.Message = auth.Message
		check.FixHint = "Configure the provider API key before running cloud model checks."
		return check
	}
	if modelID == "" {
		check.Message = "no model configured"
		check.FixHint = "Select a model from Settings or apply a local stack."
		return check
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	start := time.Now()
	names, err := models.Fetch(ctx, cfg)
	check.Latency = time.Since(start).Milliseconds()
	check.Details["models_count"] = len(names)
	if err != nil {
		check.Message = fmt.Sprintf("%s endpoint unavailable: %s", provider, truncate(err.Error(), 180))
		check.FixLabel = "Check endpoint and start the model server"
		check.FixHint = localProviderHint(provider, check.Endpoint)
		return check
	}
	if len(names) == 0 {
		check.Message = fmt.Sprintf("%s endpoint responded but returned no models", provider)
		check.FixHint = "Install or load a model, then refresh readiness."
		return check
	}
	if modelListed(modelID, names) {
		check.OK = true
		check.Message = fmt.Sprintf("%s model %q available (%d listed)", provider, modelID, len(names))
		return check
	}
	check.Message = fmt.Sprintf("%s model %q not listed; available: %s", provider, modelID, truncate(strings.Join(names, ", "), 180))
	check.FixLabel = "Select an installed model or update the stack"
	check.FixHint = "Use Settings -> Model Stack, or set model to one of the listed local models."
	return check
}

func PatchForFailed(r Report) (config.Patch, bool, error) {
	merged := map[string]interface{}{}
	for _, check := range r.Checks {
		if check.OK {
			continue
		}
		for k, v := range check.FixPatch {
			k = strings.TrimSpace(k)
			if k != "" {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return config.Patch{}, false, nil
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return config.Patch{}, false, err
	}
	var patch config.Patch
	if err := json.Unmarshal(b, &patch); err != nil {
		return config.Patch{}, false, err
	}
	return patch, true, nil
}

func Score(checks []Check) int {
	if len(checks) == 0 {
		return 0
	}
	total := 0
	earned := 0
	for _, c := range checks {
		weight := 1
		if c.Severity == "critical" {
			weight = 2
		}
		total += weight
		if c.OK {
			earned += weight
		}
	}
	if total == 0 {
		return 0
	}
	return earned * 100 / total
}

func Status(score int) string {
	switch {
	case score >= 90:
		return "production-ready"
	case score >= 75:
		return "ready-with-warnings"
	case score >= 50:
		return "needs-hardening"
	default:
		return "not-ready"
	}
}

func boolMessage(ok bool, good, bad string) string {
	if ok {
		return good
	}
	return bad
}

func modelListed(want string, names []string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	for _, n := range names {
		if strings.TrimSpace(strings.ToLower(n)) == want {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func localProviderHint(provider, endpoint string) string {
	switch config.NormalizeProvider(provider) {
	case "ollama":
		return "Start Ollama and verify `ollama list` includes the configured model."
	case "lmstudio":
		return "Start the LM Studio local server and confirm the endpoint is " + endpoint + "."
	case "omlx":
		return "Start the oMLX/OpenAI-compatible server and confirm it exposes /v1/models."
	case "vllm":
		return "Start vLLM with the OpenAI-compatible API server enabled."
	default:
		return "Confirm the endpoint is reachable and exposes an OpenAI-compatible model list."
	}
}

func fixPatchIf(cond bool, kv ...interface{}) map[string]interface{} {
	if !cond || len(kv) == 0 {
		return nil
	}
	out := map[string]interface{}{}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = kv[i+1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
