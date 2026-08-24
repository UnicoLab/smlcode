package autoresearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Reversal is by file copy, deliberately NOT by git.
//
// The tempting implementation is `git stash` / `git checkout --` — three lines,
// already installed. It is also wrong here: slmcode has no git integration, the
// project directory belongs to the user, and a research loop that starts
// committing (or stashing, or resetting) somebody's working tree as a side
// effect of an experiment is a data-loss bug wearing a convenience costume. A
// user with uncommitted work, a dirty index, or no repository at all must be
// able to run this safely.
//
// So: copy the exact files the surface can write, copy them back on revert.
// Bounded by the surface, understandable in one sentence, and correct in a
// directory that has never seen `git init`.

// snapshotDirName is where the durable pre-run snapshot lives.
const snapshotDirName = "snapshot"

// MaxSnapshotFileBytes caps one snapshotted file. Agent YAMLs and config.yaml
// are kilobytes; anything past this is not a knob file and copying it would
// turn a bounded feature into an unbounded one.
const MaxSnapshotFileBytes = 1 << 20 // 1 MiB

// FileState is one file as it was before an experiment.
type FileState struct {
	// Path is the absolute path the bytes came from.
	Path string `json:"path"`
	// Existed is false when there was no file — restoring then means deleting
	// whatever the experiment created, not writing an empty file.
	Existed bool `json:"existed"`
	// Mode is the file's permission bits, so a restore is faithful in metadata
	// as well as content.
	Mode fs.FileMode `json:"mode"`
	// SHA256 is the content hash, so a restore can be verified.
	SHA256 string `json:"sha256,omitempty"`
	// Stored is the file's name inside a durable snapshot directory. Empty for
	// an in-memory snapshot.
	Stored string `json:"stored,omitempty"`

	data []byte
}

// Snapshot is a set of files captured before a mutation.
type Snapshot struct {
	// At is when the snapshot was taken.
	At time.Time `json:"at"`
	// Files is sorted by path, so a manifest diffs cleanly.
	Files []FileState `json:"files"`
	// Dir is the durable directory, empty for an in-memory snapshot.
	Dir string `json:"-"`
}

// Capture copies files into memory. This is the per-trial mechanism: the revert
// path must not itself be able to fail on a full disk.
func Capture(paths []string) (*Snapshot, error) {
	snap := &Snapshot{At: time.Now().UTC()}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		st, err := captureFile(p)
		if err != nil {
			return nil, err
		}
		snap.Files = append(snap.Files, st)
	}
	return snap, nil
}

func captureFile(path string) (FileState, error) {
	st := FileState{Path: path, Mode: defaultYAMLMode}
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.Size() > MaxSnapshotFileBytes {
			return st, fmt.Errorf("autoresearch: %s is %d bytes, past the %d-byte snapshot cap",
				path, info.Size(), MaxSnapshotFileBytes)
		}
		st.Mode = info.Mode().Perm()
	case os.IsNotExist(err):
		return st, nil // Existed stays false: restoring means removing
	default:
		return st, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	sum := sha256.Sum256(data)
	st.Existed = true
	st.data = data
	st.SHA256 = hex.EncodeToString(sum[:])
	return st, nil
}

// Restore puts every captured file back exactly as it was.
//
// It is written to be called from a defer, which means it must be safe to call
// on a half-applied state, twice, or after a panic. Errors are joined rather
// than returned on the first failure: restoring three of four files and
// reporting the fourth beats abandoning the other three.
func (s *Snapshot) Restore() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, f := range s.Files {
		if err := f.restore(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f FileState) restore() error {
	if !f.Existed {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data := f.data
	if data == nil && f.Stored != "" {
		var err error
		data, err = os.ReadFile(f.Stored)
		if err != nil {
			return err
		}
	}
	if data == nil {
		return fmt.Errorf("autoresearch: snapshot for %s has no content", f.Path)
	}
	mode := f.Mode
	if mode == 0 {
		mode = defaultYAMLMode
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o750); err != nil { // harness state dir, owner-only
		return err
	}
	return atomicfile.Write(f.Path, data, mode)
}

// Paths lists the snapshotted paths, sorted.
func (s *Snapshot) Paths() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, f.Path)
	}
	return out
}

// Persist writes the snapshot into dir so it survives the process.
//
// This is the answer to the failure the in-process defer cannot cover: SIGKILL,
// a power cut, a laptop lid. Those leave the files mutated with no Go code
// running to put them back, and the only thing that helps afterwards is a copy
// on disk plus `slmcode autoresearch --restore`.
func (s *Snapshot) Persist(dir string) error {
	if s == nil {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil { // harness state dir, owner-only
		return err
	}
	for i := range s.Files {
		f := &s.Files[i]
		if !f.Existed {
			f.Stored = ""
			continue
		}
		name := fmt.Sprintf("%03d-%s", i, filepath.Base(f.Path))
		stored := filepath.Join(dir, name)
		if err := atomicfile.Write(stored, f.data, defaultYAMLMode); err != nil {
			return err
		}
		f.Stored = stored
	}
	s.Dir = dir
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(manifestPath(dir), data, defaultYAMLMode)
}

// LoadSnapshot reads a persisted snapshot back.
//
// An unparseable manifest is moved aside to manifest.json.corrupt and reported,
// matching the rest of the harness: a store that panics on a bad file is worse
// than one that starts clean and says so.
func LoadSnapshot(dir string) (*Snapshot, error) {
	path := manifestPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		_ = os.Rename(path, path+".corrupt")
		return nil, fmt.Errorf("autoresearch: unreadable snapshot manifest (moved to %s.corrupt): %w", path, err)
	}
	snap.Dir = dir
	// A manifest whose stored copies were deleted underneath it cannot restore
	// anything; say so now rather than half-way through a restore.
	for _, f := range snap.Files {
		if !f.Existed {
			continue
		}
		if f.Stored == "" {
			return nil, fmt.Errorf("autoresearch: snapshot manifest lists %s with no stored copy", f.Path)
		}
		if _, err := os.Stat(f.Stored); err != nil {
			return nil, fmt.Errorf("autoresearch: snapshot copy missing for %s: %w", f.Path, err)
		}
	}
	return &snap, nil
}

// Remove deletes a persisted snapshot.
func (s *Snapshot) Remove() error {
	if s == nil || s.Dir == "" {
		return nil
	}
	return os.RemoveAll(s.Dir)
}

func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

// SnapshotDir is where a project's durable pre-run snapshot lives.
func SnapshotDir(root string) string { return filepath.Join(Dir(root), snapshotDirName) }

// Hashes returns path → sha256 for the captured files, for tests and for a
// `--restore` that wants to report what it changed.
func (s *Snapshot) Hashes() map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	for _, f := range s.Files {
		out[f.Path] = f.SHA256
	}
	return out
}

// HashFiles hashes the given paths as they are on disk right now. A missing
// file hashes to the empty string, which is distinguishable from any content.
func HashFiles(paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				out[p] = ""
				continue
			}
			return nil, err
		}
		sum := sha256.Sum256(data)
		out[p] = hex.EncodeToString(sum[:])
	}
	return out, nil
}
