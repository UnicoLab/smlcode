package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The endpoint-aware max_parallel default.
//
// The numbers under test are MEASURED (see the table on
// DefaultMaxParallelLocal), so these tests assert the two named constants
// rather than literals: re-measuring the knee should change one constant and
// leave the behavior contract intact.

func writeMaxParallelProject(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o750); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(root, DirName, "config.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDefaultMaxParallelFollowsTheEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		project  string
		want     int
		wantSaid bool // does the lowered-default notice fire?
	}{
		{
			name:     "a local provider by name",
			project:  "provider: omlx\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "ollama is local wherever it runs",
			project:  "provider: ollama\nendpoint: http://gpu-box.example.com:11434\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "llama.cpp is local",
			project:  "provider: llamacpp\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			// The host is what matters: fronting a local server with an
			// OpenAI-compatible gateway is the most common local setup there
			// is, and the provider NAME says nothing about where the tokens
			// are produced.
			name:     "a loopback endpoint under a hosted-sounding provider name",
			project:  "provider: openai\nendpoint: http://127.0.0.1:8000/v1\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "localhost by name",
			project:  "provider: openai\nendpoint: http://localhost:1234/v1\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "an IPv6 loopback endpoint",
			project:  "provider: openai\nendpoint: http://[::1]:8000/v1\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "an mDNS .local host",
			project:  "provider: openai\nendpoint: http://studio.local:1234/v1\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
		{
			name:     "a genuinely remote endpoint",
			project:  "provider: openai\nendpoint: https://api.openai.com/v1\n",
			want:     DefaultMaxParallel,
			wantSaid: false,
		},
		{
			name:     "openrouter",
			project:  "provider: openrouter\nendpoint: https://openrouter.ai/api/v1\n",
			want:     DefaultMaxParallel,
			wantSaid: false,
		},
		{
			name:     "anthropic",
			project:  "provider: anthropic\nendpoint: https://api.anthropic.com/v1\n",
			want:     DefaultMaxParallel,
			wantSaid: false,
		},
		{
			// Precedence, stated as a test: a hosted provider NAME does not
			// beat a loopback endpoint. `provider: openrouter` with the
			// default local endpoint still left in place is a local setup —
			// which is exactly what the process will connect to.
			name:     "a hosted name still pointed at the local endpoint is local",
			project:  "provider: openrouter\n",
			want:     DefaultMaxParallelLocal,
			wantSaid: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			cfg, err := Load(writeMaxParallelProject(t, tc.project))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MaxParallel != tc.want {
				t.Fatalf("max_parallel = %d, want %d for %s @ %s",
					cfg.MaxParallel, tc.want, cfg.Provider, cfg.Endpoint)
			}
			if cfg.MaxParallelExplicit() {
				t.Fatal("nothing set it, so it must not read as explicit")
			}
			notice := cfg.MaxParallelNotice()
			if said := notice != ""; said != tc.wantSaid {
				t.Fatalf("notice fired = %v (%q), want %v", said, notice, tc.wantSaid)
			}
		})
	}
}

// TestExplicitMaxParallelFourOnALocalEndpointStaysFour is the override
// guarantee: the whole point of an endpoint-aware DEFAULT is that it is only a
// default. A user who writes the historical 4 into a config file on a laptop
// must get 4, and must not be told their own setting was lowered.
func TestExplicitMaxParallelFourOnALocalEndpointStaysFour(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\nmax_parallel: 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 4 {
		t.Fatalf("max_parallel = %d, want the user's 4", cfg.MaxParallel)
	}
	if !cfg.MaxParallelExplicit() {
		t.Fatal("a value written in the config file must read as explicit")
	}
	if n := cfg.MaxParallelNotice(); n != "" {
		t.Fatalf("no default was lowered, so nothing must be said: %q", n)
	}
	if got := cfg.Provenance().Describe("max_parallel"); got != "project" {
		t.Fatalf("origin = %q, want project", got)
	}
	// And it survives re-normalization, which is where a derived default would
	// otherwise creep back in.
	cfg.Normalize()
	if cfg.MaxParallel != 4 {
		t.Fatalf("Normalize re-derived an explicit value to %d", cfg.MaxParallel)
	}
}

func TestExplicitMaxParallelOneStaysOne(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\nmax_parallel: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 1 {
		t.Fatalf("max_parallel = %d, want 1", cfg.MaxParallel)
	}
	cfg.Normalize()
	if cfg.MaxParallel != 1 {
		t.Fatalf("Normalize changed an explicit 1 to %d", cfg.MaxParallel)
	}
	if n := cfg.MaxParallelNotice(); n != "" {
		t.Fatalf("an explicit value must not produce a notice: %q", n)
	}
}

func TestExplicitMaxParallelWinsFromEveryLayer(t *testing.T) {
	t.Run("user file", func(t *testing.T) {
		home := isolateHome(t)
		writeUserConfig(t, home, "max_parallel: 4\n")
		cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxParallel != 4 || !cfg.MaxParallelExplicit() {
			t.Fatalf("user layer lost: %d explicit=%v", cfg.MaxParallel, cfg.MaxParallelExplicit())
		}
	})
	t.Run("environment", func(t *testing.T) {
		isolateHome(t)
		t.Setenv("SLMCODE_MAX_PARALLEL", "6")
		cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxParallel != 6 || !cfg.MaxParallelExplicit() {
			t.Fatalf("env layer lost: %d explicit=%v", cfg.MaxParallel, cfg.MaxParallelExplicit())
		}
	})
	t.Run("patch", func(t *testing.T) {
		isolateHome(t)
		cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\n"))
		if err != nil {
			t.Fatal(err)
		}
		four := 4
		cfg.ApplyPatch(Patch{MaxParallel: &four})
		if cfg.MaxParallel != 4 || !cfg.MaxParallelExplicit() {
			t.Fatalf("patch lost: %d explicit=%v", cfg.MaxParallel, cfg.MaxParallelExplicit())
		}
	})
	t.Run("direct setter", func(t *testing.T) {
		cfg := Default(t.TempDir())
		cfg.SetMaxParallel(4)
		cfg.Normalize()
		if cfg.MaxParallel != 4 {
			t.Fatalf("SetMaxParallel lost to Normalize: %d", cfg.MaxParallel)
		}
	})
}

// TestChangingProviderRederivesAnUnsetDefault is why the default is recomputed
// in normalize rather than only in Default(): a project that names a hosted
// provider and nothing else must get the hosted fan-out.
func TestChangingProviderRederivesAnUnsetDefault(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != DefaultMaxParallelLocal {
		t.Fatalf("max_parallel = %d, want the local default", cfg.MaxParallel)
	}
	remote := "openai"
	endpoint := "https://api.openai.com/v1"
	cfg.ApplyPatch(Patch{Provider: &remote, Endpoint: &endpoint})
	if cfg.MaxParallel != DefaultMaxParallel {
		t.Fatalf("max_parallel = %d after moving to a hosted API, want %d",
			cfg.MaxParallel, DefaultMaxParallel)
	}
}

// TestAnInheritedDefaultIsNeverWrittenToTheProjectFile guards the intent-only
// persistence contract: `provider: openai` alone must not also freeze
// `max_parallel: 4` into the file, where it would outlive the default.
func TestAnInheritedDefaultIsNeverWrittenToTheProjectFile(t *testing.T) {
	isolateHome(t)
	root := writeMaxParallelProject(t, "provider: openai\nendpoint: https://api.openai.com/v1\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != DefaultMaxParallel {
		t.Fatalf("max_parallel = %d, want the hosted default", cfg.MaxParallel)
	}
	for _, k := range cfg.Diff() {
		if k == "max_parallel" {
			t.Fatal("an inherited default must not appear in the project file's intent")
		}
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, DirName, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "max_parallel") {
		t.Fatalf("max_parallel was written to a file that never set it:\n%s", data)
	}
}

func TestUnsetRestoresTheDerivedDefault(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\nmax_parallel: 8\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 8 {
		t.Fatalf("max_parallel = %d, want 8", cfg.MaxParallel)
	}
	if err := cfg.Unset("max_parallel"); err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != DefaultMaxParallelLocal {
		t.Fatalf("max_parallel = %d after unset, want the derived default", cfg.MaxParallel)
	}
	if cfg.MaxParallelExplicit() {
		t.Fatal("an unset key must stop reading as explicit")
	}
}

func TestIsLocalEndpointClassification(t *testing.T) {
	tests := []struct {
		provider, endpoint string
		want               bool
	}{
		{"omlx", "http://127.0.0.1:8000/v1", true},
		{"mlx", "", true}, // normalizes to omlx
		{"ollama", "", true},
		{"lmstudio", "", true},
		{"lm-studio", "", true},
		{"llamacpp", "http://10.0.0.5:8080/v1", true},
		{"openai", "http://127.0.0.1:8000/v1", true},
		{"openai", "http://localhost:8000/v1", true},
		{"openai", "http://[::1]:8000/v1", true},
		{"openai", "http://0.0.0.0:8000/v1", true},
		{"openai", "http://mac-studio.local:8000/v1", true},
		{"openai", "https://api.openai.com/v1", false},
		{"openai", "", false},
		{"openrouter", "", false},
		{"anthropic", "", false},
		{"groq", "https://api.groq.com/openai/v1", false},
		// Deliberately NOT loopback: pkg/server refuses wildcard *.localhost
		// for DNS-rebinding reasons, and two opinions in one binary is worse
		// than being conservative here.
		{"openai", "http://evil.localhost:8000/v1", false},
	}
	for _, tc := range tests {
		if got := IsLocalEndpoint(tc.provider, tc.endpoint); got != tc.want {
			t.Errorf("IsLocalEndpoint(%q, %q) = %v, want %v", tc.provider, tc.endpoint, got, tc.want)
		}
	}
}

func TestMaxParallelNoticeNamesTheValueTheReasonAndTheOverride(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(writeMaxParallelProject(t, "provider: omlx\n"))
	if err != nil {
		t.Fatal(err)
	}
	n := cfg.MaxParallelNotice()
	for _, want := range []string{"max_parallel=2", "single local endpoint", "127.0.0.1:8000", "slmcode config set max_parallel"} {
		if !strings.Contains(n, want) {
			t.Fatalf("notice is missing %q:\n%s", want, n)
		}
	}
}

func TestCalibrationKillSwitch(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Normalize()
	if !cfg.CalibrationEnabled() {
		t.Fatal("calibration defaults to auto")
	}
	t.Setenv("SLMCODE_NO_CALIBRATE", "1")
	if cfg.CalibrationEnabled() {
		t.Fatal("SLMCODE_NO_CALIBRATE must be a hard kill switch")
	}
	t.Setenv("SLMCODE_NO_CALIBRATE", "")
	if err := cfg.Set("calibrate", "off"); err != nil {
		t.Fatal(err)
	}
	if cfg.CalibrationEnabled() {
		t.Fatal("calibrate: off must disable the probe")
	}
}

// ── A scheme-less endpoint is a shape config files carry ─────────────────
//
// This package already tolerated it when deciding whether an endpoint is
// local. Anything that BUILT a URL from it did not: net/url refuses
// "127.0.0.1:1234/v1/models" with "first path segment in URL cannot contain
// colon", so a perfectly reachable server was reported as broken — and
// auto-configuration walked past it to something else.

func TestNormalizeEndpointGivesASchemeToWhatHasNone(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"127.0.0.1:1234/v1", "http://127.0.0.1:1234/v1"},
		{"localhost:8000", "http://localhost:8000"},
		{"  127.0.0.1:1234  ", "http://127.0.0.1:1234"},
		// Already has one: left exactly alone, https included.
		{"http://127.0.0.1:1234/v1", "http://127.0.0.1:1234/v1"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
		// Nothing to normalize.
		{"", ""},
		{"   ", ""},
	} {
		if got := NormalizeEndpoint(tc.in); got != tc.want {
			t.Errorf("NormalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The normalized form has to be something net/url will actually build a
// request from — that is the whole point.
func TestANormalizedEndpointParses(t *testing.T) {
	for _, in := range []string{"127.0.0.1:1234/v1", "localhost:8000", "http://x/v1"} {
		u, err := url.Parse(NormalizeEndpoint(in) + "/models")
		if err != nil {
			t.Errorf("url.Parse(%q) failed: %v", in, err)
			continue
		}
		if u.Host == "" {
			t.Errorf("%q parsed with no host: %+v", in, u)
		}
	}
}

// ── Free means "runs on your hardware", not "local-ish" ──────────────────

func TestAServerOnYourOwnHardwareCostsNothing(t *testing.T) {
	for _, p := range []string{"local", "omlx", "mlx", "ollama", "lmstudio", "vllm", "llamacpp", "llama-cpp"} {
		in, out, ok := PricePresetRates("auto", p)
		if !ok || in != 0 || out != 0 {
			t.Errorf("PricePresetRates(auto, %q) = %v/%v/%v, want a confident zero", p, in, out, ok)
		}
	}
}

// A gateway can front a paid API. A confident $0 over a real bill is worse than
// showing nothing, which is what "not configured" produces.
func TestAGatewayIsNotAssumedFree(t *testing.T) {
	for _, p := range []string{"litellm", "custom"} {
		if _, _, ok := PricePresetRates("auto", p); ok {
			t.Errorf("%q was assumed free, but it can proxy a paid API", p)
		}
	}
}
