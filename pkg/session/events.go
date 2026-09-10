package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EventRecord is one JSONL line under .slmcode/queries/<id>/events.jsonl.
type EventRecord struct {
	Time    string `json:"time"`
	Phase   string `json:"phase,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Agent   string `json:"agent,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Output  string `json:"output,omitempty"`
	Data    any    `json:"data,omitempty"`
	// CostUSD is optional per-event attribution (usually on usage kinds).
	CostUSD float64 `json:"cost_usd,omitempty"`
	Tokens  int     `json:"tokens,omitempty"`
	Model   string  `json:"model,omitempty"`
}

// EventsPath returns .slmcode/queries/<runID>/events.jsonl
func EventsPath(slmDir, runID string) string {
	return filepath.Join(TurnDir(slmDir, runID), "events.jsonl")
}

// ── Buffered per-run appender ─────────────────────────────────────────────
//
// AppendEvent used to open, write, fsync-maybe and close the log file for
// EVERY event under one global mutex — token deltas included, which arrive
// at decode speed and are by far the most numerous kind. A streaming worker
// therefore paid an open/close pair per token, serialized against every other
// worker's events. The log is now written through one open file per run with
// a bufio.Writer:
//
//   - token deltas are not persisted at all (they are a live-view concern; the
//     usage event that follows carries the numbers that matter afterwards);
//   - structural kinds flush immediately, so a crash loses at most the chatty
//     tail; run_end / run_stop flush, fsync and close;
//   - a timer flushes the chatty kinds within eventFlushInterval regardless;
//   - the turn's end (WriteTurnSummary) closes the appender; an appender idle
//     for eventIdleClose is closed by the timer so a run that never reports an
//     end does not pin a descriptor forever.
//
// ReadEvents flushes the run's appender first, so a reader (Studio replaying
// the current run) never observes a torn tail.

const (
	eventFlushInterval = 500 * time.Millisecond
	eventIdleClose     = 30 * time.Second
	eventBufferBytes   = 64 * 1024
)

// eventLogSkipKinds are never persisted to the JSONL log.
var eventLogSkipKinds = map[string]bool{"token": true, "delta": true}

// eventChattyKinds are buffered and flushed by the timer; everything else is
// structural and flushed on write.
var eventChattyKinds = map[string]bool{
	"output": true, "tool_call": true, "tool_result": true, "tool": true,
	"usage": true, "calibration": true, "progress": true,
}

type eventAppender struct {
	mu       sync.Mutex
	f        *os.File
	w        *bufio.Writer
	timer    *time.Timer
	lastUsed time.Time
	closed   bool
}

var (
	appendersMu sync.Mutex
	appenders   = map[string]*eventAppender{}
)

func appenderKey(slmDir, runID string) string { return slmDir + "\x00" + runID }

// appenderFor returns the open appender for the run, creating it on first use.
func appenderFor(slmDir, runID string) (*eventAppender, error) {
	key := appenderKey(slmDir, runID)
	appendersMu.Lock()
	defer appendersMu.Unlock()
	if a, ok := appenders[key]; ok && !a.closed {
		return a, nil
	}
	dir := TurnDir(slmDir, runID)
	// Session state is local to the invoking user; keep it out of reach of
	// other accounts on shared machines.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(EventsPath(slmDir, runID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	a := &eventAppender{f: f, w: bufio.NewWriterSize(f, eventBufferBytes), lastUsed: time.Now()}
	a.timer = time.AfterFunc(eventFlushInterval, func() { a.tick(key) })
	appenders[key] = a
	return a, nil
}

// tick is the timer callback: flush what is buffered, close when idle,
// otherwise re-arm.
func (a *eventAppender) tick(key string) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	_ = a.w.Flush()
	idle := time.Since(a.lastUsed) > eventIdleClose
	if idle {
		a.closeLocked(false)
		a.mu.Unlock()
		appendersMu.Lock()
		if cur, ok := appenders[key]; ok && cur == a {
			delete(appenders, key)
		}
		appendersMu.Unlock()
		return
	}
	a.timer.Reset(eventFlushInterval)
	a.mu.Unlock()
}

func (a *eventAppender) write(line []byte, structural bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return os.ErrClosed
	}
	a.lastUsed = time.Now()
	if _, err := a.w.Write(line); err != nil {
		return err
	}
	if structural {
		return a.w.Flush()
	}
	return nil
}

func (a *eventAppender) flushLocked() error { return a.w.Flush() }

// closeLocked flushes, optionally fsyncs, and closes the file.
func (a *eventAppender) closeLocked(sync bool) {
	if a.closed {
		return
	}
	_ = a.w.Flush()
	if sync {
		_ = a.f.Sync()
	}
	_ = a.f.Close()
	if a.timer != nil {
		a.timer.Stop()
	}
	a.closed = true
}

// AppendEvent appends one JSON line to the turn event log (best-effort).
func AppendEvent(slmDir, runID string, rec EventRecord) error {
	if slmDir == "" || runID == "" {
		return nil
	}
	if eventLogSkipKinds[rec.Kind] {
		return nil
	}
	if rec.Time == "" {
		rec.Time = time.Now().Format(time.RFC3339Nano)
	}
	rec.Message = truncateEvent(rec.Message, 2000)
	rec.Output = truncateEvent(rec.Output, 4000)
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	a, err := appenderFor(slmDir, runID)
	if err != nil {
		return err
	}
	structural := !eventChattyKinds[rec.Kind]
	if err := a.write(append(b, '\n'), structural); err != nil {
		if errors.Is(err, os.ErrClosed) {
			// Closed by the idle timer between lookup and write: reopen once.
			if a, err = appenderFor(slmDir, runID); err == nil {
				err = a.write(append(b, '\n'), structural)
			}
		}
		if err != nil {
			return err
		}
	}
	if shouldSyncEvent(rec) {
		CloseEventLog(slmDir, runID)
	}
	return nil
}

// shouldSyncEvent names the kinds after which the log must be durable and
// the appender can be released: the run is over (or paused for a human).
func shouldSyncEvent(rec EventRecord) bool {
	switch rec.Kind {
	case "run_end", "run_stop":
		return true
	default:
		return false
	}
}

// FlushEventLog flushes the run's buffered events to the OS (no fsync).
func FlushEventLog(slmDir, runID string) {
	appendersMu.Lock()
	a, ok := appenders[appenderKey(slmDir, runID)]
	appendersMu.Unlock()
	if !ok {
		return
	}
	a.mu.Lock()
	if !a.closed {
		_ = a.flushLocked()
	}
	a.mu.Unlock()
}

// FlushEventLogs flushes every open appender.
func FlushEventLogs() {
	appendersMu.Lock()
	all := make([]*eventAppender, 0, len(appenders))
	for _, a := range appenders {
		all = append(all, a)
	}
	appendersMu.Unlock()
	for _, a := range all {
		a.mu.Lock()
		if !a.closed {
			_ = a.flushLocked()
		}
		a.mu.Unlock()
	}
}

// CloseEventLog flushes, fsyncs and closes the run's appender. Called at the
// end of a turn; a later AppendEvent for the same run simply reopens.
func CloseEventLog(slmDir, runID string) {
	key := appenderKey(slmDir, runID)
	appendersMu.Lock()
	a, ok := appenders[key]
	if ok {
		delete(appenders, key)
	}
	appendersMu.Unlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.closeLocked(true)
	a.mu.Unlock()
}

// ReadEvents loads all event records (capped). The run's appender is flushed
// first so a live run's tail is visible.
func ReadEvents(slmDir, runID string, limit int) ([]EventRecord, error) {
	FlushEventLog(slmDir, runID)
	b, err := os.ReadFile(EventsPath(slmDir, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if limit <= 0 {
		limit = 5000
	}
	lines := strings.Split(string(b), "\n")
	var out []EventRecord
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec EventRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func truncateEvent(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
