package hitl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(Dir(slmDir, kind), 0o755); err != nil {
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

// ClearAll removes every known pending HITL ask/answer pair.
func ClearAll(slmDir string) {
	if slmDir == "" {
		return
	}
	for _, kind := range []string{"clarify", "plan", "continue", "escalate", "shell"} {
		Clear(slmDir, kind)
	}
}

// WriteAnswers stores a response payload.
func WriteAnswers(slmDir, kind string, payload any) error {
	if slmDir == "" {
		return nil
	}
	if err := os.MkdirAll(Dir(slmDir, kind), 0o755); err != nil {
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
	if err := os.MkdirAll(Dir(slmDir, kind), 0o755); err != nil {
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
				_ = os.Remove(AnswersPath(slmDir, kind))
				return false, nil
			}
			if !answerMatchesAskID(slmDir, kind, askID) {
				_ = os.Remove(AnswersPath(slmDir, kind))
				goto wait
			}
			return true, nil
		}
		if time.Now().After(deadline) {
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
