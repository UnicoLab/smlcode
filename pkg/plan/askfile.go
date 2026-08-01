package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Clear stale answers so we don't pick up a previous run.
	_ = os.Remove(ClarifyAnswersPath(slmDir))
	data, err := json.MarshalIndent(ask, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ClarifyAskPath(slmDir), data, 0o644)
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
	if slmDir == "" {
		return nil
	}
	dir := filepath.Join(slmDir, "clarify")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if ans.AnsweredAt == "" {
		ans.AnsweredAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(ans, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ClarifyAnswersPath(slmDir), data, 0o644)
}

// WaitScopeAnswers polls for answers.json until ctx done or timeout.
// Returns (answers, true, nil) on success; (empty, false, nil) on timeout.
func WaitScopeAnswers(ctx context.Context, slmDir string, timeout time.Duration) (ScopeAnswers, bool, error) {
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
			return ans, true, nil
		}
		if time.Now().After(deadline) {
			return ScopeAnswers{}, false, nil
		}
		select {
		case <-ctx.Done():
			return ScopeAnswers{}, false, ctx.Err()
		case <-ticker.C:
		}
	}
}
