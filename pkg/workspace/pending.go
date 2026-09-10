package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/permissions"
)

// Review-queue recording (permission mode "review").
//
// permissions.RecordPending writes the queue entry {path, kind, content}. The
// workspace knows more than that at the moment of the proposal — which task
// and role produced it, which query is running, and for a move where the file
// came FROM — and the appliers (Studio's review page, `slmcode apply`) need
// the extra fields to act on a kind correctly and to attribute the change.
// So the entry is stamped here, in place, right after it is recorded.

// PendingStamp is the attribution added to every review-queue entry.
type PendingStamp struct {
	// From is the source path of a ws_mv proposal ("" for other kinds).
	From string `json:"from,omitempty"`
	// TaskID is the board task whose tool call produced the proposal, from the
	// context tag pkg/loop sets per dispatch (WithTaskID). "" when untagged.
	TaskID string `json:"task_id,omitempty"`
	// Agent is the role that made the call (WithRole). "" when untagged.
	Agent string `json:"agent,omitempty"`
	// QueryID is the id of the running query, read from the live board
	// (.slmcode/board.json) at record time. "" when no board is live.
	QueryID string `json:"query_id,omitempty"`
}

// PendingHook observes a review-queue entry being recorded. Studio installs
// one to emit a `review_pending` event with the new queue depth.
type PendingHook func(path, kind, file string)

var (
	pendingHookMu sync.RWMutex
	pendingHooks  = map[int]PendingHook{}
	pendingHookID int
)

// AddPendingHook registers fn and returns a function that removes it again.
// Hooks run synchronously after the queue file exists on disk, never under a
// workspace lock.
func AddPendingHook(fn PendingHook) (remove func()) {
	if fn == nil {
		return func() {}
	}
	pendingHookMu.Lock()
	pendingHookID++
	id := pendingHookID
	pendingHooks[id] = fn
	pendingHookMu.Unlock()
	return func() {
		pendingHookMu.Lock()
		delete(pendingHooks, id)
		pendingHookMu.Unlock()
	}
}

func firePendingHooks(path, kind, file string) {
	pendingHookMu.RLock()
	hooks := make([]PendingHook, 0, len(pendingHooks))
	for _, h := range pendingHooks {
		hooks = append(hooks, h)
	}
	pendingHookMu.RUnlock()
	for _, h := range hooks {
		h(path, kind, file)
	}
}

// recordPending writes the queue entry, stamps it, and notifies hooks. It
// returns the queue file path.
func (w *Workspace) recordPending(ctx context.Context, path, kind, content, from string) (string, error) {
	file, err := permissions.RecordPending(w.SlmDir, path, kind, content)
	if err != nil {
		return "", err
	}
	stamp := PendingStamp{
		From:    from,
		TaskID:  TaskIDFrom(ctx),
		Agent:   RoleFrom(ctx),
		QueryID: liveQueryID(w.SlmDir),
	}
	// Best effort: the entry is complete without the stamp, so a failure to
	// stamp must not turn a recorded proposal into an error the model sees.
	_ = stampPendingFile(file, stamp)
	firePendingHooks(path, kind, filepath.Base(file))
	return file, nil
}

// stampPendingFile merges stamp into the JSON object in file.
func stampPendingFile(file string, stamp PendingStamp) error {
	raw, err := os.ReadFile(file) //nolint:gosec // path produced by permissions.RecordPending under SlmDir
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if stamp.From != "" {
		obj["from"] = stamp.From
	}
	if stamp.TaskID != "" {
		obj["task_id"] = stamp.TaskID
	}
	if stamp.Agent != "" {
		obj["agent"] = stamp.Agent
	}
	if stamp.QueryID != "" {
		obj["query_id"] = stamp.QueryID
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(file, append(out, '\n'), 0o644)
}

// liveQueryID reads query_id from the live board mirror, if there is one.
// Review mode only ever reaches this on a proposal, so one small read per
// proposal is the whole cost.
func liveQueryID(slmDir string) string {
	if slmDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(slmDir, "board.json")) //nolint:gosec // harness state under SlmDir
	if err != nil {
		return ""
	}
	var b struct {
		QueryID string `json:"query_id"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	return strings.TrimSpace(b.QueryID)
}
