package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Pipeline phases persisted for interrupt/resume.
const (
	PhaseInit    = "init"
	PhaseContext = "context"
	PhaseExplore = "explore"
	PhasePlan    = "plan"
	PhaseSplit   = "split"
	PhaseExecute = "execute"
	PhaseTest    = "test"
	PhaseMemory  = "memory"
	PhaseDone    = "done"
)

// MarkInterrupted snapshots the board, resets in-flight tasks to ready, and
// flags the turn so /resume can continue without a full restart.
func MarkInterrupted(slmDir string, t *Turn, board plan.Board, phase string) error {
	if t == nil {
		return fmt.Errorf("nil turn")
	}
	if phase == "" {
		phase = PhaseExecute
	}
	board = NormalizeForResume(board)
	t.Interrupted = true
	t.Phase = phase
	t.ResumeFrom = phase
	t.Success = false
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	t.Board = board
	if err := SaveTurnBoard(slmDir, t, board); err != nil {
		return err
	}
	// Lightweight interrupt note for humans / later CONTEXT enrichment.
	note := fmt.Sprintf("# Interrupted\n\n**Phase:** %s\n**When:** %s\n\nBoard preserved — resume with `/resume` or `slmcode session resume %s`.\n",
		phase, t.UpdatedAt, t.ID)
	_ = os.WriteFile(filepath.Join(TurnDir(slmDir, t.ID), "INTERRUPTED.md"), []byte(note), 0o644)
	return writeCheckpoint(slmDir, t)
}

// ClearInterrupted removes the interrupt flag after a successful resume start.
func ClearInterrupted(slmDir string, t *Turn) error {
	if t == nil {
		return fmt.Errorf("nil turn")
	}
	t.Interrupted = false
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	_ = os.Remove(filepath.Join(TurnDir(slmDir, t.ID), "INTERRUPTED.md"))
	if err := writeTurnMeta(TurnDir(slmDir, t.ID), t); err != nil {
		return err
	}
	return writeCheckpoint(slmDir, t)
}

// SetPhase updates the turn's current pipeline phase (best-effort persistence).
func SetPhase(slmDir string, t *Turn, phase string) {
	if t == nil || phase == "" {
		return
	}
	t.Phase = phase
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	_ = writeTurnMeta(TurnDir(slmDir, t.ID), t)
	_ = writeCheckpoint(slmDir, t)
}

// NormalizeForResume moves stuck in_progress/in_review/blocked tasks back to
// ready_to_dev so the execute loop can continue safely.
func NormalizeForResume(board plan.Board) plan.Board {
	for i := range board.Tasks {
		t := &board.Tasks[i]
		t.Normalize()
		switch t.Column {
		case plan.ColInProgress, plan.ColInReview:
			t.Error = ""
			t.MoveTo(plan.ColReadyToDev)
		case plan.ColBlocked:
			// Leave permanent failures blocked unless they look cancel-related.
			if looksLikeCancel(t.Error) {
				t.Error = ""
				t.MoveTo(plan.ColReadyToDev)
			}
		}
	}
	return board
}

// FindInterrupted returns the newest interrupted query turn, if any.
func FindInterrupted(slmDir string) (*Turn, error) {
	list, err := ListQueries(slmDir)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Interrupted {
			t := list[i]
			return &t, nil
		}
	}
	// Fallback: checkpoint pointer
	if id := readCheckpointID(slmDir); id != "" {
		t, err := LoadTurn(slmDir, id)
		if err == nil && t.Interrupted {
			return t, nil
		}
	}
	return nil, nil
}

// ResolveResumeTurn picks a turn by id, or the latest interrupted one.
func ResolveResumeTurn(slmDir, id string) (*Turn, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "latest" || id == "last" {
		t, err := FindInterrupted(slmDir)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("no interrupted run to resume — use /stop mid-run first, or pass a query id")
		}
		return t, nil
	}
	// Allow numeric picker from ListQueries order.
	if n := atoiSafe(id); n > 0 {
		list, err := ListQueries(slmDir)
		if err != nil {
			return nil, err
		}
		if n > len(list) {
			return nil, fmt.Errorf("session index %d out of range (have %d)", n, len(list))
		}
		t := list[n-1]
		return &t, nil
	}
	t, err := LoadTurn(slmDir, id)
	if err != nil {
		return nil, fmt.Errorf("load turn %s: %w", id, err)
	}
	return t, nil
}

// ListInterrupted returns interrupted turns newest-first.
func ListInterrupted(slmDir string) ([]Turn, error) {
	list, err := ListQueries(slmDir)
	if err != nil {
		return nil, err
	}
	var out []Turn
	for _, t := range list {
		if t.Interrupted {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

type checkpointFile struct {
	TurnID      string   `json:"turn_id"`
	Interrupted bool     `json:"interrupted"`
	Phase       string   `json:"phase"`
	UpdatedAt   string   `json:"updated_at"`
	Query       string   `json:"query,omitempty"`
	ResumeMode  string   `json:"resume_mode,omitempty"` // board | react
	ReactTasks  []string `json:"react_tasks,omitempty"` // task ids with message history
}

func checkpointPath(slmDir string) string {
	return filepath.Join(slmDir, "checkpoint.json")
}

func writeCheckpoint(slmDir string, t *Turn) error {
	if t == nil {
		return nil
	}
	_ = os.MkdirAll(slmDir, 0o755)
	mode := "board"
	var reactTasks []string
	if list, err := ListReactCheckpoints(slmDir, t.ID); err == nil {
		for _, cp := range list {
			if len(cp.Messages) > 0 {
				reactTasks = append(reactTasks, cp.TaskID)
			}
		}
	}
	if len(reactTasks) > 0 {
		mode = "react"
	}
	// Preserve prior react_tasks pointer if list failed but checkpoint existed.
	if prev, err := os.ReadFile(checkpointPath(slmDir)); err == nil {
		var old checkpointFile
		if json.Unmarshal(prev, &old) == nil && len(reactTasks) == 0 && len(old.ReactTasks) > 0 && HasReactHistory(slmDir, t.ID) {
			reactTasks = old.ReactTasks
			mode = "react"
		}
	}
	cf := checkpointFile{
		TurnID: t.ID, Interrupted: t.Interrupted, Phase: t.Phase,
		UpdatedAt: t.UpdatedAt, Query: t.Query,
		ResumeMode: mode, ReactTasks: reactTasks,
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(checkpointPath(slmDir), data, 0o644)
}

func readCheckpointID(slmDir string) string {
	data, err := os.ReadFile(checkpointPath(slmDir))
	if err != nil {
		return ""
	}
	var cf checkpointFile
	if json.Unmarshal(data, &cf) != nil {
		return ""
	}
	return cf.TurnID
}

func looksLikeCancel(errStr string) bool {
	lower := strings.ToLower(errStr)
	return strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "context cancelled") ||
		strings.Contains(lower, "canceled") ||
		strings.Contains(lower, "cancelled") ||
		strings.Contains(lower, "interrupted") ||
		strings.Contains(lower, "stopped")
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
