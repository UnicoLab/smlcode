package composer

import (
	"fmt"
	"strings"
)

// Budget classes — deciding what a task is WORTH, not just what shape it has.
//
// The composer already picks which phases run, which specialists fill them and
// which skills load. What it never decided is how much of the run's finite
// budget the request deserves, so "rename a variable" could still walk
// architect → explore → plan → split → execute → review → test. On a frontier
// API that is a rounding error. On a 7-32B model against a fixed runway the
// ceremony IS the failure: the budget is gone before the edit lands.
//
// A class is two axes:
//
//	complexity  how much can go wrong        trivial · simple · standard · critical
//	kind        what sort of work it is      inquiry · task · debug
//
// and the pair buys a Profile: a phase set, a wave budget, a think-pass count,
// and how deep the gates run.
//
// ── What this deliberately does NOT scale ────────────────────────────────
//
// Reviewer COUNT. The obvious move is "critical work gets four reviewers", and
// it is wrong here for a reason specific to this harness: our reviewers are
// LLM calls and our gates are not. A second reviewer costs a full generation
// and returns another opinion from the same weak model; a deeper gate costs a
// subprocess and returns ground truth from the filesystem. Scaling opinions
// would spend the exact resource a local run cannot spare to buy the weakest
// evidence available. So a higher class buys MORE DETERMINISM — smoke on every
// worker, static quality, more QA rounds, the strict reviewer as a second
// *gate* — and never more voices.

// Complexity levels, cheapest first.
const (
	// ComplexityTrivial is a mechanical single-file change with no behavior
	// change: a rename, a typo, a comment, a version bump.
	ComplexityTrivial = "trivial"
	// ComplexitySimple is a small, low-risk change across one or two files.
	ComplexitySimple = "simple"
	// ComplexityStandard is multi-file work or user-visible behavior. The
	// default, and deliberately so — see Classify.
	ComplexityStandard = "standard"
	// ComplexityCritical is work that DIRECTLY modifies authentication or
	// authorization logic, payment or billing calculation, secret handling,
	// destructive data operations, or production deployment.
	ComplexityCritical = "critical"
)

// Kinds of work.
const (
	// KindInquiry is read-only: a question, an explanation, an exploration.
	KindInquiry = "inquiry"
	// KindTask implements something.
	KindTask = "task"
	// KindDebug investigates a failure whose cause is not yet known.
	KindDebug = "debug"
)

// Profile is the budget one (complexity × kind) pair buys.
type Profile struct {
	Complexity string
	Kind       string
	// Phases are the non-structural phases to enable, in execution order.
	Phases []string
	// MaxWaves caps corrective execute waves.
	MaxWaves int
	// ThinkPasses is the multipass budget for generative roles.
	ThinkPasses int
	// QAGateRounds caps how many times the QA gate may drive a fix round.
	QAGateRounds int
	// RequireSmoke demands a deterministic smoke pass before approval.
	RequireSmoke bool
	// StaticQuality runs the stub/placeholder gate.
	StaticQuality bool
	// StrictReview engages reviewer-strict as a second, harsher gate.
	StrictReview bool
	// Why is one operator-facing sentence explaining the budget.
	Why string
}

// String renders the class for logs and `compose --explain`.
func (p Profile) String() string {
	return fmt.Sprintf("%s:%s (%d phases, %d waves, %d think, %d qa rounds)",
		p.Complexity, p.Kind, len(p.Phases), p.MaxWaves, p.ThinkPasses, p.QAGateRounds)
}

// PhaseSet returns the profile's phases as a lookup set.
func (p Profile) PhaseSet() map[string]bool {
	out := make(map[string]bool, len(p.Phases))
	for _, id := range p.Phases {
		out[id] = true
	}
	return out
}

// Phase sets, narrowest first.
//
// Two groups of phases are NOT in play here and must appear in every set:
//
//   - init, skills and done are structural; the engine owns them.
//   - plan, split, execute and test are a hard invariant the orchestrator
//     re-enables unconditionally (ensureCriticalComposition). Fighting it
//     would be a bad trade even if it were possible: with planning off, a run
//     falls through to fallbackTasks, which ships one task whose acceptance is
//     the string "Query goals met" — not a runnable criterion, so the whole
//     P1 contract degrades to "the reviewer must judge everything". Planning a
//     one-file edit is cheap; losing its acceptance contract is not.
//
// What a class actually buys is therefore the OPTIONAL BREADTH — explore,
// docs, architect, clarify, coord, polish, learn, memory — which is also where
// the real cost lives: those phases read the repo, design, coordinate and
// write memory, and on a small model they are what consumes the runway before
// the edit lands.
var (
	phasesTrivial  = []string{"context", "plan", "split", "execute", "test"}
	phasesSimple   = []string{"context", "explore", "plan", "split", "execute", "test"}
	phasesStandard = []string{"context", "explore", "plan", "split", "coord",
		"execute", "test", "learn", "memory"}
	phasesCritical = []string{"context", "docs", "explore", "architect", "plan", "split",
		"coord", "execute", "polish", "test", "learn", "memory"}

	// An inquiry writes nothing, so design and coordination breadth is pure
	// cost; it keeps exploration, which is the part that answers the question.
	phasesInquiry = []string{"context", "explore", "plan", "split", "execute", "test"}
	// Debug reproduces before it changes anything. Its design phases are
	// wasted — you cannot plan a fix for a cause you have not found — so it
	// trades architect/docs breadth for the same verify depth.
	phasesDebugLight = []string{"context", "explore", "plan", "split", "execute", "test"}
	phasesDebugFull  = []string{"context", "explore", "plan", "split", "coord",
		"execute", "test", "learn"}
)

// ProfileFor returns the budget for a class. Unknown values normalize to the
// standard task profile rather than erroring: an unrecognized class is a
// classifier bug, and the right response to a classifier bug is the middle of
// the range, not a crash or a free pass.
func ProfileFor(complexity, kind string) Profile {
	complexity = NormalizeComplexity(complexity)
	kind = NormalizeKind(kind)

	// An inquiry's cost is bounded by what it READS, not by how dangerous the
	// subject is, so complexity buys it very little. "Explain the auth flow"
	// is classified critical by subject and is still a read-only question.
	if kind == KindInquiry {
		return Profile{
			Complexity: complexity, Kind: kind,
			Phases: phasesInquiry, MaxWaves: 1, ThinkPasses: 1,
			// RequireSmoke off is the one gate a class may switch OFF, and only
			// here: an inquiry writes nothing, so demanding a smoke PASS before
			// approval is a gate the task cannot possibly satisfy — a deadlock,
			// not a safeguard. StaticQuality stays on; it costs nothing and
			// still catches an "inquiry" that quietly wrote a stub.
			QAGateRounds: 0, RequireSmoke: false, StaticQuality: true,
			StrictReview: complexity == ComplexityCritical,
			Why:          "read-only: no correction rounds, no smoke requirement",
		}
	}

	p := Profile{Complexity: complexity, Kind: kind}
	switch complexity {
	case ComplexityTrivial:
		p.Phases, p.MaxWaves, p.ThinkPasses, p.QAGateRounds = phasesTrivial, 1, 1, 1
		p.RequireSmoke, p.StaticQuality, p.StrictReview = true, true, false
		p.Why = "mechanical single-file change: no exploration or design, every disk gate kept"
	case ComplexitySimple:
		p.Phases, p.MaxWaves, p.ThinkPasses, p.QAGateRounds = phasesSimple, 2, 1, 2
		p.RequireSmoke, p.StaticQuality, p.StrictReview = true, true, false
		p.Why = "small scoped change: explore and verify, no design phases"
	case ComplexityCritical:
		p.Phases, p.MaxWaves, p.ThinkPasses, p.QAGateRounds = phasesCritical, 4, 3, 4
		p.RequireSmoke, p.StaticQuality, p.StrictReview = true, true, true
		p.Why = "touches auth, money, secrets, data loss or deploys: every gate, deepest budget"
	default:
		p.Phases, p.MaxWaves, p.ThinkPasses, p.QAGateRounds = phasesStandard, 3, 2, 3
		p.RequireSmoke, p.StaticQuality, p.StrictReview = true, true, false
		p.Why = "multi-file or user-visible work: the full default pipeline"
	}

	// Debug is not "a task with a bug attached". It reproduces first and its
	// design phases are wasted — you cannot plan a fix for a cause you have
	// not found — so it trades architect/plan breadth for test depth.
	if kind == KindDebug {
		switch complexity {
		case ComplexityTrivial, ComplexitySimple:
			p.Phases = phasesDebugLight
		case ComplexityCritical:
			// A critical bug keeps the full phase set: a wrong fix to auth or
			// billing is the same damage as a wrong feature.
		default:
			p.Phases = phasesDebugFull
		}
		if p.MaxWaves < 2 {
			p.MaxWaves = 2 // a reproduction round plus a fix round, minimum
		}
		p.Why = "debug: " + p.Why
	}
	return p
}

// NormalizeComplexity maps model- or operator-authored text onto a level.
//
// Unknown input becomes ComplexityStandard, never trivial. The failure
// directions are not symmetric: over-provisioning a small task wastes budget
// on a run that then succeeds, while under-provisioning a large one produces a
// confident, unverified, wrong answer.
func NormalizeComplexity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ComplexityTrivial:
		return ComplexityTrivial
	case ComplexitySimple:
		return ComplexitySimple
	case ComplexityCritical:
		return ComplexityCritical
	default:
		return ComplexityStandard
	}
}

// NormalizeKind maps text onto a kind, defaulting to KindTask.
func NormalizeKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case KindInquiry:
		return KindInquiry
	case KindDebug:
		return KindDebug
	default:
		return KindTask
	}
}

// Classification is the cheap tier's verdict on a request.
type Classification struct {
	Complexity string
	Kind       string
	// Confident reports whether the heuristics found a STRONG signal. When
	// false the caller should spend an LLM composer call; when true it can
	// skip one entirely, which is the whole point of a two-tier conductor.
	Confident bool
	// Why names the signal that decided it, for `compose --explain` and logs.
	Why string
}

// Profile returns the budget this classification buys.
func (c Classification) Profile() Profile { return ProfileFor(c.Complexity, c.Kind) }

// criticalSubjects are the code areas where a wrong change is not a bug but an
// incident. Matching one only raises the class — it never lowers it.
//
// Deliberately about the SUBJECT, not the verb: "refactor the auth package
// into modules" and "fix the password comparison" both touch authentication,
// and the harness cannot tell from the query text which one merely moves code.
// Over-classifying a refactor costs a few more gates; under-classifying the
// comparison ships a broken login.
var criticalSubjects = []string{
	"auth", "authn", "authz", "login", "logout", "password", "credential",
	"session token", "jwt", "oauth", "permission", "access control",
	"payment", "billing", "invoice", "charge", "refund", "stripe", "checkout",
	"secret", "api key", "private key", "encryption", "decrypt", "signing key",
	"drop table", "delete from", "truncate", "migration", "destructive",
	"production deploy", "deploy to prod", "prod database", "pii",
}

// trivialVerbs are mechanical edits with no behavior change.
var trivialVerbs = []string{
	"rename", "typo", "spelling", "comment", "formatting", "gofmt", "reformat",
	"bump the version", "bump version", "version bump", "changelog",
}

// debugSignals mean "something is broken and the cause is unknown".
var debugSignals = []string{
	"bug", "broken", "failing", "fails", "crash", "panic", "regression",
	"stack trace", "traceback", "not working", "doesn't work", "does not work",
	"debug", "flaky", "hangs", "deadlock", "segfault", "exception",
}

// inquiryOpeners start a question rather than an instruction.
var inquiryOpeners = []string{
	"what ", "why ", "how ", "where ", "which ", "who ", "when ",
	"explain", "describe", "summarize", "summarize", "list ", "show me",
	"tell me", "does ", "do we", "is there", "are there", "can you explain",
	"walk me through", "give me an overview",
}

// writeVerbs mean the request wants the tree changed. Their presence overrides
// an interrogative opener: "how do I add rate limiting — add it" is a task.
var writeVerbs = []string{
	"add ", "implement", "create ", "write ", "build ", "fix ", "refactor",
	"remove ", "delete ", "update ", "change ", "rename", "migrate",
	"introduce", "replace ", "extract ", "wire ", "port ", "upgrade",
}

// Classify is the cheap tier of the two-tier conductor: a deterministic pass
// that costs nothing and answers the easy majority of requests.
//
// It reports Confident only on a strong, unambiguous signal. Everything else
// returns the standard-task class with Confident=false, which tells the caller
// to spend the LLM composer call it would otherwise have spent unconditionally.
// That is the actual saving — not a cheaper classification, but a classification
// that is often free.
func Classify(query string) Classification {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Classification{
			Complexity: ComplexityStandard, Kind: KindTask,
			Confident: false, Why: "empty query",
		}
	}

	hasWrite := containsAnyToken(q, writeVerbs)
	kind := KindTask
	kindWhy := ""
	switch {
	case containsAnyToken(q, debugSignals):
		kind, kindWhy = KindDebug, "failure language in the request"
	case !hasWrite && startsWithAny(q, inquiryOpeners):
		kind, kindWhy = KindInquiry, "interrogative opener with no write verb"
	case !hasWrite && containsAnyToken(q, inquiryOpeners):
		kind, kindWhy = KindInquiry, "question language with no write verb"
	}

	// Critical outranks everything below it, including trivial: "rename the
	// password hashing function" is a rename AND a change to auth code.
	if containsAnyToken(q, criticalSubjects) {
		return Classification{
			Complexity: ComplexityCritical, Kind: kind, Confident: true,
			Why: joinWhy("names a high-risk subject (auth, money, secrets, data loss or deploy)", kindWhy),
		}
	}

	if kind == KindInquiry {
		return Classification{
			Complexity: ComplexitySimple, Kind: KindInquiry, Confident: true,
			Why: joinWhy("read-only question", kindWhy),
		}
	}

	// Trivial needs BOTH a mechanical verb and a narrow target. A "rename"
	// spanning a whole package is not a trivial change, and the file count is
	// the only cheap proxy for span the query text offers.
	if containsAnyToken(q, trivialVerbs) && countPathLikeTokens(q) <= 1 && len(q) < 160 {
		return Classification{
			Complexity: ComplexityTrivial, Kind: kind, Confident: true,
			Why: joinWhy("mechanical edit naming at most one file", kindWhy),
		}
	}

	// Everything else is standard AND unconfident: the honest answer is that
	// these heuristics cannot tell a one-file addition from a subsystem, and
	// pretending otherwise is how a budget class becomes a correctness bug.
	return Classification{
		Complexity: ComplexityStandard, Kind: kind, Confident: false,
		Why: joinWhy("no decisive signal — deferring to the composer", kindWhy),
	}
}

func joinWhy(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// startsWithAny reports whether q begins with any of the needles.
func startsWithAny(q string, needles []string) bool {
	for _, n := range needles {
		if strings.HasPrefix(q, n) {
			return true
		}
	}
	return false
}

// containsAnyToken reports whether q contains any needle at a word boundary.
//
// A plain Contains would match "add " inside "paddle" and "is there" inside
// "this therapy"; the boundary check is what keeps a one-word coincidence from
// re-routing a whole run.
func containsAnyToken(q string, needles []string) bool {
	for _, n := range needles {
		if idx := tokenIndex(q, n); idx >= 0 {
			return true
		}
	}
	return false
}

// inflections are the English endings a signal word may carry and still be the
// same signal.
//
// Without these the lists would have to spell out every form a person might
// type — "panic", "panics", "panicked" — and the one that got missed decided a
// whole run's budget. Matching is still anchored at BOTH ends, so an inflection
// can only extend a word, never let a different word through: "panic"+"s"
// matches, "auth"+"or" does not.
var inflections = []string{"s", "es", "ed", "d", "ing", "ked", "king"}

// tokenIndex finds needle in q at a word boundary, or -1. The match may carry
// an inflection (see above) but must still end at a word boundary.
func tokenIndex(q, needle string) int {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return -1
	}
	for from := 0; ; {
		i := strings.Index(q[from:], needle)
		if i < 0 {
			return -1
		}
		i += from
		if (i == 0 || !isWordByte(q[i-1])) && endsAtBoundary(q, i+len(needle)) {
			return i
		}
		from = i + 1
	}
}

// endsAtBoundary reports whether position end terminates a word, either
// directly or after one inflection.
func endsAtBoundary(q string, end int) bool {
	if end >= len(q) || !isWordByte(q[end]) {
		return true
	}
	for _, suf := range inflections {
		stop := end + len(suf)
		if stop <= len(q) && q[end:stop] == suf && (stop >= len(q) || !isWordByte(q[stop])) {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// countPathLikeTokens counts tokens that look like file paths in a query.
//
// It is a proxy for how wide a change is, and a weak one — which is why it
// only ever confirms a TRIVIAL classification that a mechanical verb already
// suggested, and never assigns a class on its own.
func countPathLikeTokens(q string) int {
	n := 0
	for _, tok := range strings.Fields(q) {
		tok = strings.Trim(tok, "\"'`,;:()[]")
		if tok == "" || !strings.Contains(tok, ".") {
			continue
		}
		// A path token has an extension-shaped tail: a dot followed by 1-5
		// letters at the end. This excludes prose ("e.g.", "etc.") and version
		// numbers ("v1.2.3").
		dot := strings.LastIndex(tok, ".")
		ext := tok[dot+1:]
		if ext == "" || len(ext) > 5 {
			continue
		}
		alpha := true
		for i := 0; i < len(ext); i++ {
			if ext[i] < 'a' || ext[i] > 'z' {
				alpha = false
				break
			}
		}
		if alpha {
			n++
		}
	}
	return n
}
