package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeQueueEntry writes a raw review-queue entry, as pkg/workspace records
// (and stamps) it, with a controllable timestamp prefix for ordering.
func writeQueueEntry(t *testing.T, slmDir string, nanos int64, obj map[string]any) string {
	t.Helper()
	dir := filepath.Join(slmDir, "pending")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	kind, _ := obj["kind"].(string)
	name := strings.ReplaceAll(time.Unix(0, nanos).UTC().Format("20060102150405.000000000"), ".", "")
	id := name + "_" + kind + "_x.patch.json"
	if err := os.WriteFile(filepath.Join(dir, id), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

func applyAll(t *testing.T, s *Server) (applied []string, failed []reviewActionFailure) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/apply", strings.NewReader(`{"all":true}`)))
	if rec.Code != 200 {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Applied []string              `json:"applied"`
		Failed  []reviewActionFailure `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Applied, out.Failed
}

func lastEventOfKind(s *Server, kind string) (map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].Event.Kind == kind {
			data, _ := s.events[i].Event.Data.(map[string]any)
			return data, true
		}
	}
	return nil, false
}

// A "delete" entry must remove the file — applying it as a write truncated
// the file to zero bytes and reported success.
func TestReviewApplyDeleteRemovesFile(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	root := h.Config.Root
	mustWrite(t, filepath.Join(root, "gone.go"), "package a\n")
	writeQueueEntry(t, h.Config.SlmDir(), 1, map[string]any{"path": "gone.go", "kind": "delete", "content": ""})

	// The listing renders it as everything-removed, not as a 0-byte write.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/review/pending", nil))
	var listing struct {
		Items []PendingChange `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)
	if len(listing.Items) != 1 || listing.Items[0].Kind != "delete" || listing.Items[0].IsNew || listing.Items[0].Stat.Removed != 1 {
		t.Fatalf("listing: %+v", listing.Items)
	}

	applied, failed := applyAll(t, s)
	if len(failed) != 0 || len(applied) != 1 {
		t.Fatalf("applied=%v failed=%+v", applied, failed)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.go")); !os.IsNotExist(err) {
		t.Fatalf("file not removed (err=%v)", err)
	}
}

// An "mv" entry moves the source; applying it as a write copied the content
// to the destination and left the source in place.
func TestReviewApplyMoveMovesSource(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	root := h.Config.Root
	mustWrite(t, filepath.Join(root, "old", "a.go"), "package a\n")
	writeQueueEntry(t, h.Config.SlmDir(), 1, map[string]any{
		"path": "new/a.go", "kind": "mv", "content": "package a\n", "from": "old/a.go",
		"task_id": "T2", "agent": "worker", "query_id": "run-7",
	})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/review/pending", nil))
	var listing struct {
		Items []PendingChange `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)
	if len(listing.Items) != 1 {
		t.Fatalf("listing: %+v", listing.Items)
	}
	it := listing.Items[0]
	if it.From != "old/a.go" || it.TaskID != "T2" || it.Agent != "worker" || it.QueryID != "run-7" {
		t.Fatalf("stamps not exposed: %+v", it)
	}
	if it.Before != "package a\n" || it.Stat.Added != 0 || it.Stat.Removed != 0 {
		t.Fatalf("move should diff against its source (unchanged content): %+v", it)
	}

	applied, failed := applyAll(t, s)
	if len(failed) != 0 || len(applied) != 1 {
		t.Fatalf("applied=%v failed=%+v", applied, failed)
	}
	if _, err := os.Stat(filepath.Join(root, "old", "a.go")); !os.IsNotExist(err) {
		t.Fatalf("source left behind (err=%v)", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "new", "a.go"))
	if err != nil || string(got) != "package a\n" {
		t.Fatalf("destination: %q err=%v", got, err)
	}
	// A move whose source escapes the workspace is refused, not performed.
	writeQueueEntry(t, h.Config.SlmDir(), 2, map[string]any{
		"path": "stolen.txt", "kind": "mv", "content": "x", "from": "../../etc/hostname",
	})
	_, failed = applyAll(t, s)
	if len(failed) != 1 {
		t.Fatalf("escaping move not refused: %+v", failed)
	}
}

// A stale "shell" mirror is never a file: hidden from the listing, skipped by
// {all:true}, refused by id.
func TestReviewShellEntriesAreNeverApplied(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	root := h.Config.Root
	id := writeQueueEntry(t, h.Config.SlmDir(), 1, map[string]any{"path": "shell.sh", "kind": "shell", "content": "rm -rf /"})
	writeQueueEntry(t, h.Config.SlmDir(), 2, map[string]any{"path": "b.go", "kind": "write", "content": "package b\n"})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/review/pending", nil))
	var listing struct {
		Count int             `json:"count"`
		Items []PendingChange `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)
	if listing.Count != 1 || listing.Items[0].Path != "b.go" {
		t.Fatalf("shell entry visible in listing: %+v", listing.Items)
	}

	applied, failed := applyAll(t, s)
	if len(applied) != 1 || len(failed) != 0 {
		t.Fatalf("all: applied=%v failed=%+v", applied, failed)
	}
	if _, err := os.Stat(filepath.Join(root, "shell.sh")); err == nil {
		t.Fatal("shell.sh was written to the project root")
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/apply", strings.NewReader(`{"id":"`+id+`"}`)))
	var out struct {
		OK     bool                  `json:"ok"`
		Failed []reviewActionFailure `json:"failed"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.OK || len(out.Failed) != 1 || !strings.Contains(out.Failed[0].Error, "shell") {
		t.Fatalf("shell entry applied by id: %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "shell.sh")); err == nil {
		t.Fatal("shell.sh was written to the project root by id")
	}
}

// Apply and reject both publish the queue depth as a review_pending event.
func TestReviewPendingEventOnApplyAndReject(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	writeQueueEntry(t, h.Config.SlmDir(), 1, map[string]any{"path": "a.go", "kind": "write", "content": "package a\n"})
	id2 := writeQueueEntry(t, h.Config.SlmDir(), 2, map[string]any{"path": "b.go", "kind": "write", "content": "package b\n"})
	writeQueueEntry(t, h.Config.SlmDir(), 3, map[string]any{"path": "c.go", "kind": "write", "content": "package c\n"})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/reject", strings.NewReader(`{"id":"`+id2+`"}`)))
	data, ok := lastEventOfKind(s, "review_pending")
	if !ok || data["pending"] != 2 {
		t.Fatalf("after reject: event=%v ok=%v", data, ok)
	}
	applyAll(t, s)
	data, ok = lastEventOfKind(s, "review_pending")
	if !ok || data["pending"] != 0 {
		t.Fatalf("after apply: event=%v ok=%v", data, ok)
	}
	// The event is in the replay buffer like any other.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/runs/latest", nil))
	if !strings.Contains(rec.Body.String(), `"kind": "review_pending"`) {
		t.Fatalf("review_pending not replayed: %s", rec.Body.String())
	}
}
