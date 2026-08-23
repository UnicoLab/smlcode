package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// User-level config layer.
//
// pkg/blocks already resolves project → user → extra → builtin, but config did
// not: every new repo started from hard-coded defaults, so a preferred provider,
// model or parallelism had to be re-set per project. This adds the missing user
// layer in the CLI's load path without touching pkg/config.
//
// Precedence (lowest first): defaults → user file → project file → env → flags.

// userConfigPaths lists candidate user-level config files, most specific first.
func userConfigPaths() []string {
	var out []string
	if x := strings.TrimSpace(os.Getenv("SLMCODE_USER_CONFIG")); x != "" {
		out = append(out, x)
	}
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		out = append(out, filepath.Join(x, "slmcode", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".slmcode", "config.yaml"),
			filepath.Join(home, ".config", "slmcode", "config.yaml"),
		)
	}
	return out
}

// UserConfigPath returns the first existing user config file, or "".
func UserConfigPath() string {
	for _, p := range userConfigPaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// readYAMLMap parses a YAML file into a flat key→value map. Nested blocks are
// preserved as-is so patch fields with structured values still work.
func readYAMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// patchFromMap converts a key→value map into a config.Patch, dropping keys the
// patch does not understand. Unknown keys are returned so callers can warn.
func patchFromMap(values map[string]any) (config.Patch, []string) {
	known := map[string]bool{}
	for _, f := range mergedSchema() {
		if f.Patchable {
			known[f.Key] = true
		}
	}
	filtered := map[string]any{}
	var unknown []string
	for k, v := range values {
		if known[k] {
			filtered[k] = v
			continue
		}
		unknown = append(unknown, k)
	}
	var patch config.Patch
	if len(filtered) == 0 {
		return patch, unknown
	}
	// Round-trip through JSON: config.Patch is tagged for JSON only.
	b, err := json.Marshal(filtered)
	if err != nil {
		return patch, unknown
	}
	// A single bad value must not discard the whole layer, so decode key by key.
	if err := json.Unmarshal(b, &patch); err != nil {
		patch = config.Patch{}
		for k, v := range filtered {
			one, e := json.Marshal(map[string]any{k: v})
			if e != nil {
				unknown = append(unknown, k)
				continue
			}
			if e := json.Unmarshal(one, &patch); e != nil {
				unknown = append(unknown, k)
			}
		}
	}
	return patch, unknown
}

// applyUserConfigLayer applies ~/.slmcode/config.yaml for keys the project
// config does not already define. It returns the path that was applied ("" when
// there is none) and any keys it could not map.
func applyUserConfigLayer(c *config.Config, slmDir string) (string, []string) {
	path := UserConfigPath()
	if path == "" || c == nil {
		return "", nil
	}
	values, err := readYAMLMap(path)
	if err != nil {
		return "", nil
	}
	// Never let a user file shadow a real project decision. config.Save()
	// rewrites every field, so "present in config.yaml" is not evidence of
	// intent — only a value that differs from the built-in default is.
	projectKeys := readConfigFileValues(slmDir)
	defaults := effectiveConfigMap(config.Default(c.Root))
	for k := range values {
		v, defined := projectKeys[k]
		if !defined {
			continue
		}
		d, hasDefault := defaults[k]
		if !hasDefault || fmt.Sprint(d) != fmt.Sprint(v) {
			delete(values, k) // the project explicitly set this
		}
	}
	if len(values) == 0 {
		return path, nil
	}
	patch, unknown := patchFromMap(values)
	c.ApplyPatch(patch)
	return path, unknown
}
