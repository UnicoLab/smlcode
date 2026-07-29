package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczkowski/slmcode/pkg/permissions"
	"gopkg.in/yaml.v3"
)

const (
	DirName = ".slmcode"

	DefaultProvider = "omlx"
	DefaultEndpoint = "http://127.0.0.1:8000/v1"
	DefaultModel    = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"

	DefaultMaxRetries   = 2
	DefaultMaxParallel  = 3
	DefaultThinkPasses  = 2
	DefaultMaxContextKB = 16
	DefaultTaskTimeout  = 5 * time.Minute
)

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

	Provider string `yaml:"provider" json:"provider"` // omlx | ollama | openai
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

	DryRun      bool `yaml:"dry_run" json:"dry_run"`
	Verbose     bool `yaml:"verbose" json:"verbose"`
	AutoApprove bool `yaml:"auto_approve" json:"auto_approve"`

	// Permission: auto | dry-run | review (Claude Code–style write policy)
	Permission string `yaml:"permission" json:"permission"`

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
		Root:          root,
		Provider:      DefaultProvider,
		Endpoint:      DefaultEndpoint,
		Model:         DefaultModel,
		Backend:       BackendSLMCode,
		Mode:          ModeFull,
		Temperature:   0.2,
		MaxTokens:     4096,
		MaxRetries:    DefaultMaxRetries,
		MaxParallel:   DefaultMaxParallel,
		MaxContextKB:  DefaultMaxContextKB,
		ThinkPasses:   DefaultThinkPasses,
		TaskTimeout:   DefaultTaskTimeout,
		Listen:        "127.0.0.1:7420",
		ClaudeCodeBin: "claude",
		Permission:    "auto",
	}
}

func (c *Config) SlmDir() string     { return filepath.Join(c.Root, DirName) }
func (c *Config) ConfigPath() string { return filepath.Join(c.SlmDir(), "config.yaml") }
func (c *Config) SkillsDir() string  { return filepath.Join(c.SlmDir(), "skills") }

// ResolveAPIKey fills API key from env or ~/.omlx/settings.json when using omlx.
func (c *Config) ResolveAPIKey() {
	if c.APIKey != "" {
		return
	}
	if v := os.Getenv("SLMCODE_API_KEY"); v != "" {
		c.APIKey = v
		return
	}
	if v := os.Getenv("OMLX_API_KEY"); v != "" {
		c.APIKey = v
		return
	}
	if c.Provider == "omlx" || c.Provider == "" {
		if k := readOmlxAPIKey(); k != "" {
			c.APIKey = k
		}
	}
	if c.Provider == "openai" {
		c.APIKey = os.Getenv("OPENAI_API_KEY")
	}
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
	var s struct {
		Auth struct {
			APIKey string `json:"api_key"`
		} `json:"auth"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	return s.Auth.APIKey
}

func Load(root string) (*Config, error) {
	cfg := Default(root)
	data, err := os.ReadFile(cfg.ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			cfg.ResolveAPIKey()
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
	cfg.ResolveAPIKey()
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
	if c.Provider == "" {
		c.Provider = DefaultProvider
	}
	if c.Endpoint == "" {
		if c.Provider == "ollama" {
			c.Endpoint = "http://127.0.0.1:11434"
		} else {
			c.Endpoint = DefaultEndpoint
		}
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
}

// Patch is a partial config update. Nil fields are left unchanged.
type Patch struct {
	Model        *string  `json:"model,omitempty"`
	Provider     *string  `json:"provider,omitempty"`
	Endpoint     *string  `json:"endpoint,omitempty"`
	Backend      *string  `json:"backend,omitempty"`
	Mode         *string  `json:"mode,omitempty"`
	Specialist   *string  `json:"specialist,omitempty"`
	PinnedSkills *[]string `json:"pinned_skills,omitempty"`
	ThinkPasses  *int     `json:"think_passes,omitempty"`
	MaxParallel  *int     `json:"max_parallel,omitempty"`
	MaxRetries   *int     `json:"max_retries,omitempty"`
	MaxContextKB *int     `json:"max_context_kb,omitempty"`
	DryRun       *bool    `json:"dry_run,omitempty"`
	Verbose      *bool    `json:"verbose,omitempty"`
	Permission   *string  `json:"permission,omitempty"`
	Listen       *string  `json:"listen,omitempty"`
}

// ApplyPatch merges a partial update and re-normalizes permission/dry-run.
func (c *Config) ApplyPatch(p Patch) {
	if p.Model != nil && *p.Model != "" {
		c.Model = *p.Model
	}
	if p.Provider != nil && *p.Provider != "" {
		c.Provider = *p.Provider
	}
	if p.Endpoint != nil && *p.Endpoint != "" {
		c.Endpoint = *p.Endpoint
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
