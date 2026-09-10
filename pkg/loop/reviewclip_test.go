package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// TestReviewerAlwaysSeesHarnessEvidenceEvenWhenProseIsHuge is the regression
// guard for the worst defect found by live testing.
//
// The reviewer's rules are written in terms of harness-appended sections
// ("approve if ... Disk evidence section shows changed files", "reject if
// ## Deterministic smoke shows FAILED"). runGates appends those sections to the
// END of Task.Output. The prompt used to clip Output head-first, so a verbose
// worker pushed the evidence past the cut — and the reviewer, told to judge on
// evidence it could no longer see, fell through to "reject if output is only
// claims" and rejected correct, test-passing code. Observed live: seven
// consecutive score-0 rejections over ~22 minutes on an implementation whose
// tests all passed.
func TestReviewerAlwaysSeesHarnessEvidenceEvenWhenProseIsHuge(t *testing.T) {
	prose := strings.Repeat("The worker reasons at length about the change. ", 400) // ~19 KB
	evidence := "## Disk evidence\nchanged: stats.go\n\n" +
		"## Deterministic smoke\nPASSED cmd: go test ./...\n"
	out := prose + "\n\n" + evidence

	if len(out) <= 3500 {
		t.Fatal("fixture is too small to exercise truncation")
	}
	// The bug, stated as an assertion: a head-first clip loses the evidence.
	if strings.Contains(truncate(out, 3500), "## Disk evidence") {
		t.Fatal("fixture no longer reproduces the defect — head clip kept the evidence")
	}

	got := clipForReview(out, 3500)
	for _, want := range []string{"## Disk evidence", "changed: stats.go", "## Deterministic smoke", "PASSED"} {
		if !strings.Contains(got, want) {
			t.Errorf("review prompt lost %q — the reviewer cannot apply a rule about evidence it cannot see", want)
		}
	}
	if !strings.Contains(got, "The worker reasons") {
		t.Error("review prompt dropped the worker answer entirely; the reviewer still needs the claim")
	}
}

// TestCorrectorAlwaysSeesHarnessEvidenceEvenWhenProseIsHuge mirrors the
// reviewer guard above for the CORRECTOR. formatCorrectPrompt clipped the
// previous output head-first at 2.5 KB while runGates appends the failing
// command's section and observation to the END of it — so a verbose worker
// meant the corrector was told to fix a smoke failure it was never shown. The
// prompt must carry the FAILED evidence, the focus files it is scoped to, the
// acceptance and the project language.
func TestCorrectorAlwaysSeesHarnessEvidenceEvenWhenProseIsHuge(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prose := strings.Repeat("The worker reasons at length about the change. ", 400) // ~19 KB
	failed := quality.SmokeSectionHeader + "\n" + quality.SmokeFailedMarker + " cmd: go test ./...\n" +
		"--- FAIL: TestMedian (0.00s)\n    stats_test.go:12: got 2, want 3\n"
	out := prose + "\n\n" + failed
	if strings.Contains(truncate(out, correctorOutputBudget), quality.SmokeFailedMarker) {
		t.Fatal("fixture no longer reproduces the defect — head clip kept the evidence")
	}

	r := NewRunner(nil, nil)
	r.Root = root
	task := plan.Task{
		ID: "T1", Title: "Implement Median", Role: plan.RoleWorker,
		Description: "Implement Median in stats.go.", Acceptance: "go test ./... passes",
		Files:  []string{"pkg/stats/stats.go", "pkg/stats/stats_test.go"},
		Output: out,
	}
	got := r.formatCorrectPrompt(task, plan.ReviewResult{
		Summary: "rejected: deterministic smoke failed",
		Issues:  []string{"deterministic smoke command failed — corrector must fix"},
	})
	for _, want := range []string{
		quality.SmokeSectionHeader, quality.SmokeFailedMarker, "--- FAIL: TestMedian", "got 2, want 3",
		"## Focus files (HARD SCOPE)", "pkg/stats/stats.go", "pkg/stats/stats_test.go",
		"## Acceptance\ngo test ./... passes", "## Project language\nProject language: Go.",
		"## Review issues\n- deterministic smoke command failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("corrector prompt lost %q — it cannot fix a failure it cannot see", want)
		}
	}
	if !strings.Contains(got, "The worker reasons") {
		t.Error("corrector prompt dropped the previous answer entirely")
	}
}

// TestClipForReviewLeavesShortOutputAlone pins the no-regression case: when
// everything fits, the reviewer sees exactly what it saw before.
func TestClipForReviewLeavesShortOutputAlone(t *testing.T) {
	out := "done: implemented Median\n\n## Disk evidence\nchanged: stats.go\n"
	if got, want := clipForReview(out, 3500), strings.TrimSpace(out); got != want {
		t.Errorf("clipForReview mangled a short output:\n got %q\nwant %q", got, want)
	}
}

// TestClipForReviewWithNoEvidenceSectionsIsPlainTruncation keeps the behavior
// identical for outputs the harness never annotated.
func TestClipForReviewWithNoEvidenceSectionsIsPlainTruncation(t *testing.T) {
	out := strings.Repeat("x", 9000)
	got := clipForReview(out, 3500)
	if want := truncate(out, 3500); got != want {
		t.Errorf("clipForReview(len=%d) = len %d, want plain truncate len %d", len(out), len(got), len(want))
	}
}

// TestReviewPromptCarriesEvidenceEndToEnd exercises the real prompt builder
// rather than the helper, so a future refactor that stops calling clipForReview
// is caught here.
func TestReviewPromptCarriesEvidenceEndToEnd(t *testing.T) {
	r := &Runner{}
	task := plan.Task{
		ID: "T1", Title: "Implement Median", Role: plan.RoleWorker,
		Acceptance: "go test ./... passes",
		Output: strings.Repeat("verbose reasoning about the approach. ", 300) +
			"\n\n" + quality.DiskEvidenceHeader + "\nchanged: stats.go\n",
	}
	prompt := r.formatReviewPrompt(task)
	if !strings.Contains(prompt, quality.DiskEvidenceHeader) {
		t.Fatalf("review prompt does not contain %q; the reviewer is told to approve on it", quality.DiskEvidenceHeader)
	}
	if !strings.Contains(prompt, "changed: stats.go") {
		t.Error("review prompt contains the evidence header but not its content")
	}
}
