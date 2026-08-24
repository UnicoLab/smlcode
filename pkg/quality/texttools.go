package quality

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/repair"
)

// ExtractedCall is a tool call recovered from assistant prose (little-coder
// output-parser).
type ExtractedCall struct {
	Name   string
	Input  map[string]interface{}
	Format string // fenced | tag | bare | liquid
}

var (
	reFenceTool = regexp.MustCompile("(?s)```(?:tool|json)\\s*\\n(.*?)\\n```")
	reTagTool   = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)
	reBareTool  = regexp.MustCompile(`\{[^{}]*"name"\s*:\s*"(\w+)"[^{}]*\}`)
	reLiquid    = regexp.MustCompile(`(?s)<\|tool_call_start\|>(.*?)<\|tool_call_end\|>`)
	reLiquidFn  = regexp.MustCompile(`(\w+)\((.*)\)`)
)

// ParseTextToolCalls extracts structured tool calls from fenced/XML/bare/liquid text.
func ParseTextToolCalls(text string) []ExtractedCall {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var calls []ExtractedCall
	seen := map[string]bool{}
	add := func(c ExtractedCall) {
		if strings.TrimSpace(c.Name) == "" {
			return
		}
		key := c.Name + "|" + mustJSON(c.Input)
		if seen[key] {
			return
		}
		seen[key] = true
		calls = append(calls, c)
	}

	for _, m := range reLiquid.FindAllStringSubmatch(text, -1) {
		for _, fn := range parseLiquidCalls(m[1]) {
			fn.Format = "liquid"
			add(fn)
		}
	}
	for _, m := range reFenceTool.FindAllStringSubmatch(text, -1) {
		if c, ok := callFromJSON(m[1], "fenced"); ok {
			add(c)
		}
	}
	for _, m := range reTagTool.FindAllStringSubmatch(text, -1) {
		if c, ok := callFromJSON(m[1], "tag"); ok {
			add(c)
		}
	}
	if len(calls) == 0 {
		for _, m := range reBareTool.FindAllStringSubmatch(text, -1) {
			if c, ok := callFromJSON(m[0], "bare"); ok {
				add(c)
			}
		}
	}
	return calls
}

// TextToolNudge builds an arg-rich corrector issue (little-coder followUp).
func TextToolNudge(calls []ExtractedCall) string {
	if len(calls) == 0 {
		return CorrectionMessage("text_tool_calls:unknown")
	}
	liquidOnly := true
	for _, c := range calls {
		if c.Format != "liquid" {
			liquidOnly = false
			break
		}
	}
	if liquidOnly {
		return "Your model emitted Liquid/LFM2 Pythonic tool calls in text. " +
			"This harness expects native OpenAI-style tool calls. Re-issue as native " +
			"ws_* tool calls (not <|tool_call_start|> prose), then finish with status JSON."
	}
	var b strings.Builder
	b.WriteString("You embedded tool calls in text instead of using the native tool channel. ")
	b.WriteString("Re-issue these as NATIVE tool calls (not fenced ```tool / <tool_call>), ")
	b.WriteString("then finish with status JSON:\n")
	for i, c := range calls {
		if i >= 4 {
			b.WriteString("…\n")
			break
		}
		fmt.Fprintf(&b, "%d) %s(%s)\n", i+1, c.Name, summarizeInput(c.Input))
	}
	return strings.TrimSpace(b.String())
}

func callFromJSON(raw, format string) (ExtractedCall, bool) {
	fixed, err := repair.RepairJSON(raw)
	if err != nil {
		var m map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &m) != nil {
			return ExtractedCall{}, false
		}
		return extractNamed(m, format)
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(fixed), &m) != nil {
		return ExtractedCall{}, false
	}
	return extractNamed(m, format)
}

func extractNamed(m map[string]interface{}, format string) (ExtractedCall, bool) {
	name, _ := m["name"].(string)
	if name == "" {
		return ExtractedCall{}, false
	}
	input := map[string]interface{}{}
	for _, key := range []string{"input", "parameters", "args", "arguments"} {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case map[string]interface{}:
				input = t
			case string:
				var nested map[string]interface{}
				if repair.RepairAndUnmarshal(t, &nested) == nil {
					input = nested
				}
			}
			break
		}
	}
	// Flat form: {"name":"ws_edit","path":"...","old_str":"..."}
	if len(input) == 0 {
		for k, v := range m {
			if k == "name" || k == "id" {
				continue
			}
			input[k] = v
		}
	}
	return ExtractedCall{Name: name, Input: input, Format: format}, true
}

func parseLiquidCalls(body string) []ExtractedCall {
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "[")
	body = strings.TrimSuffix(body, "]")
	var out []ExtractedCall
	for _, part := range splitLiquidArgs(body) {
		part = strings.TrimSpace(part)
		m := reLiquidFn.FindStringSubmatch(part)
		if len(m) < 3 {
			continue
		}
		out = append(out, ExtractedCall{
			Name:  normalizeLiquidName(m[1]),
			Input: parseLiquidArgs(m[2]),
		})
	}
	return out
}

func normalizeLiquidName(name string) string {
	switch strings.ToLower(name) {
	case "read":
		return "ws_read"
	case "write":
		return "ws_write"
	case "edit":
		return "ws_edit"
	case "bash", "shell":
		return "ws_shell"
	case "grep":
		return "ws_grep"
	case "glob":
		return "ws_glob"
	default:
		return name
	}
}

func parseLiquidArgs(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// path='/a.c', pattern="x"
	parts := splitLiquidArgs(raw)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(p[:eq])
		v := strings.TrimSpace(p[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

func splitLiquidArgs(s string) []string {
	var parts []string
	var b strings.Builder
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote != 0 {
			b.WriteByte(ch)
			if ch == inQuote && (i == 0 || s[i-1] != '\\') {
				inQuote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			inQuote = ch
			b.WriteByte(ch)
		case '{', '[', '(':
			depth++
			b.WriteByte(ch)
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
			b.WriteByte(ch)
		case ',':
			if depth == 0 {
				parts = append(parts, b.String())
				b.Reset()
				continue
			}
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func summarizeInput(in map[string]interface{}) string {
	if len(in) == 0 {
		return "{}"
	}
	prefer := []string{"path", "old_str", "new_str", "command", "pattern", "patch", "content"}
	var parts []string
	for _, k := range prefer {
		if v, ok := in[k]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:60] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s=%q", k, s))
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		raw := mustJSON(in)
		if len(raw) > 80 {
			raw = raw[:80] + "…"
		}
		return raw
	}
	return strings.Join(parts, ", ")
}
