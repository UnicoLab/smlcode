package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// ClarifyAskPath returns .slmcode/clarify/ask.json under slmDir.
func ClarifyAskPath(slmDir string) string {
	return filepath.Join(slmDir, "clarify", "ask.json")
}

// ClarifyAnswersPath returns .slmcode/clarify/answers.json under slmDir.
func ClarifyAnswersPath(slmDir string) string {
	return filepath.Join(slmDir, "clarify", "answers.json")
}

// WriteScopeAsk persists the pending interview for Studio/TUI/CLI.
func WriteScopeAsk(slmDir string, ask ScopeAsk) error {
	if slmDir == "" {
		return nil
	}
	dir := filepath.Join(slmDir, "clarify")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Clear stale answers so we don't pick up a previous run.
	_ = os.Remove(ClarifyAnswersPath(slmDir))
	data, err := json.MarshalIndent(ask, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(ClarifyAskPath(slmDir), data, 0o644)
}

// ClearScopeAsk removes pending ask/answer files.
func ClearScopeAsk(slmDir string) {
	if slmDir == "" {
		return
	}
	_ = os.Remove(ClarifyAskPath(slmDir))
	_ = os.Remove(ClarifyAnswersPath(slmDir))
}

// ReadScopeAnswers loads answers if present.
func ReadScopeAnswers(slmDir string) (ScopeAnswers, bool, error) {
	path := ClarifyAnswersPath(slmDir)
	//nolint:gosec // G304: path is derived from the harness's own .slmcode dir.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ScopeAnswers{}, false, nil
		}
		return ScopeAnswers{}, false, err
	}
	var ans ScopeAnswers
	if err := json.Unmarshal(data, &ans); err != nil {
		return ScopeAnswers{}, false, err
	}
	return ans, true, nil
}

// WriteScopeAnswers stores user answers (API / Studio / CLI).
func WriteScopeAnswers(slmDir string, ans ScopeAnswers) error {
	return writeScopeAnswers(slmDir, ans, false)
}

// WriteScopeAnswersOnce stores user answers only when no answer exists yet.
func WriteScopeAnswersOnce(slmDir string, ans ScopeAnswers) error {
	return writeScopeAnswers(slmDir, ans, true)
}

func writeScopeAnswers(slmDir string, ans ScopeAnswers, once bool) error {
	if slmDir == "" {
		return nil
	}
	dir := filepath.Join(slmDir, "clarify")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(ans, "", "  ")
	if err != nil {
		return err
	}
	if once {
		return atomicfile.WriteOnce(ClarifyAnswersPath(slmDir), data, 0o644)
	}
	return atomicfile.Write(ClarifyAnswersPath(slmDir), data, 0o644)
}

// WaitScopeAnswers polls for answers.json until ctx done or timeout.
// Returns (answers, true, nil) on success; (empty, false, nil) on timeout.
func WaitScopeAnswers(ctx context.Context, slmDir string, timeout time.Duration) (ScopeAnswers, bool, error) {
	return WaitScopeAnswersForID(ctx, slmDir, "", timeout)
}

// WaitScopeAnswersForID polls for a matching answers.json until ctx done or timeout.
func WaitScopeAnswersForID(ctx context.Context, slmDir, askID string, timeout time.Duration) (ScopeAnswers, bool, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ans, ok, err := ReadScopeAnswers(slmDir)
		if err != nil {
			return ScopeAnswers{}, false, err
		}
		if ok {
			if scopeAnswerWrittenAfter(slmDir, deadline) {
				ClearScopeAsk(slmDir)
				return ScopeAnswers{}, false, nil
			}
			if askID != "" && ans.AskID != askID {
				_ = os.Remove(ClarifyAnswersPath(slmDir))
				goto wait
			}
			return ans, true, nil
		}
		if time.Now().After(deadline) {
			ClearScopeAsk(slmDir)
			return ScopeAnswers{}, false, nil
		}
	wait:
		select {
		case <-ctx.Done():
			return ScopeAnswers{}, false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func scopeAnswerWrittenAfter(slmDir string, deadline time.Time) bool {
	info, err := os.Stat(ClarifyAnswersPath(slmDir))
	if err != nil {
		return false
	}
	return info.ModTime().After(deadline)
}
