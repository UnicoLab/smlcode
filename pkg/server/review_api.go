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
	ID        string     `json:"id"`
	Path      string     `json:"path"`
	Kind      string     `json:"kind"`
	CreatedAt string     `json:"created_at"`
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

type pendingFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
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
		ID:     id,
		Path:   filepath.ToSlash(pf.Path),
		Kind:   pf.Kind,
		After:  pf.Content,
		Bytes:  len(pf.Content),
		Exists: true,
	}
	if info, err := os.Stat(full); err == nil {
		ch.CreatedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	if ts := leadingNanos(id); ts > 0 {
		ch.CreatedAt = time.Unix(0, ts).UTC().Format(time.RFC3339)
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
		return s.listPendingIDs()
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return pf.Path, err
	}
	// target is a project source file (re-validated above to stay inside the
	// workspace) — conventional perms, not secret state.
	if err := os.WriteFile(target, []byte(pf.Content), 0o644); err != nil { //nolint:gosec // project source file, conventional perms
		return pf.Path, err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return pf.Path, err
	}
	return filepath.ToSlash(pf.Path), nil
}

func (s *Server) pendingCount() int {
	ids, _ := s.listPendingIDs()
	return len(ids)
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
