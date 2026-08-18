package readiness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestBuildReportsFixesForDisabledSLMGuards(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DynamicPipeline = false
	cfg.ContextCompact = false
	cfg.ReactCompact = false
	cfg.SessionEventLog = false
	cfg.WriteGuard = false
	cfg.ReadBeforeEdit = false
	cfg.FileCheckpoints = false
	cfg.ShellWriteGuard = false
	cfg.ShellWhitelist = false
	cfg.QAGate = false
	cfg.RequireSmoke = false
	cfg.ClaimsGate = false
	cfg.OverEditGuard = false

	report := Build(cfg, 0)
	if report.OK || report.Score >= 80 {
		t.Fatalf("weak config should not be ready: %+v", report)
	}
	if !hasFix(report, "dynamic_pipeline", "dynamic_pipeline") {
		t.Fatalf("missing dynamic fix: %+v", report.Checks)
	}
	if !hasFix(report, "shell_guards", "shell_whitelist") {
		t.Fatalf("missing shell whitelist fix: %+v", report.Checks)
	}
	if report.Guards["shell_whitelist"] {
		t.Fatalf("guard state should expose shell_whitelist=false: %+v", report.Guards)
	}
}

func TestPatchForFailedAppliesAllReadinessFixes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DynamicPipeline = false
	cfg.ContextCompact = false
	cfg.ReactCompact = false
	cfg.SessionEventLog = false
	cfg.WriteGuard = false
	cfg.ReadBeforeEdit = false
	cfg.FileCheckpoints = false
	cfg.ShellWriteGuard = false
	cfg.ShellWhitelist = false
	cfg.QAGate = false
	cfg.RequireSmoke = false
	cfg.ClaimsGate = false
	cfg.OverEditGuard = false

	patch, ok, err := PatchForFailed(Build(cfg, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected fixes")
	}
	cfg.ApplyPatch(patch)
	next := Build(cfg, 1)
	if !next.OK || next.Score < 90 {
		t.Fatalf("fixes did not harden config: score=%d checks=%+v", next.Score, next.Checks)
	}
}

func TestBuildWithProbeFindsOpenAICompatibleModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-coder"}]}`))
	}))
	defer srv.Close()

	cfg := probeConfig(t, "omlx", srv.URL, "local-coder")
	report := BuildWithProbe(context.Background(), cfg, 1)
	check := findReadinessCheck(t, report, "provider_model")

	if !check.OK {
		t.Fatalf("provider model should be ready: %+v", check)
	}
	if check.Endpoint != srv.URL {
		t.Fatalf("endpoint=%q want %q", check.Endpoint, srv.URL)
	}
	if check.Latency < 0 {
		t.Fatalf("latency should be recorded: %+v", check)
	}
}

func TestBuildWithProbeReportsMissingModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"other-model"}]}`))
	}))
	defer srv.Close()

	cfg := probeConfig(t, "omlx", srv.URL, "local-coder")
	base := Build(cfg, 1)
	report := BuildWithProbe(context.Background(), cfg, 1)
	check := findReadinessCheck(t, report, "provider_model")

	if check.OK {
		t.Fatalf("missing model should fail: %+v", check)
	}
	if !strings.Contains(check.Message, "not listed") {
		t.Fatalf("message should explain missing model: %+v", check)
	}
	if check.FixHint == "" {
		t.Fatalf("missing model should include fix hint: %+v", check)
	}
	if report.Score >= base.Score {
		t.Fatalf("probe failure should lower score: base=%d probed=%d", base.Score, report.Score)
	}
}

func TestBuildWithProbeUsesOllamaTagsEndpoint(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if r.URL.Path != "/api/tags" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:14b"}]}`))
	}))
	defer srv.Close()

	cfg := probeConfig(t, "ollama", srv.URL+"/v1", "qwen2.5-coder:14b")
	report := BuildWithProbe(context.Background(), cfg, 1)
	check := findReadinessCheck(t, report, "provider_model")

	if !check.OK {
		t.Fatalf("ollama model should be ready: %+v", check)
	}
	if seenPath != "/api/tags" {
		t.Fatalf("ollama probe path=%q", seenPath)
	}
}

func TestBuildWithProbeReportsEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[{"id":"local-coder"}]}`))
	}))
	defer srv.Close()

	cfg := probeConfig(t, "omlx", srv.URL, "local-coder")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	report := BuildWithProbe(ctx, cfg, 1)
	check := findReadinessCheck(t, report, "provider_model")

	if check.OK {
		t.Fatalf("timeout should fail provider check: %+v", check)
	}
	if !strings.Contains(check.Message, "endpoint unavailable") {
		t.Fatalf("message should explain endpoint failure: %+v", check)
	}
	if check.FixHint == "" {
		t.Fatalf("endpoint failure should include fix hint: %+v", check)
	}
}

func hasFix(r Report, checkID, key string) bool {
	for _, check := range r.Checks {
		if check.ID == checkID {
			_, ok := check.FixPatch[key]
			return ok
		}
	}
	return false
}

func probeConfig(t *testing.T, provider, endpoint, model string) *config.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Provider = provider
	cfg.Endpoint = endpoint
	cfg.Model = model
	cfg.DynamicPipeline = true
	cfg.ContextCompact = true
	cfg.ReactCompact = true
	cfg.SessionEventLog = true
	cfg.WriteGuard = true
	cfg.ReadBeforeEdit = true
	cfg.FileCheckpoints = true
	cfg.ShellWriteGuard = true
	cfg.ShellWhitelist = true
	cfg.QAGate = true
	cfg.RequireSmoke = true
	cfg.ClaimsGate = true
	cfg.OverEditGuard = true
	return cfg
}

func findReadinessCheck(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, check := range r.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %q missing: %+v", id, r.Checks)
	return Check{}
}
