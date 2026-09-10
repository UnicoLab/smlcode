package hitl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// File-based HITL handshake under .slmcode/<kind>/{ask,answers}.json
// Shared by clarify (legacy paths), plan approve, and shell ask.

func Dir(slmDir, kind string) string {
	return filepath.Join(slmDir, kind)
}

func AskPath(slmDir, kind string) string {
	return filepath.Join(slmDir, kind, "ask.json")
}

func AnswersPath(slmDir, kind string) string {
	return filepath.Join(slmDir, kind, "answers.json")
}

// WriteAsk persists a pending ask payload.
func WriteAsk(slmDir, kind string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(Dir(slmDir, kind), 0o750); err != nil { // HITL ask/answer state, owner-only
		return err
	}
	_ = os.Remove(AnswersPath(slmDir, kind))
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(AskPath(slmDir, kind), data, 0o644)
}

// Clear removes ask + answers for kind.
func Clear(slmDir, kind string) {
	if slmDir == "" {
		return
	}
	_ = os.Remove(AskPath(slmDir, kind))
	_ = os.Remove(AnswersPath(slmDir, kind))
}

// ClearAll removes every known pending HITL ask/answer pair, including the
// per-ask files (see WriteAskID).
func ClearAll(slmDir string) {
	if slmDir == "" {
		return
	}
	for _, kind := range []string{"clarify", "plan", "continue", "escalate", "shell"} {
		Clear(slmDir, kind)
		_ = os.RemoveAll(AsksDir(slmDir, kind))
		_ = os.RemoveAll(AnswersDir(slmDir, kind))
	}
}

// ── Per-ask files ──────────────────────────────────────────────────────────
//
// The single ask.json / answers.json slot above is fine for asks that are by
// construction one-at-a-time (plan approval, continue). Shell approval is not:
// a wave runs N workers in parallel and each may need a command approved. With
// one slot the second WriteAsk overwrote the first (and deleted its answer),
// and WaitAnswersForID's "not my id → delete it" step made the loser destroy
// the winner's decision. Each ask now lives in its own pair of files:
//
//	.slmcode/<kind>/asks/<id>.json
//	.slmcode/<kind>/answers/<id>.json
//
// so N concurrent asks each wait on exactly their own answer.

// AsksDir is where per-ask payloads live for kind.
func AsksDir(slmDir, kind string) string { return filepath.Join(slmDir, kind, "asks") }

// AnswersDir is where per-ask answers live for kind.
func AnswersDir(slmDir, kind string) string { return filepath.Join(slmDir, kind, "answers") }

// AskIDPath / AnswerIDPath name the files for one ask id.
func AskIDPath(slmDir, kind, id string) string {
	return filepath.Join(AsksDir(slmDir, kind), safeAskID(id)+".json")
}

// AnswerIDPath is the answer file for one ask id.
func AnswerIDPath(slmDir, kind, id string) string {
	return filepath.Join(AnswersDir(slmDir, kind), safeAskID(id)+".json")
}

// safeAskID flattens an id into one file-name component.
func safeAskID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "ask"
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// WriteAskID persists one ask under its own id without touching any other
// pending ask of the same kind.
func WriteAskID(slmDir, kind, id string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(AsksDir(slmDir, kind), 0o750); err != nil { // HITL ask/answer state, owner-only
		return err
	}
	_ = os.Remove(AnswerIDPath(slmDir, kind, id))
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(AskIDPath(slmDir, kind, id), data, 0o644)
}

// ReadAskID loads one ask by id.
func ReadAskID(slmDir, kind, id string, dest any) (bool, error) {
	data, err := os.ReadFile(AskIDPath(slmDir, kind, id))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}
	return true, nil
}

// ListAskIDs returns the ids of every pending per-ask file for kind, oldest
// first (ids carry a nanosecond timestamp, so name order is creation order).
func ListAskIDs(slmDir, kind string) ([]string, error) {
	entries, err := os.ReadDir(AsksDir(slmDir, kind))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// WriteAnswerIDOnce stores the answer for one ask, refusing to overwrite an
// existing decision (os.IsExist on the returned error).
func WriteAnswerIDOnce(slmDir, kind, id string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(AnswersDir(slmDir, kind), 0o750); err != nil { // HITL ask/answer state, owner-only
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteOnce(AnswerIDPath(slmDir, kind, id), data, 0o644)
}

// ReadAnswerID loads the answer for one ask if present.
func ReadAnswerID(slmDir, kind, id string, dest any) (bool, error) {
	data, err := os.ReadFile(AnswerIDPath(slmDir, kind, id))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}
	return true, nil
}

// ClearID removes the ask and answer files for one id only.
func ClearID(slmDir, kind, id string) {
	if slmDir == "" {
		return
	}
	_ = os.Remove(AskIDPath(slmDir, kind, id))
	_ = os.Remove(AnswerIDPath(slmDir, kind, id))
}

// WaitAnswerID polls for THIS ask's answer until timeout or ctx cancel. It
// never touches another ask's files. On timeout the ask is withdrawn (its
// files removed) and ok=false is returned; an answer file that appears after
// the deadline is ignored the same way.
func WaitAnswerID(ctx context.Context, slmDir, kind, id string, timeout time.Duration, dest any) (bool, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := ReadAnswerID(slmDir, kind, id, dest)
		if err != nil {
			return false, err
		}
		if ok {
			if info, serr := os.Stat(AnswerIDPath(slmDir, kind, id)); serr == nil && info.ModTime().After(deadline) {
				ClearID(slmDir, kind, id)
				return false, nil
			}
			return true, nil
		}
		if time.Now().After(deadline) {
			ClearID(slmDir, kind, id)
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WriteAnswers stores a response payload.
func WriteAnswers(slmDir, kind string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(Dir(slmDir, kind), 0o750); err != nil { // HITL ask/answer state, owner-only
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(AnswersPath(slmDir, kind), data, 0o644)
}

// WriteAnswersOnce stores a response only when no answer exists yet.
func WriteAnswersOnce(slmDir, kind string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(Dir(slmDir, kind), 0o750); err != nil { // HITL ask/answer state, owner-only
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteOnce(AnswersPath(slmDir, kind), data, 0o644)
}

// ReadAsk loads ask.json if present.
func ReadAsk(slmDir, kind string, dest any) (bool, error) {
	data, err := os.ReadFile(AskPath(slmDir, kind))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}
	return true, nil
}

// ReadAnswers loads answers.json if present.
func ReadAnswers(slmDir, kind string, dest any) (bool, error) {
	data, err := os.ReadFile(AnswersPath(slmDir, kind))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}
	return true, nil
}

// WaitAnswers polls for answers until timeout or ctx cancel.
func WaitAnswers(ctx context.Context, slmDir, kind string, timeout time.Duration, dest any) (bool, error) {
	return WaitAnswersForID(ctx, slmDir, kind, "", timeout, dest)
}

// WaitAnswersForID polls for answers until timeout or ctx cancel, ignoring stale
// answers for a different ask_id when askID is provided.
func WaitAnswersForID(ctx context.Context, slmDir, kind, askID string, timeout time.Duration, dest any) (bool, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := ReadAnswers(slmDir, kind, dest)
		if err != nil {
			return false, err
		}
		if ok {
			if answerWrittenAfter(slmDir, kind, deadline) {
				Clear(slmDir, kind)
				return false, nil
			}
			if !answerMatchesAskID(slmDir, kind, askID) {
				_ = os.Remove(AnswersPath(slmDir, kind))
				goto wait
			}
			return true, nil
		}
		if time.Now().After(deadline) {
			Clear(slmDir, kind)
			return false, nil
		}
	wait:
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func answerWrittenAfter(slmDir, kind string, deadline time.Time) bool {
	info, err := os.Stat(AnswersPath(slmDir, kind))
	if err != nil {
		return false
	}
	return info.ModTime().After(deadline)
}

func answerMatchesAskID(slmDir, kind, askID string) bool {
	askID = strings.TrimSpace(askID)
	if askID == "" {
		return true
	}
	data, err := os.ReadFile(AnswersPath(slmDir, kind))
	if err != nil {
		return false
	}
	var meta struct {
		AskID string `json:"ask_id"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return false
	}
	return strings.TrimSpace(meta.AskID) == askID
}
