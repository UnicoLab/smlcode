package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/readiness"
)

func readinessCmd() *cobra.Command {
	var asJSON bool
	var fix bool
	var probe bool
	cmd := &cobra.Command{
		Use:     "readiness",
		Aliases: []string{"ready"},
		Short:   "Score and optionally harden SLM production settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			skills, _ := ws.Skills.List()
			report := buildReadinessReport(ws.Config, len(skills), probe)
			fixed := false
			if fix {
				patch, ok, err := readiness.PatchForFailed(report)
				if err != nil {
					return err
				}
				if ok {
					ws.Config.ApplyPatch(patch)
					if err := ws.Config.Save(); err != nil {
						return err
					}
					fixed = true
					report = buildReadinessReport(ws.Config, len(skills), probe)
				}
			}
			if asJSON {
				payload := map[string]interface{}{
					"fixed":     fixed,
					"readiness": report,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			fmt.Print(formatReadinessCLI(report, fixed))
			if !report.OK {
				return fmt.Errorf("readiness score %d (%s) — run `slmcode readiness --fix` to apply safe local-model defaults", report.Score, report.Status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&fix, "fix", false, "apply safe config fixes for failed checks")
	cmd.Flags().BoolVar(&probe, "probe", true, "check configured provider endpoint and model availability")
	return cmd
}

func buildReadinessReport(cfg *config.Config, skillCount int, probe bool) readiness.Report {
	if !probe {
		return readiness.Build(cfg, skillCount)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return readiness.BuildWithProbe(ctx, cfg, skillCount)
}

func formatReadinessCLI(r readiness.Report, fixed bool) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(cli.Bold(cli.Accent("▸ SLM Readiness")))
	b.WriteString("\n")
	b.WriteString(cli.Dim(strings.Repeat("─", 19)))
	b.WriteString("\n")
	writeCLIKeyVal(&b, "score", fmt.Sprintf("%d", r.Score))
	writeCLIKeyVal(&b, "status", r.Status)
	writeCLIKeyVal(&b, "provider", r.Provider)
	writeCLIKeyVal(&b, "model", r.Model)
	if r.ActiveStack != "" {
		writeCLIKeyVal(&b, "stack", r.ActiveStack)
	}
	if fixed {
		b.WriteString("  ")
		b.WriteString(cli.Success("applied readiness fixes"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(cli.Bold("Checks"))
	b.WriteString("\n")
	for _, check := range r.Checks {
		icon := cli.Success("ok")
		if !check.OK {
			if check.Severity == "critical" {
				icon = cli.Error("fail")
			} else {
				icon = cli.Warn("warn")
			}
		}
		fmt.Fprintf(&b, "  %-18s %-14s %s\n", check.ID, icon, check.Message)
		if !check.OK && check.FixLabel != "" {
			fmt.Fprintf(&b, "  %-18s %s\n", "", cli.Dim("fix: "+check.FixLabel))
		}
	}
	if readinessHasFailedChecks(r) {
		b.WriteString("\n")
		b.WriteString(cli.Dim("  Apply safe defaults: slmcode readiness --fix"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func readinessHasFailedChecks(r readiness.Report) bool {
	for _, check := range r.Checks {
		if !check.OK {
			return true
		}
	}
	return false
}
