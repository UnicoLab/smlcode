package plan

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixedNow is an injectable clock so age-based behavior is deterministic.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func openTestAttempts(t *testing.T, root string, now time.Time) *Attempts {
	t.Helper()
	s, err := OpenAttemptsWith(root, AttemptOptions{Now: fixedNow(now)})
	if err != nil {
		t.Fatalf("OpenAttempts: %v", err)
	}
	return s
}

// appendChain writes n attempts at taskID, each pointing at the previous one.
func appendChain(t *testing.T, s *Attempts, runID, taskID string, n int, base time.Time) []Attempt {
	t.Helper()
	var (
		out    []Attempt
		parent string
	)
	for i := 1; i <= n; i++ {
		a, err := s.Append(Attempt{
			RunID: runID, TaskID: taskID, N: i, ParentID: parent,
			Role: RoleWorker, Verdict: AttemptRejected,
			Hypothesis: "approach " + string(rune('A'+i-1)),
			Issues:     []string{"issue " + string(rune('A'+i-1))},
			At:         base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		parent = a.ID
		out = append(out, a)
	}
	return out
}

func TestAttemptLineageReconstructsRootToLeafOrder(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, t.TempDir(), base)
	want := appendChain(t, s, "run-1", "T1", 4, base)

	got := s.Lineage("T1")
	if len(got) != 4 {
		t.Fatalf("lineage depth = %d, want 4: %+v", len(got), got)
	}
	for i, a := range got {
		if a.ID != want[i].ID {
			t.Fatalf("lineage[%d] = %s, want %s (order must be root→leaf)", i, a.ID, want[i].ID)
		}
		if a.N != i+1 {
			t.Fatalf("lineage[%d].N = %d, want %d", i, a.N, i+1)
		}
	}
	if got[0].ParentID != "" {
		t.Fatalf("root attempt has a parent: %q", got[0].ParentID)
	}
	for i := 1; i < len(got); i++ {
		if got[i].ParentID != got[i-1].ID {
			t.Fatalf("attempt %d parent = %q, want %q", i+1, got[i].ParentID, got[i-1].ID)
		}
	}
}

func TestAttemptLineageSurvivesReopenAndPicksNewestLeaf(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, root, base)
	chain := appendChain(t, s, "run-1", "T1", 3, base)
	// A branch off attempt 2 that is OLDER than the tip: the tip must win.
	if _, err := s.Append(Attempt{
		RunID: "run-1", TaskID: "T1", N: 9, ParentID: chain[1].ID,
		Verdict: AttemptRejected, Issues: []string{"stale branch"},
		At: base.Add(30 * time.Second),
	}); err != nil {
		t.Fatalf("append branch: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := openTestAttempts(t, root, base)
	got := reopened.Lineage("T1")
	if len(got) != 3 {
		t.Fatalf("lineage after reopen = %d records, want 3: %+v", len(got), got)
	}
	if got[2].ID != chain[2].ID {
		t.Fatalf("leaf = %s, want %s", got[2].ID, chain[2].ID)
	}
	if kids := reopened.Children(chain[1].ID); len(kids) != 2 {
		t.Fatalf("children of %s = %d, want 2", chain[1].ID, len(kids))
	}
	if leaves := reopened.Leaves("run-1"); len(leaves) != 2 {
		t.Fatalf("leaves = %d, want 2 (tip + stale branch)", len(leaves))
	}
	if all := reopened.ForTask("run-1", "T1"); len(all) != 4 {
		t.Fatalf("ForTask = %d, want 4", len(all))
	}
	if other := reopened.ForTask("run-2", "T1"); other != nil {
		t.Fatalf("ForTask on another run returned %d records", len(other))
	}
}

func TestAttemptStoreSkipsCorruptLineAndKeepsNeighbors(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, root, base)
	appendChain(t, s, "run-1", "T1", 2, base)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	logPath := filepath.Join(root, slmStateDirName, AttemptsDirName, AttemptsLogFileName)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2", len(lines))
	}
	// A truncated record wedged BETWEEN two good ones.
	spliced := lines[0] + "\n{\"id\":\"broken\",\"task_id\"\n" + lines[1] + "\n"
	if err := os.WriteFile(logPath, []byte(spliced), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	// Drop the index so the store has to rebuild from the damaged log.
	if err := os.Remove(filepath.Join(root, slmStateDirName, AttemptsDirName, AttemptsIndexFileName)); err != nil {
		t.Fatalf("remove index: %v", err)
	}

	reopened := openTestAttempts(t, root, base)
	if n := reopened.Count(); n != 2 {
		t.Fatalf("count after corrupt line = %d, want 2 (records either side survive)", n)
	}
	warns := strings.Join(reopened.Warnings(), " | ")
	if !strings.Contains(warns, "corrupt attempt record") {
		t.Fatalf("no corruption warning: %q", warns)
	}
	lineage := reopened.Lineage("T1")
	if len(lineage) != 2 {
		t.Fatalf("lineage = %d, want 2", len(lineage))
	}
}

func TestAttemptStoreQuarantinesUnreadableIndex(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, root, base)
	appendChain(t, s, "run-1", "T1", 2, base)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	idxPath := filepath.Join(root, slmStateDirName, AttemptsDirName, AttemptsIndexFileName)
	if err := os.WriteFile(idxPath, []byte("this is not json"), 0o600); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}
	reopened := openTestAttempts(t, root, base)
	if n := reopened.Count(); n != 2 {
		t.Fatalf("count after corrupt index = %d, want 2", n)
	}
	if _, err := os.Stat(idxPath + ".corrupt"); err != nil {
		t.Fatalf("corrupt index not quarantined: %v", err)
	}
}

func TestAttemptPruneRespectsCapAndAge(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, root, now)

	// Three old attempts and three fresh ones, all on distinct tasks so the
	// prune is not confused by a chain.
	for i := 1; i <= 3; i++ {
		if _, err := s.Append(Attempt{
			RunID: "run-1", TaskID: "old" + strconv.Itoa(i), N: 1,
			Verdict: AttemptRejected, Issues: []string{"old"},
			At: now.Add(-200 * 24 * time.Hour).Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("append old: %v", err)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.Append(Attempt{
			RunID: "run-1", TaskID: "new" + strconv.Itoa(i), N: 1,
			Verdict: AttemptRejected, Issues: []string{"new"},
			At: now.Add(-time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("append new: %v", err)
		}
	}
	if n := s.Count(); n != 6 {
		t.Fatalf("count = %d, want 6", n)
	}

	// Age first: the default 180d window drops exactly the three old ones.
	if removed := s.Prune(AttemptPrunePolicy{MaxAttempts: -1}); removed != 3 {
		t.Fatalf("age prune removed %d, want 3", removed)
	}
	if n := s.Count(); n != 3 {
		t.Fatalf("count after age prune = %d, want 3", n)
	}
	// Then the cap.
	if removed := s.Prune(AttemptPrunePolicy{MaxAttempts: 2, MaxAge: -1}); removed != 1 {
		t.Fatalf("cap prune removed %d, want 1", removed)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The log was rewritten, not merely re-indexed: a fresh open sees two.
	reopened := openTestAttempts(t, root, now)
	if n := reopened.Count(); n != 2 {
		t.Fatalf("count after reopen = %d, want 2", n)
	}
	for _, a := range reopened.All() {
		if !strings.HasPrefix(a.TaskID, "new") {
			t.Fatalf("prune kept the wrong record: %s", a.TaskID)
		}
		if a.Issues == nil {
			t.Fatalf("prune rewrote %s without its payload", a.ID)
		}
	}
}

func TestForgetAttemptsIsSupported(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s := openTestAttempts(t, root, base)
	appendChain(t, s, "run-1", "T1", 2, base)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := ForgetAttempts(root); err != nil {
		t.Fatalf("ForgetAttempts: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, slmStateDirName, AttemptsDirName)); !os.IsNotExist(err) {
		t.Fatalf("attempts dir survived Forget: %v", err)
	}
	reopened := openTestAttempts(t, root, base)
	if n := reopened.Count(); n != 0 {
		t.Fatalf("count after rm -rf = %d, want 0", n)
	}
	if _, err := reopened.Append(Attempt{RunID: "run-1", TaskID: "T1", N: 1, Verdict: AttemptRejected}); err != nil {
		t.Fatalf("append after Forget: %v", err)
	}
}

func TestAttemptNormalizeCapsAndDerivesID(t *testing.T) {
	a := Attempt{
		TaskID:     "T7",
		Output:     strings.Repeat("x", MaxAttemptOutputLen*3),
		Hypothesis: "line one\nline two",
		Issues:     []string{"same", "same", " same ", "other"},
		Score:      -4,
	}
	a.Normalize(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	if len(a.Output) != MaxAttemptOutputLen {
		t.Fatalf("output len = %d, want %d", len(a.Output), MaxAttemptOutputLen)
	}
	if strings.Contains(a.Hypothesis, "\n") {
		t.Fatalf("hypothesis kept a newline: %q", a.Hypothesis)
	}
	if len(a.Issues) != 2 {
		t.Fatalf("issues = %v, want 2 after dedupe", a.Issues)
	}
	if a.RunID != UnknownRunID {
		t.Fatalf("run id = %q, want %q", a.RunID, UnknownRunID)
	}
	if a.ID != AttemptID(UnknownRunID, "T7", 1) {
		t.Fatalf("id = %q", a.ID)
	}
	if a.Verdict != AttemptRejected {
		t.Fatalf("verdict = %q, want %q (issues imply a rejection)", a.Verdict, AttemptRejected)
	}
	if a.Score != 0 {
		t.Fatalf("score = %v, want 0", a.Score)
	}
	if (Attempt{}).Validate() == nil {
		t.Fatal("an attempt with no task id must not validate")
	}
}

func TestDeriveHypothesisPrefersFinalizeSummaryAndNeverFabricates(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		motivating []string
		want       string
	}{{
		name:   "finalize summary wins",
		output: "chatter\n```json\n{\"status\":\"done\",\"summary\":\"widen the ws_patch anchor\"}\n```",
		want:   "widen the ws_patch anchor",
	}, {
		name:   "first prose line when there is no summary",
		output: "Observation: ws_shell exit status 1\nRewrote parser.go to drop the stub\n{\"a\":1}",
		want:   "Rewrote parser.go to drop the stub",
	}, {
		name:       "falls back to the issue that motivated the pass",
		output:     "{}",
		motivating: []string{"stub/placeholder code detected"},
		want:       "address review issue: stub/placeholder code detected",
	}, {
		name:   "nothing derivable stays empty rather than invented",
		output: "```\n{}\n```",
		want:   "",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveHypothesis(tc.output, tc.motivating)
			if got != tc.want {
				t.Fatalf("DeriveHypothesis = %q, want %q", got, tc.want)
			}
			if again := DeriveHypothesis(tc.output, tc.motivating); again != got {
				t.Fatalf("not deterministic: %q then %q", got, again)
			}
		})
	}
}

func TestRejectedApproachSectionRendersReasonsOncePerReason(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	lineage := []Attempt{
		{N: 1, Verdict: AttemptRejected, Hypothesis: "rewrite parser.go wholesale",
			Issues: []string{"stub/placeholder code detected"}, At: at},
		{N: 2, Verdict: AttemptRejected, Hypothesis: "rewrite parser.go wholesale again",
			Issues: []string{"Stub/placeholder code detected."}, At: at.Add(time.Minute)},
		{N: 3, Verdict: AttemptEscalated, Hypothesis: "delete the failing test",
			Issues: []string{"deterministic smoke failed"}, At: at.Add(2 * time.Minute)},
	}
	for i := range lineage {
		lineage[i].Normalize(at)
	}

	groups := RejectedApproaches(lineage)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (same reason renders once): %+v", len(groups), groups)
	}
	if len(groups[1].Attempts) != 2 {
		t.Fatalf("merged group covers attempts %v, want both 1 and 2", groups[1].Attempts)
	}
	if groups[1].Attempts[0] != 1 || groups[1].Attempts[1] != 2 {
		t.Fatalf("merged attempts out of order: %v", groups[1].Attempts)
	}

	// Case and punctuation are not a new rejection, so the two spellings of the
	// stub complaint must collapse into a single rendered reason.
	section := RejectedApproachSection(lineage, maxTestSectionBytes)
	lower := strings.ToLower(section)
	if strings.Count(lower, "stub/placeholder code detected") != 1 {
		t.Fatalf("duplicate reason rendered more than once:\n%s", section)
	}
	if !strings.Contains(section, "delete the failing test") ||
		!strings.Contains(section, "deterministic smoke failed") {
		t.Fatalf("most recent rejection missing:\n%s", section)
	}
	if !strings.Contains(section, "rewrite parser.go wholesale") {
		t.Fatalf("approach text missing:\n%s", section)
	}
	// Newest first: the smoke rejection must precede the stub rejection.
	if strings.Index(lower, "deterministic smoke failed") > strings.Index(lower, "stub/placeholder") {
		t.Fatalf("rejections are not newest-first:\n%s", section)
	}
}

func TestRejectedApproachSectionRespectsByteBudget(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	var lineage []Attempt
	for i := 1; i <= 50; i++ {
		a := Attempt{
			N: i, Verdict: AttemptRejected,
			Hypothesis: strings.Repeat("hypothesis ", 60) + strconv.Itoa(i),
			Issues:     []string{strings.Repeat("reason ", 60) + strconv.Itoa(i)},
			At:         at.Add(time.Duration(i) * time.Minute),
		}
		a.Normalize(at)
		lineage = append(lineage, a)
	}
	for _, budget := range []int{0, 1, 120, 400, maxTestSectionBytes} {
		got := RejectedApproachSection(lineage, budget)
		if len(got) > budget {
			t.Fatalf("budget %d produced %d bytes", budget, len(got))
		}
	}
	full := RejectedApproachSection(lineage, maxTestSectionBytes)
	if strings.Count(full, "\n- ") > MaxRenderedApproaches {
		t.Fatalf("rendered more than %d approaches:\n%s", MaxRenderedApproaches, full)
	}
	if !strings.Contains(full, "50") {
		t.Fatalf("most recent rejection was dropped in favor of older ones:\n%s", full)
	}
}

func TestRejectedApproachSectionIgnoresApprovedAndReasonlessAttempts(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	lineage := []Attempt{
		{N: 1, Verdict: AttemptApproved, Hypothesis: "did the thing", Score: 90, At: at},
		{N: 2, Verdict: AttemptRejected, Hypothesis: "no reason recorded", At: at.Add(time.Minute)},
	}
	for i := range lineage {
		lineage[i].Normalize(at)
	}
	if got := RejectedApproachSection(lineage, maxTestSectionBytes); got != "" {
		t.Fatalf("section rendered with nothing to say:\n%s", got)
	}
	// A recorded failure class is reason enough.
	lineage[1].FailureClass = "review_rejected"
	got := RejectedApproachSection(lineage, maxTestSectionBytes)
	if !strings.Contains(got, "review_rejected") {
		t.Fatalf("failure class not used as a reason:\n%s", got)
	}
	if strings.Contains(got, "did the thing") {
		t.Fatalf("an APPROVED attempt was rendered as a rejection:\n%s", got)
	}
}

func TestAttemptsInMemoryStoreIsFullyUsable(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s, err := OpenAttemptsWith("", AttemptOptions{Now: fixedNow(base)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.Dir() != "" {
		t.Fatalf("in-memory store has dir %q", s.Dir())
	}
	appendChain(t, s, "run-1", "T1", 3, base)
	got := s.Lineage("T1")
	if len(got) != 3 || got[2].Hypothesis == "" {
		t.Fatalf("in-memory lineage = %+v", got)
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func TestAttemptsReadOnlyStoreNeverWrites(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	s, err := OpenAttemptsWith(root, AttemptOptions{ReadOnly: true, Now: fixedNow(base)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Append(Attempt{RunID: "run-1", TaskID: "T1", N: 1, Verdict: AttemptRejected}); err != nil {
		t.Fatalf("read-only append: %v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("read-only store recorded %d attempts", s.Count())
	}
	if _, err := os.Stat(filepath.Join(root, slmStateDirName, AttemptsDirName)); !os.IsNotExist(err) {
		t.Fatalf("read-only open created the store dir: %v", err)
	}
}

// maxTestSectionBytes mirrors the loop's prompt budget for the rejected-approach
// block. Kept local so this package's tests do not depend on pkg/loop.
const maxTestSectionBytes = 1200
