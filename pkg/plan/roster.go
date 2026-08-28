package plan

import (
	"sort"
	"strings"
)

// ── Who the manager is shown, and in what order ──────────────────────────
//
// The triage roster used to be an alphabetical list of every implementer the
// factory registers. That is the worst possible order for the decision being
// made: sorted by name, `corrector` and `deep` come before `go-worker` and
// `python-corrector`, so the generic agents sit at the top of the list a 30B
// model reads first — and it picks one, even though its own prompt told it to
// prefer a specialist for the language of the files.
//
// A generic corrector handed a failing Go handler brings nothing the generic
// worker that already failed did not have. The specialist brings the language's
// idioms, its test conventions and its compiler's error vocabulary, which is
// the entire reason the language packs exist.
//
// So the roster is ranked by fitness and labeled, and the choice is enforced
// afterwards rather than merely requested.

// RankedAgent is one roster entry with the reason it is where it is.
type RankedAgent struct {
	ID string
	// Note is the short qualifier shown to the manager ("Go specialist").
	Note string
	// Specialist is true when this agent is tuned for the task's language.
	Specialist bool
}

// RankRoster orders candidate agents by fitness for a task's files.
//
// Language specialists for the task's own language first, then specialists for
// other languages, then generics. Nothing is removed: a defect whose fix lives
// outside the file's language is exactly the case that needs an agent the
// ranking would have buried, and a manager that cannot reach one has no more
// choice than the deterministic router it replaced.
func RankRoster(roster, files []string) []RankedAgent {
	lang := LanguageOf(files)
	out := make([]RankedAgent, 0, len(roster))
	for _, id := range roster {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		prefix := agentLanguage(trimmed)
		entry := RankedAgent{ID: trimmed}
		switch {
		case prefix != "" && prefix == lang:
			entry.Specialist = true
			entry.Note = languageLabel(prefix) + " specialist"
		case prefix != "":
			entry.Note = languageLabel(prefix) + " specialist"
		default:
			entry.Note = "generic"
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return rosterRank(out[i], lang) < rosterRank(out[j], lang)
	})
	return out
}

// rosterRank is the sort key: lower sorts first.
func rosterRank(a RankedAgent, lang string) int {
	switch {
	case a.Specialist:
		return 0
	case a.Note != "generic":
		return 1
	default:
		return 2
	}
}

// PreferSpecialist upgrades a generic pick to the language specialist for the
// task's files, when the roster offers one.
//
// The triage prompt already asks for this. Asking is not enough: it is the rule
// a small model skips most often, and the cost of skipping it is a correction
// that brings nothing the failed attempt did not have. Returns the agent to use
// and whether it changed.
//
// It never overrides a SPECIALIST pick — a manager that deliberately reached for
// another language's expert has a reason the file extensions cannot see — and
// never returns the role that just failed.
func PreferSpecialist(pick, failed string, files, roster []string) (string, bool) {
	pick = strings.TrimSpace(pick)
	if pick == "" || agentLanguage(pick) != "" {
		return pick, false
	}
	lang := LanguageOf(files)
	if lang == "" {
		return pick, false
	}
	offered := map[string]bool{}
	for _, id := range roster {
		offered[strings.ToLower(strings.TrimSpace(id))] = true
	}
	// The corrector first: its whole prompt is "somebody else's code is failing,
	// fix it", which is what a rejected delivery is.
	for _, suffix := range []string{RoleCorrector, RoleWorker} {
		id := lang + "-" + suffix
		if offered[id] && !strings.EqualFold(id, failed) && !strings.EqualFold(id, pick) {
			return id, true
		}
	}
	return pick, false
}

// agentLanguage is the language-pack prefix an agent id carries, "" for a
// generic one.
func agentLanguage(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	i := strings.Index(id, "-")
	if i <= 0 {
		return ""
	}
	prefix := id[:i]
	if languageLabel(prefix) == "" {
		return ""
	}
	return prefix
}

// languageLabel is the human name for a language-pack prefix, "" when the
// prefix names no pack this harness knows.
//
// Keyed off exactly the set langOf maps file extensions into, so an agent id is
// called a specialist only when a file could actually route to it. A prefix
// that is not in that set is somebody's naming convention, not a language:
// "backend-worker" is a generic worker with a team name on it.
func languageLabel(prefix string) string {
	return languageNames[strings.ToLower(strings.TrimSpace(prefix))]
}

// languageNames is langOf's output set with display names. Keep it in step with
// langOf — TestEveryLanguagePrefixHasALabel fails when they drift.
var languageNames = map[string]string{
	"go":     "Go",
	"react":  "React",
	"ts":     "TypeScript",
	"python": "Python",
	"rust":   "Rust",
	"java":   "Java",
	"kotlin": "Kotlin",
	"ruby":   "Ruby",
	"php":    "PHP",
	"swift":  "Swift",
	"dotnet": ".NET",
	"cpp":    "C/C++",
	"shell":  "Shell",
	"web":    "Web",
}
