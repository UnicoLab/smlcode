package skills

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Progressive disclosure.
//
// Rendering the ENTIRE SKILL.md body for four to six matched skills puts
// hundreds of tokens of always-on behavioural directives in front of a 7B with
// a 3K budget, and multiple simultaneous directives measurably degrade
// small-model instruction-following. Two stages instead:
//
//	stage 1 — a ~30-token card (name + description + triggers) for every match
//	stage 2 — the full body, only for explicit @skill: references and skills
//	          scoring above the specialist-default tier
//
// A tool (ws_skill) can pull any remaining body on demand via ExpandBody.
const (
	// SpecialistDefaultScore is the score ResolveForRun assigns a skill that
	// matched purely because it targets this specialist. Anything at or below
	// this tier stays a card.
	SpecialistDefaultScore = 80
	// ExplicitRefScore is the score for an @skill: reference or a pin.
	ExplicitRefScore = 1000
	// DefaultMaxExpanded caps how many bodies are ever inlined at once.
	DefaultMaxExpanded = 2
	// CardBodyPreviewBytes is how much of a card's description survives.
	CardBodyPreviewBytes = 220
)

// Match is a scored skill from ResolveMatches.
type Match struct {
	Skill    Skill `json:"skill"`
	Score    int   `json:"score"`
	Explicit bool  `json:"explicit"` // named via @skill: / /skill / a pin
}

// PackOptions configures RenderMatches.
type PackOptions struct {
	MaxChars int
	// Expand names additional skills to inline in full (case-insensitive).
	Expand []string
	// ExpandAll restores the historical dump-everything behaviour.
	ExpandAll bool
	// CardsOnly suppresses every body, even explicit refs.
	CardsOnly bool
	// MaxExpanded caps inlined bodies (default DefaultMaxExpanded).
	MaxExpanded int
}

// ShouldExpand reports whether a match earns its full body inline.
func (m Match) ShouldExpand() bool {
	return m.Explicit || m.Score > SpecialistDefaultScore
}

// RenderCards renders every skill as a compact card — no bodies.
func RenderCards(list []Skill, maxChars int) string {
	matches := make([]Match, 0, len(list))
	for _, s := range list {
		matches = append(matches, Match{Skill: s})
	}
	return RenderMatches(matches, PackOptions{MaxChars: maxChars, CardsOnly: true})
}

// RenderMatches is the two-stage renderer.
func RenderMatches(matches []Match, opts PackOptions) string {
	if len(matches) == 0 {
		return ""
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = 4000
	}
	maxExpanded := opts.MaxExpanded
	switch {
	case opts.CardsOnly:
		maxExpanded = 0
	case opts.ExpandAll:
		maxExpanded = len(matches)
	case maxExpanded == 0 && !hasExplicitOpt(opts):
		maxExpanded = DefaultMaxExpanded
	}
	force := map[string]bool{}
	for _, n := range opts.Expand {
		force[strings.ToLower(strings.TrimSpace(n))] = true
	}

	var b strings.Builder
	b.WriteString("## Active skills\n\n")
	b.WriteString("Cards below are summaries. Ask for a full skill with `@skill:name` in the query.\n\n")

	expanded := 0
	// Pass 1: cards for everything (cheap, so nothing is silently dropped).
	for _, m := range matches {
		card := renderCard(m.Skill)
		if b.Len()+len(card) > maxChars {
			// One fat entry must not drop every lower-ranked skill: keep going.
			continue
		}
		b.WriteString(card)
	}
	// Pass 2: bodies for the ones that earned it.
	for _, m := range matches {
		if opts.CardsOnly {
			break
		}
		if expanded >= maxExpanded && !force[strings.ToLower(m.Skill.Name)] {
			continue
		}
		if !opts.ExpandAll && !force[strings.ToLower(m.Skill.Name)] && !m.ShouldExpand() {
			continue
		}
		section := renderBody(m.Skill)
		if b.Len()+len(section) > maxChars {
			continue
		}
		b.WriteString(section)
		expanded++
	}
	return b.String()
}

func hasExplicitOpt(opts PackOptions) bool { return len(opts.Expand) > 0 }

func renderCard(s Skill) string {
	agents := "*"
	if len(s.Agents) > 0 {
		agents = strings.Join(s.Agents, ", ")
	}
	desc := textutil.Truncate(strings.TrimSpace(s.Description), CardBodyPreviewBytes, "…")
	var b strings.Builder
	fmt.Fprintf(&b, "- **skill:%s** — %s", s.Name, desc)
	if len(s.Triggers) > 0 {
		fmt.Fprintf(&b, " _(triggers: %s)_", strings.Join(s.Triggers, ", "))
	}
	fmt.Fprintf(&b, " <!-- agents: %s -->\n", agents)
	return b.String()
}

func renderBody(s Skill) string {
	agents := "*"
	if len(s.Agents) > 0 {
		agents = strings.Join(s.Agents, ", ")
	}
	return fmt.Sprintf("\n### skill:%s\n%s\n<!-- agents: %s -->\n\n%s\n\n",
		s.Name, s.Description, agents, strings.TrimSpace(s.Body))
}

// ExpandBody returns one skill's full body on demand — the backing call for a
// `ws_skill` tool, so a specialist can pull a skill it only saw as a card.
func (l *Loader) ExpandBody(name string) (string, bool) {
	sk, ok := l.Get(name)
	if !ok {
		return "", false
	}
	return renderBody(sk), true
}

// ResolveMatches is ResolveForRun with the scores retained, so a caller can
// decide what to expand.
func (l *Loader) ResolveMatches(query, agent string, pins []string, limit int) []Match {
	scores, explicit, ranked := l.resolveScored(query, agent, pins, limit)
	out := make([]Match, 0, len(ranked))
	for _, s := range ranked {
		key := strings.ToLower(s.Name)
		out = append(out, Match{Skill: s, Score: scores[key], Explicit: explicit[key]})
	}
	return out
}

// PackForAgentTiered renders the two-stage pack for one specialist.
func (l *Loader) PackForAgentTiered(agent, query string, maxChars int) string {
	return RenderMatches(l.ResolveMatches(query, agent, nil, 6), PackOptions{MaxChars: maxChars})
}
