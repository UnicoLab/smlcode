// Package blocks provides a YAML-config-driven catalog of reusable SLMCode
// building blocks: pipelines, agents, quality check packs, and language packs.
//
// Discovery order (first id wins per kind): project (.slmcode/blocks) →
// user (~/.slmcode/blocks) → SLMCODE_BLOCKS → repo blocks/ → embedded builtins.
//
// Schema is marketplace-ready: versioned api_version, kind, id, name,
// description, tags, language, author, license, and shareable flag.
package blocks

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// APIVersion is the current YAML schema version.
	APIVersion = "blocks/v1"

	KindPipeline = "pipeline"
	KindAgent    = "agent"
	KindQuality  = "quality"
	KindPack     = "pack"

	SourceBuiltin = "builtin"
	SourceProject = "project"
	SourceUser    = "user"
	SourceExtra   = "extra"
)

var blockIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// Meta is shared marketplace-oriented metadata for every building block.
type Meta struct {
	APIVersion  string   `yaml:"api_version" json:"api_version"`
	Kind        string   `yaml:"kind" json:"kind"`
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Version     string   `yaml:"version,omitempty" json:"version,omitempty"`
	Author      string   `yaml:"author,omitempty" json:"author,omitempty"`
	License     string   `yaml:"license,omitempty" json:"license,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Language    string   `yaml:"language,omitempty" json:"language,omitempty"`
	Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Shareable   *bool    `yaml:"shareable,omitempty" json:"shareable,omitempty"`

	// Runtime-only fields (not authored in YAML).
	Source string `yaml:"-" json:"source,omitempty"`
	Path   string `yaml:"-" json:"path,omitempty"`
}

// Normalize fills defaults and lowercases identifiers.
func (m *Meta) Normalize() {
	if m == nil {
		return
	}
	m.APIVersion = strings.TrimSpace(m.APIVersion)
	if m.APIVersion == "" {
		m.APIVersion = APIVersion
	}
	m.Kind = strings.ToLower(strings.TrimSpace(m.Kind))
	m.ID = strings.ToLower(strings.TrimSpace(m.ID))
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Version = strings.TrimSpace(m.Version)
	if m.Version == "" {
		m.Version = "1.0.0"
	}
	m.Author = strings.TrimSpace(m.Author)
	if m.Author == "" {
		m.Author = "UnicoLab"
	}
	m.License = strings.TrimSpace(m.License)
	if m.License == "" {
		m.License = "MIT"
	}
	m.Language = strings.ToLower(strings.TrimSpace(m.Language))
	m.Icon = strings.TrimSpace(m.Icon)
	var tags []string
	seen := map[string]bool{}
	for _, t := range m.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	m.Tags = tags
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Shareable == nil {
		t := true
		m.Shareable = &t
	}
}

// Validate checks marketplace identity rules.
func (m *Meta) Validate() error {
	if m == nil {
		return fmt.Errorf("nil meta")
	}
	m.Normalize()
	if m.APIVersion != APIVersion {
		return fmt.Errorf("unsupported api_version %q (want %s)", m.APIVersion, APIVersion)
	}
	switch m.Kind {
	case KindPipeline, KindAgent, KindQuality, KindPack:
	default:
		return fmt.Errorf("unknown kind %q", m.Kind)
	}
	if !blockIDRe.MatchString(m.ID) {
		return fmt.Errorf("invalid id %q", m.ID)
	}
	return nil
}

// CatalogEntry is the discovery/API list row for any block.
type CatalogEntry struct {
	Meta
	Builtin bool `json:"builtin"`
	Custom  bool `json:"custom"`
}

// ToEntry builds a catalog row.
func (m Meta) ToEntry() CatalogEntry {
	return CatalogEntry{
		Meta:    m,
		Builtin: m.Source == SourceBuiltin,
		Custom:  m.Source == SourceProject || m.Source == SourceUser || m.Source == SourceExtra,
	}
}
