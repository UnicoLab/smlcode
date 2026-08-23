package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// `slmcode config` — schema-driven, with provenance.
//
// The old `config set` had a hand-written switch above the schema path that
// accepted anything: `config set parallel abc` printed "✔ set parallel = abc"
// while Sscanf silently left the value unchanged. Everything now goes through
// config.Schema(), which validates types and enums, so a bad value is an error.

// configAliases maps the historical short keys onto real schema keys so the
// documented spellings keep working.
var configAliases = map[string]string{
	"parallel":   "max_parallel",
	"retries":    "max_retries",
	"think":      "think_passes",
	"context_kb": "max_context_kb",
	"qa_cmd":     "qa_gate_command",
	"qa_rounds":  "qa_gate_max_rounds",
	"perm":       "permission",
	"dry-run":    "dry_run",
	"agent":      "specialist",
	"skills":     "pinned_skills",
	"dynamic":    "dynamic_pipeline",
	"composer":   "dynamic_pipeline",
}

func canonicalConfigKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if real, ok := configAliases[k]; ok {
		return real
	}
	return k
}

// configOrigin describes where an effective value came from.
type configOrigin string

const (
	originDefault configOrigin = "default"
	originUser    configOrigin = "user"
	originFile    configOrigin = "project"
	originEnv     configOrigin = "env"
	originFlag    configOrigin = "flag"
)

// envKeyFor maps a schema key onto its SLMCODE_* environment variable.
func envKeyFor(key string) string { return "SLMCODE_" + strings.ToUpper(key) }

// flagSetKeys lists schema keys a persistent flag can override this run.
var flagSetKeys = map[string]func() bool{
	"model":        func() bool { return flagModel != "" },
	"provider":     func() bool { return flagProvider != "" },
	"endpoint":     func() bool { return flagEndpoint != "" },
	"backend":      func() bool { return flagBackend != "" },
	"api_key":      func() bool { return flagAPIKey != "" },
	"dry_run":      func() bool { return flagDryRun },
	"max_parallel": func() bool { return flagMaxParallel > 0 },
	"max_retries":  func() bool { return flagMaxRetries > 0 },
	"think_passes": func() bool { return flagThink > 0 },
	"verbose":      func() bool { return flagVerbose || flagVeryVerbose },
}

// originOf resolves where the effective value for key came from.
//
// Note: config.Save() rewrites every field, so "present in config.yaml" is not
// evidence that a human chose it. A file value therefore only counts as
// "project" when it actually differs from the built-in default.
func originOf(key string, fileValues, defaults map[string]any, effective any) configOrigin {
	if fn, ok := flagSetKeys[key]; ok && fn() {
		return originFlag
	}
	if os.Getenv(envKeyFor(key)) != "" {
		return originEnv
	}
	if userPath != "" {
		if v, ok := userValues[key]; ok && fmt.Sprint(v) == fmt.Sprint(effective) {
			if d, has := defaults[key]; !has || fmt.Sprint(d) != fmt.Sprint(effective) {
				return originUser
			}
		}
	}
	if _, inFile := fileValues[key]; inFile {
		if d, has := defaults[key]; !has || fmt.Sprint(d) != fmt.Sprint(effective) {
			return originFile
		}
	}
	return originDefault
}

// userPath / userValues are populated by loadOriginSources before rendering.
var (
	userPath   string
	userValues map[string]any
)

// loadOriginSources caches the user-layer file so origin reporting can name it.
func loadOriginSources() {
	userPath = UserConfigPath()
	userValues = map[string]any{}
	if userPath == "" {
		return
	}
	if m, err := readYAMLMap(userPath); err == nil {
		userValues = m
	}
}

// defaultConfigMap renders the built-in defaults for comparison.
func defaultConfigMap(root string) map[string]any {
	return effectiveConfigMap(config.Default(root))
}

// configFilePath returns the project config.yaml location.
func configFilePath(slmDir string) string { return filepath.Join(slmDir, "config.yaml") }

// readConfigFileValues loads the raw project config.yaml as a key→value map so
// `config show --origin` can tell "written down" from "default".
func readConfigFileValues(slmDir string) map[string]any {
	out := map[string]any{}
	data, err := os.ReadFile(configFilePath(slmDir))
	if err != nil {
		return out
	}
	// The config is YAML but every scalar we care about is a simple `key: value`
	// line; parsing that directly avoids depending on the config package's
	// internal marshalling.
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || line != trimmed {
			continue // skip comments and any nested/indented block
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(v, `"'`)
	}
	return out
}

// effectiveConfigMap renders the config as a flat key→value map via its JSON
// tags, which mirror the schema keys.
func effectiveConfigMap(c *config.Config) map[string]any {
	out := map[string]any{}
	data, err := json.Marshal(c.Public())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, get, set and unset harness config",
		Example: `  slmcode config show
  slmcode config show --origin
  slmcode config show --json
  slmcode config get max_parallel
  slmcode config set max_parallel 6
  slmcode config unset fast_model
  slmcode config path`,
	}

	// ── show ──
	var showJSON, showOrigin bool
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective config",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(showJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			eff := effectiveConfigMap(ws.Config)
			fileVals := readConfigFileValues(ws.Config.SlmDir())
			defaults := defaultConfigMap(ws.Config.Root)
			loadOriginSources()

			if showJSON {
				payload := map[string]any{"config": ws.Config.Public()}
				if showOrigin {
					origins := map[string]string{}
					for k, v := range eff {
						origins[k] = string(originOf(k, fileVals, defaults, v))
					}
					payload["origin"] = origins
					payload["user_path"] = userPath
				}
				payload["path"] = configFilePath(ws.Config.SlmDir())
				return emitJSON(payload)
			}

			cli.Header("Config")
			keys := make([]string, 0, len(eff))
			for k := range eff {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := eff[k]
				if k == "api_key" {
					v = redactKey(fmt.Sprint(v))
				}
				val := formatConfigValue(v)
				if showOrigin {
					org := originOf(k, fileVals, defaults, eff[k])
					fmt.Printf("  %s  %s  %s\n", cli.Dim(cli.PadWidth(k, 26)),
						cli.PadWidth(val, 34), originTag(org))
					continue
				}
				fmt.Printf("  %s  %s\n", cli.Dim(cli.PadWidth(k, 26)), val)
			}
			fmt.Println()
			fmt.Println(cli.Dim("  file: " + configFilePath(ws.Config.SlmDir())))
			if userPath != "" {
				fmt.Println(cli.Dim("  user: " + userPath))
			}
			if !showOrigin {
				fmt.Println(cli.Dim("  slmcode config show --origin   where each value came from"))
			}
			return nil
		},
	}
	showCmd.Flags().BoolVar(&showJSON, "json", false, "machine-readable output")
	showCmd.Flags().BoolVar(&showOrigin, "origin", false, "annotate each value with default|project|env|flag")
	cmd.AddCommand(showCmd)

	// ── get ──
	var getJSON bool
	getCmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Print one effective value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(getJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := canonicalConfigKey(args[0])
			eff := effectiveConfigMap(ws.Config)
			v, ok := eff[key]
			if !ok {
				return failf(2, "unknown key %q — see `slmcode config show`", args[0])
			}
			if key == "api_key" {
				v = redactKey(fmt.Sprint(v))
			}
			if getJSON {
				loadOriginSources()
				return emitJSON(map[string]any{
					"key":   key,
					"value": v,
					"origin": string(originOf(key, readConfigFileValues(ws.Config.SlmDir()),
						defaultConfigMap(ws.Config.Root), v)),
				})
			}
			fmt.Println(formatConfigValue(v))
			return nil
		},
	}
	getCmd.Flags().BoolVar(&getJSON, "json", false, "machine-readable output")
	cmd.AddCommand(getCmd)

	// ── set ──
	cmd.AddCommand(&cobra.Command{
		Use:     "set [key] [value]",
		Short:   "Set a config value (validated against the schema)",
		Args:    cobra.ExactArgs(2),
		Example: "  slmcode config set max_parallel 6\n  slmcode config set permission review",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := canonicalConfigKey(args[0])
			value := args[1]

			patch, ok, err := configPatchFromSchemaValue(key, value)
			if err != nil {
				return failf(2, "%s", err.Error())
			}
			if !ok {
				return failf(2, "unknown or non-patchable key %q — `slmcode config show` lists every key", args[0])
			}
			c := ws.Config
			// Switching provider re-defaults the endpoint only when the user has
			// not pinned one via flag/env/explicit config value.
			if key == "provider" {
				next := config.NormalizeProvider(value)
				endpointPinned := flagEndpoint != "" ||
					strings.TrimSpace(os.Getenv("SLMCODE_ENDPOINT")) != "" ||
					(c.Endpoint != "" && c.Endpoint != config.DefaultEndpointFor(c.Provider))
				if next != config.NormalizeProvider(c.Provider) && !endpointPinned {
					c.Endpoint = config.DefaultEndpointFor(next)
					fmt.Println(cli.Dim("  endpoint → " + c.Endpoint + " (provider default)"))
				}
			}
			c.ApplyPatch(patch)
			if key == "permission" {
				c.DryRun = strings.EqualFold(value, "dry-run")
			}
			if key == "dry_run" {
				if b, perr := parseConfigBool(value); perr == nil && b {
					c.Permission = "dry-run"
				}
			}
			if err := c.Save(); err != nil {
				return err
			}
			// Read back so the printed value is what was actually stored.
			eff := effectiveConfigMap(c)
			stored := formatConfigValue(eff[key])
			fmt.Println(cli.Success(fmt.Sprintf("%s = %s", key, stored)))
			return nil
		},
	})

	// ── unset ──
	cmd.AddCommand(&cobra.Command{
		Use:   "unset [key]",
		Short: "Reset a config value to its default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := canonicalConfigKey(args[0])
			field, ok := schemaField(key)
			if !ok {
				return failf(2, "unknown or non-patchable key %q", args[0])
			}
			zero := zeroForType(field.Type)
			patch, _, err := configPatchFromSchemaValue(key, zero)
			if err != nil {
				return failf(2, "%s", err.Error())
			}
			ws.Config.ApplyPatch(patch)
			if err := ws.Config.Save(); err != nil {
				return err
			}
			eff := effectiveConfigMap(ws.Config)
			fmt.Println(cli.Success(fmt.Sprintf("%s reset to %s", key, formatConfigValue(eff[key]))))
			return nil
		},
	})

	// ── path ──
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			fmt.Println(configFilePath(ws.Config.SlmDir()))
			return nil
		},
	})

	return cmd
}

func schemaField(key string) (config.FieldSchema, bool) {
	for _, f := range mergedSchema() {
		if f.Key == key && f.Patchable {
			return f, true
		}
	}
	return config.FieldSchema{}, false
}

// zeroForType returns the "unset" literal for a schema type. For enums the
// first allowed value is used, since an empty string would fail validation.
func zeroForType(t string) string {
	switch t {
	case "bool":
		return "false"
	case "int":
		return "0"
	case "float":
		return "0"
	case "string[]":
		return "-"
	default:
		return ""
	}
}

func formatConfigValue(v any) string {
	switch t := v.(type) {
	case nil:
		return cli.Dim("(unset)")
	case string:
		if t == "" {
			return cli.Dim("(unset)")
		}
		return t
	case bool:
		if t {
			return cli.Green("true")
		}
		return cli.Dim("false")
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []any:
		if len(t) == 0 {
			return cli.Dim("(empty)")
		}
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(v)
	}
}

func originTag(o configOrigin) string {
	switch o {
	case originFlag:
		return cli.Yellow("flag")
	case originEnv:
		return cli.Cyan("env")
	case originUser:
		return cli.Blue("user")
	case originFile:
		return cli.Green("project")
	default:
		return cli.Dim("default")
	}
}

func redactKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + strings.Repeat("*", 6) + k[len(k)-4:]
}
