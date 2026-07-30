package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// CustomSpec is a user-defined specialist persisted as YAML under agents/.
type CustomSpec struct {
	ID           string   `yaml:"id" json:"id"`
	Title        string   `yaml:"title" json:"title"`
	Description  string   `yaml:"description,omitempty" json:"description,omitempty"`
	SystemPrompt string   `yaml:"system_prompt" json:"system_prompt"`
	Skills       []string `yaml:"skills,omitempty" json:"skills,omitempty"`
	Model        string   `yaml:"model,omitempty" json:"model,omitempty"`
	Provider     string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	Tools        bool     `yaml:"tools" json:"tools"`
	MaxIter      int      `yaml:"max_iter" json:"max_iter"`
	Temperature  float64  `yaml:"temperature" json:"temperature"`
	MaxTokens    int      `yaml:"max_tokens" json:"max_tokens"`
	Path         string   `yaml:"-" json:"path,omitempty"`
	Custom       bool     `yaml:"-" json:"custom"`
	Builtin      bool     `yaml:"-" json:"builtin"`
}

var agentIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// BuiltinIDs returns reserved specialist ids that cannot be overwritten/deleted.
func BuiltinIDs() map[string]bool {
	out := map[string]bool{}
	for _, s := range Specs() {
		out[s.ID] = true
	}
	return out
}

// NormalizeCustom validates and fills defaults for a custom agent.
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
	if !agentIDRe.MatchString(c.ID) {
		return fmt.Errorf("invalid id %q (use lowercase letters, digits, _-; 2–64 chars)", c.ID)
	}
	if BuiltinIDs()[c.ID] {
		return fmt.Errorf("id %q is a built-in specialist — choose another name", c.ID)
	}
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
	c.Custom = true
	c.Builtin = false
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

// LoadCustomSpecs discovers custom agents from directories (project first, then global).
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
		return CustomSpec{}, err
	}
	var c CustomSpec
	if err := yaml.Unmarshal(data, &c); err != nil {
		return CustomSpec{}, err
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

// WriteCustom persists a custom agent under dir as <id>.yaml.
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
		Tools:        c.Tools,
		MaxIter:      c.MaxIter,
		Temperature:  c.Temperature,
		MaxTokens:    c.MaxTokens,
	}
	data, err := yaml.Marshal(&payload)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteCustom removes a project-level custom agent file.
func DeleteCustom(dir, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if BuiltinIDs()[id] {
		return fmt.Errorf("cannot delete built-in agent %q", id)
	}
	if !agentIDRe.MatchString(id) {
		return fmt.Errorf("invalid id")
	}
	path := filepath.Join(dir, id+".yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// also try .yml
		path = filepath.Join(dir, id+".yml")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("custom agent %q not found in %s", id, dir)
		}
		return err
	}
	return nil
}

// ToRoleSpec converts a custom definition into a RoleSpec for the factory.
func (c CustomSpec) ToRoleSpec(codingTools []string) RoleSpec {
	tools := []string(nil)
	if c.Tools {
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
		Skills:       append([]string{}, c.Skills...),
		Custom:       true,
	}
}
