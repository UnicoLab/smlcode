package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/hooks"
)

// `slmcode hooks` is the supported path for an operator who has legitimate
// repository hooks.
//
// pkg/hooks fails closed: `.slmcode/hooks.json` lives inside the project, so a
// clone can ship one, and the harness will not execute it until this operator
// has approved that exact file content. Before this command the only way to get
// past that was SLMCODE_TRUST_HOOKS=1, which trusts EVERY hooks file on the
// machine forever — an escape hatch wide enough that people would use it once
// and leave it in their shell profile. `hooks trust` approves one file, one
// content digest, and records it in the user's config directory (never in the
// repository, so a repo cannot ship its own approval).
//
// The listing always prints every command that would run BEFORE asking, because
// approval the operator cannot inspect is not approval.

func hooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Inspect and approve .slmcode/hooks.json (repo-supplied shell commands)",
		Long: `Repository hooks are code execution by design.

.slmcode/hooks.json makes the harness run shell commands around every tool call,
and it lives inside the project — a cloned repository can ship one. The harness
therefore refuses to load a hooks file until you have approved its exact
contents; any later edit changes the digest and needs approval again.

Approvals are stored per user (in your OS config directory), never in the repo.

Two more things must be true before a hook actually fires:
  * hooks_enabled must be true (it defaults to FALSE — set it with
    ` + "`slmcode config set hooks_enabled true`" + `), and
  * the file must be trusted (` + "`slmcode hooks trust`" + `).

` + hooks.TrustEnvVar + `=1 force-trusts every hooks file on the machine. It is for CI
images that generate their own hooks file; do not set it when you run code you
did not write.`,
		Example: `  slmcode hooks list      # show every command the file would run, and its trust state
  slmcode hooks trust     # approve this file's current contents
  slmcode hooks untrust   # withdraw approval`,
		RunE: func(cmd *cobra.Command, args []string) error { return runHooksList(false) },
	}

	var asJSON bool
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "show", "status"},
		Short:   "Show every command .slmcode/hooks.json would run, and whether it is trusted",
		RunE:    func(cmd *cobra.Command, args []string) error { return runHooksList(asJSON) },
	}
	list.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.AddCommand(list)

	var yes bool
	trust := &cobra.Command{
		Use:   "trust",
		Short: "Approve the current contents of .slmcode/hooks.json",
		Long: `Print every command the hooks file would run, then record your approval of
that exact content. Editing the file afterwards revokes the approval.`,
		RunE: func(cmd *cobra.Command, args []string) error { return runHooksTrust(yes) },
	}
	trust.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (still prints the commands)")
	cmd.AddCommand(trust)

	cmd.AddCommand(&cobra.Command{
		Use:   "untrust",
		Short: "Withdraw approval of .slmcode/hooks.json",
		RunE:  func(cmd *cobra.Command, args []string) error { return runHooksUntrust() },
	})
	return cmd
}

// hooksState is everything the three subcommands need to know.
type hooksState struct {
	Path        string `json:"path"`
	Root        string `json:"root"`
	Exists      bool   `json:"exists"`
	Enabled     bool   `json:"hooks_enabled"`
	Trusted     bool   `json:"trusted"`
	EnvOverride bool   `json:"env_override"`
	Commands    int    `json:"commands"`
	Describe    string `json:"-"`
	ParseError  string `json:"parse_error,omitempty"`
}

func readHooksState() (hooksState, error) {
	ws, err := openWorkspace()
	if err != nil {
		return hooksState{}, err
	}
	st := hooksState{
		Path:        hooks.DefaultPath(ws.Config.SlmDir()),
		Root:        ws.Config.Root,
		Enabled:     ws.Config.HooksEnabled,
		EnvOverride: strings.TrimSpace(os.Getenv(hooks.TrustEnvVar)) != "",
	}
	// LoadUnchecked on purpose: the operator must be shown what is in the file
	// even — especially — when it is untrusted. Nothing here executes.
	cfg, exists, perr := hooks.LoadUnchecked(st.Path)
	st.Exists = exists
	if perr != nil {
		st.ParseError = perr.Error()
		return st, nil
	}
	for _, list := range cfg.Hooks {
		for _, h := range list {
			if strings.TrimSpace(h.Command) != "" {
				st.Commands++
			}
		}
	}
	st.Describe = cfg.Describe()
	if exists {
		data, rerr := os.ReadFile(st.Path) //nolint:gosec // our own <slmDir>/hooks.json
		if rerr == nil {
			st.Trusted = hooks.IsTrusted(st.Path, data)
		}
	}
	return st, nil
}

func runHooksList(asJSON bool) error {
	jsonMode(asJSON)
	st, err := readHooksState()
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(st)
	}

	cli.Header("Hooks")
	cli.KeyVal("file", st.Path)
	cli.KeyVal("hooks_enabled", fmt.Sprintf("%v", st.Enabled))

	if !st.Exists {
		fmt.Println(cli.Dim("  (no hooks file — nothing runs)"))
		fmt.Println()
		fmt.Println(cli.Dim("  a hooks file is a JSON object of event → [{matcher, command}];"))
		fmt.Println(cli.Dim("  see .slmcode-hooks.example.json in the slmcode repository."))
		return nil
	}
	if st.ParseError != "" {
		fmt.Println(cli.Error("hooks.json does not parse: " + st.ParseError))
		fmt.Println(cli.Dim("  nothing will run until it is valid JSON"))
		return failf(1, "invalid hooks file")
	}

	fmt.Println()
	fmt.Println(cli.Bold("  commands this file would run:"))
	if st.Describe == "" {
		fmt.Println(cli.Dim("    (none — the file declares no commands)"))
	} else {
		fmt.Print(st.Describe)
	}
	fmt.Println()

	switch {
	case st.EnvOverride:
		fmt.Println(cli.Warn(hooks.TrustEnvVar + " is set — every hooks file on this machine is force-trusted"))
		fmt.Println(cli.Dim("  unset it to go back to per-file approval"))
	case st.Trusted:
		fmt.Println(cli.Success("trusted — these exact contents are approved for this path"))
	default:
		fmt.Println(cli.Warn("NOT trusted — these commands will not run"))
		fmt.Println(cli.Dim("  read them, then: slmcode hooks trust"))
	}
	if st.Commands > 0 && !st.Enabled {
		fmt.Println(cli.Warn("hooks_enabled is false — nothing runs even once trusted"))
		fmt.Println(cli.Dim("  enable with: slmcode config set hooks_enabled true"))
	}
	return nil
}

func runHooksTrust(yes bool) error {
	st, err := readHooksState()
	if err != nil {
		return err
	}
	if !st.Exists {
		return failf(3, "no hooks file at %s — nothing to trust", st.Path)
	}
	if st.ParseError != "" {
		return failf(1, "hooks file does not parse (%s) — fix it before trusting it", st.ParseError)
	}
	if st.Commands == 0 {
		return failf(1, "%s declares no commands — nothing to trust", st.Path)
	}

	cli.Header("Trust hooks")
	cli.KeyVal("file", st.Path)
	fmt.Println()
	fmt.Println(cli.Bold("  approving this file lets the harness run, on every matching tool call:"))
	fmt.Print(st.Describe)
	fmt.Println()
	fmt.Println(cli.Dim("  they run as you, with your environment, with cwd " + st.Root))
	fmt.Println()

	// The prompt is the point of the command. --yes still prints the commands
	// above, so an automated approval is at least auditable in the log.
	if !yes && !confirm("Approve these commands?", false) {
		return failf(6, "not approved — hooks stay disabled")
	}
	if err := hooks.Trust(st.Path); err != nil {
		return err
	}
	fmt.Println(cli.Success("trusted — recorded for this exact file content"))
	fmt.Println(cli.Dim("  any edit to the file revokes this and needs approval again"))
	if !st.Enabled {
		fmt.Println(cli.Warn("hooks_enabled is false — hooks still will not run"))
		fmt.Println(cli.Dim("  enable with: slmcode config set hooks_enabled true"))
	}
	if st.EnvOverride {
		fmt.Println(cli.Dim("  note: " + hooks.TrustEnvVar + " is set, so this approval was not what unblocked them"))
	}
	return nil
}

func runHooksUntrust() error {
	st, err := readHooksState()
	if err != nil {
		return err
	}
	if err := hooks.Untrust(st.Path); err != nil {
		return err
	}
	fmt.Println(cli.Success("approval withdrawn for " + st.Path))
	if st.EnvOverride {
		fmt.Println(cli.Warn(hooks.TrustEnvVar + " is still set — the file remains force-trusted regardless"))
	}
	return nil
}
