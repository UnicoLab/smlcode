package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
	var noProbe bool
	cmd := &cobra.Command{
		Use:     "readiness",
		Aliases: []string{"ready"},
		Short:   "Score and optionally harden SLM production settings",
		Example: "  slmcode readiness\n  slmcode readiness --fix\n  slmcode readiness --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			skills, _ := ws.Skills.List()
			if noProbe {
				probe = false
			}
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
	cmd.Flags().BoolVar(&noProbe, "no-probe", false, "skip configured provider endpoint and model availability check")
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
		for _, line := range readinessCLIDetailLines(check) {
			fmt.Fprintf(&b, "  %-18s %s\n", "", cli.Dim(line))
		}
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

func readinessCLIDetailLines(check readiness.Check) []string {
	var lines []string
	if check.Endpoint != "" {
		lines = append(lines, "endpoint: "+check.Endpoint)
	}
	if check.Latency > 0 {
		lines = append(lines, fmt.Sprintf("latency: %d ms", check.Latency))
	}
	if !check.OK && check.FixHint != "" {
		lines = append(lines, "hint: "+check.FixHint)
	}
	if !check.OK && len(check.Details) > 0 {
		if detail := compactReadinessDetails(check.Details); detail != "" {
			lines = append(lines, "details: "+detail)
		}
	}
	return lines
}

func compactReadinessDetails(details map[string]interface{}) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for k := range details {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := details[k]
		switch val := v.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", k, val))
			}
		case bool:
			parts = append(parts, fmt.Sprintf("%s=%v", k, val))
		case int:
			parts = append(parts, fmt.Sprintf("%s=%d", k, val))
		case int64:
			parts = append(parts, fmt.Sprintf("%s=%d", k, val))
		case float64:
			parts = append(parts, fmt.Sprintf("%s=%g", k, val))
		case fmt.Stringer:
			if strings.TrimSpace(val.String()) != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", k, val.String()))
			}
		}
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func readinessHasFailedChecks(r readiness.Report) bool {
	for _, check := range r.Checks {
		if !check.OK {
			return true
		}
	}
	return false
}
