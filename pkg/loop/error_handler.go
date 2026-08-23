package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TaskFailureRecord holds detailed information about task failures.
type TaskFailureRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	TaskID        string    `json:"task_id"`
	Attempt       int       `json:"attempt"`
	ErrorType     string    `json:"error_type"`
	ErrorMessage  string    `json:"error_message"`
	AgentRole     string    `json:"agent_role"`
	Files         []string  `json:"files"`
	Acceptance    string    `json:"acceptance"`
	Output        string    `json:"output"`
	ReviewSummary string    `json:"review_summary"`
	Notes         string    `json:"notes"`
	Retries       int       `json:"retries"`
	Stack         string    `json:"stack,omitempty"`
}

// EnhancedFailureHandler provides robust error handling with observability.
type EnhancedFailureHandler struct {
	projectRoot string
	logDir      string
}

// NewEnhancedFailureHandler creates a failure handler writing under .slmcode/errors.
func NewEnhancedFailureHandler(projectRoot string) *EnhancedFailureHandler {
	return &EnhancedFailureHandler{
		projectRoot: projectRoot,
		logDir:      filepath.Join(projectRoot, ".slmcode", "errors"),
	}
}

// ReportTaskFailure logs structured failure JSON + appends to errors.md.
func (efh *EnhancedFailureHandler) ReportTaskFailure(board *plan.Board, t plan.Task, err error, attempt int) error {
	if efh == nil || efh.projectRoot == "" {
		return nil
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	record := TaskFailureRecord{
		Timestamp:     time.Now(),
		TaskID:        t.ID,
		Attempt:       attempt,
		ErrorType:     efh.determineErrorType(err),
		ErrorMessage:  msg,
		AgentRole:     t.Role,
		Files:         t.Files,
		Acceptance:    t.Acceptance,
		Output:        efh.truncate(t.Output, 500),
		ReviewSummary: t.Review,
		Notes:         t.Notes,
		Retries:       t.Retries,
		Stack:         efh.getStack(),
	}

	if err := os.MkdirAll(efh.logDir, 0o755); err != nil {
		return fmt.Errorf("failed to create errors dir: %w", err)
	}

	jsonFile := filepath.Join(efh.logDir, fmt.Sprintf("failure_%s_%s_%d.json",
		time.Now().Format("20060102_150405"), t.ID, attempt))

	jsonContent, mErr := json.MarshalIndent(record, "", "  ")
	if mErr != nil {
		return fmt.Errorf("failed to marshal error record: %w", mErr)
	}
	if wErr := atomicfile.Write(jsonFile, jsonContent, 0o644); wErr != nil {
		return fmt.Errorf("failed to write error json: %w", wErr)
	}

	markdownFile := filepath.Join(efh.logDir, "errors.md")
	markdownEntry := efh.formatMarkdownEntry(record, jsonFile)
	f, oErr := os.OpenFile(markdownFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if oErr != nil {
		return fmt.Errorf("failed to open errors.md: %w", oErr)
	}
	defer f.Close()
	if _, wErr := f.WriteString(markdownEntry); wErr != nil {
		return fmt.Errorf("failed to write error to errors.md: %w", wErr)
	}
	return nil
}

func (efh *EnhancedFailureHandler) formatMarkdownEntry(record TaskFailureRecord, jsonFile string) string {
	return fmt.Sprintf(`## %s - %s (Attempt %d)

**Task ID:** %s
**Timestamp:** %s
**Agent Role:** %s
**Status:** failed

**Error:**
%s

**Context:**
- Files: %s
- Acceptance: %s
- Output (truncated): %s
- Review: %s
- Notes: %s
- Retries: %d

**Analysis:**
%s

**Recovery Path:**
1. Examine detailed error in `+"`%s`"+`
2. Consider manual intervention if pattern persists
3. Review wave lessons for recurring issues
4. Promote the task back to ready after fixing acceptance/context

---
`,
		record.Timestamp.Format("2006-01-02_15-04-05"),
		record.TaskID,
		record.Attempt,
		record.TaskID,
		record.Timestamp.Format(time.RFC3339),
		record.AgentRole,
		record.ErrorMessage,
		strings.Join(record.Files, ", "),
		efh.truncate(record.Acceptance, 100),
		efh.truncate(record.Output, 500),
		efh.truncate(record.ReviewSummary, 100),
		efh.truncate(record.Notes, 100),
		record.Retries,
		efh.analyzeErrorPattern(record),
		jsonFile,
	)
}

func (efh *EnhancedFailureHandler) analyzeErrorPattern(record TaskFailureRecord) string {
	msg := strings.ToLower(record.ErrorMessage)
	switch {
	case strings.Contains(msg, "review rejected") || strings.Contains(msg, "max retries"):
		return "Repetitive review rejections — worker likely returned JSON without tool edits, or acceptance is unreachable."
	case strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout"):
		return "Timeout — split the task smaller, raise task_timeout, or lower max_parallel to reduce SLM contention."
	case strings.Contains(msg, "canceled") || strings.Contains(msg, "cancelled"):
		return "Context canceled — run was stopped or parent context ended mid-task."
	case strings.Contains(msg, "file") || strings.Contains(msg, "missing"):
		return "File path issue — verify workspace inventory and reconcile hallucinated paths."
	default:
		return "Review error details above to understand root cause."
	}
}

func (efh *EnhancedFailureHandler) determineErrorType(err error) string {
	if err == nil {
		return "none"
	}
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "review rejected"):
		return "review_rejection"
	case strings.Contains(errStr, "max retries"):
		return "max_retries_exceeded"
	case strings.Contains(errStr, "deadline") || strings.Contains(errStr, "timeout"):
		return "timeout"
	case strings.Contains(errStr, "canceled") || strings.Contains(errStr, "cancelled"):
		return "canceled"
	case strings.Contains(errStr, "file"):
		return "file_access_error"
	case strings.Contains(errStr, "missing"):
		return "missing_file_error"
	default:
		return "general_error"
	}
}

func (efh *EnhancedFailureHandler) getStack() string {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// truncate clips s to at most maxLen bytes on a rune boundary.
//
// The old body was `s[:maxLen-3] + "..."`, which panics with a negative slice
// bound for any maxLen < 3 and splits multi-byte runes for every other value.
func (efh *EnhancedFailureHandler) truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return textutil.Clip(s, maxLen)
	}
	return textutil.Clip(s, maxLen-3) + "..."
}

// ReportAndLogWaveLesson captures a durable lesson for recurring failures.
func (efh *EnhancedFailureHandler) ReportAndLogWaveLesson(board *plan.Board, task plan.Task, err error) error {
	if efh == nil || efh.projectRoot == "" {
		return nil
	}
	lessonPath := filepath.Join(efh.projectRoot, ".slmcode", "errors", "wave_lessons.md")
	if mkErr := os.MkdirAll(filepath.Dir(lessonPath), 0o755); mkErr != nil {
		return mkErr
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	lessonContent := fmt.Sprintf(`## Wave Lesson - %s

**Issue:** %s
**Task:** %s — %s
**Agent:** %s

**Context:**
- Files: %s
- Acceptance: %s
- Output: %s
- Review: %s

**Action:** Refine acceptance or split the task; avoid repeating the same blocked pattern.

**Previous Attempts:** %d

---
`,
		time.Now().Format("2006-01-02 15:04:05"),
		msg,
		task.ID,
		task.Title,
		task.Role,
		strings.Join(task.Files, ", "),
		efh.truncate(task.Acceptance, 200),
		efh.truncate(task.Output, 200),
		efh.truncate(task.Review, 200),
		task.Retries,
	)

	f, oErr := os.OpenFile(lessonPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if oErr != nil {
		return fmt.Errorf("failed to write wave lesson: %w", oErr)
	}
	defer f.Close()
	if _, wErr := f.WriteString(lessonContent); wErr != nil {
		return fmt.Errorf("failed to write lesson to wave_lessons.md: %w", wErr)
	}
	return nil
}

// AddWaveLesson is an alias kept for call-site clarity.
func (efh *EnhancedFailureHandler) AddWaveLesson(board *plan.Board, task plan.Task, err error) error {
	return efh.ReportAndLogWaveLesson(board, task, err)
}
