package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Windows reserved device names (landmines on Windows; mistakes everywhere).
var reservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ReadTracker remembers paths successfully read or authored in this session.
// little-coder invariant: Edit requires a prior Read (or Write) of the same file.
type ReadTracker struct {
	mu    sync.Mutex
	paths map[string]bool
}

// NewReadTracker creates an empty session tracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{paths: map[string]bool{}}
}

// Mark records that path is known (read or authored).
func (t *ReadTracker) Mark(path string) {
	if t == nil || path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paths == nil {
		t.paths = map[string]bool{}
	}
	t.paths[filepath.Clean(path)] = true
}

// Has reports whether path was read/authored this session.
func (t *ReadTracker) Has(path string) bool {
	if t == nil {
		return true // disabled tracker → allow
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paths[filepath.Clean(path)]
}

// Clear resets the session (tests / new run).
func (t *ReadTracker) Clear() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paths = map[string]bool{}
}

// IsReservedDeviceName reports Windows reserved basenames (nul, con, com1…).
func IsReservedDeviceName(filePath string) bool {
	base := strings.ToLower(filepath.Base(filePath))
	stem := base
	if i := strings.IndexByte(base, '.'); i >= 0 {
		stem = base[:i]
	}
	return reservedDeviceNames[stem]
}

// NormalizeWritePath rewrites mistaken root-anchored bare filenames to cwd,
// and joins relative paths against root. Absolute paths with directories are kept.
func NormalizeWritePath(filePath, root string) (resolved string, rewrittenFrom string) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", ""
	}
	// "/foo.md" (single segment) → <root>/foo.md — models often invent root paths.
	if strings.HasPrefix(filePath, "/") && !strings.Contains(strings.TrimPrefix(filePath, "/"), "/") {
		rewritten := filepath.Join(root, strings.TrimPrefix(filePath, "/"))
		return filepath.Clean(rewritten), filePath
	}
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath), ""
	}
	return filepath.Clean(filepath.Join(root, filePath)), ""
}

// WriteRefuseReason builds the little-coder Edit recipe when Write hits an
// existing file the model has NOT read this session.
//
// The recipe deliberately offers the read-then-rewrite route as well: since
// checkOverwrite gained its read-this-session escape hatch, ws_write on a file
// that was read IS allowed, and it is the tool the `whole_file` edit-format arm
// (pkg/loop) instructs the worker to use. Telling the model ws_write can never
// touch an existing file would make that arm unusable by construction.
func WriteRefuseReason(path string) string {
	return fmt.Sprintf(
		"Write refused — %s already exists and has not been read in this session.\n\n"+
			"Two ways forward. To change PART of the file, use ws_edit:\n"+
			`  {"path": "%s", "old_str": "<exact text currently in the file>", "new_str": "<replacement>"}`+"\n"+
			"To replace the WHOLE file, ws_read it first and then repeat this ws_write with the "+
			"complete new content.\n\n"+
			"Either way ws_read comes first, so old_str (or the new content) matches the file "+
			"exactly — whitespace and indentation included. Include 2–3 surrounding lines to make "+
			"old_str unique. For multi-hunk changes prefer ws_patch. "+
			"Do NOT repeat this ws_write unchanged — it will be refused again.",
		path, path,
	)
}

// EditBeforeReadReason redirects the model to Read first.
func EditBeforeReadReason(path string) string {
	return fmt.Sprintf(
		"File must be read first before edit — %s has not been read in this session.\n\n"+
			"Call ws_read on %s to get the exact current text for old_str "+
			"(whitespace and indentation must match), then issue ws_edit / ws_patch. "+
			"Do NOT guess the file's contents.",
		path, path,
	)
}

// EditNotFoundReason guides recovery when old_str misses.
func EditNotFoundReason(path string) string {
	return fmt.Sprintf(
		"old_str not found in %s.\n\n"+
			"RECOVERY: ws_read the file to get the exact current content (whitespace often differs), "+
			"then retry ws_edit with the exact string. Include 2–3 surrounding lines if the match "+
			"is ambiguous. If the change is large enough that an anchor is not worth finding, "+
			"ws_write the complete file instead — allowed now that you have read it.",
		path,
	)
}

// CheckWriteDestination decides whether a path may be created via ws_write.
// truncates=true means the op replaces content (not append).
func CheckWriteDestination(absPath string, truncates bool) (refuse bool, reason string) {
	if IsReservedDeviceName(absPath) {
		return true, fmt.Sprintf(
			"Write refused — %s uses a reserved device name (nul/con/com/lpt). "+
				"Pick a real filename.",
			filepath.Base(absPath),
		)
	}
	if !truncates {
		return false, ""
	}
	st, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, ""
		}
		return true, err.Error()
	}
	if st.IsDir() {
		return true, fmt.Sprintf("Write refused — %s is a directory", absPath)
	}
	return true, WriteRefuseReason(absPath)
}
