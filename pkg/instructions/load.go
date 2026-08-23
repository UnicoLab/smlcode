// Package instructions loads Claude Code / Cursor / AGENTS.md style project
// instructions and renders them for a specialist prompt.
package instructions

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Defaults for Load.
const (
	// DefaultMaxBytes is the total instruction budget.
	DefaultMaxBytes = 12000
	// DefaultPerFileBytes caps a single instruction file.
	DefaultPerFileBytes = 4000
)

// DefaultSources are the instruction files, most-authoritative first.
//
// README.md is deliberately NOT here. A README is badges, install steps and
// marketing prose; injecting up to 4000 chars of it as "project instructions"
// is catastrophic dilution for a 7B. Opt back in with Options.IncludeReadme.
func DefaultSources() []string {
	return []string{
		"AGENTS.md",
		"CLAUDE.md",
		"AGENT.md",
		".cursorrules",
		filepath.Join(".cursor", "rules"),
		filepath.Join(".slmcode", "AGENTS.md"),
		filepath.Join(".slmcode", "PROJECT.md"),
	}
}

// Options configures Load.
type Options struct {
	Root string
	// Sources overrides DefaultSources (repo-relative paths).
	Sources []string
	// MaxBytes is the total budget (default DefaultMaxBytes).
	MaxBytes int
	// PerFileBytes caps one file (default DefaultPerFileBytes).
	PerFileBytes int
	// ScopePaths are the files currently in scope for this run. Sections gated
	// with a `paths:` glob only survive when one of these matches.
	ScopePaths []string
	// IncludeReadme opts README.md back into the source list (off by default).
	IncludeReadme bool
}

// LoadProjectInstructions gathers project instructions with default options.
func LoadProjectInstructions(root string) string {
	return Load(Options{Root: root})
}

// LoadForScope is LoadProjectInstructions with path-glob gating active for the
// files this run actually touches.
func LoadForScope(root string, scopePaths []string) string {
	return Load(Options{Root: root, ScopePaths: scopePaths})
}

// Load reads, gates and budgets project instructions.
//
// Three fixes over the historical implementation:
//   - README.md is no longer treated as instructions.
//   - the budget is checked AFTER accounting for the file just added, so a
//     4000-byte file can no longer push the total past the budget.
//   - de-duplication keys on the relative PATH, not on lowercased basename, so
//     `.slmcode/AGENTS.md` layers under the root `AGENTS.md` instead of
//     silently shadowing it.
func Load(opts Options) string {
	root := opts.Root
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	perFile := opts.PerFileBytes
	if perFile <= 0 {
		perFile = DefaultPerFileBytes
	}
	sources := opts.Sources
	if len(sources) == 0 {
		sources = DefaultSources()
		if opts.IncludeReadme {
			sources = append(sources, "README.md")
		}
	}

	var parts []string
	seen := map[string]bool{}
	used := 0
	for _, rel := range sources {
		if used >= maxBytes {
			break
		}
		key := filepath.ToSlash(filepath.Clean(rel))
		if seen[key] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // fixed source list
		if err != nil || len(data) == 0 {
			continue
		}
		seen[key] = true

		body := GateSections(string(data), opts.ScopePaths)
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		// Per-file cap, then the remaining total budget — checked BEFORE the
		// content is committed, not after.
		body = textutil.TruncateDefault(body, perFile)
		remaining := maxBytes - used
		header := "## " + filepath.ToSlash(rel) + "\n\n"
		if len(header)+len(body) > remaining {
			if remaining <= len(header)+64 {
				break
			}
			body = textutil.TruncateDefault(body, remaining-len(header))
		}
		section := header + body
		parts = append(parts, section)
		used += len(section)
	}
	return strings.Join(parts, "\n\n")
}
