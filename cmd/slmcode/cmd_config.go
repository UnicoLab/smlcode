package main

import (
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
// Every key, its type, its allowed values, its default and its environment
// variable come from config.Schema(). The CLI used to carry a second
// hand-written table for the ~40 keys the schema did not describe; there is
// now one table, so `config set` validation, `config show --origin` and the
// Studio settings page cannot disagree.

// flagSources maps a schema key onto the persistent flag that overrides it,
// when that flag was actually given this run.
var flagSources = map[string]func() (string, bool){
	"model":         func() (string, bool) { return "--model", flagModel != "" },
	"provider":      func() (string, bool) { return "--provider", flagProvider != "" },
	"endpoint":      func() (string, bool) { return "--endpoint", flagEndpoint != "" },
	"backend":       func() (string, bool) { return "--backend", flagBackend != "" },
	"api_key":       func() (string, bool) { return "--api-key", flagAPIKey != "" },
	"dry_run":       func() (string, bool) { return "--dry-run", flagDryRun },
	"max_parallel":  func() (string, bool) { return "--parallel", flagMaxParallel > 0 },
	"max_retries":   func() (string, bool) { return "--retries", flagMaxRetries > 0 },
	"think_passes":  func() (string, bool) { return "--think-passes", flagThink > 0 },
	"verbose":       func() (string, bool) { return "--verbose", flagVerbose || flagVeryVerbose },
	"deterministic": func() (string, bool) { return "--no-explore", flagNoExplore },
	"evolve": func() (string, bool) {
		if flagNoEvolve {
			return "--no-evolve", true
		}
		return "--evolve", flagEvolve
	},
	"max_task_calls":      func() (string, bool) { return "--max-task-calls", flagMaxTaskCalls > 0 },
	"architect_editor":    func() (string, bool) { return "--architect-editor", flagArchitectEditor },
	"structured_decoding": func() (string, bool) { return "--structured-decoding", flagStructuredDecoding != "" },
	"listen":              func() (string, bool) { return "--listen", flagListen != "" },
}

// markFlagOrigins records every persistent flag that actually set a value, so
// `config show --origin` can report "flag --model" instead of guessing.
func markFlagOrigins(c *config.Config) {
	for key, fn := range flagSources {
		if name, set := fn(); set {
			c.MarkFlag(key, name)
		}
	}
}

// originTag renders one origin for the human-readable table.
func originTag(origin string) string {
	switch {
	case strings.HasPrefix(origin, "flag"):
		return cli.Yellow(origin)
	case strings.HasPrefix(origin, "env"):
		return cli.Cyan(origin)
	case origin == string(config.LayerUser):
		return cli.Blue(origin)
	case origin == string(config.LayerProject):
		return cli.Green(origin)
	default:
		return cli.Dim(origin)
	}
}

// configFilePath returns the project config.yaml location.
func configFilePath(slmDir string) string { return filepath.Join(slmDir, "config.yaml") }

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, get, set and unset harness config",
		Example: `  slmcode config show
  slmcode config show --all
  slmcode config show --origin
  slmcode config show --group hitl
  slmcode config get max_parallel
  slmcode config set max_parallel 6
  slmcode config set --user provider ollama
  slmcode config unset fast_model
  slmcode config schema --json
  slmcode config path`,
	}

	cmd.AddCommand(configShowCmd(), configGetCmd(), configSetCmd(),
		configUnsetCmd(), configSchemaCmd(), configPathCmd())
	return cmd
}

func configShowCmd() *cobra.Command {
	var showJSON, showOrigin, showAll bool
	var group string
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the effective config",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(showJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			cfg := ws.Config
			prov := cfg.Provenance()
			values := cfg.Values()

			visible := make([]config.FieldSchema, 0, len(config.Schema()))
			for _, f := range config.Schema() {
				if group != "" && !strings.EqualFold(f.Group, group) {
					continue
				}
				if f.Advanced && !showAll && group == "" {
					continue
				}
				visible = append(visible, f)
			}

			if showJSON {
				payload := map[string]any{
					"config": cfg.Public(),
					"path":   configFilePath(cfg.SlmDir()),
				}
				if prov.UserPath != "" {
					payload["user_path"] = prov.UserPath
				}
				if showOrigin {
					origins := map[string]string{}
					for k := range values {
						origins[k] = prov.Describe(k)
					}
					payload["origin"] = origins
				}
				if len(prov.Warnings) > 0 {
					payload["warnings"] = prov.Warnings
				}
				return emitJSON(payload)
			}

			cli.Header("Config")
			lastGroup := ""
			for _, f := range visible {
				if f.Group != lastGroup {
					lastGroup = f.Group
					fmt.Println()
					fmt.Println("  " + cli.Bold(strings.ToUpper(f.Group)))
				}
				v := values[f.Key]
				if f.Secret {
					v = redactKey(fmt.Sprint(v))
				}
				val := formatConfigValue(v)
				if showOrigin {
					fmt.Printf("  %s  %s  %s\n", cli.Dim(cli.PadWidth(f.Key, 30)),
						cli.PadWidth(val, 30), originTag(prov.Describe(f.Key)))
					continue
				}
				fmt.Printf("  %s  %s\n", cli.Dim(cli.PadWidth(f.Key, 30)), val)
			}
			fmt.Println()
			fmt.Println(cli.Dim("  file: " + configFilePath(cfg.SlmDir())))
			if prov.UserPath != "" {
				fmt.Println(cli.Dim("  user: " + prov.UserPath))
			}
			if prov.Migrated {
				fmt.Println(cli.Dim(fmt.Sprintf("  migrated from config_version %d → %d",
					prov.FromVersion, config.CurrentConfigVersion)))
			}
			for _, w := range prov.Warnings {
				fmt.Println(cli.Warn(w))
			}
			if !showAll && group == "" {
				fmt.Println(cli.Dim("  slmcode config show --all      include advanced keys"))
			}
			if !showOrigin {
				fmt.Println(cli.Dim("  slmcode config show --origin   where each value came from"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&showJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&showOrigin, "origin", false, "annotate each value with default|user|project|env SLMCODE_X|flag --x")
	c.Flags().BoolVar(&showAll, "all", false, "include advanced keys")
	c.Flags().StringVar(&group, "group", "", "only this group ("+strings.Join(config.Groups, ", ")+")")
	return c
}

func configGetCmd() *cobra.Command {
	var getJSON bool
	c := &cobra.Command{
		Use:   "get [key]",
		Short: "Print one effective value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(getJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := config.CanonicalKey(args[0])
			v, ok := ws.Config.Get(key)
			if !ok {
				return failf(2, "unknown key %q — `slmcode config show --all` lists every key", args[0])
			}
			if f, ok := config.Field(key); ok && f.Secret {
				v = redactKey(fmt.Sprint(v))
			}
			if getJSON {
				return emitJSON(map[string]any{
					"key":    key,
					"value":  v,
					"origin": ws.Config.Provenance().Describe(key),
				})
			}
			fmt.Println(formatConfigValue(v))
			return nil
		},
	}
	c.Flags().BoolVar(&getJSON, "json", false, "machine-readable output")
	return c
}

func configSetCmd() *cobra.Command {
	var toUser bool
	c := &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Set a config value (validated against the schema)",
		Args:  cobra.ExactArgs(2),
		Example: `  slmcode config set max_parallel 6
  slmcode config set permission review
  slmcode config set escalate_ask_timeout 10m
  slmcode config set --user provider ollama`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := config.CanonicalKey(args[0])
			value := args[1]

			field, ok := config.PatchableField(key)
			if !ok {
				if _, exists := config.Field(key); exists {
					return failf(2, "%s is read-only — edit it in %s",
						key, configFilePath(ws.Config.SlmDir()))
				}
				return failf(2, "unknown config key %q — `slmcode config show --all` lists every key%s",
					args[0], didYouMean(key))
			}
			if toUser {
				return setUserConfigValue(field, value)
			}

			cfg := ws.Config
			// Switching provider re-defaults the endpoint only when the user has
			// not pinned one via flag/env/explicit config value.
			if key == "provider" {
				next := config.NormalizeProvider(value)
				endpointPinned := flagEndpoint != "" ||
					strings.TrimSpace(os.Getenv("SLMCODE_ENDPOINT")) != "" ||
					(cfg.Endpoint != "" && cfg.Endpoint != config.DefaultEndpointFor(cfg.Provider))
				if next != config.NormalizeProvider(cfg.Provider) && !endpointPinned {
					cfg.Endpoint = config.DefaultEndpointFor(next)
					fmt.Println(cli.Dim("  endpoint → " + cfg.Endpoint + " (provider default)"))
				}
				// A manual provider choice unpins the stack highlight.
				cfg.ActiveStack = ""
			}
			if err := cfg.Set(key, value); err != nil {
				return failf(2, "%s", err.Error())
			}
			cfg.Normalize()
			if err := cfg.Save(); err != nil {
				return err
			}
			stored, _ := cfg.Get(key)
			fmt.Println(cli.Success(fmt.Sprintf("%s = %s", key, formatConfigValue(stored))))
			fmt.Println(cli.Dim("  " + configFilePath(cfg.SlmDir())))
			return nil
		},
	}
	c.Flags().BoolVar(&toUser, "user", false, "write to the user-level config instead of this project")
	return c
}

// setUserConfigValue writes one key into the user-level config file, creating
// it if needed. It rewrites only that key, leaving the rest of the file alone.
func setUserConfigValue(field config.FieldSchema, value string) error {
	path := config.UserConfigPath()
	if path == "" {
		path = config.DefaultUserConfigPath()
	}
	if path == "" {
		return failf(1, "cannot resolve a user config location — set SLMCODE_USER_CONFIG")
	}
	// Validate against a throwaway config before touching the file.
	probe := config.Default("")
	if err := probe.Set(field.Key, value); err != nil {
		return failf(2, "%s", err.Error())
	}
	stored, _ := probe.Get(field.Key)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := config.WriteUserValue(path, field.Key, stored); err != nil {
		return err
	}
	fmt.Println(cli.Success(fmt.Sprintf("%s = %s (user)", field.Key, formatConfigValue(stored))))
	fmt.Println(cli.Dim("  " + path))
	return nil
}

func configUnsetCmd() *cobra.Command {
	// --json here is not decoration: the CLI contract says every `config`
	// subcommand takes it, and `unset` was the one that did not, so a script
	// that walked the whole surface with --json failed on exactly one command.
	var asJSON bool
	c := &cobra.Command{
		Use:     "unset [key]",
		Short:   "Reset a config value to what it would inherit (user config, else default)",
		Example: "  slmcode config unset max_parallel\n  slmcode config unset model --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			key := config.CanonicalKey(args[0])
			if _, ok := config.PatchableField(key); !ok {
				return failf(2, "unknown or read-only key %q%s", args[0], didYouMean(key))
			}
			if err := ws.Config.Unset(key); err != nil {
				return failf(2, "%s", err.Error())
			}
			if err := ws.Config.Save(); err != nil {
				return err
			}
			v, _ := ws.Config.Get(key)
			if asJSON {
				return emitJSON(map[string]any{
					"key":    key,
					"value":  v,
					"origin": ws.Config.Provenance().Describe(key),
					"unset":  true,
				})
			}
			fmt.Println(cli.Success(fmt.Sprintf("%s reset to %s (%s)",
				key, formatConfigValue(v), ws.Config.Provenance().Describe(key))))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

func configSchemaCmd() *cobra.Command {
	var asJSON bool
	var group string
	c := &cobra.Command{
		Use:     "schema",
		Short:   "List every config key with its type, default and allowed values",
		Example: "  slmcode config schema\n  slmcode config schema --group hitl\n  slmcode config schema --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			fields := config.Schema()
			if group != "" {
				var kept []config.FieldSchema
				for _, f := range fields {
					if strings.EqualFold(f.Group, group) {
						kept = append(kept, f)
					}
				}
				fields = kept
			}
			if asJSON {
				return emitJSON(map[string]any{"fields": fields, "groups": config.Groups})
			}
			cli.Header("Config schema")
			lastGroup := ""
			for _, f := range fields {
				if f.Group != lastGroup {
					lastGroup = f.Group
					fmt.Println()
					fmt.Println("  " + cli.Bold(strings.ToUpper(f.Group)))
				}
				kind := f.Type
				if len(f.Enum) > 0 {
					kind = strings.Join(f.Enum, "|")
				}
				fmt.Printf("  %s %s  %s\n",
					cli.Accent(cli.PadWidth(f.Key, 30)),
					cli.Dim(cli.PadWidth(kind, 22)),
					cli.Dim("default: "+formatPlainValue(f.Default)))
				if f.Description != "" {
					fmt.Printf("  %s%s\n", strings.Repeat(" ", 30), cli.Dim(f.Description))
				}
			}
			fmt.Println()
			fmt.Println(cli.Dim("  every key is also settable as its environment variable, e.g. SLMCODE_MAX_PARALLEL"))
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().StringVar(&group, "group", "", "only this group")
	return c
}

func configPathCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "path",
		Short: "Print the config file paths (project and user)",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			project := configFilePath(ws.Config.SlmDir())
			user := config.UserConfigPath()
			if asJSON {
				return emitJSON(map[string]any{
					"project":         project,
					"user":            user,
					"user_candidates": config.UserConfigPaths(),
				})
			}
			fmt.Println(project)
			if user != "" {
				fmt.Println(cli.Dim("user: " + user))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}

// didYouMean suggests the closest key when a typo is likely.
func didYouMean(key string) string {
	var best string
	bestScore := 0
	for _, f := range config.Schema() {
		score := commonPrefixLen(f.Key, key)
		if score > bestScore && score >= 4 {
			best, bestScore = f.Key, score
		}
	}
	if best == "" {
		return ""
	}
	return " — did you mean " + best + "?"
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
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
	case int:
		return strconv.Itoa(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []string:
		if len(t) == 0 {
			return cli.Dim("(empty)")
		}
		return strings.Join(t, ", ")
	case []any:
		if len(t) == 0 {
			return cli.Dim("(empty)")
		}
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, ", ")
	case map[string]int:
		if len(t) == 0 {
			return cli.Dim("(empty)")
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, t[k]))
		}
		return strings.Join(parts, " ")
	default:
		return formatPlainValue(v)
	}
}

// formatPlainValue is formatConfigValue without color, for schema listings.
func formatPlainValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		if t == "" {
			return `""`
		}
		return t
	case []string:
		if len(t) == 0 {
			return "[]"
		}
		return strings.Join(t, ",")
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	}
	rv := fmt.Sprint(v)
	if rv == "map[]" || rv == "[]" {
		return "-"
	}
	return cli.Clip(rv, 40)
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
