package squads

import (
	"fmt"
	"strings"
)

// ContractFile is the on-disk name of the frozen inter-squad contract.
const ContractFile = "CONTRACT.md"

// RenderContract writes the contract as the document both squads build against.
//
// On disk, not in a prompt variable, and written BEFORE any worker starts. That
// is the whole mechanism: two squads running concurrently cannot ask each other
// what the seam looks like, so the seam has to be a file they both read. It is
// also the artifact a human can correct between phases — the cheapest possible
// intervention point, because fixing one line here is worth more than reviewing
// either half afterwards.
func RenderContract(p Plan) string {
	var b strings.Builder
	b.WriteString("# Interface contract\n\n")
	b.WriteString("FROZEN. Both squads build against this text. If your half needs a change\n")
	b.WriteString("here, say so in your output instead of implementing something different —\n")
	b.WriteString("a unilateral change is how the halves stop fitting together.\n\n")

	if p.Summary != "" {
		b.WriteString(p.Summary + "\n\n")
	}
	if p.Contract.Summary != "" {
		b.WriteString("## Overview\n\n" + p.Contract.Summary + "\n\n")
	}

	b.WriteString("## Squads\n\n")
	for _, s := range p.Squads {
		fmt.Fprintf(&b, "### %s (`%s`)\n\n", s.Name, s.ID)
		if s.Charter != "" {
			b.WriteString(s.Charter + "\n\n")
		}
		b.WriteString("- Owns: " + joinCode(s.Owns) + "\n")
		if s.Acceptance != "" {
			fmt.Fprintf(&b, "- Acceptance: `%s`\n", s.Acceptance)
		}
		b.WriteString("\n")
	}

	if len(p.Contract.Interfaces) > 0 {
		b.WriteString("## Interfaces\n\n")
		for _, in := range p.Contract.Interfaces {
			fmt.Fprintf(&b, "### %s\n\n", in.ID)
			fmt.Fprintf(&b, "- Provided by: `%s`\n", in.Provider)
			if len(in.Consumers) > 0 {
				fmt.Fprintf(&b, "- Consumed by: %s\n", joinCode(in.Consumers))
			}
			if in.Spec != "" {
				b.WriteString("\n```\n" + strings.TrimRight(in.Spec, "\n") + "\n```\n")
			}
			b.WriteString("\n")
		}
	}

	if p.Integration.Acceptance != "" || len(p.Integration.Notes) > 0 {
		b.WriteString("## Integration\n\n")
		if p.Integration.Acceptance != "" {
			fmt.Fprintf(&b, "Runs after every squad is green: `%s`\n\n", p.Integration.Acceptance)
		}
		for _, n := range p.Integration.Notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Brief is the compact per-squad instruction injected into that squad's task
// packs.
//
// Deliberately short. A worker in a 30B-class model that is handed the whole
// contract spends its attention budget reading the other team's half; what it
// needs is its own charter, its boundary, and the clauses it is on the hook
// for. The "do not edit" line is the one that keeps a frontend task from
// rewriting the API when the frontend build fails against it.
func (p *Plan) Brief(squadID string) string {
	s, ok := p.Squad(squadID)
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Your squad: %s (`%s`)\n\n", s.Name, s.ID)
	if s.Charter != "" {
		b.WriteString(s.Charter + "\n\n")
	}
	b.WriteString("You own (write only here): " + joinCode(s.Owns) + "\n")

	var others []string
	for _, o := range p.Squads {
		if o.ID != s.ID {
			others = append(others, o.Owns...)
		}
	}
	if len(others) > 0 {
		b.WriteString("Owned by another squad working RIGHT NOW — do not edit, do not \"fix\": " +
			joinCode(dedupePaths(others)) + "\n")
	}
	if s.Acceptance != "" {
		fmt.Fprintf(&b, "Your half is done when this passes: `%s`\n", s.Acceptance)
	}

	provides, consumes := p.interfacesFor(s.ID)
	if len(provides) > 0 {
		b.WriteString("\n### You PROVIDE these — other squads are building against them now\n\n")
		for _, in := range provides {
			writeInterface(&b, in)
		}
	}
	if len(consumes) > 0 {
		b.WriteString("\n### You CONSUME these — they may not exist on disk yet; build to the spec\n\n")
		for _, in := range consumes {
			writeInterface(&b, in)
		}
	}
	if len(provides) > 0 || len(consumes) > 0 {
		fmt.Fprintf(&b, "\nThe full contract is in `%s`. It is frozen: if it is wrong, report that "+
			"in your output rather than diverging from it.\n", ContractFile)
	}
	return b.String()
}

func writeInterface(b *strings.Builder, in Interface) {
	fmt.Fprintf(b, "- **%s**", in.ID)
	if in.Spec != "" {
		fmt.Fprintf(b, " — %s", strings.ReplaceAll(strings.TrimSpace(in.Spec), "\n", " "))
	}
	b.WriteString("\n")
}

func (p *Plan) interfacesFor(squadID string) (provides, consumes []Interface) {
	for _, in := range p.Contract.Interfaces {
		if in.Provider == squadID {
			provides = append(provides, in)
			continue
		}
		for _, c := range in.Consumers {
			if c == squadID {
				consumes = append(consumes, in)
				break
			}
		}
	}
	return provides, consumes
}

func joinCode(items []string) string {
	if len(items) == 0 {
		return "(nothing)"
	}
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, "`"+s+"`")
	}
	return strings.Join(out, ", ")
}

// Summarize renders a one-line org chart for an event stream.
func (p *Plan) Summarize() string {
	if p == nil || len(p.Squads) == 0 {
		return "no squads"
	}
	parts := make([]string, 0, len(p.Squads))
	for _, s := range p.Squads {
		parts = append(parts, fmt.Sprintf("%s(%s)", s.ID, strings.Join(s.Owns, ",")))
	}
	return fmt.Sprintf("%d squads · %d interfaces · %s",
		len(p.Squads), len(p.Contract.Interfaces), strings.Join(parts, " | "))
}
