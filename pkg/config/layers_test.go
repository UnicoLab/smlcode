package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeUserConfig(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, DirName, "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrecedenceDefaultsUserProjectEnv(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		project    string
		env        map[string]string
		wantValue  func(*Config) any
		want       any
		wantOrigin string
	}{
		{
			name:       "default when nothing sets it",
			wantValue:  func(c *Config) any { return c.MaxParallel },
			want:       DefaultMaxParallel,
			wantOrigin: "default",
		},
		{
			name:       "user beats default",
			user:       "max_parallel: 8\n",
			wantValue:  func(c *Config) any { return c.MaxParallel },
			want:       8,
			wantOrigin: "user",
		},
		{
			name:       "project beats user",
			user:       "max_parallel: 8\n",
			project:    "max_parallel: 3\n",
			wantValue:  func(c *Config) any { return c.MaxParallel },
			want:       3,
			wantOrigin: "project",
		},
		{
			name:       "env beats project",
			user:       "max_parallel: 8\n",
			project:    "max_parallel: 3\n",
			env:        map[string]string{"SLMCODE_MAX_PARALLEL": "11"},
			wantValue:  func(c *Config) any { return c.MaxParallel },
			want:       11,
			wantOrigin: "env SLMCODE_MAX_PARALLEL",
		},
		{
			name:       "user layer reaches a non-patchable field too",
			user:       "claude_code_bin: /opt/claude\n",
			wantValue:  func(c *Config) any { return c.ClaudeCodeBin },
			want:       "/opt/claude",
			wantOrigin: "user",
		},
		{
			name:       "env covers a new knob",
			env:        map[string]string{"SLMCODE_EVOLVE": "false"},
			wantValue:  func(c *Config) any { return c.Evolve },
			want:       false,
			wantOrigin: "env SLMCODE_EVOLVE",
		},
		{
			name:       "env parses a duration",
			env:        map[string]string{"SLMCODE_ESCALATE_ASK_TIMEOUT": "90s"},
			wantValue:  func(c *Config) any { return c.EscalateAskTimeout },
			want:       90 * time.Second,
			wantOrigin: "env SLMCODE_ESCALATE_ASK_TIMEOUT",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateHome(t)
			if tc.user != "" {
				writeUserConfig(t, home, tc.user)
			}
			root := t.TempDir()
			if tc.project != "" {
				if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, DirName, "config.yaml"),
					[]byte(tc.project), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.wantValue(cfg); got != tc.want {
				t.Fatalf("value = %v, want %v", got, tc.want)
			}
			key := "max_parallel"
			switch tc.name {
			case "user layer reaches a non-patchable field too":
				key = "claude_code_bin"
			case "env covers a new knob":
				key = "evolve"
			case "env parses a duration":
				key = "escalate_ask_timeout"
			}
			if got := cfg.Provenance().Describe(key); got != tc.wantOrigin {
				t.Fatalf("origin(%s) = %q, want %q", key, got, tc.wantOrigin)
			}
		})
	}
}

func TestFlagOriginIsReported(t *testing.T) {
	isolateHome(t)
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = "gpt-4o-mini"
	cfg.MarkFlag("model", "--model")
	if got := cfg.Provenance().Describe("model"); got != "flag --model" {
		t.Fatalf("origin = %q", got)
	}
}

func TestCorruptUserConfigWarnsInsteadOfFailing(t *testing.T) {
	home := isolateHome(t)
	writeUserConfig(t, home, "max_parallel: [not, a, number]\n")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("a bad user file must not make the workspace unopenable: %v", err)
	}
	if cfg.MaxParallel != DefaultMaxParallel {
		t.Fatalf("bad value was applied: %d", cfg.MaxParallel)
	}
	if len(cfg.Provenance().Warnings) == 0 {
		t.Fatal("the problem must be reported as a warning")
	}
}

func TestUserConfigPathsOrder(t *testing.T) {
	home := isolateHome(t)
	explicit := filepath.Join(home, "explicit.yaml")
	t.Setenv("SLMCODE_USER_CONFIG", explicit)
	paths := UserConfigPaths()
	if len(paths) == 0 || paths[0] != explicit {
		t.Fatalf("SLMCODE_USER_CONFIG must win: %v", paths)
	}
	if err := os.WriteFile(explicit, []byte("fast_model: explicit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := UserConfigPath(); got != explicit {
		t.Fatalf("UserConfigPath = %q", got)
	}
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FastModel != "explicit" {
		t.Fatalf("fast_model = %q", cfg.FastModel)
	}
}

func TestWriteUserValueRewritesOneKey(t *testing.T) {
	home := isolateHome(t)
	path := writeUserConfig(t, home, "fast_model: keep-me\nmax_parallel: 2\n")
	if err := WriteUserValue(path, "max_parallel", 7); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 7 {
		t.Fatalf("max_parallel = %d", cfg.MaxParallel)
	}
	if cfg.FastModel != "keep-me" {
		t.Fatalf("the untouched key was lost: %q", cfg.FastModel)
	}
}

func TestUnsetFallsBackToTheInheritedValue(t *testing.T) {
	home := isolateHome(t)
	writeUserConfig(t, home, "max_parallel: 8\n")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DirName, "config.yaml"),
		[]byte("max_parallel: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Unset("max_parallel"); err != nil {
		t.Fatal(err)
	}
	// Unset means "stop deciding here", not "zero it": the user layer's 8 is
	// what this project would have had without the override.
	if cfg.MaxParallel != 8 {
		t.Fatalf("max_parallel = %d, want the inherited 8", cfg.MaxParallel)
	}
}
