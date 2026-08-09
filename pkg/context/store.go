package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Document names under .slmcode/
const (
	DocProject = "PROJECT.md"
	DocContext = "CONTEXT.md"
	DocQuery   = "QUERY.md"
	DocPlan    = "PLAN.md"
	DocTasks   = "TASKS.md"
	DocMemory  = "MEMORY.md"
	DocScratch = "SCRATCH.md"
	DocSkills  = "SKILLS.md"
)

// Store reads/writes the markdown context workspace used as durable memory.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// New creates a store rooted at slmDir (typically <project>/.slmcode).
func New(slmDir string) *Store {
	return &Store{dir: slmDir}
}

// Dir returns the absolute .slmcode directory.
func (s *Store) Dir() string { return s.dir }

// Init creates the directory and default markdown documents if missing.
// Bodies are empty scaffolds — agents populate them during a real run.
func (s *Store) Init(projectName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	for name, body := range defaultDocuments(projectName) {
		path := filepath.Join(s.dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}
	return os.MkdirAll(filepath.Join(s.dir, "sessions"), 0o755)
}

// Path returns the absolute path for a document name.
func (s *Store) Path(name string) string {
	return filepath.Join(s.dir, name)
}

// Read returns document contents (empty string if missing).
func (s *Store) Read(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Write overwrites a document.
func (s *Store) Write(name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(s.Path(name), []byte(content), 0o644)
}

// Append adds a timestamped section to a document.
func (s *Store) Append(name, sectionTitle, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.Path(name)
	existing, _ := os.ReadFile(path)
	stamp := time.Now().Format(time.RFC3339)
	block := fmt.Sprintf("\n\n## %s (%s)\n\n%s\n", sectionTitle, stamp, strings.TrimSpace(body))
	return os.WriteFile(path, append(existing, []byte(block)...), 0o644)
}

// Bundle packs selected docs into a single prompt-friendly string, truncated
// to maxBytes to keep SLM context windows healthy. An 80 % safety margin is
// applied so the bundle does not crowd out system prompts and response space.
func (s *Store) Bundle(maxBytes int, names ...string) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024
	}
	// Safety margin: reserve 20 % for system prompt + response overhead.
	effective := int(float64(maxBytes) * 0.80)
	var b strings.Builder
	for _, name := range names {
		body, err := s.Read(name)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(body) == "" {
			continue
		}
		section := fmt.Sprintf("### %s\n\n%s\n\n", name, body)
		if b.Len()+len(section) > effective {
			remain := effective - b.Len() - 32
			if remain > 0 {
				b.WriteString(section[:remain])
				b.WriteString("\n…\n")
			}
			break
		}
		b.WriteString(section)
	}
	return b.String(), nil
}

// SetQuery writes the active user query document.
func (s *Store) SetQuery(query string) error {
	body := "# Current Query\n\n" + strings.TrimSpace(query) + "\n"
	return s.Write(DocQuery, body)
}

func defaultDocuments(projectName string) map[string]string {
	if projectName == "" {
		projectName = "project"
	}
	return map[string]string{
		DocProject: fmt.Sprintf("# Project: %s\n\n## Overview\n\n\n\n## Conventions\n\n\n\n## Key paths\n\n| Path | Role |\n|------|------|\n| | |\n", projectName),
		DocContext: "# Working Context\n\n## Active focus\n\n\n\n## Recent findings\n\n\n\n## Open questions\n\n\n",
		DocQuery:   "# Current Query\n\n\n",
		DocPlan:    "# Plan\n\n## Summary\n\n\n\n## Goals\n\n\n\n## Steps\n\n\n",
		DocTasks:   "# Tasks\n\n| ID | Title | Role | Status | Depends |\n|----|-------|------|--------|---------|\n",
		DocMemory:  "# Long-term Memory\n\n## Lessons\n\n\n",
		DocScratch: "# Scratch\n\n\n",
		DocSkills:  "# Project Skills\n\n## Catalog\n\n\n",
	}
}
