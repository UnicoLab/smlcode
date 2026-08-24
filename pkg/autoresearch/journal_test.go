package autoresearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalAppendAndLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	for i := 1; i <= 3; i++ {
		if err := j.Append(Trial{
			Seq: i, Seed: 7, KnobID: "config:think_passes",
			After: "2", Score: healthy(0.5), Kept: i == 2, Reason: "because",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := j.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d trials, want 3", len(got))
	}
	if !got[1].Kept || got[0].Kept {
		t.Errorf("kept flags did not round-trip: %v", []bool{got[0].Kept, got[1].Kept, got[2].Kept})
	}
	if got[0].Seed != 7 || got[0].KnobID != "config:think_passes" {
		t.Errorf("record did not round-trip: %+v", got[0])
	}
	if got[0].At.IsZero() {
		t.Error("Normalize did not stamp a time")
	}
}

// TestJournalSurvivesACorruptLine: a half-written record must cost that record
// and nothing else. A log that refuses to load because one line is truncated
// throws away every experiment on either side of it.
func TestJournalSurvivesACorruptLine(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	if err := j.Append(Trial{Seq: 1, KnobID: "a", After: "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Splice in exactly what a crash mid-write leaves behind.
	f, err := os.OpenFile(j.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(`{"seq":2,"knob_id":"b","aft` + "\n" + "not json at all\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := j.Append(Trial{Seq: 3, KnobID: "c", After: "3"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := j.Load()
	if err != nil {
		t.Fatalf("Load returned an error for a corrupt line: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d trials, want the 2 intact ones", len(got))
	}
	if got[0].KnobID != "a" || got[1].KnobID != "c" {
		t.Errorf("the records either side of the corruption were lost: %v", []string{got[0].KnobID, got[1].KnobID})
	}
	warnings := j.Warnings()
	if len(warnings) == 0 || !strings.Contains(warnings[0], "corrupt") {
		t.Errorf("corruption was skipped silently; warnings = %v", warnings)
	}
}

func TestJournalPruneRespectsTheCap(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	for i := 1; i <= 25; i++ {
		if err := j.Append(Trial{Seq: i, KnobID: "config:think_passes", After: "2"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	removed, err := j.Prune(10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 15 {
		t.Errorf("Prune removed %d, want 15", removed)
	}
	got, err := j.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("kept %d trials, want 10", len(got))
	}
	// The most recent ones are what survive.
	if got[0].Seq != 16 || got[9].Seq != 25 {
		t.Errorf("kept seq %d..%d, want 16..25", got[0].Seq, got[9].Seq)
	}
	// The file itself must shrink — an append-only log that is never compacted
	// is an unbounded log.
	info, err := os.Stat(j.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() > 10*512 {
		t.Errorf("pruned file is %d bytes — it was not rewritten", info.Size())
	}
	// Pruning again is a no-op.
	if removed, err := j.Prune(10); err != nil || removed != 0 {
		t.Errorf("second Prune removed %d (err %v), want 0", removed, err)
	}
}

func TestJournalNormalizeBoundsFreeText(t *testing.T) {
	tr := Trial{
		KnobID: "  agent:worker.system_prompt ",
		After:  strings.Repeat("x", MaxJournalValue*3),
		Reason: strings.Repeat("y", MaxJournalReason*3),
		Error:  strings.Repeat("z", MaxJournalReason*3),
	}
	tr.Normalize(time.Unix(0, 0))
	if len(tr.After) > MaxJournalValue {
		t.Errorf("After is %d bytes, past the %d cap", len(tr.After), MaxJournalValue)
	}
	if len(tr.Reason) > MaxJournalReason || len(tr.Error) > MaxJournalReason {
		t.Errorf("reason/error were not bounded: %d/%d", len(tr.Reason), len(tr.Error))
	}
	if tr.KnobID != "agent:worker.system_prompt" {
		t.Errorf("KnobID = %q, want it trimmed", tr.KnobID)
	}
}

func TestJournalAppendIsOneLinePerRecord(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	// A rewritten prompt has newlines in it; they must be JSON-escaped, not
	// written raw, or one record would parse as several corrupt ones.
	if err := j.Append(Trial{Seq: 1, KnobID: "agent:worker.system_prompt", After: "line one\nline two"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	body := readFile(t, j.Path())
	if strings.Count(strings.TrimRight(body, "\n"), "\n") != 0 {
		t.Fatalf("a multi-line value produced multiple lines:\n%q", body)
	}
	var tr Trial
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &tr); err != nil {
		t.Fatalf("the record does not parse: %v", err)
	}
	if tr.After != "line one\nline two" {
		t.Errorf("After = %q", tr.After)
	}
}

func TestWriteBestRecordsWhatWasKeptAndWhyItStopped(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	res := Result{
		Seed: 5, Baseline: healthy(0.5), Best: healthy(0.8),
		Kept:        []Change{{KnobID: "config:think_passes", Before: "1", After: "2"}},
		Experiments: 3,
		Trials: []Trial{
			{Seq: 1, KnobID: "config:think_passes", After: "2", Kept: true},
			{Seq: 2, KnobID: "agent:worker.max_tokens", After: "8192",
				Guard: "tokens per task", Reason: "reverted: tokens per task regressed"},
		},
		StopReason: StopExperiments,
		StopDetail: StopExperiments.Sentence(),
	}
	if err := j.WriteBest(res); err != nil {
		t.Fatalf("WriteBest: %v", err)
	}
	body := readFile(t, BestPath(root))
	for _, want := range []string{
		"config:think_passes",
		"Rejected by a guard",
		"tokens per task",
		"stopped because",
		"NOT exhausted",
		"--restore",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("BEST.md is missing %q:\n%s", want, body)
		}
	}
}

func TestResetRemovesEverythingThePackageWrote(t *testing.T) {
	root := t.TempDir()
	j := OpenJournal(root)
	if err := j.Append(Trial{Seq: 1, KnobID: "x"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.WriteBest(Result{StopReason: StopExhausted}); err != nil {
		t.Fatalf("WriteBest: %v", err)
	}
	snap, err := Capture([]string{filepath.Join(root, "nothing.yaml")})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := snap.Persist(SnapshotDir(root)); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	if err := Reset(root); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(Dir(root)); !os.IsNotExist(err) {
		t.Fatalf("%s still exists after Reset", Dir(root))
	}
	// And the package keeps working afterwards — a reset is not a wound.
	if got, err := OpenJournal(root).Load(); err != nil || len(got) != 0 {
		t.Errorf("post-reset Load = %v, %v; want empty and no error", got, err)
	}
}

func TestJournalPathsLiveUnderOneDirectory(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	for _, p := range []string{TrialsPath(root), BestPath(root), SnapshotDir(root)} {
		if !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
			t.Errorf("%s is outside %s — `rm -rf` would not be a complete reset", p, dir)
		}
	}
}
