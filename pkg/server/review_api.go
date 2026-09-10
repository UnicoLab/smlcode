package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// ── Pending review queue (permission mode "review") ──
//
// When Config.Permission is "review", every proposed file write is recorded as
// .slmcode/pending/<nano>_<kind>_<mangled-path>.patch.json holding
// {path, kind, content}. Until now only `slmcode apply` could act on that
// queue; Studio had no UI at all. These endpoints expose it as reviewable
// diffs with per-file apply/reject.

// PendingChange is one queued file change with both sides of the diff.
type PendingChange struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
	// From is the source path of a ws_mv proposal; empty for other kinds.
	From string `json:"from,omitempty"`
	// TaskID / Agent / QueryID attribute the proposal to the board task, the
	// role and the query that produced it, when the tool call carried them.
	TaskID    string     `json:"task_id,omitempty"`
	Agent     string     `json:"agent,omitempty"`
	QueryID   string     `json:"query_id,omitempty"`
	Exists    bool       `json:"exists"`
	IsNew     bool       `json:"is_new"`
	Before    string     `json:"before"`
	After     string     `json:"after"`
	Bytes     int        `json:"bytes"`
	Stat      DiffStat   `json:"stat"`
	Hunks     []DiffHunk `json:"hunks,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// pendingFile is the on-disk queue entry. path/kind/content come from
// permissions.RecordPending; the rest is stamped by pkg/workspace at record
// time (workspace.PendingStamp) and is absent from older entries.
type pendingFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	From    string `json:"from,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Agent   string `json:"agent,omitempty"`
	QueryID string `json:"query_id,omitempty"`
}

// Queue kinds. write/edit/patch all mean "the file's next content is
// Content"; delete and mv are the two that must NOT be applied as a write —
// doing so truncated a "deleted" file to zero bytes and left a moved file's
// source in place. shell entries were never files at all (an older workspace
// mirrored every shell ask into the queue); they are hidden and never applied.
const (
	pendingKindDelete = "delete"
	pendingKindMove   = "mv"
	pendingKindShell  = "shell"
)

// pendingKindApplicable reports whether a queue kind is something apply can
// act on.
func pendingKindApplicable(kind string) bool {
	return kind != pendingKindShell
}

func (s *Server) pendingDir() string {
	return filepath.Join(s.slmDir(), "pending")
}

// pendingIDPath validates a queue id (a bare file name) and returns its path.
func (s *Server) pendingIDPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return "", errors.New("invalid change id")
	}
	if !strings.HasSuffix(id, ".patch.json") {
		return "", errors.New("invalid change id")
	}
	return filepath.Join(s.pendingDir(), id), nil
}

// pendingTargetAllowed is the single rule for what a review-queue entry may
// name: inside the workspace, and not harness control state. Both the diff
// endpoint (which READS the target) and apply (which WRITES it) go through it,
// so the display path and the write path can never disagree.
func (s *Server) pendingTargetAllowed(rel string) error {
	target, err := s.workspacePath(rel)
	if err != nil {
		return ErrPathEscape
	}
	inRoot, rerr := filepath.Rel(s.realRootDir(), target)
	if rerr != nil {
		return ErrPathEscape
	}
	return workspace.CheckHarnessStateWrite(inRoot)
}

// readPending loads and diffs one queue entry.
func (s *Server) readPending(id string, withHunks bool, context int) (PendingChange, error) {
	full, err := s.pendingIDPath(id)
	if err != nil {
		return PendingChange{}, err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return PendingChange{}, err
	}
	var pf pendingFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return PendingChange{ID: id, Error: "unreadable patch: " + err.Error()}, nil
	}
	if strings.TrimSpace(pf.Path) == "" {
		return PendingChange{ID: id, Error: "patch has no path"}, nil
	}

	ch := PendingChange{
		ID:      id,
		Path:    filepath.ToSlash(pf.Path),
		Kind:    pf.Kind,
		From:    filepath.ToSlash(pf.From),
		TaskID:  pf.TaskID,
		Agent:   pf.Agent,
		QueryID: pf.QueryID,
		After:   pf.Content,
		Bytes:   len(pf.Content),
		Exists:  true,
	}
	if info, err := os.Stat(full); err == nil {
		ch.CreatedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	if ts := leadingNanos(id); ts > 0 {
		ch.CreatedAt = time.Unix(0, ts).UTC().Format(time.RFC3339)
	}
	if !pendingKindApplicable(pf.Kind) {
		ch.Error = "not a file change (kind " + pf.Kind + ") — reject it"
		ch.After = ""
		ch.Bytes = 0
		return ch, nil
	}
	if pf.Kind == pendingKindMove {
		if strings.TrimSpace(pf.From) == "" {
			ch.Error = "move entry has no source path"
			return ch, nil
		}
		if herr := s.pendingTargetAllowed(pf.From); herr != nil {
			ch.Error = "source: " + herr.Error()
			ch.Exists = false
			ch.After = ""
			ch.Bytes = 0
			return ch, nil
		}
	}

	// A queue entry is a FILE in `.slmcode/pending/`, which means a cloned
	// repository can ship one — nothing here proves the harness wrote it. An
	// entry naming .slmcode/auth.json used to make this endpoint render the
	// operator's provider API keys as the "before" side of a diff, and an entry
	// naming .slmcode/hooks.json turned the approve button into a one-click
	// arbitrary-bash install. applyPending already refuses to WRITE those
	// paths; refusing to READ or display them closes the other half.
	if herr := s.pendingTargetAllowed(pf.Path); herr != nil {
		ch.Error = herr.Error()
		ch.Exists = false
		ch.After = ""
		ch.Bytes = 0
		return ch, nil
	}
	target, err := s.workspacePath(pf.Path)
	if err != nil {
		ch.Error = "target path escapes workspace root"
		return ch, nil
	}
	before, err := os.ReadFile(target)
	if err != nil {
		if !os.IsNotExist(err) {
			ch.Error = err.Error()
		}
		ch.IsNew = true
		ch.Exists = false
	} else {
		ch.Before = string(before)
	}
	switch pf.Kind {
	case pendingKindDelete:
		// The diff of a delete is "everything removed"; the recorded content
		// is empty by construction.
		ch.After = ""
		ch.Bytes = 0
		ch.IsNew = false
	case pendingKindMove:
		// Rendered at the DESTINATION path: before is what the source holds
		// now (the destination normally does not exist yet), after is the
		// content that will land there.
		if src, err := s.workspacePath(pf.From); err == nil {
			if data, rerr := os.ReadFile(src); rerr == nil {
				ch.Before = string(data)
			} else if os.IsNotExist(rerr) && ch.Before == "" {
				ch.Error = "source " + filepath.ToSlash(pf.From) + " no longer exists; the recorded content will be written instead"
			}
		}
	}

	d := ComputeDiff(ch.Before, ch.After, context)
	ch.Stat = d.Stat
	ch.Truncated = d.Truncated
	if withHunks {
		ch.Hunks = d.Hunks
	}
	return ch, nil
}

func leadingNanos(name string) int64 {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0
	}
	n, err := strconv.ParseInt(name[:idx], 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func (s *Server) listPendingIDs() ([]string, error) {
	entries, err := os.ReadDir(s.pendingDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch.json") {
			continue
		}
		ids = append(ids, e.Name())
	}
	sort.Strings(ids) // nano-prefixed names sort chronologically
	return ids, nil
}

// handleReviewPending — GET /api/review/pending?hunks=1&context=3
func (s *Server) handleReviewPending(w http.ResponseWriter, r *http.Request) {
	ids, err := s.listPendingIDs()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	withHunks := boolParam(r, "hunks", true)
	context := intParam(r, "context", 3)

	items := make([]PendingChange, 0, len(ids))
	added, removed := 0, 0
	for _, id := range ids {
		ch, err := s.readPending(id, withHunks, context)
		if err != nil {
			continue
		}
		if !pendingKindApplicable(ch.Kind) {
			// Stale shell mirrors from an older workspace: not a change.
			continue
		}
		added += ch.Stat.Added
		removed += ch.Stat.Removed
		items = append(items, ch)
	}
	writeJSON(w, map[string]any{
		"count":      len(items),
		"items":      items,
		"dir":        s.pendingDir(),
		"permission": s.permissionMode(),
		"stat":       DiffStat{Added: added, Removed: removed},
	})
}

// handleReviewChange — GET /api/review/pending/{id}
func (s *Server) handleReviewChange(w http.ResponseWriter, r *http.Request) {
	ch, err := s.readPending(r.PathValue("id"), true, intParam(r, "context", 3))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, ch)
}

type reviewActionRequest struct {
	IDs []string `json:"ids"`
	ID  string   `json:"id"`
	All bool     `json:"all"`
}

type reviewActionFailure struct {
	ID    string `json:"id"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error"`
}

func (s *Server) reviewTargets(r *http.Request) ([]string, error) {
	var req reviewActionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.All {
		ids, err := s.listPendingIDs()
		if err != nil {
			return nil, err
		}
		// "all" means every CHANGE; stale shell mirrors are hidden from the
		// listing and are skipped here for the same reason.
		out := ids[:0]
		for _, id := range ids {
			if ch, err := s.readPending(id, false, 0); err == nil && !pendingKindApplicable(ch.Kind) {
				continue
			}
			out = append(out, id)
		}
		return out, nil
	}
	ids := append([]string(nil), req.IDs...)
	if strings.TrimSpace(req.ID) != "" {
		ids = append(ids, req.ID)
	}
	if len(ids) == 0 {
		return nil, errors.New("ids required (or {\"all\": true})")
	}
	return ids, nil
}

// handleReviewApply — POST /api/review/apply {ids|id|all}
func (s *Server) handleReviewApply(w http.ResponseWriter, r *http.Request) {
	if s.rejectMutationWhileRunning(w) {
		return
	}
	ids, err := s.reviewTargets(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	applied := make([]string, 0, len(ids))
	failed := make([]reviewActionFailure, 0)
	for _, id := range ids {
		path, err := s.applyPending(id)
		if err != nil {
			failed = append(failed, reviewActionFailure{ID: id, Path: path, Error: err.Error()})
			continue
		}
		applied = append(applied, path)
	}
	if len(applied) > 0 {
		s.emit(orchestrator.Event{
			Phase: "review", Kind: "file_change", Level: "success",
			Message: fmt.Sprintf("applied %d pending change(s) from Studio", len(applied)),
			Output:  strings.Join(applied, "\n"), Time: time.Now(),
		})
		s.emitReviewPending()
	}
	writeJSON(w, map[string]any{
		"ok": len(failed) == 0, "applied": applied, "failed": failed,
		"remaining": s.pendingCount(),
	})
}

// handleReviewReject — POST /api/review/reject {ids|id|all}
func (s *Server) handleReviewReject(w http.ResponseWriter, r *http.Request) {
	ids, err := s.reviewTargets(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rejected := make([]string, 0, len(ids))
	failed := make([]reviewActionFailure, 0)
	for _, id := range ids {
		full, err := s.pendingIDPath(id)
		if err != nil {
			failed = append(failed, reviewActionFailure{ID: id, Error: err.Error()})
			continue
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			failed = append(failed, reviewActionFailure{ID: id, Error: err.Error()})
			continue
		}
		rejected = append(rejected, id)
	}
	if len(rejected) > 0 {
		s.emit(orchestrator.Event{
			Phase: "review", Kind: "output", Level: "warning",
			Message: fmt.Sprintf("rejected %d pending change(s) from Studio", len(rejected)),
			Time:    time.Now(),
		})
		s.emitReviewPending()
	}
	writeJSON(w, map[string]any{
		"ok": len(failed) == 0, "rejected": rejected, "failed": failed,
		"remaining": s.pendingCount(),
	})
}

// applyPending writes one queued change into the workspace and removes it from
// the queue. The destination is re-validated against the workspace root, so a
// malicious or buggy patch cannot write outside the project.
func (s *Server) applyPending(id string) (string, error) {
	full, err := s.pendingIDPath(id)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	var pf pendingFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return "", fmt.Errorf("unreadable patch: %w", err)
	}
	if strings.TrimSpace(pf.Path) == "" {
		return "", errors.New("patch has no path")
	}
	target, err := s.workspacePath(pf.Path)
	if err != nil {
		return pf.Path, ErrPathEscape
	}
	// The queue entry is a file on disk; approving a diff in the UI must not be
	// a way to land .slmcode/hooks.json or .slmcode/config.yaml (arbitrary bash
	// on the next run, or a switch that turns the guards off). The tool layer
	// already refuses those writes — this is the second, independent check on
	// the path that actually reaches os.WriteFile, and the same rule the diff
	// endpoint applies before it reads anything.
	if herr := s.pendingTargetAllowed(pf.Path); herr != nil {
		return pf.Path, herr
	}
	// The KIND decides what "apply" means. Every kind used to be applied as
	// "write Content to Path": a delete entry (Content "") truncated the file
	// instead of removing it, a move entry copied the destination and left the
	// source behind, and a shell entry wrote shell.sh at the project root.
	switch pf.Kind {
	case pendingKindShell:
		return pf.Path, errors.New("shell approvals are not file changes and cannot be applied; reject the entry")
	case pendingKindDelete:
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return pf.Path, err
		}
	case pendingKindMove:
		if strings.TrimSpace(pf.From) == "" {
			return pf.Path, errors.New("move entry has no source path")
		}
		if herr := s.pendingTargetAllowed(pf.From); herr != nil {
			return pf.Path, fmt.Errorf("source: %w", herr)
		}
		src, err := s.workspacePath(pf.From)
		if err != nil {
			return pf.Path, ErrPathEscape
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // directory in the user's source tree — conventional 0755, not harness state
			return pf.Path, err
		}
		if err := movePendingFile(src, target, pf.Content); err != nil {
			return pf.Path, err
		}
	default:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // directory in the user's source tree — conventional 0755, not harness state
			return pf.Path, err
		}
		// target is a project source file (re-validated above to stay inside
		// the workspace) — conventional perms, not secret state.
		if err := os.WriteFile(target, []byte(pf.Content), 0o644); err != nil { //nolint:gosec // project source file, conventional perms
			return pf.Path, err
		}
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return pf.Path, err
	}
	return filepath.ToSlash(pf.Path), nil
}

// movePendingFile realises a queued ws_mv: rename when the source still
// exists, else write the recorded content (the source was moved or deleted by
// something else in the meantime, and the proposal's intent is the content at
// the destination).
func movePendingFile(src, dst, content string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination %s already exists", filepath.Base(dst))
	}
	if _, err := os.Lstat(src); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(dst, []byte(content), 0o644) //nolint:gosec // project source file, conventional perms
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device or otherwise un-renamable: copy, verify, then remove.
	data, err := os.ReadFile(src) //nolint:gosec // src re-validated against the workspace root by the caller
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // project source file, conventional perms
		return err
	}
	if st, err := os.Stat(dst); err != nil || st.Size() != int64(len(data)) {
		_ = os.Remove(dst)
		return errors.New("copy of the source was incomplete; source left in place")
	}
	return os.Remove(src)
}

// pendingCount is the number of applicable queue entries (shell mirrors from
// an older workspace are not counted; the listing hides them too).
func (s *Server) pendingCount() int {
	ids, _ := s.listPendingIDs()
	n := 0
	for _, id := range ids {
		full, err := s.pendingIDPath(id)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(full) //nolint:gosec // queue file under the harness state dir
		if err != nil {
			continue
		}
		var pf pendingFile
		if json.Unmarshal(raw, &pf) == nil && !pendingKindApplicable(pf.Kind) {
			continue
		}
		n++
	}
	return n
}

// emitReviewPending publishes the queue depth as a `review_pending` event
// ({"pending": N}) so Studio's badge follows the queue without polling. It is
// called after every apply/reject and, through the workspace hook installed in
// NewWithOptions, after every proposal a running agent records.
func (s *Server) emitReviewPending() {
	n := s.pendingCount()
	s.emit(orchestrator.Event{
		Phase: "review", Kind: "review_pending", Level: "info",
		Message: fmt.Sprintf("%d pending review change(s)", n),
		Data:    map[string]any{"pending": n},
		Time:    time.Now(),
	})
}

func boolParam(r *http.Request, key string, def bool) bool {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func intParam(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	if n > 50 {
		n = 50
	}
	return n
}
