package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTodoArgs(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want []TodoItem
	}{
		{
			name: "array of strings",
			args: map[string]interface{}{"todos": []interface{}{"read a.go", "fix bug"}},
			want: []TodoItem{{Text: "read a.go"}, {Text: "fix bug"}},
		},
		{
			name: "checkbox markers",
			args: map[string]interface{}{"todos": []interface{}{"[x] read a.go", "[ ] fix bug"}},
			want: []TodoItem{{Text: "read a.go", Done: true}, {Text: "fix bug"}},
		},
		{
			name: "markdown string",
			args: map[string]interface{}{"todos": "- [x] one\n- [ ] two\n"},
			want: []TodoItem{{Text: "one", Done: true}, {Text: "two"}},
		},
		{
			name: "numbered string",
			args: map[string]interface{}{"todos": "1. one\n2. two"},
			want: []TodoItem{{Text: "one"}, {Text: "two"}},
		},
		{
			name: "objects",
			args: map[string]interface{}{"todos": []interface{}{
				map[string]interface{}{"text": "one", "done": true},
				map[string]interface{}{"text": "two", "status": "pending"},
			}},
			want: []TodoItem{{Text: "one", Done: true}, {Text: "two"}},
		},
		{
			name: "objects with status done",
			args: map[string]interface{}{"todos": []interface{}{
				map[string]interface{}{"task": "one", "status": "done"},
			}},
			want: []TodoItem{{Text: "one", Done: true}},
		},
		{
			name: "items alias",
			args: map[string]interface{}{"items": []interface{}{"one"}},
			want: []TodoItem{{Text: "one"}},
		},
		{
			name: "nothing",
			args: map[string]interface{}{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTodoArgs(tc.args)
			if len(got) != len(tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %#v want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestTodoToolRendersBack(t *testing.T) {
	w, root := newTestWS(t)
	out := strOut(w.todoTool(context.Background(), map[string]interface{}{
		"todos": []interface{}{"[x] read pkg/a.go", "add nil check", "run go test"},
	}))
	for _, want := range []string{"1. [x] read pkg/a.go", "2. [ ] add nil check", "1/3 done", "NEXT: add nil check"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// Persisted under the one agent-writable path in .slmcode/.
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ScratchDir), "TODO.md"))
	if err != nil {
		t.Fatalf("todo should persist to scratch: %v", err)
	}
	if !strings.Contains(string(data), "add nil check") {
		t.Fatalf("scratch file = %q", data)
	}
}

func TestTodoToolBareCallReadsBack(t *testing.T) {
	w, _ := newTestWS(t)
	w.SetTodos([]TodoItem{{Text: "one"}})
	out := strOut(w.todoTool(context.Background(), map[string]interface{}{}))
	if !strings.Contains(out, "1. [ ] one") {
		t.Fatalf("bare call should re-render the list: %q", out)
	}
}

func TestTodoToolAllDoneSteersToFinish(t *testing.T) {
	w, _ := newTestWS(t)
	out := strOut(w.todoTool(context.Background(), map[string]interface{}{
		"todos": []interface{}{"[x] one", "[x] two"},
	}))
	if !strings.Contains(out, "All items complete") || !strings.Contains(out, "status JSON") {
		t.Fatalf("got %q", out)
	}
}

func TestTodoToolUnparseableIsActionable(t *testing.T) {
	w, _ := newTestWS(t)
	out := strOut(w.todoTool(context.Background(), map[string]interface{}{"todos": 42}))
	if !strings.Contains(out, "JSON array of strings") {
		t.Fatalf("got %q", out)
	}
}

func TestTodoListIsCapped(t *testing.T) {
	w, _ := newTestWS(t)
	var many []TodoItem
	for i := 0; i < 40; i++ {
		many = append(many, TodoItem{Text: "item"})
	}
	w.SetTodos(many)
	if got := len(w.Todos()); got != MaxTodoItems {
		t.Fatalf("list should cap at %d, got %d", MaxTodoItems, got)
	}
}

func TestTodoToolRegistered(t *testing.T) {
	for _, n := range ToolNames() {
		if n == "ws_todo" {
			return
		}
	}
	t.Fatal("ws_todo must be in ToolNames()")
}
