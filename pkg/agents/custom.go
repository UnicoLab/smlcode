package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// CustomSpec is a user-defined specialist (or builtin override) persisted as YAML under agents/.
type CustomSpec struct {
	ID           string   `yaml:"id" json:"id"`
	Title        string   `yaml:"title" json:"title"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
	SystemPrompt string   `yaml:"system_prompt" json:"system_prompt"`
	Skills       []string `yaml:"skills,omitempty" json:"skills,omitempty"`
	Model        string   `yaml:"model,omitempty" json:"model,omitempty"`
	Provider     string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Endpoint     string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Tools is a pointer so omitempty/omitted YAML does not clear builtin tools on override.
	Tools       *bool   `yaml:"tools,omitempty" json:"tools"`
	MaxIter     int     `yaml:"max_iter" json:"max_iter"`
	Temperature float64 `yaml:"temperature" json:"temperature"`
	MaxTokens   int     `yaml:"max_tokens" json:"max_tokens"`
	Path        string  `yaml:"-" json:"path,omitempty"`
	Custom      bool    `yaml:"-" json:"custom"`
	Builtin     bool    `yaml:"-" json:"builtin"`
	Override    bool    `yaml:"-" json:"override,omitempty"` // true when patching a built-in
}

var agentIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// BoolPtr is a small helper for tests and API callers.
func BoolPtr(v bool) *bool { return &v }

// ToolsEnabled reports whether coding tools are on (default true for new customs).
func (c CustomSpec) ToolsEnabled() bool {
	if c.Tools == nil {
		return !c.Override // customs default on; overrides leave base alone
	}
	return *c.Tools
}

// BuiltinIDs returns reserved specialist ids.
func BuiltinIDs() map[string]bool {
	out := map[string]bool{}
	for _, s := range Specs() {
		out[s.ID] = true
	}
	return out
}

// NormalizeCustom validates and fills defaults for a custom agent or builtin override.
func NormalizeCustom(c *CustomSpec) error {
	if c == nil {
		return fmt.Errorf("agent required")
	}
	c.ID = strings.ToLower(strings.TrimSpace(c.ID))
	c.Title = strings.TrimSpace(c.Title)
	c.Description = strings.TrimSpace(c.Description)
	c.SystemPrompt = strings.TrimSpace(c.SystemPrompt)
	c.Model = strings.TrimSpace(c.Model)
	c.Provider = strings.TrimSpace(c.Provider)
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if !agentIDRe.MatchString(c.ID) {
		return fmt.Errorf("invalid id %q (use lowercase letters, digits, _-; 2–64 chars)", c.ID)
	}
	isBuiltin := BuiltinIDs()[c.ID]
	if isBuiltin {
		c.Override = true
		c.Custom = false
		c.Builtin = true
		if c.Title == "" {
			for _, s := range Specs() {
				if s.ID == c.ID {
					c.Title = s.Title
					break
				}
			}
		}
		if c.Title == "" {
			c.Title = c.ID
		}
		// 0 numeric fields mean "keep builtin default" at merge time.
	} else {
		if c.Title == "" {
			c.Title = c.ID
		}
		if c.SystemPrompt == "" {
			c.SystemPrompt = "You are a custom SLMCode specialist. Complete the assigned task carefully.\n" +
				"Prefer workspace tools for code changes. Finish with a short status summary."
		}
		if c.MaxIter <= 0 {
			c.MaxIter = 10
		}
		if c.MaxTokens <= 0 {
			c.MaxTokens = 2048
		}
		if c.Temperature <= 0 {
			c.Temperature = 0.2
		}
		if c.Tools == nil {
			c.Tools = BoolPtr(true)
		}
		c.Custom = true
		c.Builtin = false
		c.Override = false
	}
	var skills []string
	seen := map[string]bool{}
	for _, s := range c.Skills {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		skills = append(skills, s)
	}
	c.Skills = skills
	return nil
}

// GlobalAgentRoots returns user-level agent directories (first wins on name clash).
func GlobalAgentRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".slmcode", "agents"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		roots = append(roots, filepath.Join(xdg, "slmcode", "agents"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".config", "slmcode", "agents"))
	}
	return roots
}

// LoadCustomSpecs discovers custom agents / overrides from directories (project first, then global).
// First id wins.
func LoadCustomSpecs(dirs ...string) ([]CustomSpec, error) {
	seen := map[string]bool{}
	var out []CustomSpec
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
				continue
			}
			path := filepath.Join(dir, name)
			c, err := ReadCustomFile(path)
			if err != nil {
				continue
			}
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// ReadCustomFile loads one agent YAML.
func ReadCustomFile(path string) (CustomSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return CustomSpec{}, err
		}
		data, err = os.ReadFile(atomicfile.BackupPath(path))
		if err != nil {
			return CustomSpec{}, err
		}
	}
	var c CustomSpec
	if err := yaml.Unmarshal(data, &c); err != nil {
		backup, bakErr := os.ReadFile(atomicfile.BackupPath(path))
		if bakErr != nil {
			return CustomSpec{}, err
		}
		data = backup
		c = CustomSpec{}
		if err := yaml.Unmarshal(data, &c); err != nil {
			return CustomSpec{}, err
		}
	}
	if strings.TrimSpace(c.ID) == "" {
		base := filepath.Base(path)
		c.ID = strings.TrimSuffix(strings.TrimSuffix(base, filepath.Ext(base)), ".yml")
	}
	if err := NormalizeCustom(&c); err != nil {
		return CustomSpec{}, err
	}
	c.Path = path
	return c, nil
}

// WriteCustom persists a custom agent (or built-in override) under dir as <id>.yaml.
func WriteCustom(dir string, c CustomSpec) (string, error) {
	if err := NormalizeCustom(&c); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, c.ID+".yaml")
	payload := CustomSpec{
		ID:           c.ID,
		Title:        c.Title,
		Description:  c.Description,
		SystemPrompt: c.SystemPrompt,
		Skills:       c.Skills,
		Model:        c.Model,
		Provider:     c.Provider,
		Endpoint:     c.Endpoint,
		Tools:        c.Tools,
		MaxIter:      c.MaxIter,
		Temperature:  c.Temperature,
		MaxTokens:    c.MaxTokens,
	}
	data, err := yaml.Marshal(&payload)
	if err != nil {
		return "", err
	}
	if err := atomicfile.WriteWithBackup(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteCustom removes a project-level custom agent or built-in override file.
// Built-in specialists themselves cannot be deleted — only their override YAML.
func DeleteCustom(dir, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if !agentIDRe.MatchString(id) {
		return fmt.Errorf("invalid id")
	}
	path := filepath.Join(dir, id+".yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(dir, id+".yml")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			if BuiltinIDs()[id] {
				return fmt.Errorf("built-in agent %q has no project override to delete", id)
			}
			return fmt.Errorf("custom agent %q not found in %s", id, dir)
		}
		return err
	}
	return nil
}

// ToRoleSpec converts a custom definition into a RoleSpec for the factory.
func (c CustomSpec) ToRoleSpec(codingTools []string) RoleSpec {
	tools := []string(nil)
	if c.ToolsEnabled() {
		tools = codingTools
	}
	prompt := c.SystemPrompt
	if len(c.Skills) > 0 {
		prompt += "\n\n## Preferred skills\nUse conventions from: " + strings.Join(c.Skills, ", ")
	}
	return RoleSpec{
		ID:           c.ID,
		Title:        c.Title,
		SystemPrompt: prompt,
		Tools:        tools,
		MaxIter:      c.MaxIter,
		Temperature:  c.Temperature,
		MaxTokens:    c.MaxTokens,
		Model:        c.Model,
		Provider:     c.Provider,
		Endpoint:     c.Endpoint,
		Skills:       append([]string{}, c.Skills...),
		Custom:       !c.Override,
	}
}

// ApplyOverride merges a YAML override onto a built-in RoleSpec.
// Empty override fields leave the builtin value unchanged.
func ApplyOverride(base *RoleSpec, o CustomSpec, codingTools []string) {
	if base == nil {
		return
	}
	if strings.TrimSpace(o.Title) != "" {
		base.Title = strings.TrimSpace(o.Title)
	}
	if strings.TrimSpace(o.SystemPrompt) != "" {
		prompt := strings.TrimSpace(o.SystemPrompt)
		if len(o.Skills) > 0 {
			prompt += "\n\n## Preferred skills\nUse conventions from: " + strings.Join(o.Skills, ", ")
		}
		base.SystemPrompt = prompt
	} else if len(o.Skills) > 0 {
		if !strings.Contains(base.SystemPrompt, "## Preferred skills") {
			base.SystemPrompt += "\n\n## Preferred skills\nUse conventions from: " + strings.Join(o.Skills, ", ")
		}
	}
	if strings.TrimSpace(o.Model) != "" {
		base.Model = strings.TrimSpace(o.Model)
	}
	if strings.TrimSpace(o.Provider) != "" {
		base.Provider = strings.TrimSpace(o.Provider)
	}
	if strings.TrimSpace(o.Endpoint) != "" {
		base.Endpoint = strings.TrimSpace(o.Endpoint)
	}
	if o.MaxIter > 0 {
		base.MaxIter = o.MaxIter
	}
	if o.MaxTokens > 0 {
		base.MaxTokens = o.MaxTokens
	}
	if o.Temperature > 0 {
		base.Temperature = o.Temperature
	}
	if o.Tools != nil {
		if *o.Tools {
			base.Tools = codingTools
		} else {
			base.Tools = nil
		}
	}
	if len(o.Skills) > 0 {
		base.Skills = append([]string{}, o.Skills...)
	}
}
