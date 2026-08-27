package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

type compositionCLIResponse struct {
	Mode           string               `json:"mode"`
	DynamicEnabled bool                 `json:"dynamic_enabled"`
	ModelProfile   config.ModelProfile  `json:"model_profile"`
	Composition    composer.Composition `json:"composition"`
}

func composeCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "compose [query...]",
		Short: "Preview the task-specific dynamic pipeline and selected agents",
		Long: cli.Dim(`Preview the deterministic dynamic pipeline for a query without calling an LLM.
With no query, prints the latest saved runtime composition from .slmcode/.`),
		Example: "  slmcode compose \"add JWT auth\"\n  slmcode compose            # the composition the last run used\n  slmcode compose \"fix the flaky test\" --json",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			mode := "preview"
			query := strings.TrimSpace(strings.Join(args, " "))
			var comp composer.Composition
			if query == "" {
				mode = "latest"
				latest, err := readLatestComposition(ws.Config.SlmDir())
				if err != nil {
					return err
				}
				comp = latest
			} else {
				comp = orchestrator.PreviewCompositionForConfig(ws.Config, query)
			}
			prof := config.ResolveModelProfile(ws.Config.ModelProfiles, ws.Config.Model)
			resp := compositionCLIResponse{
				Mode:           mode,
				DynamicEnabled: ws.Config.DynamicPipeline,
				ModelProfile:   prof,
				Composition:    comp,
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}
			fmt.Print(formatCompositionCLI(resp))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}

func readLatestComposition(slmDir string) (composer.Composition, error) {
	comp, ok, err := composer.LoadDynamic(slmDir)
	if err != nil {
		return comp, err
	}
	if !ok {
		return comp, fmt.Errorf("no saved composition at %s — run `slmcode compose \"your task\"` to preview one", filepath.Join(slmDir, composer.DynamicFileName))
	}
	return comp, nil
}

// plural renders "1 wave" / "3 waves". Words ending in "s" take "es".
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	if strings.HasSuffix(word, "s") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// strictReviewNote appends the strict-reviewer marker to a budget line.
//
// It is called out separately because it is the one budget knob that costs an
// extra LLM call rather than an extra subprocess, and an operator reading this
// preview to explain a slow run should see it named.
func strictReviewNote(on bool) string {
	if on {
		return " · strict reviewer"
	}
	return ""
}

func formatCompositionCLI(resp compositionCLIResponse) string {
	c := resp.Composition
	var b strings.Builder
	title := "Composition Preview"
	if resp.Mode == "latest" {
		title = "Latest Composition"
	}
	b.WriteString("\n")
	b.WriteString(cli.Bold(cli.Accent("▸ " + title)))
	b.WriteString("\n")
	b.WriteString(cli.Dim(strings.Repeat("─", minInt(60, len(title)+4))))
	b.WriteString("\n")
	writeCLIKeyVal(&b, "mode", resp.Mode)
	writeCLIKeyVal(&b, "dynamic", fmt.Sprintf("%v", resp.DynamicEnabled))
	writeCLIKeyVal(&b, "model_profile", fmt.Sprintf("ctx=%d max_tokens=%d think=%d skills=%d",
		resp.ModelProfile.ContextLimit, resp.ModelProfile.MaxTokens,
		resp.ModelProfile.ThinkingBudgetTokens, resp.ModelProfile.SkillTokenBudget))
	// The budget class, and what it bought. Printed above the summary because
	// it is the decision every other line in this preview follows from: how
	// many phases run, how many waves, how deep the gates go.
	prof := c.Profile()
	writeCLIKeyVal(&b, "budget_class", fmt.Sprintf("%s:%s — %s", prof.Complexity, prof.Kind, prof.Why))
	writeCLIKeyVal(&b, "budget", fmt.Sprintf("%s · %s · %s · %s%s",
		plural(len(prof.Phases), "phase"), plural(prof.MaxWaves, "wave"),
		plural(prof.ThinkPasses, "think pass"), plural(prof.QAGateRounds, "QA round"),
		strictReviewNote(prof.StrictReview)))
	if strings.TrimSpace(c.Summary) != "" {
		writeCLIKeyVal(&b, "summary", c.Summary)
	}
	if strings.TrimSpace(c.Strategy) != "" {
		writeCLIKeyVal(&b, "strategy", c.Strategy)
	}
	if !resp.DynamicEnabled {
		b.WriteString("  ")
		b.WriteString(cli.Warn("dynamic_pipeline is disabled; this is an inspection preview"))
		b.WriteString("\n")
	}
	if hints := compositionSLMFit(resp); len(hints) > 0 {
		b.WriteString("\n")
		b.WriteString(cli.Bold("SLM Fit"))
		b.WriteString("\n")
		for _, hint := range hints {
			b.WriteString("  - ")
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}

	if len(c.Handoff) > 0 {
		b.WriteString("\n")
		b.WriteString(cli.Bold("Handoff"))
		b.WriteString("\n")
		for _, item := range c.Handoff {
			b.WriteString("  - ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}

	if len(c.Phases) > 0 {
		b.WriteString("\n")
		b.WriteString(cli.Bold("Pipeline"))
		b.WriteString("\n")
		for _, p := range c.Phases {
			state := "enabled"
			if !p.Enabled || p.When == "never" {
				state = "disabled"
			}
			when := p.When
			if when == "" {
				when = "default"
			}
			agent := p.Agent
			if agent == "" {
				agent = "-"
			}
			fmt.Fprintf(&b, "  %-10s -> %-14s %-8s %s\n", p.ID, agent, when, cli.Dim(state))
		}
	}

	b.WriteString("\n")
	b.WriteString(cli.Bold("Execute Loop"))
	b.WriteString("\n")
	writeCLIKeyVal(&b, "worker", c.Execute.DefaultRole)
	writeCLIKeyVal(&b, "reviewer", c.Execute.Reviewer)
	writeCLIKeyVal(&b, "corrector", c.Execute.Corrector)
	writeCLIKeyVal(&b, "max_waves", fmt.Sprintf("%d", c.Execute.MaxWaves))

	if len(c.Team) > 0 {
		b.WriteString("\n")
		b.WriteString(cli.Bold("Team"))
		b.WriteString("\n")
		for _, member := range c.Team {
			skills := "-"
			if len(member.Skills) > 0 {
				skills = strings.Join(member.Skills, ", ")
			}
			fmt.Fprintf(&b, "  %-16s %s\n", member.Role, cli.Dim(skills))
		}
	}

	if len(c.Slots) > 0 {
		b.WriteString("\n")
		b.WriteString(cli.Bold("Slots"))
		b.WriteString("\n")
		for _, slot := range c.Slots {
			anchor := slot.After
			pos := "after"
			if slot.Before != "" {
				anchor = slot.Before
				pos = "before"
			} else if slot.Replace != "" {
				anchor = slot.Replace
				pos = "replace"
			}
			fmt.Fprintf(&b, "  %-14s %-7s %-10s -> %s\n", slot.ID, pos, anchor, slot.Agent)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func compositionSLMFit(resp compositionCLIResponse) []string {
	return composer.FitHints(resp.Composition, resp.DynamicEnabled, resp.ModelProfile.ContextLimit)
}

func writeCLIKeyVal(b *strings.Builder, k, v string) {
	if strings.TrimSpace(v) == "" {
		v = "-"
	}
	fmt.Fprintf(b, "  %s  %s\n", cli.Dim(fmt.Sprintf("%-14s", k)), v)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
