package quality

import "strings"

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
	if cut >= 0 {
		s = s[:cut]
	}
	return strings.TrimSpace(s)
}
