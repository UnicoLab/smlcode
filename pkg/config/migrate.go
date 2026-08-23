package config

import (
	"sort"
	"strings"
)

// CurrentConfigVersion is the generation `Save` stamps into config.yaml.
//
// There was no migration mechanism at all before this: renaming a key silently
// broke every existing project, because an unknown YAML key decodes to nothing
// and the field quietly reverts to its default. Every rename or semantic change
// from here on gets a migration step below and a version bump, so an old file
// is upgraded on load instead of being half-ignored.
//
//	1 — first versioned generation. Files written before versioning report 0
//	    and are treated as version 1 with the legacy fix-ups in migrations[0].
const CurrentConfigVersion = 1

// migration transforms a raw decoded config document from version From to
// From+1. Steps run in order, so a version-0 file walks the whole chain.
type migration struct {
	From int
	Note string
	// Apply mutates the raw key→value document in place.
	Apply func(raw map[string]any)
}

// migrations is the ordered upgrade chain.
var migrations = []migration{
	{
		From: 0,
		Note: "drop the embedded absolute root path; adopt intent-only files",
		Apply: func(raw map[string]any) {
			// Pre-versioning files carried `root: /abs/path/on/another/machine`,
			// which Load honored. It is derived from the workspace now.
			delete(raw, "root")
		},
	},
}

// renames maps retired YAML keys onto their current spelling. It is applied on
// every load regardless of version, so a file that was hand-edited from an old
// doc snippet still lands on the right field.
var renames = map[string]string{
	// Historic spellings kept working by the CLI's alias table; honoring them
	// in the file too means a copy-pasted `parallel: 8` is not silently dropped.
	"parallel":   "max_parallel",
	"retries":    "max_retries",
	"think":      "think_passes",
	"context_kb": "max_context_kb",
	"qa_cmd":     "qa_gate_command",
	"qa_rounds":  "qa_gate_max_rounds",
	"perm":       "permission",
	"dry-run":    "dry_run",
}

// MigrationNotes describes what migrate would do to a document at version v.
// Used by `slmcode config show --origin` and doctor to explain an upgrade.
func MigrationNotes(v int) []string {
	var out []string
	for _, m := range migrations {
		if m.From >= v {
			out = append(out, m.Note)
		}
	}
	return out
}

// migrate upgrades a raw decoded config document in place and reports the
// version it was written by. A document with no `config_version` is version 0.
func migrate(raw map[string]any) int {
	if raw == nil {
		return CurrentConfigVersion
	}
	from := documentVersion(raw)

	// Renames first: a migration step may depend on the canonical spelling.
	for _, old := range sortedKeys(renames) {
		v, ok := raw[old]
		if !ok {
			continue
		}
		delete(raw, old)
		if _, taken := raw[renames[old]]; !taken {
			raw[renames[old]] = v
		}
	}

	for _, m := range migrations {
		if m.From < from {
			continue
		}
		m.Apply(raw)
	}
	raw["config_version"] = CurrentConfigVersion
	return from
}

// documentVersion reads config_version out of a raw document, tolerating the
// int / float64 / string spellings different YAML paths produce.
func documentVersion(raw map[string]any) int {
	switch v := raw["config_version"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n := 0
		for _, r := range strings.TrimSpace(v) {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int(r-'0')
		}
		return n
	default:
		return 0
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
