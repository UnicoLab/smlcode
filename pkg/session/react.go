package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// React schema version for mid-run HITL checkpoints.
const ReactSchemaVersion = 1

// ReactMessage is a serializable chat/tool turn for GoLangGraph resume.
type ReactMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ReactToolCall `json:"tool_calls,omitempty"`
}

// ReactToolCall is a pending or completed tool invocation.
type ReactToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ReactCheckpoint stores enough state for the loop runner / GoLangGraph to
// continue a mid-ReAct (or mid-tool-call) interrupt without a cold replan.
type ReactCheckpoint struct {
	SchemaVersion    int             `json:"schema_version"`
	TurnID           string          `json:"turn_id"`
	TaskID           string          `json:"task_id"`
	AgentID          string          `json:"agent_id,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Model            string          `json:"model,omitempty"`
	Iteration        int             `json:"iteration"`
	MaxIterations    int             `json:"max_iterations,omitempty"`
	Status           string          `json:"status,omitempty"` // interrupted|running|done
	PendingToolCalls []ReactToolCall `json:"pending_tool_calls,omitempty"`
	Messages         []ReactMessage  `json:"messages"`
	UpdatedAt        string          `json:"updated_at"`
}

// ReactDir returns .slmcode/queries/<turn>/react/
func ReactDir(slmDir, turnID string) string {
	return filepath.Join(TurnDir(slmDir, turnID), "react")
}

// ReactPath returns the per-task react checkpoint file.
func ReactPath(slmDir, turnID, taskID string) string {
	return filepath.Join(ReactDir(slmDir, turnID), sanitizeID(taskID)+".json")
}

// SaveReactCheckpoint persists mid-run ReAct state for a task.
func SaveReactCheckpoint(slmDir string, cp ReactCheckpoint) error {
	if strings.TrimSpace(cp.TurnID) == "" || strings.TrimSpace(cp.TaskID) == "" {
		return fmt.Errorf("react checkpoint requires turn_id and task_id")
	}
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = ReactSchemaVersion
	}
	if cp.Status == "" {
		cp.Status = "interrupted"
	}
	cp.UpdatedAt = time.Now().Format(time.RFC3339)
	dir := ReactDir(slmDir, cp.TurnID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(ReactPath(slmDir, cp.TurnID, cp.TaskID), data, 0o644); err != nil {
		return err
	}
	return updateCheckpointReactTasks(slmDir, cp.TurnID, cp.TaskID, true)
}

// LoadReactCheckpoint loads a per-task react checkpoint, or nil if absent.
func LoadReactCheckpoint(slmDir, turnID, taskID string) (*ReactCheckpoint, error) {
	data, err := os.ReadFile(ReactPath(slmDir, turnID, taskID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cp ReactCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// ListReactCheckpoints returns all react checkpoints for a turn.
func ListReactCheckpoints(slmDir, turnID string) ([]ReactCheckpoint, error) {
	dir := ReactDir(slmDir, turnID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ReactCheckpoint
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var cp ReactCheckpoint
		if json.Unmarshal(data, &cp) != nil {
			continue
		}
		out = append(out, cp)
	}
	return out, nil
}

// ClearReactCheckpoint removes one task's react state after successful resume.
func ClearReactCheckpoint(slmDir, turnID, taskID string) error {
	_ = os.Remove(ReactPath(slmDir, turnID, taskID))
	return updateCheckpointReactTasks(slmDir, turnID, taskID, false)
}

// ClearAllReactCheckpoints removes the react/ directory for a turn.
func ClearAllReactCheckpoints(slmDir, turnID string) error {
	_ = os.RemoveAll(ReactDir(slmDir, turnID))
	return updateCheckpointReactTasks(slmDir, turnID, "", false)
}

// HasReactHistory reports whether any task has persisted messages for resume.
func HasReactHistory(slmDir, turnID string) bool {
	list, err := ListReactCheckpoints(slmDir, turnID)
	if err != nil {
		return false
	}
	for _, cp := range list {
		if len(cp.Messages) > 0 {
			return true
		}
	}
	return false
}

func updateCheckpointReactTasks(slmDir, turnID, taskID string, add bool) error {
	path := checkpointPath(slmDir)
	data, err := os.ReadFile(path)
	var cf checkpointFile
	if err == nil {
		_ = json.Unmarshal(data, &cf)
	}
	if cf.TurnID == "" {
		cf.TurnID = turnID
	}
	if add && taskID != "" {
		found := false
		for _, id := range cf.ReactTasks {
			if id == taskID {
				found = true
				break
			}
		}
		if !found {
			cf.ReactTasks = append(cf.ReactTasks, taskID)
		}
		cf.ResumeMode = "react"
	} else if !add {
		if taskID == "" {
			cf.ReactTasks = nil
		} else {
			var kept []string
			for _, id := range cf.ReactTasks {
				if id != taskID {
					kept = append(kept, id)
				}
			}
			cf.ReactTasks = kept
		}
		if len(cf.ReactTasks) == 0 {
			if cf.ResumeMode == "react" {
				cf.ResumeMode = "board"
			}
		}
	}
	cf.UpdatedAt = time.Now().Format(time.RFC3339)
	_ = os.MkdirAll(slmDir, 0o755)
	out, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, out, 0o644)
}
