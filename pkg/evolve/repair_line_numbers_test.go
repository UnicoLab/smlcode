package evolve

import (
	"encoding/json"
	"testing"
)

// TestStripLineNumbersNeverTouchesFileContent pins defect 5.
//
// strip_line_number_prefix removes ws_read's `   42|` gutter so a copied
// old_str can match. Applied to `content`/`body` it was removing an ordinary
// line of pipe-delimited data from the file the harness was about to WRITE —
// a markdown table row, a CSV dump, an ASCII table — and the corruption was
// indistinguishable downstream from the model having written it that way.
func TestStripLineNumbersNeverTouchesFileContent(t *testing.T) {
	table := "| n | name |\n|---|------|\n1 | Alpha\n2 | Beta\n"

	t.Run("ws_write content is left alone", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{"path": "docs/table.md", "content": table})
		out, changed := ApplyTransform(TransformStripLineNumbers, string(in))
		if changed {
			t.Fatalf("the transform rewrote whole-file content: %s", out)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got["content"] != table {
			t.Fatalf("content mangled:\n got %q\nwant %q", got["content"], table)
		}
	})

	t.Run("a body argument is left alone", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{"path": "a.md", "body": table})
		if out, changed := ApplyTransform(TransformStripLineNumbers, string(in)); changed {
			t.Fatalf("the transform rewrote a body argument: %s", out)
		}
	})

	t.Run("new_str with no gutter in old_str is data, not transcription", func(t *testing.T) {
		// The model is inserting a markdown table row. old_str carries no
		// gutter, so there is no evidence it was copied out of a ws_read.
		in, _ := json.Marshal(map[string]any{
			"path": "a.md", "old_str": "| n | name |", "new_str": "| n | name |\n1 | Alpha",
		})
		if out, changed := ApplyTransform(TransformStripLineNumbers, string(in)); changed {
			t.Fatalf("a table row was mistaken for a ws_read gutter: %s", out)
		}
	})

	t.Run("new_str follows old_str when old_str proves a ws_read copy", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{
			"path": "a.go", "old_str": "   12| func Add(a, b int) int {",
			"new_str": "   12| func Add(a, b int64) int64 {",
		})
		out, changed := ApplyTransform(TransformStripLineNumbers, string(in))
		if !changed {
			t.Fatal("a mirrored ws_read gutter must be stripped from both halves")
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got["old_str"] != "func Add(a, b int) int {" {
			t.Fatalf("old_str = %q", got["old_str"])
		}
		if got["new_str"] != "func Add(a, b int64) int64 {" {
			t.Fatalf("new_str = %q", got["new_str"])
		}
	})

	t.Run("patch hunks still get the gutter stripped", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{
			"path":  "a.go",
			"patch": "<<<<<<< SEARCH\n     1|package a\n=======\npackage b\n>>>>>>> REPLACE",
		})
		out, changed := ApplyTransform(TransformStripLineNumbers, string(in))
		if !changed {
			t.Fatalf("a patch body's gutter must still be stripped: %s", out)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if s, _ := got["patch"].(string); s != "<<<<<<< SEARCH\npackage a\n=======\npackage b\n>>>>>>> REPLACE" {
			t.Fatalf("patch = %q", s)
		}
	})
}
