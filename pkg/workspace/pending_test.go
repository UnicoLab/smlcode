package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/hitl"
)

func reviewWS(t *testing.T) (*Workspace, string, string) {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	slm := filepath.Join(root, ".slmcode")
	ws, _, err := NewWorkspace(root, ToolOpts{
		Permission: "review", ShellPermission: "ask", SlmDir: slm,
		DisableSyntaxCheck: true, DisableReadBeforeEdit: true, ShellAskTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws, root, slm
}

func readQueue(t *testing.T, slm string) []map[string]any {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(slm, "pending"))
	var out []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".patch.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(slm, "pending", e.Name())) //nolint:gosec // test-owned path
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		obj["_file"] = e.Name()
		out = append(out, obj)
	}
	return out
}

func TestReviewQueueEntriesAreStamped(t *testing.T) {
	ws, root, slm := reviewWS(t)
	if err := os.MkdirAll(slm, 0o750); err != nil {
		t.Fatal(err)
	}
	// The live board mirror is where the query id comes from.
	if err := os.WriteFile(filepath.Join(slm, "board.json"), []byte(`{"query_id":"run-42","tasks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var hooked []string
	remove := AddPendingHook(func(path, kind, file string) {
		mu.Lock()
		hooked = append(hooked, kind+":"+path)
		mu.Unlock()
	})
	defer remove()

	ctx := WithRole(WithTaskID(context.Background(), "T3"), "worker")
	if out := strOut(ws.writeFile(ctx, map[string]interface{}{"path": "new.go", "content": "package b\n"})); !strings.HasPrefix(out, "review: staged new.go") {
		t.Fatalf("write in review mode: %q", out)
	}
	if out := strOut(ws.moveFile(ctx, map[string]interface{}{"from": "old.go", "to": "moved.go"})); !strings.HasPrefix(out, "review: staged moved.go") {
		t.Fatalf("mv in review mode: %q", out)
	}
	if out := strOut(ws.deleteFile(ctx, map[string]interface{}{"path": "old.go"})); !strings.HasPrefix(out, "review: staged old.go") {
		t.Fatalf("delete in review mode: %q", out)
	}
	// Nothing was touched on disk.
	if _, err := os.Stat(filepath.Join(root, "new.go")); err == nil {
		t.Fatal("review mode wrote new.go")
	}
	if _, err := os.Stat(filepath.Join(root, "old.go")); err != nil {
		t.Fatal("review mode removed old.go")
	}

	q := readQueue(t, slm)
	if len(q) != 3 {
		t.Fatalf("queue has %d entries, want 3", len(q))
	}
	byKind := map[string]map[string]any{}
	for _, e := range q {
		byKind[e["kind"].(string)] = e
	}
	for _, kind := range []string{"write", "mv", "delete"} {
		e := byKind[kind]
		if e == nil {
			t.Fatalf("no %s entry", kind)
		}
		if e["task_id"] != "T3" || e["agent"] != "worker" || e["query_id"] != "run-42" {
			t.Errorf("%s entry not stamped: %v", kind, e)
		}
	}
	if byKind["mv"]["from"] != "old.go" || byKind["mv"]["path"] != "moved.go" || byKind["mv"]["content"] != "package a\n" {
		t.Errorf("mv entry: %v", byKind["mv"])
	}
	if byKind["delete"]["content"] != "" || byKind["delete"]["path"] != "old.go" {
		t.Errorf("delete entry: %v", byKind["delete"])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hooked) != 3 || hooked[0] != "write:new.go" || hooked[1] != "mv:moved.go" || hooked[2] != "delete:old.go" {
		t.Errorf("hooks saw %v", hooked)
	}
}

func TestShellAskIsNotAQueueEntry(t *testing.T) {
	ws, _, slm := reviewWS(t)
	ws.OnShellAsk = func(ask ShellAsk) {
		// Approve from "the UI" through the per-ask answer file.
		go func() {
			_ = hitl.WriteAnswerIDOnce(slm, "shell", ask.ID, ShellAnswer{AskID: ask.ID, Decision: "approve"})
		}()
	}
	ok, err := ws.waitShellApproval(context.Background(), "echo hi")
	if err != nil || !ok {
		t.Fatalf("approval: ok=%v err=%v", ok, err)
	}
	if q := readQueue(t, slm); len(q) != 0 {
		t.Fatalf("a shell ask landed in the review queue: %v", q)
	}
	if ids, _ := hitl.ListAskIDs(slm, "shell"); len(ids) != 0 {
		t.Fatalf("answered ask not cleared: %v", ids)
	}
}

// Two workers asking at once each get their own decision (the single-slot
// ask.json used to make the second ask erase the first).
func TestParallelShellAsksAreIndependent(t *testing.T) {
	ws, _, slm := reviewWS(t)
	decisions := map[string]string{"go test ./a": "approve", "rm -rf build": "deny"}
	ws.OnShellAsk = func(ask ShellAsk) {
		d := decisions[ask.Command]
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = hitl.WriteAnswerIDOnce(slm, "shell", ask.ID, ShellAnswer{AskID: ask.ID, Decision: d})
		}()
	}
	var wg sync.WaitGroup
	got := map[string]bool{}
	var mu sync.Mutex
	for cmd := range decisions {
		wg.Add(1)
		go func(cmd string) {
			defer wg.Done()
			ok, err := ws.waitShellApproval(context.Background(), cmd)
			if err != nil {
				t.Errorf("%s: %v", cmd, err)
			}
			mu.Lock()
			got[cmd] = ok
			mu.Unlock()
		}(cmd)
	}
	wg.Wait()
	if !got["go test ./a"] || got["rm -rf build"] {
		t.Fatalf("decisions crossed: %v", got)
	}
}
