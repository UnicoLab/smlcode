package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ws_todo is deliberately a no-op with respect to the world: it stores a short
// checklist and renders it straight back. Its whole value is that the plan
// reappears in the model's RECENT context on every turn, which is what keeps a
// 7B model from drifting off the task after six tool calls (deepagents'
// write_todos). It is not a scheduler and nothing else reads it.

// MaxTodoItems caps the list — a long list defeats the purpose.
const MaxTodoItems = 12

// TodoItem is one checklist entry.
type TodoItem struct {
	Text string
	Done bool
}

// SetTodos replaces the checklist and persists a copy under .slmcode/scratch/.
func (w *Workspace) SetTodos(items []TodoItem) {
	if w == nil {
		return
	}
	if len(items) > MaxTodoItems {
		items = items[:MaxTodoItems]
	}
	w.todoMu.Lock()
	w.todos = append([]TodoItem(nil), items...)
	rendered := renderTodos(w.todos)
	w.todoMu.Unlock()
	w.persistTodos(rendered)
}

// Todos returns the current checklist.
func (w *Workspace) Todos() []TodoItem {
	if w == nil {
		return nil
	}
	w.todoMu.Lock()
	defer w.todoMu.Unlock()
	return append([]TodoItem(nil), w.todos...)
}

func (w *Workspace) persistTodos(rendered string) {
	dir := w.scratchDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "TODO.md"), []byte(rendered+"\n"), 0o644)
}

// scratchDir is the only agent-writable location under .slmcode/.
func (w *Workspace) scratchDir() string {
	if w == nil || w.Root == "" {
		return ""
	}
	return filepath.Join(w.Root, filepath.FromSlash(ScratchDir))
}

// ParseTodoArgs accepts the shapes an SLM actually emits:
//
//	{"todos": ["a", "b"]}
//	{"todos": [{"text":"a","done":true}]}
//	{"todos": "- [x] a\n- [ ] b"}
//	{"items": ...} / {"tasks": ...}   (common aliases)
func ParseTodoArgs(args map[string]interface{}) []TodoItem {
	var raw interface{}
	for _, key := range []string{"todos", "items", "tasks", "list", "plan"} {
		if v, ok := args[key]; ok && v != nil {
			raw = v
			break
		}
	}
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		return parseTodoLines(v)
	case []interface{}:
		var out []TodoItem
		for _, e := range v {
			switch it := e.(type) {
			case string:
				if t := parseTodoLine(it); t.Text != "" {
					out = append(out, t)
				}
			case map[string]interface{}:
				text := strArg(it, "text")
				if text == "" {
					text = strArg(it, "task")
				}
				if text == "" {
					text = strArg(it, "title")
				}
				if strings.TrimSpace(text) == "" {
					continue
				}
				done := boolArg(it, "done", false) ||
					strings.EqualFold(strArg(it, "status"), "done") ||
					strings.EqualFold(strArg(it, "status"), "completed")
				out = append(out, TodoItem{Text: strings.TrimSpace(text), Done: done})
			}
		}
		return out
	case []string:
		var out []TodoItem
		for _, s := range v {
			if t := parseTodoLine(s); t.Text != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

func parseTodoLines(s string) []TodoItem {
	var out []TodoItem
	for _, ln := range strings.Split(s, "\n") {
		if t := parseTodoLine(ln); t.Text != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseTodoLine strips "- ", "* ", "1. " and a "[x]"/"[ ]" checkbox.
func parseTodoLine(s string) TodoItem {
	t := strings.TrimSpace(s)
	if t == "" {
		return TodoItem{}
	}
	t = strings.TrimLeft(t, "-*• \t")
	// Numbered list prefix.
	for i := 0; i < len(t); i++ {
		if t[i] >= '0' && t[i] <= '9' {
			continue
		}
		if i > 0 && (t[i] == '.' || t[i] == ')') {
			t = strings.TrimSpace(t[i+1:])
		}
		break
	}
	done := false
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(lower, "[x]"):
		done = true
		t = strings.TrimSpace(t[3:])
	case strings.HasPrefix(lower, "[ ]"):
		t = strings.TrimSpace(t[3:])
	case strings.HasPrefix(lower, "[]"):
		t = strings.TrimSpace(t[2:])
	}
	return TodoItem{Text: strings.TrimSpace(t), Done: done}
}

func renderTodos(items []TodoItem) string {
	if len(items) == 0 {
		return "TODO list is empty."
	}
	var b strings.Builder
	done := 0
	b.WriteString("TODO (re-read this before your next tool call):\n")
	for i, it := range items {
		mark := " "
		if it.Done {
			mark = "x"
			done++
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, mark, it.Text)
	}
	fmt.Fprintf(&b, "\n%d/%d done.", done, len(items))
	if done < len(items) {
		for _, it := range items {
			if !it.Done {
				fmt.Fprintf(&b, " NEXT: %s", it.Text)
				break
			}
		}
	} else {
		b.WriteString(" All items complete — finish with status JSON.")
	}
	return b.String()
}

// todoTool is the ws_todo executor.
func (w *Workspace) todoTool(_ context.Context, args map[string]interface{}) (interface{}, error) {
	items := ParseTodoArgs(args)
	if items == nil {
		if _, present := args["todos"]; !present {
			// A bare call is a read of the current list.
			return renderTodos(w.Todos()), nil
		}
		return "ws_todo: could not read the checklist. Pass todos as a JSON array of strings, " +
			`e.g. {"todos": ["read pkg/x/y.go", "add the missing nil check", "run go test ./pkg/x"]}. ` +
			"Mark finished items by prefixing them with [x].", nil
	}
	w.SetTodos(items)
	return renderTodos(w.Todos()), nil
}
