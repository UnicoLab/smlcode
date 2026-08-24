package autoresearch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// On-disk layout. Everything this package writes lives under one directory, so
// `rm -rf .slmcode/autoresearch` is a complete, supported reset.
const (
	// DirName is the subdirectory of .slmcode this package owns.
	DirName = "autoresearch"
	// TrialsFile is the append-only trial log.
	TrialsFile = "trials.jsonl"
	// BestFile is the human-readable summary of what was retained.
	BestFile = "BEST.md"
)

// Bounds. An append-only log that is never compacted is an unbounded log.
const (
	// MaxTrials is how many trial records the journal keeps.
	MaxTrials = 2000
	// MaxLineLen is the longest JSONL line that will be parsed. A longer one is
	// treated as corrupt rather than buffered without limit.
	MaxLineLen = 64 * 1024
	// MaxJournalReason caps the reason text stored per trial.
	MaxJournalReason = 400
	// MaxJournalValue caps the before/after values stored per trial. A rewritten
	// system prompt is thousands of characters and the journal is a log, not a
	// backup — the snapshot is the backup.
	MaxJournalValue = 600
)

// Trial is one experiment: what was changed, what it scored, and what happened
// to it. One JSON object per line in trials.jsonl.
type Trial struct {
	Seq  int       `json:"seq"`
	At   time.Time `json:"at"`
	Seed int64     `json:"seed"`

	KnobID string `json:"knob_id"`
	Before string `json:"before"`
	After  string `json:"after"`
	Origin string `json:"origin,omitempty"`

	// Baseline is what this trial was measured against — the champion at the
	// time. Storing it makes each line self-contained: a reader does not have
	// to replay the file to know what "better" meant here.
	Baseline Score `json:"baseline"`
	Score    Score `json:"score"`

	Kept   bool   `json:"kept"`
	Reason string `json:"reason,omitempty"`
	// Guard names the guarded metric that vetoed the change, when one did.
	// This is the field that distinguishes "did not help" from "helped, but
	// paid for it somewhere you said not to".
	Guard string `json:"guard,omitempty"`

	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// Normalize bounds every free-text field before the record is written.
func (t *Trial) Normalize(now time.Time) {
	if t.At.IsZero() {
		t.At = now
	}
	t.At = t.At.UTC().Truncate(time.Second)
	t.KnobID = strings.TrimSpace(t.KnobID)
	t.Origin = strings.TrimSpace(t.Origin)
	t.Before = clipRaw(t.Before, MaxJournalValue)
	t.After = clipRaw(t.After, MaxJournalValue)
	t.Reason = clipRaw(t.Reason, MaxJournalReason)
	t.Guard = clipRaw(t.Guard, 80)
	t.Error = clipRaw(t.Error, MaxJournalReason)
	if t.DurationMS < 0 {
		t.DurationMS = 0
	}
}

// Dir is the project's autoresearch directory.
func Dir(root string) string { return filepath.Join(root, ".slmcode", DirName) }

// TrialsPath is the project's trial log.
func TrialsPath(root string) string { return filepath.Join(Dir(root), TrialsFile) }

// BestPath is the project's retained-changes summary.
func BestPath(root string) string { return filepath.Join(Dir(root), BestFile) }

// Journal is the append-only trial log plus its human-readable summary.
type Journal struct {
	dir      string
	warnings []string
}

// OpenJournal prepares the journal for a project root. It creates nothing until
// something is written, so `--surface` and `--dry-run` leave no trace.
func OpenJournal(root string) *Journal { return &Journal{dir: Dir(root)} }

// Path is the trial log's path.
func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return filepath.Join(j.dir, TrialsFile)
}

// Append writes one trial.
//
// Append-only and one write(2) per record, like the rest of the harness's JSONL
// logs: on POSIX a sub-PIPE_BUF append is atomic, so a crashed run leaves whole
// records rather than a spliced one. This is the one write path in the package
// that does NOT go through atomicfile, and that is why.
func (j *Journal) Append(t Trial) error {
	if j == nil || j.dir == "" {
		return nil
	}
	t.Normalize(time.Now())
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(j.dir, 0o750); err != nil { // harness state dir, owner-only
		return err
	}
	f, err := os.OpenFile(j.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the caller's own project journal
	if err != nil {
		return err
	}
	_, wErr := f.Write(append(data, '\n'))
	return errors.Join(wErr, f.Close())
}

// Load reads every trial, oldest first.
//
// A corrupt line is skipped and counted, never fatal: a half-written record
// must not cost you the history either side of it. A missing file is an empty
// history and no error.
func (j *Journal) Load() ([]Trial, error) {
	if j == nil || j.dir == "" {
		return nil, nil
	}
	f, err := os.Open(j.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		out     []Trial
		corrupt int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineLen)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var t Trial
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			corrupt++
			continue
		}
		out = append(out, t)
	}
	if err := sc.Err(); err != nil {
		// An over-long line stops the scan. Report it and keep what parsed.
		corrupt++
	}
	if corrupt > 0 {
		j.warnings = append(j.warnings,
			fmt.Sprintf("skipped %d corrupt trial record(s) in %s", corrupt, j.Path()))
	}
	return out, nil
}

// Prune rewrites the log keeping at most max of the most recent trials.
func (j *Journal) Prune(max int) (int, error) {
	if j == nil || j.dir == "" {
		return 0, nil
	}
	if max <= 0 {
		max = MaxTrials
	}
	all, err := j.Load()
	if err != nil {
		return 0, err
	}
	if len(all) <= max {
		return 0, nil
	}
	kept := all[len(all)-max:]
	var buf []byte
	for _, t := range kept {
		data, err := json.Marshal(t)
		if err != nil {
			continue
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if err := atomicfile.Write(j.Path(), buf, 0o600); err != nil {
		return 0, err
	}
	return len(all) - len(kept), nil
}

// Warnings reports non-fatal problems seen while reading the journal.
func (j *Journal) Warnings() []string {
	if j == nil {
		return nil
	}
	out := make([]string, len(j.warnings))
	copy(out, j.warnings)
	return out
}

// WriteBest renders BEST.md — what was retained, what was reverted and why, and
// the stated reason the run stopped.
//
// The "why it stopped" line is not decoration. A ratchet that spends its budget
// and reports only its best score is indistinguishable from one that converged,
// and those are very different results.
func (j *Journal) WriteBest(res Result) error {
	if j == nil || j.dir == "" {
		return nil
	}
	if err := os.MkdirAll(j.dir, 0o750); err != nil { // harness state dir, owner-only
		return err
	}
	return atomicfile.Write(filepath.Join(j.dir, BestFile), []byte(res.RenderBest()), 0o600)
}

// Reset deletes everything this package wrote. Equivalent to
// `rm -rf .slmcode/autoresearch`, and just as safe: the next run starts from
// the harness as it currently stands, with no memory of past experiments.
func Reset(root string) error { return os.RemoveAll(Dir(root)) }

// clipRaw truncates to n BYTES, ellipsis included, without splitting a rune.
//
// Both halves of that sentence are bugs waiting to happen: an ellipsis is three
// bytes in UTF-8, so the obvious `s[:n-1] + "…"` overshoots the cap it was
// written to enforce; and slicing at an arbitrary byte offset in a rewritten
// prompt produces invalid UTF-8, which then encodes as a replacement character
// in the journal.
func clipRaw(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	const ellipsis = "…"
	if n <= len(ellipsis) {
		return ""
	}
	cut := n - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}
