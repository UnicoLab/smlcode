package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// LiveStore is a thread-safe board persisted as board.json (+ optional markdown sync).
// CLI, Studio UI, and the agent loop all share this so humans can edit while agents run.
type LiveStore struct {
	mu       sync.RWMutex
	path     string
	board    Board
	onChange func(*Board)
}

func NewLiveStore(slmDir string) *LiveStore {
	return &LiveStore{
		path:  filepath.Join(slmDir, "board.json"),
		board: Board{},
	}
}

func (s *LiveStore) Path() string { return s.path }

func (s *LiveStore) OnChange(fn func(*Board)) { s.onChange = fn }

// Load reads board.json if present.
func (s *LiveStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var b Board
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
	}
	s.board = b
	return nil
}

// Save writes board.json.
func (s *LiveStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *LiveStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.board, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Snapshot returns a copy of the board.
func (s *LiveStore) Snapshot() Board {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneBoard(s.board)
}

// Replace overwrites the entire board.
func (s *LiveStore) Replace(b Board) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
	}
	s.board = b
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.fire()
	return nil
}

// MergeFrom reloads from disk then merges into memory (picks up CLI/UI edits mid-run).
func (s *LiveStore) MergeFromDisk() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var disk Board
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := map[string]Task{}
	for _, t := range s.board.Tasks {
		byID[t.ID] = t
	}
	for _, t := range disk.Tasks {
		t.Normalize()
		byID[t.ID] = t
	}
	// Preserve plan from newer disk if set
	if disk.Plan.Summary != "" {
		s.board.Plan = disk.Plan
	}
	s.board.Tasks = s.board.Tasks[:0]
	for _, t := range byID {
		s.board.Tasks = append(s.board.Tasks, t)
	}
	return s.saveLocked()
}

// Update applies a mutator under lock and persists.
func (s *LiveStore) Update(fn func(*Board) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.board); err != nil {
		return err
	}
	for i := range s.board.Tasks {
		s.board.Tasks[i].Normalize()
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.fire()
	return nil
}

// GetTask returns a task copy.
func (s *LiveStore) GetTask(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.board.Get(id)
}

func (s *LiveStore) fire() {
	if s.onChange == nil {
		return
	}
	b := cloneBoard(s.board)
	go s.onChange(&b)
}

func cloneBoard(b Board) Board {
	data, _ := json.Marshal(b)
	var out Board
	_ = json.Unmarshal(data, &out)
	return out
}

// SyncMarkdown writes PLAN.md / TASKS.md via callback (store stays JSON-first).
func (s *LiveStore) Markdown() (planMD, tasksMD string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.board.ToMarkdown()
}

// EnsureTaskID validates id exists.
func (s *LiveStore) EnsureTaskID(id string) error {
	if _, ok := s.GetTask(id); !ok {
		return fmt.Errorf("task %q not found", id)
	}
	return nil
}
