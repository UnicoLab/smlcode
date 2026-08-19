package main

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/session"
)

func formatLatestRunDiagnostics(slmDir string) string {
	turns, err := session.ListQueries(slmDir)
	if err != nil || len(turns) == 0 {
		return ""
	}
	for _, turn := range turns {
		events, err := session.ReadEvents(slmDir, turn.ID, 2000)
		if err != nil || len(events) == 0 {
			continue
		}
		return formatRunDiagnosticsCLI(turn, session.AnalyzeEvents(events))
	}
	return ""
}

func formatRunDiagnosticsCLI(turn session.Turn, summary session.EventSummary) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(cli.Bold("Latest Run Diagnostics"))
	b.WriteString("\n")
	writeCLIKeyVal(&b, "run", turn.ID)
	writeCLIKeyVal(&b, "query", clipStatusLine(turn.Query, 96))
	writeCLIKeyVal(&b, "events", fmt.Sprintf("%d", summary.TotalEvents))
	writeCLIKeyVal(&b, "final", valueOrStatus(summary.FinalPhase, "pending"))
	writeCLIKeyVal(&b, "pressure", fmt.Sprintf("tasks=%d retries=%d replans=%d failures=%d errors=%d",
		summary.Tasks, summary.Retries, summary.Replans, summary.Failures, summary.Errors))
	if summary.Tokens > 0 || summary.CostUSD > 0 {
		writeCLIKeyVal(&b, "usage", fmt.Sprintf("tokens=%d cost=$%.4f", summary.Tokens, summary.CostUSD))
	}
	if len(summary.Models) > 0 {
		writeCLIKeyVal(&b, "models", compactCounts(summary.Models, 4))
	}
	if len(summary.Agents) > 0 {
		writeCLIKeyVal(&b, "agents", compactCounts(summary.Agents, 5))
	}
	if len(summary.Insights) > 0 {
		b.WriteString(cli.Dim("  insights\n"))
		for _, insight := range summary.Insights[:minInt(len(summary.Insights), 3)] {
			line := insight.Title
			if insight.Detail != "" {
				line += " — " + insight.Detail
			}
			fmt.Fprintf(&b, "    - %s\n", clipStatusLine(line, 140))
		}
	}
	if len(summary.Actions) > 0 {
		b.WriteString(cli.Dim("  next_actions\n"))
		for _, action := range summary.Actions[:minInt(len(summary.Actions), 3)] {
			line := action.Title
			if action.Command != "" {
				line += " (" + action.Command + ")"
			}
			if action.Detail != "" {
				line += " — " + action.Detail
			}
			fmt.Fprintf(&b, "    - %s\n", clipStatusLine(line, 140))
		}
	}
	return b.String()
}

func compactCounts(counts []session.EventNameCount, limit int) string {
	if len(counts) == 0 {
		return "-"
	}
	if limit <= 0 || limit > len(counts) {
		limit = len(counts)
	}
	parts := make([]string, 0, limit)
	for _, c := range counts[:limit] {
		parts = append(parts, fmt.Sprintf("%s×%d", c.Name, c.Count))
	}
	if len(counts) > limit {
		parts = append(parts, fmt.Sprintf("+%d", len(counts)-limit))
	}
	return strings.Join(parts, ", ")
}

func valueOrStatus(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func clipStatusLine(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
