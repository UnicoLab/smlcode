package repair

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ExtractJSON returns the first balanced JSON object/array in s, or "".
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "JSON")
		rest = strings.TrimLeft(rest, "\n\r ")
		if j := strings.Index(rest, "```"); j >= 0 {
			s = rest[:j]
		}
	}
	s = strings.TrimSpace(s)
	for _, open := range []byte{'{', '['} {
		start := strings.IndexByte(s, open)
		if start < 0 {
			continue
		}
		if out := balance(s[start:]); out != "" {
			return out
		}
	}
	return ""
}

func balance(s string) string {
	if s == "" {
		return ""
	}
	open := s[0]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		r := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if r == '\\' {
				esc = true
				continue
			}
			if r == '"' {
				inStr = false
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				cand := s[:i+1]
				if json.Valid([]byte(cand)) {
					return cand
				}
				return ""
			}
		}
	}
	return ""
}

// RepairJSON attempts common SLM JSON fixes: trailing commas, single quotes,
// bare keys, truncated closing braces, python bools, markdown fences, and
// extraneous text before/after the JSON object.
func RepairJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty json")
	}
	if extracted := ExtractJSON(raw); extracted != "" {
		raw = extracted
	}
	// Apply SLM-specific pre-fixes
	raw = fixPythonBools(raw)
	raw = truncateAfterClosingBrace(raw)
	if json.Valid([]byte(raw)) {
		return raw, nil
	}
	candidates := []string{
		raw,
		stripTrailingCommas(raw),
		singleToDoubleQuotes(raw),
		stripTrailingCommas(singleToDoubleQuotes(raw)),
		closeOpenBraces(raw),
		closeOpenBraces(stripTrailingCommas(raw)),
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		c = fixPythonBools(c)
		if json.Valid([]byte(c)) {
			return c, nil
		}
		if extracted := ExtractJSON(c); extracted != "" {
			extracted = fixPythonBools(extracted)
			if json.Valid([]byte(extracted)) {
				return extracted, nil
			}
		}
	}
	return "", fmt.Errorf("unrepairable json")
}

// fixPythonBools converts Python-style True/False/None to JSON true/false/null.
func fixPythonBools(s string) string {
	// Only replace whole-word matches to avoid false positives
	s = regexp.MustCompile(`\bTrue\b`).ReplaceAllString(s, "true")
	s = regexp.MustCompile(`\bFalse\b`).ReplaceAllString(s, "false")
	s = regexp.MustCompile(`\bNone\b`).ReplaceAllString(s, "null")
	return s
}

// truncateAfterClosingBrace removes extraneous text after the final closing
// brace or bracket. SLMs often append explanations after the JSON.
func truncateAfterClosingBrace(s string) string {
	// Find the last complete JSON object or array
	depth := 0
	lastClose := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				lastClose = i
			}
		}
	}
	if lastClose > 0 && lastClose < len(s)-1 {
		cand := strings.TrimSpace(s[:lastClose+1])
		if json.Valid([]byte(cand)) {
			return cand
		}
	}
	return s
}

// RepairAndUnmarshal repairs then unmarshals into dest.
func RepairAndUnmarshal(raw string, dest any) error {
	fixed, err := RepairJSON(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(fixed), dest)
}

// RepairToolArgs repairs tool-call argument JSON for weak SLMs.
func RepairToolArgs(args string) (string, error) {
	fixed, err := RepairJSON(args)
	if err != nil {
		// Last resort: wrap bare key=value prose into an object when it looks like path edits.
		if m := pathEditFallback(args); m != "" {
			return m, nil
		}
		return "", err
	}
	return fixed, nil
}

var (
	trailingCommaRE = regexp.MustCompile(`,\s*([}\]])`)
	// danglingCommaRE catches a comma the model never got to follow up on:
	// `{"path":"a.go","old_str":"x",` truncated by max_tokens right after a
	// complete pair. Dropping it recovers the object without inventing content.
	danglingCommaRE = regexp.MustCompile(`,\s*$`)
)

func stripTrailingCommas(s string) string {
	prev := ""
	for s != prev {
		prev = s
		s = trailingCommaRE.ReplaceAllString(s, "$1")
	}
	return danglingCommaRE.ReplaceAllString(s, "")
}

func singleToDoubleQuotes(s string) string {
	// Only rewrite when double quotes are absent (avoid mangling valid JSON).
	if strings.Contains(s, `"`) {
		return s
	}
	return strings.ReplaceAll(s, `'`, `"`)
}

func closeOpenBraces(s string) string {
	opens := strings.Count(s, "{") - strings.Count(s, "}")
	opensArr := strings.Count(s, "[") - strings.Count(s, "]")
	var b strings.Builder
	b.WriteString(s)
	for opensArr > 0 {
		b.WriteByte(']')
		opensArr--
	}
	for opens > 0 {
		b.WriteByte('}')
		opens--
	}
	return b.String()
}

func pathEditFallback(args string) string {
	args = strings.TrimSpace(args)
	// path=foo.go old=a new=b style (common SLM slip)
	if !strings.Contains(args, "path=") && !strings.Contains(args, "path:") {
		return ""
	}
	m := map[string]string{}
	for _, part := range splitKV(args) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			k, v, ok = strings.Cut(part, ":")
		}
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.Trim(k, `"'`))
		v = strings.TrimSpace(strings.Trim(v, `"'`))
		if k != "" {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func splitKV(s string) []string {
	// Split on commas / newlines that look like separators.
	s = strings.ReplaceAll(s, "\n", ",")
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
