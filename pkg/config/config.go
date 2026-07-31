package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/permissions"
	"gopkg.in/yaml.v3"
)

const (
	DirName = ".slmcode"

	DefaultProvider = "omlx"
	DefaultEndpoint = "http://127.0.0.1:8000/v1"
	DefaultModel    = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"

	DefaultMaxRetries   = 4
	DefaultMaxParallel  = 2
	DefaultThinkPasses  = 1
	DefaultMaxContextKB = 32
	DefaultTaskTimeout  = 12 * time.Minute
	DefaultQAGateRounds = 3
)

// NormalizeProvider canonicalizes provider aliases.
// Unknown names are kept (treated as OpenAI-compatible gateways).
func NormalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "", "default":
		return DefaultProvider
	case "mlx":
		return "omlx"
	case "openai-compatible", "openai_compat", "openai-compat", "openai_compatible":
		return "openai"
	case "lm-studio", "lm_studio":
		return "lmstudio"
	default:
		return p
	}
}

// IsOllama reports whether the provider uses the native Ollama API.
func IsOllama(p string) bool {
	return NormalizeProvider(p) == "ollama"
}

// IsOpenAICompat reports whether the provider speaks OpenAI Chat Completions.
// Everything except Ollama is treated as OpenAI-compatible.
func IsOpenAICompat(p string) bool {
	return !IsOllama(p)
}

// DefaultEndpointFor returns a sensible base URL for well-known providers.
func DefaultEndpointFor(provider string) string {
	switch NormalizeProvider(provider) {
	case "ollama":
		return "http://127.0.0.1:11434"
	case "lmstudio":
		return "http://127.0.0.1:1234/v1"
	case "openai":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "together":
		return "https://api.together.xyz/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "fireworks":
		return "https://api.fireworks.ai/inference/v1"
	case "vllm", "litellm", "custom":
		return "http://127.0.0.1:8000/v1"
	default: // omlx and unknown local gateways
		return DefaultEndpoint
	}
}

// Backend selects the execution engine.
const (
	BackendSLMCode    = "slmcode"
	BackendClaudeCode = "claude-code"
)

// Mode selects full pipeline vs single specialist.
const (
	ModeFull       = "full"
	ModeSpecialist = "specialist"
)

// Config is persisted in .slmcode/config.yaml and overridable via flags/env.
type Config struct {
	Root string `yaml:"root" json:"root"`

	// Provider selects the LLM backend. Built-ins: omlx | ollama | openai |
	// lmstudio | openrouter | vllm | litellm | together | groq | deepseek | …
	// Any other name is treated as an OpenAI-compatible gateway (set endpoint).
	Provider string `yaml:"provider" json:"provider"`
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	APIKey   string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Model    string `yaml:"model" json:"model"`

	Backend string `yaml:"backend" json:"backend"` // slmcode | claude-code

	// Mode: full (default pipeline) | specialist (single role)
	Mode string `yaml:"mode" json:"mode"`
	// Specialist role id when Mode=specialist (worker, explorer, …)
	Specialist string `yaml:"specialist" json:"specialist"`
	// PinnedSkills are always loaded (in addition to @skill: refs / matching).
	PinnedSkills []string `yaml:"pinned_skills" json:"pinned_skills"`

	Temperature  float64       `yaml:"temperature" json:"temperature"`
	MaxTokens    int           `yaml:"max_tokens" json:"max_tokens"`
	MaxRetries   int           `yaml:"max_retries" json:"max_retries"`
	MaxParallel  int           `yaml:"max_parallel" json:"max_parallel"`
	MaxContextKB int           `yaml:"max_context_kb" json:"max_context_kb"`
	ThinkPasses  int           `yaml:"think_passes" json:"think_passes"`
	TaskTimeout  time.Duration `yaml:"task_timeout" json:"task_timeout"`

	// QAGate runs an iterate-until-green test command after the board finishes.
	QAGate          bool   `yaml:"qa_gate" json:"qa_gate"`
	QAGateCommand   string `yaml:"qa_gate_command" json:"qa_gate_command"` // empty = auto-detect
	QAGateMaxRounds int    `yaml:"qa_gate_max_rounds" json:"qa_gate_max_rounds"`

	DryRun      bool `yaml:"dry_run" json:"dry_run"`
	Verbose     bool `yaml:"verbose" json:"verbose"`
	AutoApprove bool `yaml:"auto_approve" json:"auto_approve"`

	// Permission: auto | dry-run | review (Claude Code–style write policy)
	Permission string `yaml:"permission" json:"permission"`
	// ShellPermission: allow | ask | deny (ws_shell policy; independent of file writes)
	ShellPermission string `yaml:"shell_permission" json:"shell_permission"`
	// CompactMode trims live event verbosity in TUI/CLI.
	CompactMode bool `yaml:"compact_mode" json:"compact_mode"`

	// SkillsDirs are extra skill roots (in addition to bundled + .slmcode/skills).
	SkillsDirs []string `yaml:"skills_dirs" json:"skills_dirs"`

	// Server
	Listen string `yaml:"listen" json:"listen"`

	// ClaudeCodeBin when backend=claude-code
	ClaudeCodeBin string `yaml:"claude_code_bin" json:"claude_code_bin"`
}

func Default(root string) *Config {
	if root == "" {
		root, _ = os.Getwd()
	}
	return &Config{
		Root:            root,
		Provider:        DefaultProvider,
		Endpoint:        DefaultEndpoint,
		Model:           DefaultModel,
		Backend:         BackendSLMCode,
		Mode:            ModeFull,
		Temperature:     0.2,
		MaxTokens:       4096,
		MaxRetries:      DefaultMaxRetries,
		MaxParallel:     DefaultMaxParallel,
		MaxContextKB:    DefaultMaxContextKB,
		ThinkPasses:     DefaultThinkPasses,
		TaskTimeout:     DefaultTaskTimeout,
		QAGate:          true,
		QAGateMaxRounds: DefaultQAGateRounds,
		Listen:          "127.0.0.1:7420",
		ClaudeCodeBin:   "claude",
		Permission:      "auto",
		ShellPermission: "allow",
	}
}

func (c *Config) SlmDir() string     { return filepath.Join(c.Root, DirName) }
func (c *Config) ConfigPath() string { return filepath.Join(c.SlmDir(), "config.yaml") }
func (c *Config) SkillsDir() string  { return filepath.Join(c.SlmDir(), "skills") }
func (c *Config) AgentsDir() string  { return filepath.Join(c.SlmDir(), "agents") }

// ResolveAPIKey fills API key from env or provider-specific stores.
func (c *Config) ResolveAPIKey() {
	if c.APIKey != "" {
		return
	}
	if v := os.Getenv("SLMCODE_API_KEY"); v != "" {
		c.APIKey = v
		return
	}
	p := NormalizeProvider(c.Provider)
	switch p {
	case "omlx":
		if v := os.Getenv("OMLX_API_KEY"); v != "" {
			c.APIKey = v
			return
		}
		if k := readOmlxAPIKey(); k != "" {
			c.APIKey = k
		}
	case "openai":
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "openrouter":
		if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "groq":
		if v := os.Getenv("GROQ_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "together":
		if v := os.Getenv("TOGETHER_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "deepseek":
		if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
			c.APIKey = v
		}
	default:
		// Generic OpenAI-compat gateways often reuse OPENAI_API_KEY.
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			c.APIKey = v
			return
		}
		if v := os.Getenv("OMLX_API_KEY"); v != "" {
			c.APIKey = v
		}
	}
}

// ApplyEnv overlays SLMCODE_* / OPENAI_BASE_URL env vars onto the config.
// Call after Load (or Default) and before flag overrides.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("SLMCODE_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("SLMCODE_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("SLMCODE_ENDPOINT"); v != "" {
		c.Endpoint = v
	} else if v := os.Getenv("OPENAI_BASE_URL"); v != "" && IsOpenAICompat(c.Provider) {
		c.Endpoint = v
	}
	if v := os.Getenv("SLMCODE_BACKEND"); v != "" {
		c.Backend = v
	}
	normalize(c)
	c.ResolveAPIKey()
}

func readOmlxAPIKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".omlx", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Support common key spellings across oMLX versions.
	var s struct {
		APIKey string `json:"api_key"`
		Auth   struct {
			APIKey    string `json:"api_key"`
			APIKeyAlt string `json:"apiKey"`
		} `json:"auth"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	if k := strings.TrimSpace(s.Auth.APIKey); k != "" {
		return k
	}
	if k := strings.TrimSpace(s.Auth.APIKeyAlt); k != "" {
		return k
	}
	return strings.TrimSpace(s.APIKey)
}

func Load(root string) (*Config, error) {
	cfg := Default(root)
	data, err := os.ReadFile(cfg.ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			cfg.ApplyEnv()
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Root == "" {
		cfg.Root = root
	}
	normalize(cfg)
	cfg.ApplyEnv()
	return cfg, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(c.SlmDir(), 0o755); err != nil {
		return err
	}
	// Never persist secrets by default — keep api_key out of yaml unless explicitly set via env copy.
	copy := *c
	if os.Getenv("SLMCODE_PERSIST_API_KEY") != "1" {
		copy.APIKey = ""
	}
	data, err := yaml.Marshal(&copy)
	if err != nil {
		return err
	}
	return os.WriteFile(c.ConfigPath(), data, 0o644)
}

func normalize(c *Config) {
	c.Provider = NormalizeProvider(c.Provider)
	if c.Endpoint == "" {
		c.Endpoint = DefaultEndpointFor(c.Provider)
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Backend == "" {
		c.Backend = BackendSLMCode
	}
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "", ModeFull:
		c.Mode = ModeFull
	case ModeSpecialist, "single", "agent":
		c.Mode = ModeSpecialist
	default:
		c.Mode = ModeFull
	}
	c.Specialist = strings.TrimSpace(c.Specialist)
	// MaxRetries=0 means no review retries (one attempt). Only negative is invalid.
	if c.MaxRetries < 0 {
		c.MaxRetries = DefaultMaxRetries
	}
	if c.MaxParallel <= 0 {
		c.MaxParallel = DefaultMaxParallel
	}
	if c.ThinkPasses <= 0 {
		c.ThinkPasses = DefaultThinkPasses
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 4096
	}
	if c.MaxContextKB <= 0 {
		c.MaxContextKB = DefaultMaxContextKB
	}
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = DefaultTaskTimeout
	}
	if c.QAGateMaxRounds <= 0 {
		c.QAGateMaxRounds = DefaultQAGateRounds
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:7420"
	}
	if c.ClaudeCodeBin == "" {
		c.ClaudeCodeBin = "claude"
	}
	c.Permission = permissions.Normalize(c.Permission)
	if c.DryRun {
		c.Permission = permissions.ModeDryRun
	} else if c.Permission == permissions.ModeDryRun {
		c.DryRun = true
	}
	c.ShellPermission = permissions.NormalizeShell(c.ShellPermission)
}

// Patch is a partial config update. Nil fields are left unchanged.
type Patch struct {
	Model           *string   `json:"model,omitempty"`
	Provider        *string   `json:"provider,omitempty"`
	Endpoint        *string   `json:"endpoint,omitempty"`
	APIKey          *string   `json:"api_key,omitempty"`
	Backend         *string   `json:"backend,omitempty"`
	Mode            *string   `json:"mode,omitempty"`
	Specialist      *string   `json:"specialist,omitempty"`
	PinnedSkills    *[]string `json:"pinned_skills,omitempty"`
	ThinkPasses     *int      `json:"think_passes,omitempty"`
	MaxParallel     *int      `json:"max_parallel,omitempty"`
	MaxRetries      *int      `json:"max_retries,omitempty"`
	MaxContextKB    *int      `json:"max_context_kb,omitempty"`
	QAGate          *bool     `json:"qa_gate,omitempty"`
	QAGateCommand   *string   `json:"qa_gate_command,omitempty"`
	QAGateMaxRounds *int      `json:"qa_gate_max_rounds,omitempty"`
	DryRun          *bool     `json:"dry_run,omitempty"`
	Verbose         *bool     `json:"verbose,omitempty"`
	Permission      *string   `json:"permission,omitempty"`
	ShellPermission *string   `json:"shell_permission,omitempty"`
	CompactMode     *bool     `json:"compact_mode,omitempty"`
	Listen          *string   `json:"listen,omitempty"`
}

// ApplyPatch merges a partial update and re-normalizes permission/dry-run.
func (c *Config) ApplyPatch(p Patch) {
	if p.Model != nil && *p.Model != "" {
		c.Model = *p.Model
	}
	prevProvider := NormalizeProvider(c.Provider)
	prevDefaultEP := DefaultEndpointFor(prevProvider)
	providerChanged := false
	if p.Provider != nil && *p.Provider != "" {
		next := NormalizeProvider(*p.Provider)
		if next != prevProvider {
			providerChanged = true
		}
		c.Provider = next
	}
	if providerChanged {
		// Refresh endpoint unless the caller sent a non-default override.
		explicit := p.Endpoint != nil && *p.Endpoint != "" &&
			*p.Endpoint != prevDefaultEP && *p.Endpoint != c.Endpoint
		if explicit {
			c.Endpoint = *p.Endpoint
		} else {
			c.Endpoint = DefaultEndpointFor(c.Provider)
		}
	} else if p.Endpoint != nil && *p.Endpoint != "" {
		c.Endpoint = *p.Endpoint
	}
	if p.APIKey != nil {
		key := strings.TrimSpace(*p.APIKey)
		// Ignore redacted placeholder from Public() round-trips.
		if key != "" && key != "***" {
			c.APIKey = key
		}
	}
	if p.Backend != nil && *p.Backend != "" {
		c.Backend = *p.Backend
	}
	if p.Mode != nil && *p.Mode != "" {
		c.Mode = *p.Mode
	}
	if p.Specialist != nil {
		c.Specialist = strings.TrimSpace(*p.Specialist)
	}
	if p.PinnedSkills != nil {
		c.PinnedSkills = append([]string{}, (*p.PinnedSkills)...)
	}
	if p.ThinkPasses != nil && *p.ThinkPasses > 0 {
		c.ThinkPasses = *p.ThinkPasses
	}
	if p.MaxParallel != nil && *p.MaxParallel > 0 {
		c.MaxParallel = *p.MaxParallel
	}
	if p.MaxRetries != nil && *p.MaxRetries >= 0 {
		c.MaxRetries = *p.MaxRetries
	}
	if p.MaxContextKB != nil && *p.MaxContextKB > 0 {
		c.MaxContextKB = *p.MaxContextKB
	}
	if p.QAGate != nil {
		c.QAGate = *p.QAGate
	}
	if p.QAGateCommand != nil {
		c.QAGateCommand = strings.TrimSpace(*p.QAGateCommand)
	}
	if p.QAGateMaxRounds != nil && *p.QAGateMaxRounds > 0 {
		c.QAGateMaxRounds = *p.QAGateMaxRounds
	}
	if p.Verbose != nil {
		c.Verbose = *p.Verbose
	}
	if p.Listen != nil && *p.Listen != "" {
		c.Listen = *p.Listen
	}
	// Permission and dry-run stay in sync. Explicit permission wins when both set.
	if p.Permission != nil && *p.Permission != "" {
		c.Permission = permissions.Normalize(*p.Permission)
		c.DryRun = c.Permission == permissions.ModeDryRun
	} else if p.DryRun != nil {
		c.DryRun = *p.DryRun
		if c.DryRun {
			c.Permission = permissions.ModeDryRun
		} else if c.Permission == permissions.ModeDryRun {
			c.Permission = permissions.ModeAuto
		}
	}
	if p.ShellPermission != nil && *p.ShellPermission != "" {
		c.ShellPermission = permissions.NormalizeShell(*p.ShellPermission)
	}
	if p.CompactMode != nil {
		c.CompactMode = *p.CompactMode
	}
	normalize(c)
}

// Public returns a safe copy for the UI (redacts api key).
func (c *Config) Public() Config {
	p := *c
	if p.APIKey != "" {
		p.APIKey = "***"
	}
	return p
}
