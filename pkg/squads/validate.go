package squads

import (
	"fmt"
	"sort"
	"strings"
)

// Severity separates "this plan cannot run" from "this plan will run badly".
type Severity string

const (
	// SeverityError means executing this plan risks corrupting work. The
	// caller must not start squads.
	SeverityError Severity = "error"
	// SeverityWarn means the plan runs but gives up a guarantee — usually the
	// one that makes squads worth having.
	SeverityWarn Severity = "warn"
)

// Problem is one validation finding.
type Problem struct {
	Severity Severity
	Squad    string // the squad it concerns, when it concerns one
	Message  string
}

func (p Problem) String() string {
	if p.Squad != "" {
		return fmt.Sprintf("[%s] %s: %s", p.Severity, p.Squad, p.Message)
	}
	return fmt.Sprintf("[%s] %s", p.Severity, p.Message)
}

// Problems is an ordered finding list.
type Problems []Problem

// Errors reports whether any finding blocks execution.
func (ps Problems) Errors() bool {
	for _, p := range ps {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Strings renders the findings for an event or a log line.
func (ps Problems) Strings() []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

// Validate checks a plan before any worker runs.
//
// The one rule that MUST hold is disjoint ownership. Everything squads buys —
// running two teams at once without a lock between them — rests on the promise
// that no two squads can write the same file. Overlap silently converts
// "parallel teams" into "two agents racing on one file", and the loser's edit
// is simply gone. So overlap is an error, and the check is deliberately
// conservative: it would rather demand a more specific glob than let a pair
// through it cannot prove disjoint.
func (p *Plan) Validate() Problems {
	var out Problems
	if p == nil {
		return append(out, Problem{Severity: SeverityError, Message: "no squad plan"})
	}

	if len(p.Squads) < 2 {
		out = append(out, Problem{
			Severity: SeverityError,
			Message: fmt.Sprintf("a squad plan needs at least 2 squads to be worth its overhead, got %d — "+
				"run the normal single-stream pipeline instead", len(p.Squads)),
		})
	}

	for _, s := range p.Squads {
		if len(s.Owns) == 0 {
			out = append(out, Problem{
				Severity: SeverityError, Squad: s.ID,
				Message: "owns no paths, so no task can ever be routed to it and it would sit idle for the whole run",
			})
		}
		if s.Acceptance == "" {
			out = append(out, Problem{
				Severity: SeverityWarn, Squad: s.ID,
				Message: "no acceptance command — this squad's half cannot be proven green on its own, " +
					"so a break in it surfaces only at integration",
			})
		}
	}

	// The safety property.
	for i := 0; i < len(p.Squads); i++ {
		for j := i + 1; j < len(p.Squads); j++ {
			a, b := p.Squads[i], p.Squads[j]
			for _, ga := range a.Owns {
				for _, gb := range b.Owns {
					if !globsIntersect(ga, gb) {
						continue
					}
					out = append(out, Problem{
						Severity: SeverityError,
						Message: fmt.Sprintf(
							"squads %q and %q both claim %q / %q — two teams writing one path in parallel "+
								"loses one of the two edits; give each squad a disjoint subtree",
							a.ID, b.ID, ga, gb),
					})
				}
			}
		}
	}

	known := map[string]bool{}
	for _, s := range p.Squads {
		known[s.ID] = true
	}
	for _, in := range p.Contract.Interfaces {
		if in.Provider == "" {
			out = append(out, Problem{
				Severity: SeverityError,
				Message:  fmt.Sprintf("interface %q has no provider — nobody is building it", in.ID),
			})
		} else if !known[in.Provider] {
			out = append(out, Problem{
				Severity: SeverityError,
				Message:  fmt.Sprintf("interface %q names provider %q, which is not a squad in this plan", in.ID, in.Provider),
			})
		}
		for _, c := range in.Consumers {
			if !known[c] {
				out = append(out, Problem{
					Severity: SeverityError,
					Message:  fmt.Sprintf("interface %q names consumer %q, which is not a squad in this plan", in.ID, c),
				})
			}
		}
		if strings.TrimSpace(in.Spec) == "" {
			out = append(out, Problem{
				Severity: SeverityWarn,
				Message: fmt.Sprintf("interface %q has no spec — an interface named but not specified is exactly "+
					"the gap each side fills in differently", in.ID),
			})
		}
	}

	if len(p.Squads) >= 2 && len(p.Contract.Interfaces) == 0 {
		out = append(out, Problem{
			Severity: SeverityWarn,
			Message: "no interfaces in the contract — with nothing frozen between them, the squads will each " +
				"invent their own version of the seam and integration is where you find out",
		})
	}
	if len(p.Squads) >= 2 && p.Integration.Acceptance == "" {
		out = append(out, Problem{
			Severity: SeverityWarn,
			Message: "no integration acceptance command — every squad can be green with the assembled " +
				"application still broken, and nothing would catch it",
		})
	}

	return out
}

// globsIntersect reports whether two ownership patterns can match a common path.
//
// Exact glob intersection is undecidable in general for these patterns, so this
// answers a deliberately conservative approximation: compare the LITERAL PREFIX
// of each (the segments before the first wildcard) and call them intersecting
// when one prefix contains the other. A wrong "yes" costs the plan author one
// more specific glob; a wrong "no" costs a lost edit, so the bias is not
// symmetric and neither is the rule.
func globsIntersect(a, b string) bool {
	a, b = normalizePath(a), normalizePath(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	// A pattern that is purely wildcards owns the world.
	if isCatchAll(a) || isCatchAll(b) {
		return true
	}
	// If either side is literal, the real matcher can answer exactly.
	aLit, bLit := !hasMeta(a), !hasMeta(b)
	if aLit && bLit {
		return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
	}
	if aLit {
		return matchOwn(b, a) || literalPrefixOverlap(a, b)
	}
	if bLit {
		return matchOwn(a, b) || literalPrefixOverlap(b, a)
	}
	return literalPrefixOverlap(a, b)
}

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

func isCatchAll(s string) bool {
	for _, seg := range strings.Split(s, "/") {
		if seg != "**" && seg != "*" {
			return false
		}
	}
	return true
}

// literalPrefixOverlap compares the wildcard-free heads of two patterns.
func literalPrefixOverlap(a, b string) bool {
	pa, pb := literalPrefix(a), literalPrefix(b)
	if pa == "" || pb == "" {
		// One of them starts with a wildcard, so it can reach anywhere.
		return true
	}
	return pa == pb || strings.HasPrefix(pa, pb+"/") || strings.HasPrefix(pb, pa+"/")
}

func literalPrefix(pattern string) string {
	segs := strings.Split(pattern, "/")
	var out []string
	for _, s := range segs {
		if hasMeta(s) {
			break
		}
		out = append(out, s)
	}
	return strings.Join(out, "/")
}

// Coverage reports which of the given paths no squad owns.
//
// Run against the files a split produced, this is the difference between "the
// plan is fine" and "the plan has a hole nobody will work in".
func (p *Plan) Coverage(paths []string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, path := range paths {
		rel := normalizePath(path)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		if _, ok := p.Owner(rel); !ok {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	return missing
}
