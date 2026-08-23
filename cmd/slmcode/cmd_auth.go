package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// `slmcode auth` — the CLI half of the API-key store.
//
// pkg/cli/probe.go tells a user whose endpoint answered 401 to run
// `slmcode auth set <key>`, but no such command existed: pkg/authstore was
// reachable only through Studio's PUT /api/auth. A CLI-first tool whose only
// way to set an API key is a web UI is a real gap, so the command the
// remediation already names is the one implemented here.
//
// The store itself is .slmcode/auth.json at 0600, written atomically, and
// `slmcode init` puts auth.json in .slmcode/.gitignore. Nothing below ever
// prints a key back: `get` and `list` report presence and a masked tail only.

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store provider API keys in .slmcode/auth.json (0600, git-ignored)",
		Long: `Manage the per-project API key store.

Keys live in .slmcode/auth.json with 0600 permissions, separate from
config.yaml, and are resolved automatically for the active provider (an
explicit SLMCODE_API_KEY / --api-key still wins).

Values are never echoed: run ` + "`auth set`" + ` with no key to be prompted
without echo, or pipe one in with --stdin. Passing the key as an argument works
but leaves it in your shell history.`,
		Example: `  slmcode auth set                     # prompt for the active provider's key
  slmcode auth set sk-…                # key for the active provider
  slmcode auth set openai sk-…         # key for a named provider
  echo "$KEY" | slmcode auth set --stdin
  slmcode auth list
  slmcode auth get openai
  slmcode auth rm openai`,
	}
	cmd.AddCommand(authSetCmd(), authGetCmd(), authRmCmd(), authListCmd())
	return cmd
}

// authTarget resolves the workspace and the provider an auth subcommand acts on.
func authTarget(explicit string) (slmDir, provider string, err error) {
	ws, err := openWorkspace()
	if err != nil {
		return "", "", err
	}
	if err := ws.EnsureInitialized(); err != nil {
		return "", "", err
	}
	provider = strings.TrimSpace(explicit)
	if provider == "" {
		provider = ws.Config.Provider
	}
	return ws.Config.SlmDir(), config.NormalizeProvider(provider), nil
}

// knownAuthProviders are the provider ids `auth set <provider> <key>` accepts
// as a first word. Anything else in that position is read as the KEY, which is
// what makes the 401 remediation's `slmcode auth set <key>` work verbatim.
// (Unknown names are still settable — pass them as the two-argument form.)
var knownAuthProviders = []string{
	"omlx", "ollama", "openai", "lmstudio", "openrouter", "groq", "together",
	"deepseek", "fireworks", "mistral", "gemini", "google", "anthropic",
	"vllm", "litellm", "custom", "mlx", "lm-studio",
}

// looksLikeProvider reports whether tok is plausibly a provider name rather
// than a key. Provider names are short, lowercase and known; keys are not.
func looksLikeProvider(tok string) bool {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if tok == "" || len(tok) > 24 || strings.ContainsAny(tok, " /:.") {
		return false
	}
	for _, p := range knownAuthProviders {
		if tok == p {
			return true
		}
	}
	return false
}

func authSetCmd() *cobra.Command {
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set [provider] [key]",
		Short: "Store an API key (prompted without echo when omitted)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, key := "", ""
			switch len(args) {
			case 1:
				// One word is a key unless it names a known provider — which is
				// what `slmcode auth set <key>` (the 401 remediation) needs.
				if looksLikeProvider(args[0]) {
					provider = args[0]
				} else {
					key = args[0]
				}
			case 2:
				provider, key = args[0], args[1]
			}

			slmDir, provider, err := authTarget(provider)
			if err != nil {
				return err
			}

			switch {
			case fromStdin:
				if key != "" {
					return failf(2, "--stdin and an explicit key are mutually exclusive")
				}
				if key, err = cli.ReadSecretLine(); err != nil {
					return fmt.Errorf("read key from stdin: %w", err)
				}
			case key == "":
				if !cli.IsInteractive() {
					return failf(2, "no key given — pass one as an argument or pipe it with --stdin")
				}
				if key, err = cli.ReadSecret(fmt.Sprintf("API key for %s: ", provider)); err != nil {
					return fmt.Errorf("read key: %w", err)
				}
			}
			if strings.TrimSpace(key) == "" {
				return failf(2, "empty key — use `slmcode auth rm %s` to remove one", provider)
			}
			if err := authstore.Set(slmDir, provider, key); err != nil {
				return err
			}
			// The key itself is never echoed back, only its shape.
			cli.KeyVal("provider", provider)
			cli.KeyVal("key", cli.MaskSecret(key))
			cli.KeyVal("stored", authstore.Path(slmDir))
			fmt.Println(cli.Dim("  auth.json is 0600 and git-ignored; SLMCODE_API_KEY / --api-key still take precedence"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read the key from stdin (for CI / pipes)")
	return cmd
}

func authGetCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "get [provider]",
		Short: "Report whether a key is stored (never prints the value)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			slmDir, provider, err := authTarget(first(args))
			if err != nil {
				return err
			}
			key, ok := authstore.Get(slmDir, provider)
			if asJSON {
				return emitJSON(map[string]any{
					"provider": provider,
					"stored":   ok,
					"masked":   cli.MaskSecret(key),
					"path":     authstore.Path(slmDir),
				})
			}
			cli.KeyVal("provider", provider)
			if !ok {
				cli.KeyVal("key", cli.Dim("(none)"))
				fmt.Println(cli.Dim("  set one with `slmcode auth set " + provider + "`"))
				return nil
			}
			cli.KeyVal("key", cli.MaskSecret(key))
			cli.KeyVal("path", authstore.Path(slmDir))
			if hint := authEnvHint(); hint != "" {
				fmt.Println(cli.Warn(hint))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func authRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm [provider]",
		Aliases: []string{"remove", "delete", "unset"},
		Short:   "Remove a stored API key",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slmDir, provider, err := authTarget(first(args))
			if err != nil {
				return err
			}
			if _, ok := authstore.Get(slmDir, provider); !ok {
				fmt.Println(cli.Warn("no key stored for " + provider))
				return nil
			}
			// authstore.Set with an empty value deletes the entry.
			if err := authstore.Set(slmDir, provider, ""); err != nil {
				return err
			}
			fmt.Println(cli.Success("removed the stored key for " + provider))
			return nil
		},
	}
	return cmd
}

func authListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List providers with a stored key (values redacted)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			slmDir, active, err := authTarget("")
			if err != nil {
				return err
			}
			present := authstore.PublicKeys(slmDir)
			names := make([]string, 0, len(present))
			for p := range present {
				names = append(names, p)
			}
			sort.Strings(names)
			if asJSON {
				return emitJSON(map[string]any{
					"path":      authstore.Path(slmDir),
					"active":    active,
					"providers": names,
				})
			}
			cli.KeyVal("store", authstore.Path(slmDir))
			if len(names) == 0 {
				fmt.Println(cli.Dim("  no keys stored — `slmcode auth set <key>`"))
				return nil
			}
			for _, p := range names {
				line := "  " + p
				if p == active {
					line += cli.Dim("  (active provider)")
				}
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// authEnvHint names the environment variable that outranks the store, so the
// "I set a key and it still 401s" case is diagnosable.
func authEnvHint() string {
	if v := strings.TrimSpace(os.Getenv("SLMCODE_API_KEY")); v != "" {
		return "SLMCODE_API_KEY is set and takes precedence over auth.json"
	}
	return ""
}
