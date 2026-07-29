package main

import (
	"os"

	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate completion scripts for your shell.

Zsh (Homebrew):
  slmcode completion zsh > "$(brew --prefix)/share/zsh/site-functions/_slmcode"

Zsh (user):
  mkdir -p ~/.zsh/completions
  slmcode completion zsh > ~/.zsh/completions/_slmcode

Bash:
  slmcode completion bash > /usr/local/etc/bash_completion.d/slmcode

Fish:
  slmcode completion fish > ~/.config/fish/completions/slmcode.fish
`,
		DisableFlagsInUseLine: true,
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return nil
		},
	}
	return cmd
}
