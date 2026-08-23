package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func formatPipelineStatus(c *config.Config) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(cli.Bold("Pipeline"))
	b.WriteString("\n")
	writeCLIKeyVal(&b, "mode", c.Mode)
	if c.Mode == config.ModeSpecialist {
		specialist := strings.TrimSpace(c.Specialist)
		if specialist == "" {
			specialist = "(default worker)"
		}
		writeCLIKeyVal(&b, "specialist", specialist)
	}
	writeCLIKeyVal(&b, "dynamic", pipelineDynamicStatus(c))
	writeCLIKeyVal(&b, "active_pack", pipelineValueOrDefault(c.ActivePack, "(none)"))
	writeCLIKeyVal(&b, "active_pipeline", pipelineValueOrDefault(c.ActivePipeline, "(default)"))
	writeCLIKeyVal(&b, "plan_approve", fmt.Sprintf("%s (timeout=%s)", c.PlanApprove, compactDuration(c.PlanApproveTimeout)))
	writeCLIKeyVal(&b, "plan_gate", pipelinePlanGateStatus(c))
	prof := config.ResolveModelProfile(c.ModelProfiles, c.Model)
	writeCLIKeyVal(&b, "slm_budget", fmt.Sprintf("context=%d max_tokens=%d max_parallel=%d", prof.ContextLimit, prof.MaxTokens, c.MaxParallel))
	if !c.DynamicPipeline {
		b.WriteString("  ")
		b.WriteString(cli.Dim("enable task-adaptive composition with `slmcode run --dynamic ...` or `slmcode config set dynamic_pipeline true`"))
		b.WriteString("\n")
	}
	return b.String()
}

func pipelineDynamicStatus(c *config.Config) string {
	if c.DynamicPipeline {
		if c.Mode == config.ModeSpecialist {
			return "enabled (full pipeline composition is bypassed in specialist mode)"
		}
		return "enabled (composer selects phases, agents, slots)"
	}
	return "disabled (static configured pipeline)"
}

func pipelinePlanGateStatus(c *config.Config) string {
	if c.AutoApprove {
		return "bypassed (auto_approve=true)"
	}
	mode := config.NormalizePlanApprove(c.PlanApprove)
	switch mode {
	case "off":
		return "off (execute starts without approval)"
	case "auto":
		return "auto (plan summary emitted; execute continues)"
	case "ask":
		timeout := compactDuration(c.PlanApproveTimeout)
		var ask plan.PlanApproveAsk
		ok, err := hitl.ReadAsk(c.SlmDir(), "plan", &ask)
		if err != nil {
			return "ask (pending approval state unreadable: " + err.Error() + ")"
		}
		if ok && strings.TrimSpace(ask.ID) != "" {
			return fmt.Sprintf("waiting for approval id=%s tasks=%d timeout=%s", ask.ID, ask.TaskCount, timeout)
		}
		// NOT "timeout auto-approves": with a terminal attached the gate blocks
		// until it is answered, and headless it follows --on-gate-timeout,
		// which defaults to stop. Nothing auto-approves a plan any more.
		return "ask (pauses before execute · on a terminal it waits for you · headless it follows --on-gate-timeout, default stop; engine timeout " + timeout + ")"
	default:
		return mode
	}
}

func formatLatestCompositionStatus(c *config.Config) string {
	if c == nil {
		return ""
	}
	comp, ok, err := composer.LoadDynamic(c.SlmDir())
	if err != nil {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(cli.Bold("Latest Composition"))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cli.Warn("saved composition could not be read: " + err.Error()))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cli.Dim("run `slmcode compose \"your task\"` to preview a fresh composition or start a new run"))
		b.WriteString("\n")
		return b.String()
	}
	if !ok {
		return ""
	}
	prof := config.ResolveModelProfile(c.ModelProfiles, c.Model)
	hints := composer.FitHints(comp, c.DynamicPipeline, prof.ContextLimit)
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(cli.Bold("Latest Composition"))
	b.WriteString("\n")
	if strings.TrimSpace(comp.Summary) != "" {
		writeCLIKeyVal(&b, "summary", comp.Summary)
	}
	writeCLIKeyVal(&b, "phases", strings.Join(enabledCompositionPhases(comp), " → "))
	writeCLIKeyVal(&b, "team", compositionTeamSummary(comp))
	writeCLIKeyVal(&b, "loop", fmt.Sprintf("worker=%s reviewer=%s corrector=%s waves=%d",
		pipelineValueOrDefault(comp.Execute.DefaultRole, "worker"),
		pipelineValueOrDefault(comp.Execute.Reviewer, "reviewer"),
		pipelineValueOrDefault(comp.Execute.Corrector, "corrector"),
		comp.Execute.MaxWaves))
	if len(hints) > 0 {
		writeCLIKeyVal(&b, "slm_fit", hints[0])
		for _, hint := range hints[1:minInt(len(hints), 3)] {
			b.WriteString("  ")
			b.WriteString(cli.Dim("• " + hint))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func enabledCompositionPhases(comp composer.Composition) []string {
	var out []string
	for _, p := range comp.Phases {
		if p.Enabled && p.When != "never" {
			if p.Agent != "" {
				out = append(out, p.ID+"@"+p.Agent)
			} else {
				out = append(out, p.ID)
			}
		}
	}
	if len(out) == 0 {
		return []string{"(none)"}
	}
	if len(out) > 10 {
		return append(out[:10], fmt.Sprintf("+%d more", len(out)-10))
	}
	return out
}

func compositionTeamSummary(comp composer.Composition) string {
	if len(comp.Team) == 0 {
		return "(default agents)"
	}
	var out []string
	for _, member := range comp.Team {
		role := strings.TrimSpace(member.Role)
		if role != "" {
			out = append(out, role)
		}
	}
	if len(out) == 0 {
		return "(default agents)"
	}
	if len(out) > 8 {
		return strings.Join(out[:8], ", ") + fmt.Sprintf(", +%d more", len(out)-8)
	}
	return strings.Join(out, ", ")
}

func pipelineValueOrDefault(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func compactDuration(d time.Duration) string {
	if d <= 0 {
		return "default"
	}
	return d.Round(time.Second).String()
}
