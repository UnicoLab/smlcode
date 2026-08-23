package repair

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// Rung names, in the order the ladder tries them. The name of the rung that
// fixed a document is returned by Repair so the harness can learn which failure
// mode its current model actually has.
const (
	RungNone         = "none"    // already valid JSON
	RungFence        = "fence"   // ```json … ``` wrapper removed
	RungExtract      = "extract" // balanced object/array carved out of prose
	RungTrailingComa = "trailing_comma"
	RungQuotes       = "quotes"        // 'single' → "double"
	RungPyLiterals   = "python_bools"  // True/False/None → true/false/null
	RungControlChars = "control_chars" // raw newline/tab inside a string escaped
	RungCloseBraces  = "close_braces"  // missing } / ] appended
	RungCoerce       = "coerce"        // schema-driven type coercion
	RungFailed       = "failed"        // nothing worked
	RungTruncated    = "truncated"     // cut off mid-string by max_tokens
)

// Rungs lists the ladder in order, for reporting.
var Rungs = []string{
	RungNone, RungFence, RungExtract, RungTrailingComa, RungQuotes,
	RungPyLiterals, RungControlChars, RungCloseBraces, RungCoerce,
}

// ErrTruncated means the model's JSON was cut off mid-string — almost always
// because it hit max_tokens.
//
// It is reported distinctly because the correct response is to raise max_tokens
// or re-ask, never to guess the missing text: a truncated string has no
// recoverable content, and appending closing braces to it produces a document
// that parses but lies.
var ErrTruncated = errors.New("repair: json truncated mid-string (raise max_tokens or re-ask)")

// ErrUnrepairable means the ladder ran out of rungs.
var ErrUnrepairable = errors.New("repair: unrepairable json")

// Repair walks the ordered repair ladder over raw and returns the first
// document that parses, the name of the rung that fixed it, and an error only
// when nothing worked.
//
// When spec carries a schema (a non-empty Spec.Schema), the parsed document is
// additionally coerced toward it — "true" becomes true, "3" becomes 3, a bare
// scalar becomes a one-element array — and the returned rung gains a "+coerce"
// suffix when that changed anything. Pass the zero Spec to skip coercion.
func Repair(raw string, spec schema.Spec) (fixed []byte, rung string, err error) {
	fixed, rung, err = repairLadder(raw, spec)
	Stats.Record(rung, err)
	return fixed, rung, err
}

func repairLadder(raw string, spec schema.Spec) ([]byte, string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, RungFailed, fmt.Errorf("%w: empty output", ErrUnrepairable)
	}

	// Rung 0 — already valid.
	if out, ok := finish(s, spec, RungNone); ok {
		return out.body, out.rung, nil
	}

	// Rung 1 — markdown fences.
	if f := stripFences(s); f != s {
		s = f
		if out, ok := finish(s, spec, RungFence); ok {
			return out.body, out.rung, nil
		}
	}

	// Rung 2 — carve the balanced document out of surrounding prose.
	if span := firstOpenSpan(s); span != "" {
		// Keep the fragment either way, so the later rungs and the truncation
		// check work on the JSON-looking part rather than on the prose.
		s = span
		if bal := balance(span); bal != "" {
			if out, ok := finish(bal, spec, RungExtract); ok {
				return out.body, out.rung, nil
			}
		}
	}

	// Truncation is checked here, before any rung that would append closing
	// delimiters: closing the braces around a half-written string invents
	// content the model never produced.
	if truncatedMidString(s) {
		return nil, RungTruncated, ErrTruncated
	}

	// Rungs 3-7 — cumulative textual fixes.
	steps := []struct {
		name string
		fn   func(string) string
	}{
		{RungTrailingComa, stripTrailingCommas},
		{RungQuotes, singleToDoubleQuotes},
		{RungPyLiterals, fixPythonBools},
		{RungControlChars, escapeControlCharsInStrings},
		{RungCloseBraces, closeOpenBraces},
	}
	for _, step := range steps {
		next := step.fn(s)
		if next == s {
			continue
		}
		s = next
		if out, ok := finish(s, spec, step.name); ok {
			return out.body, out.rung, nil
		}
		// A textual fix can expose a newly balanced span. Balance from the FIRST
		// opening delimiter only: falling back to an inner array would return
		// `["a"]` for a truncated `{"summary":"s","steps":["a"],`, silently
		// discarding the object the caller asked for.
		if e := balance(firstOpenSpan(s)); e != "" && e != s {
			if out, ok := finish(e, spec, step.name); ok {
				return out.body, out.rung, nil
			}
		}
	}

	if truncatedMidString(s) {
		return nil, RungTruncated, ErrTruncated
	}
	return nil, RungFailed, fmt.Errorf("%w (last candidate: %s)", ErrUnrepairable, truncateForError(s))
}

type ladderResult struct {
	body []byte
	rung string
}

// finish validates the candidate and, when a schema is supplied, coerces it.
func finish(candidate string, spec schema.Spec, rung string) (ladderResult, bool) {
	candidate = strings.TrimSpace(candidate)
	if !json.Valid([]byte(candidate)) {
		return ladderResult{}, false
	}
	if len(spec.Schema) == 0 {
		return ladderResult{body: []byte(candidate), rung: rung}, true
	}
	coerced, err := schema.CoerceSpec(spec, []byte(candidate))
	if err != nil {
		return ladderResult{body: []byte(candidate), rung: rung}, true
	}
	if sameJSON(candidate, string(coerced)) {
		return ladderResult{body: []byte(candidate), rung: rung}, true
	}
	return ladderResult{body: coerced, rung: rung + "+" + RungCoerce}, true
}

func sameJSON(a, b string) bool {
	var av, bv any
	if json.Unmarshal([]byte(a), &av) != nil || json.Unmarshal([]byte(b), &bv) != nil {
		return a == b
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

// RepairRole is Repair against a registered schema role id.
func RepairRole(raw, role string) (fixed []byte, rung string, err error) {
	spec, ok := schema.For(role)
	if !ok {
		return Repair(raw, schema.Spec{})
	}
	return Repair(raw, spec)
}

// RepairInto repairs raw against a role's schema and unmarshals into dest.
func RepairInto(raw, role string, dest any) (rung string, err error) {
	fixed, rung, err := RepairRole(raw, role)
	if err != nil {
		return rung, err
	}
	return rung, json.Unmarshal(fixed, dest)
}

// ---------------------------------------------------------------------------
// Rung implementations
// ---------------------------------------------------------------------------

// stripFences removes a ```json … ``` (or bare ```) wrapper.
func stripFences(s string) string {
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	rest := s[i+3:]
	for _, tag := range []string{"json", "JSON", "Json"} {
		rest = strings.TrimPrefix(rest, tag)
	}
	rest = strings.TrimLeft(rest, "\r\n ")
	if j := strings.Index(rest, "```"); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	// Unterminated fence — the model ran out of tokens before closing it.
	return strings.TrimSpace(rest)
}

// firstOpenSpan returns the text from the first { or [ onwards.
func firstOpenSpan(s string) string {
	i := strings.IndexAny(s, "{[")
	if i < 0 {
		return ""
	}
	return s[i:]
}

// escapeControlCharsInStrings escapes raw newlines, tabs and other control
// bytes that appear inside a JSON string literal. Models writing a multi-line
// `summary` or a code snippet into `new_str` produce these constantly.
func escapeControlCharsInStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inStr {
			if c == '"' {
				inStr = true
			}
			b.WriteByte(c)
			continue
		}
		if esc {
			esc = false
			b.WriteByte(c)
			continue
		}
		switch {
		case c == '\\':
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = false
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// truncatedMidString reports whether s ends inside an unterminated string
// literal — the signature of a completion cut off by max_tokens.
func truncatedMidString(s string) bool {
	inStr := false
	esc := false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	// An unterminated string is truncation. So is a dangling escape.
	return inStr || esc
}

func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 160 {
		return s
	}
	return s[:160] + "…"
}

// ---------------------------------------------------------------------------
// Telemetry
// ---------------------------------------------------------------------------

// Counter records which rung fixed each document. The distribution is the
// cheapest available signal about a model's actual failure mode: a spike in
// close_braces means max_tokens is too low, a spike in extract means the
// prompt's output contract is not landing, and a spike in truncated means both.
type Counter struct {
	mu      sync.Mutex
	rungs   map[string]int
	total   int
	failed  int
	cutoff  int
	coerced int
}

// Stats is the process-wide counter Repair writes to.
var Stats = &Counter{}

// Record folds one repair outcome into the counter.
func (c *Counter) Record(rung string, err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rungs == nil {
		c.rungs = map[string]int{}
	}
	c.total++
	c.rungs[rung]++
	switch {
	case errors.Is(err, ErrTruncated):
		c.cutoff++
	case err != nil:
		c.failed++
	}
	if strings.HasSuffix(rung, "+"+RungCoerce) {
		c.coerced++
	}
}

// Snapshot returns a copy of the per-rung counts.
func (c *Counter) Snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.rungs))
	for k, v := range c.rungs {
		out[k] = v
	}
	return out
}

// Totals returns the aggregate counts: documents seen, unrepairable ones,
// truncated ones, and those that needed schema coercion.
func (c *Counter) Totals() (total, failed, truncated, coerced int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total, c.failed, c.cutoff, c.coerced
}

// Top returns the rung that fired most often, excluding clean passes.
func (c *Counter) Top() (string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	best, n := "", 0
	keys := make([]string, 0, len(c.rungs))
	for k := range c.rungs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == RungNone {
			continue
		}
		if c.rungs[k] > n {
			best, n = k, c.rungs[k]
		}
	}
	return best, n
}

// Reset clears the counter.
func (c *Counter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rungs = map[string]int{}
	c.total, c.failed, c.cutoff, c.coerced = 0, 0, 0, 0
}

// Report renders a stable, sorted one-line-per-rung summary.
func (c *Counter) Report() []string {
	snap := c.Snapshot()
	total, failed, truncated, coerced := c.Totals()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys)+1)
	out = append(out, fmt.Sprintf("total=%d failed=%d truncated=%d coerced=%d", total, failed, truncated, coerced))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("  %s=%d", k, snap[k]))
	}
	return out
}
