package repair

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// ── The repair layer meets the worst of what a model says ────────────────
//
// This package exists BECAUSE model JSON is malformed: it closes open braces,
// strips trailing commas, converts Python booleans and single quotes, and
// truncates after the closing brace. Every one of those runs on a string the
// harness did not produce and cannot constrain.
//
// A panic here is worse than elsewhere: repair is the last thing between a bad
// answer and a usable one, so it runs on exactly the inputs nothing else could
// handle.

func hostile() []string {
	return []string{
		"", " ", "\n", "\x00", "\xff\xfe", "no json here",
		"{", "}", "[", "]", "{{{{", "}}}}", "[[[[", "]]]]",
		`{"a":`, `{"a":1,`, `{"a":1,,}`, `{,}`, `{"a"}`, `{:1}`,
		`{'a': 1}`, `{"a": True}`, `{"a": False}`, `{"a": None}`,
		`{"a": 1,}`, `[1,2,3,]`, `{"a":"unterminated`,
		`{"a":"\"}`, `{"a":"\\"}`, `{"a":"}"}`, `{"a":"{"}`,
		// Braces inside strings must not be counted as structure.
		`{"charter":"we own {web} and {api}"}`,
		// Depth and size.
		strings.Repeat("{", 500), strings.Repeat("{\"a\":", 200) + "1",
		strings.Repeat("[", 1000) + strings.Repeat("]", 1000),
		strings.Repeat("x", 200000),
		// Text around JSON, which is the common real case.
		"Sure! Here is the JSON:\n```json\n{\"ok\":true}\n```\nLet me know.",
		"```\n{\"ok\":true}\n```",
		"{\"ok\":true} and then some trailing prose that should be dropped",
		// Invalid encodings.
		"{\"a\":\"\xc3(\"}", "{\"a\":\"\xed\xa0\x80\"}",
	}
}

func TestRepairNeverPanics(t *testing.T) {
	fns := map[string]func(string){
		"ExtractJSON":    func(s string) { _ = ExtractJSON(s) },
		"RepairJSON":     func(s string) { _, _ = RepairJSON(s) },
		"RepairToolArgs": func(s string) { _, _ = RepairToolArgs(s) },
		"RepairAndUnmarshal": func(s string) {
			var dest map[string]any
			_ = RepairAndUnmarshal(s, &dest)
		},
	}
	for name, fn := range fns {
		for i, in := range hostile() {
			t.Run(name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s panicked on input %d (%.50q): %v", name, i, in, r)
					}
				}()
				fn(in)
			})
		}
	}
}

// Repair either produces something json.Unmarshal accepts, or reports failure.
// Returning a nil error over a string that is still not JSON would make every
// caller's error check useless.
func TestSuccessfulRepairIsActuallyParseable(t *testing.T) {
	for _, in := range hostile() {
		out, err := RepairJSON(in)
		if err != nil {
			continue
		}
		var dest any
		if uerr := json.Unmarshal([]byte(out), &dest); uerr != nil {
			t.Errorf("RepairJSON(%.40q) reported success but produced unparseable JSON: %q (%v)",
				in, out, uerr)
		}
	}
}

// Repaired text goes into a prompt or a tool call. Invalid UTF-8 there is
// rejected by the provider.
func TestRepairedTextStaysValidUTF8(t *testing.T) {
	for _, in := range hostile() {
		if out, err := RepairJSON(in); err == nil && !utf8.ValidString(out) {
			t.Errorf("RepairJSON(%.40q) produced invalid UTF-8: %q", in, out)
		}
		if out, err := RepairToolArgs(in); err == nil && !utf8.ValidString(out) {
			t.Errorf("RepairToolArgs(%.40q) produced invalid UTF-8: %q", in, out)
		}
	}
}
