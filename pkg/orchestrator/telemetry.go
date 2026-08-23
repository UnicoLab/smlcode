package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/backends"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/repair"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// emitTelemetrySummary surfaces the decoding/repair telemetry in the run
// summary. These counters exist to answer one operational question — is the
// model's structured output actually landing, or is the harness papering over
// it — and until now nothing read them.
func (o *Orchestrator) emitTelemetrySummary() {
	if o == nil {
		return
	}
	md := buildTelemetryMarkdown()
	if strings.TrimSpace(md) == "" {
		return
	}
	o.emitFull("usage", stream.KindUsage, "", "", telemetryHeadline(), "", md)
	if o.store != nil {
		// ReplaceSection, not Append: this is a per-run write-back.
		_ = o.store.ReplaceSection(contextstore.DocScratch, "Decoding telemetry", md)
	}
}

// telemetryHeadline is the one-line version for the CLI footer.
func telemetryHeadline() string {
	parts := []string{}
	if mechs := backends.RoleMechanisms(); len(mechs) > 0 {
		counts := map[string]int{}
		for _, m := range mechs {
			counts[m]++
		}
		parts = append(parts, "decoding "+joinCounts(counts))
	}
	if dropped := totalDropped(); dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped tool call(s)", dropped))
	}
	if lines := repair.Stats.Report(); len(lines) > 0 {
		parts = append(parts, strings.TrimSpace(lines[0]))
	}
	if len(parts) == 0 {
		return "decoding telemetry"
	}
	return strings.Join(parts, " · ")
}

func totalDropped() int {
	n := 0
	for _, v := range backends.DroppedToolCalls() {
		n += v
	}
	return n
}

// buildTelemetryMarkdown renders the full report.
func buildTelemetryMarkdown() string {
	var b strings.Builder

	if lines := backends.TelemetryReport(); len(lines) > 0 {
		b.WriteString("### Backend decoding\n\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
		b.WriteString("\n")
	}

	if mechs := backends.RoleMechanisms(); len(mechs) > 0 {
		b.WriteString("### Mechanism per role\n\n")
		for _, role := range sortedKeys(mechs) {
			b.WriteString("- " + role + ": " + mechs[role] + "\n")
		}
		b.WriteString("\n")
	}

	if dropped := backends.DroppedToolCalls(); len(dropped) > 0 {
		b.WriteString("### Dropped tool calls\n\n")
		for _, k := range sortedIntKeys(dropped) {
			b.WriteString(fmt.Sprintf("- %s: %d\n", k, dropped[k]))
		}
		b.WriteString("\n")
	}

	if lines := repair.Stats.Report(); len(lines) > 0 {
		b.WriteString("### JSON repair ladder\n\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinCounts(m map[string]int) string {
	keys := sortedIntKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}
