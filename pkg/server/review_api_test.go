package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/permissions"
)

func TestReviewPendingListsDiffs(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	root := h.Config.Root
	mustWrite(t, filepath.Join(root, "a.go"), "package a\n\nfunc A() int { return 1 }\n")

	if _, err := permissions.RecordPending(h.Config.SlmDir(), "a.go", "write",
		"package a\n\nfunc A() int { return 2 }\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := permissions.RecordPending(h.Config.SlmDir(), "b.go", "write",
		"package a\n\nfunc B() {}\n"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/review/pending", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Count int             `json:"count"`
		Items []PendingChange `json:"items"`
		Stat  DiffStat        `json:"stat"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 2 {
		t.Fatalf("count=%d", out.Count)
	}
	byPath := map[string]PendingChange{}
	for _, it := range out.Items {
		byPath[it.Path] = it
	}
	a := byPath["a.go"]
	if a.IsNew {
		t.Fatal("a.go marked as new")
	}
	if !strings.Contains(a.Before, "return 1") || !strings.Contains(a.After, "return 2") {
		t.Fatalf("before/after wrong: %+v", a)
	}
	if a.Stat.Added != 1 || a.Stat.Removed != 1 {
		t.Fatalf("stat=%+v", a.Stat)
	}
	if len(a.Hunks) == 0 {
		t.Fatal("no hunks")
	}
	b := byPath["b.go"]
	if !b.IsNew || b.Before != "" {
		t.Fatalf("b.go should be a new file: %+v", b)
	}
	if out.Stat.Added != a.Stat.Added+b.Stat.Added {
		t.Fatalf("totals wrong: %+v", out.Stat)
	}
}

func TestReviewApplyAndReject(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	root := h.Config.Root
	mustWrite(t, filepath.Join(root, "a.go"), "old\n")

	applyPath, err := permissions.RecordPending(h.Config.SlmDir(), "a.go", "write", "new\n")
	if err != nil {
		t.Fatal(err)
	}
	rejectPath, err := permissions.RecordPending(h.Config.SlmDir(), "b.go", "write", "never\n")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"ids":["` + filepath.Base(applyPath) + `"]}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/apply", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "a.go"))
	if err != nil || string(got) != "new\n" {
		t.Fatalf("file not applied: %q %v", string(got), err)
	}
	if _, err := os.Stat(applyPath); !os.IsNotExist(err) {
		t.Fatal("applied patch stayed in the queue")
	}

	body = `{"ids":["` + filepath.Base(rejectPath) + `"]}`
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/reject", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("reject status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "b.go")); !os.IsNotExist(err) {
		t.Fatal("rejected change was written")
	}
	if _, err := os.Stat(rejectPath); !os.IsNotExist(err) {
		t.Fatal("rejected patch stayed in the queue")
	}
	if n := s.pendingCount(); n != 0 {
		t.Fatalf("pending=%d", n)
	}
}

func TestReviewApplyAllAndPathEscape(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)

	if _, err := permissions.RecordPending(h.Config.SlmDir(), "x/y.go", "write", "ok\n"); err != nil {
		t.Fatal(err)
	}
	// A hand-crafted malicious patch must not escape the workspace.
	evil := filepath.Join(h.Config.SlmDir(), "pending", "1_write_evil.patch.json")
	mustWrite(t, evil, `{"path":"../../escaped.txt","kind":"write","content":"pwned"}`)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/apply", strings.NewReader(`{"all":true}`)))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK      bool     `json:"ok"`
		Applied []string `json:"applied"`
		Failed  []struct {
			Error string `json:"error"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "x/y.go" {
		t.Fatalf("applied=%v", out.Applied)
	}
	if len(out.Failed) != 1 || !strings.Contains(out.Failed[0].Error, "escapes") {
		t.Fatalf("escape not rejected: %+v", out.Failed)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(h.Config.Root)), "escaped.txt")); err == nil {
		t.Fatal("escaped write happened")
	}
}

func TestReviewRejectsInvalidID(t *testing.T) {
	s := New(newHarness(t), nil)
	for _, id := range []string{"../../etc/passwd", "nope", "a/b.patch.json"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/reject",
			strings.NewReader(`{"ids":["`+id+`"]}`)))
		var out struct {
			OK bool `json:"ok"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.OK {
			t.Fatalf("id %q accepted", id)
		}
	}
}

func TestReviewApplyBlockedWhileRunning(t *testing.T) {
	s := New(newHarness(t), nil)
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/review/apply", strings.NewReader(`{"all":true}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d", rec.Code)
	}
}
