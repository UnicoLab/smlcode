package session

import (
	"encoding/json"
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

var eventMu sync.Mutex

// EventsPath returns .slmcode/queries/<runID>/events.jsonl
func EventsPath(slmDir, runID string) string {
	return filepath.Join(TurnDir(slmDir, runID), "events.jsonl")
}

// AppendEvent appends one JSON line to the turn event log (best-effort).
func AppendEvent(slmDir, runID string, rec EventRecord) error {
	if slmDir == "" || runID == "" {
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
	eventMu.Lock()
	defer eventMu.Unlock()
	dir := TurnDir(slmDir, runID)
	// Session state is local to the invoking user; keep it out of reach of
	// other accounts on shared machines.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(EventsPath(slmDir, runID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// A close error here can't be surfaced to the caller once the write
	// below has already reported success/failure; append is best-effort.
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if shouldSyncEvent(rec) {
		return f.Sync()
	}
	return nil
}

func shouldSyncEvent(rec EventRecord) bool {
	switch rec.Kind {
	case "ask_answered", "run_start", "run_end", "run_stop":
		return true
	default:
		return false
	}
}

// ReadEvents loads all event records (capped).
func ReadEvents(slmDir, runID string, limit int) ([]EventRecord, error) {
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
