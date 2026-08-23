package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

const maxPromptHistory = 100

// PromptHistory persists ↑/↓ recall across TUI sessions (little-coder prompt-history).
type PromptHistory struct {
	mu    sync.Mutex
	path  string
	items []string
	idx   int // -1 = not browsing; 0 = newest when browsing
}

// LoadPromptHistory reads the prompt history file at path (see
// DefaultPromptHistoryPath for the per-OS location).
func LoadPromptHistory(path string) *PromptHistory {
	h := &PromptHistory{path: path, idx: -1}
	if path == "" {
		return h
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var items []string
	if json.Unmarshal(data, &items) == nil {
		h.items = items
	}
	return h
}

// DefaultPromptHistoryPath returns the per-OS history file location:
//
//	Linux/BSD  $XDG_CONFIG_HOME/slmcode/prompt-history.json
//	           (~/.config/slmcode/prompt-history.json when unset)
//	macOS      ~/Library/Application Support/slmcode/prompt-history.json
//	Windows    %AppData%\slmcode\prompt-history.json
//
// It follows os.UserConfigDir, so the old "always ~/.config/slmcode" claim was
// wrong on macOS and Windows.
func DefaultPromptHistoryPath() string {
	home, err := os.UserConfigDir()
	if err != nil || home == "" {
		home, _ = os.UserHomeDir()
		return filepath.Join(home, ".config", "slmcode", "prompt-history.json")
	}
	return filepath.Join(home, "slmcode", "prompt-history.json")
}

// Add records a submitted prompt (dedupes consecutive duplicates).
func (h *PromptHistory) Add(line string) {
	if h == nil {
		return
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "/") {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.items) > 0 && h.items[len(h.items)-1] == line {
		h.idx = -1
		return
	}
	h.items = append(h.items, line)
	if len(h.items) > maxPromptHistory {
		h.items = h.items[len(h.items)-maxPromptHistory:]
	}
	h.idx = -1
	h.saveLocked()
}

// Prev moves toward older entries; returns the line and ok.
func (h *PromptHistory) Prev() (string, bool) {
	if h == nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.items) == 0 {
		return "", false
	}
	if h.idx < 0 {
		h.idx = len(h.items) - 1
	} else if h.idx > 0 {
		h.idx--
	}
	return h.items[h.idx], true
}

// Next moves toward newer entries.
func (h *PromptHistory) Next() (string, bool) {
	if h == nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.items) == 0 || h.idx < 0 {
		return "", false
	}
	if h.idx >= len(h.items)-1 {
		h.idx = -1
		return "", true // cleared draft
	}
	h.idx++
	return h.items[h.idx], true
}

// Recent returns the last n prompts (newest last).
func (h *PromptHistory) Recent(n int) []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n <= 0 || len(h.items) == 0 {
		return nil
	}
	if n > len(h.items) {
		n = len(h.items)
	}
	out := make([]string, n)
	copy(out, h.items[len(h.items)-n:])
	return out
}

func (h *PromptHistory) saveLocked() {
	if h.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(h.path), 0o755)
	data, err := json.MarshalIndent(h.items, "", "  ")
	if err != nil {
		return
	}
	_ = atomicfile.Write(h.path, data, 0o644)
}
