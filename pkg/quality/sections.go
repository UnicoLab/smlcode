package quality

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Harness-authored section headers.
//
// These are the EXACT markdown headers the harness appends to a worker's
// output. They are exported because three consumers have to agree on them
// character for character:
//
//   - the formatters in this package, which emit them;
//   - the strip helpers here and in pkg/loop, which remove them before the
//     model's own text is judged for completeness;
//   - the review gates, which look for FAILED inside them.
//
// Two of them used to be duplicated as literals in three places and were
// WRONG in two: pkg/quality/finalize.go and pkg/loop looked for
// "## Static quality" and "## Claims gate" while static.go/claims.go emit
// "## Static quality gate" and "## Claimed files gate". Nothing ever matched,
// so harness-authored gate markdown — including the literal word FAILED —
// stayed glued onto the model's finalize text when completeness was judged,
// and an empty finalize looked non-empty. One definition, one strip list.
const (
	// SmokeSectionHeader heads the deterministic post-worker smoke section.
	SmokeSectionHeader = "## Deterministic smoke"
	// AcceptanceSectionHeader heads the acceptance-command smoke section.
	AcceptanceSectionHeader = "## Acceptance smoke"
	// CriteriaSectionHeader heads the per-criterion verification section
	// (see FormatCriteriaSection). It supersedes AcceptanceSectionHeader for
	// any task that carries structured plan.Criteria; the two never both run
	// for one task, so a reviewer never sees the same command twice.
	CriteriaSectionHeader = "## Acceptance criteria"
	// StaticSectionHeader heads the stub/placeholder gate (see FormatStaticSection).
	StaticSectionHeader = "## Static quality gate"
	// ClaimsSectionHeader heads the files_changed gate (see FormatClaimsSection).
	ClaimsSectionHeader = "## Claimed files gate"
	// DiskEvidenceHeader heads the harness-authored disk evidence section.
	// pkg/loop writes this one; it is declared here so every strip list can
	// iterate a single slice.
	DiskEvidenceHeader = "## Disk evidence"

	// SmokeFailedMarker / SmokePassedMarker are the verdict words the review
	// gates match inside the sections above.
	SmokeFailedMarker = "FAILED"
	SmokePassedMarker = "PASSED"
)

// HarnessSectionHeaders is every harness-appended section header, in the order
// they are normally appended. Strip lists MUST iterate this slice rather than
// re-listing literals — that duplication is exactly how two of them drifted.
var HarnessSectionHeaders = []string{
	DiskEvidenceHeader,
	SmokeSectionHeader,
	AcceptanceSectionHeader,
	CriteriaSectionHeader,
	StaticSectionHeader,
	ClaimsSectionHeader,
}

// StripHarnessSections removes every harness-appended evidence/gate section,
// leaving only the model's own text. It cuts at the FIRST header found so a
// section that itself quotes a later header cannot leave a tail behind.
func StripHarnessSections(s string) string {
	cut := -1
	for _, header := range HarnessSectionHeaders {
		if i := strings.Index(s, "\n"+header); i >= 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	// The genuine provenance stamp also ends the model's text, and it does so
	// for sections this list has not been taught yet — the header list is a
	// registry somebody must remember to update, while the stamp is minted by
	// the append itself. Never sectionStampRe: a model that could end this
	// region by writing its own stamp could hide the rest of its output from
	// every gate that reads it.
	if i := strings.Index(s, SectionStamp()); i >= 0 && (cut < 0 || i < cut) {
		cut = i
	}
	if cut >= 0 {
		s = s[:cut]
	}
	// The provenance stamp sits one line ABOVE the header it vouches for, so a
	// cut at the header leaves it dangling. It is an HTML comment either way,
	// but the model's own text should not carry the harness's bookkeeping.
	s = sectionStampRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// ── Forged-marker defense ─────────────────────────────────────────────────
//
// Everything above is a MARKER THE HARNESS MINTS and the review gates then
// read back as ground truth. The markers are plain markdown, and three
// untrusted sources can put plain markdown into the string the gates scan:
//
//   - the repository (a file the worker read, AGENTS.md, a SKILL.md, a commit
//     message, or the stdout of the project's own test suite);
//   - the model's own prose — the worker prompt literally lists these headers,
//     so a confused SLM copying the format is the common case, not the
//     exotic one;
//   - anything that later concatenates the two.
//
// The direction that matters is asymmetric. Forging a FAILURE only makes the
// harness stricter, so the *FailedInOutput predicates stay plain string
// matches — which is also what lets them survive a process restart, where the
// section was persisted to the board by an earlier run.
//
// Forging a PASS is the dangerous direction: SmokePassedInOutput suppresses
// both the RequireSmoke gate and the review-time smoke insurance run, so a
// single forged line turns "the project compiles" into "the model said so".
// The genuine section therefore carries a per-process nonce, and
// SmokePassedInOutput requires it.

// smokeNonce is a fresh random tag per harness process. It is unguessable by
// anything that has not seen this process's own output — which covers every
// repository-supplied string and any model text written before the harness
// emitted a section.
//
// Residual, stated plainly: the nonce travels inside the section text that is
// fed back to the model, so a model that deliberately parroted its own context
// could reproduce it. That is a strictly weaker adversary than the ones this
// defends against (a model that wants to lie about tests already controls its
// own tool calls), and closing it needs the verdict to travel out-of-band of
// the prompt entirely.
var smokeNonce = newSectionNonce()

func newSectionNonce() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// A predictable nonce is still better than none: it keeps repo content
		// and pre-existing model text from matching.
		return "nonceless"
	}
	return hex.EncodeToString(buf)
}

// smokePassStamp is appended to a genuine PASSED deterministic-smoke verdict.
func smokePassStamp() string {
	return fmt.Sprintf("<!-- slmcode:smoke-pass:%s -->", smokeNonce)
}

// harnessMarkerRe matches every marker an untrusted string could use to forge
// a harness verdict: the section headers, the tester's execution-evidence
// frames, and a runner exit line.
var harnessMarkerRe = regexp.MustCompile(
	`(?im)^[ \t]*(#{1,6}[ \t]*(?:Deterministic smoke|Acceptance smoke|Acceptance criteria|` +
		`Static quality gate|Claimed files gate|Disk evidence)|Observation:|exit error:|` +
		`exit[ _]?(?:code|status)[ \t]*[:=]?[ \t]*-?\d+)`)

// smokeStampRe strips any pre-existing (or forged) pass stamp.
var smokeStampRe = regexp.MustCompile(`<!--\s*slmcode:smoke-pass:[0-9a-zA-Z]*\s*-->`)

// sectionStampRe matches a harness-section provenance stamp with ANY nonce —
// genuine or forged. DefuseHarnessMarkers strips every match, so untrusted text
// cannot smuggle in a marker that makes the rest of itself look harness-minted.
var sectionStampRe = regexp.MustCompile(`<!--\s*slmcode:section:[0-9a-zA-Z]*\s*-->`)

// SectionStamp is the per-process provenance tag the harness puts in front of
// every section it appends to a model's output.
//
// It answers the question DefuseModelText has to ask and DefuseHarnessMarkers
// cannot: "is this `## Deterministic smoke` mine, or did the model write it?"
// Position is not the answer — pkg/orchestrator PREPENDS a smoke section to a
// tester finalize, and a task output accumulates several sections across
// passes, so "the tail is the harness's" is false in both directions. The
// nonce is unguessable to anything that has not seen this process's own output.
func SectionStamp() string {
	return fmt.Sprintf("<!-- slmcode:section:%s -->", smokeNonce)
}

// StampHarnessSection marks sec as harness-authored so a later defuse pass
// leaves its markers armed. Empty sections are returned unchanged.
func StampHarnessSection(sec string) string {
	if strings.TrimSpace(sec) == "" {
		return sec
	}
	return "\n" + SectionStamp() + sec
}

// DefuseModelText defuses harness markers in a task output that may ALREADY
// carry genuine harness sections.
//
// Everything outside a stamped section is model text (or repository text the
// model pasted, or the stdout of the project's own tests) and is defused;
// everything from the first genuine stamp on is the harness's own and keeps its
// authority. Without this the harness disarmed its own evidence every time it
// appended a second section.
func DefuseModelText(s string) string {
	if s == "" {
		return s
	}
	if i := strings.Index(s, SectionStamp()); i >= 0 {
		return DefuseHarnessMarkers(s[:i]) + s[i:]
	}
	return DefuseHarnessMarkers(s)
}

// DefuseHarnessMarkers neutralizes harness-minted markers in UNTRUSTED text —
// repository instructions, skill bodies, block descriptions, tool output, and
// model prose — so the text can be shown to a model or concatenated with real
// harness sections without ever being mistaken for one.
//
// It is deliberately lossy in one direction only: the words survive, their
// structural authority does not. `## Deterministic smoke` becomes
// `> (quoted) Deterministic smoke`, which reads the same to a human and to a
// model but matches no gate.
func DefuseHarnessMarkers(s string) string {
	if s == "" {
		return s
	}
	s = smokeStampRe.ReplaceAllString(s, "")
	s = sectionStampRe.ReplaceAllString(s, "")
	return harnessMarkerRe.ReplaceAllStringFunc(s, func(m string) string {
		trimmed := strings.TrimLeft(m, " \t")
		indent := m[:len(m)-len(trimmed)]
		trimmed = strings.TrimLeft(trimmed, "#")
		return indent + "> (quoted) " + strings.TrimSpace(trimmed)
	})
}
