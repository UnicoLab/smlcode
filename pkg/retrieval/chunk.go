package retrieval

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Chunking limits.
const (
	// MaxChunkBytes is the ceiling for one retrievable chunk. A whole 24 KB
	// INDEX.md as a single chunk produces an embedding that is the average of
	// 40 unrelated summaries — a vector that means nothing and matches nothing.
	MaxChunkBytes = 1500
	// MinChunkBytes drops fragments too small to carry meaning. Measured on
	// the heading+body text that is actually embedded, not on the body alone.
	MinChunkBytes = 24
)

// SplitSections breaks a markdown document into retrievable chunks on `## `
// headings, then splits any section still over MaxChunkBytes on paragraph
// boundaries. Each chunk keeps its heading so the embedding has a topic.
func SplitSections(id, source, text, query string) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []Chunk
	n := 0
	emit := func(heading, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		for _, piece := range splitToSize(body, MaxChunkBytes) {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			text := piece
			if heading != "" && !strings.HasPrefix(piece, heading) {
				text = heading + "\n" + piece
			}
			// The floor applies to what is actually embedded.
			if len(text) < MinChunkBytes {
				continue
			}
			out = append(out, Chunk{
				ID:      id + "#" + itoa(n),
				Source:  source,
				Text:    textutil.Clip(text, MaxChunkBytes),
				Query:   query,
				Heading: strings.TrimSpace(strings.TrimPrefix(heading, "## ")),
			})
			n++
		}
	}

	lines := strings.Split(text, "\n")
	heading := ""
	var buf strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			emit(heading, buf.String())
			buf.Reset()
			heading = strings.TrimSpace(line)
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	emit(heading, buf.String())
	return out
}

// splitToSize breaks a body into <=max-byte pieces at paragraph, then line,
// then rune boundaries.
func splitToSize(body string, max int) []string {
	if len(body) <= max {
		return []string{body}
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	for _, para := range strings.Split(body, "\n\n") {
		if len(para) > max {
			flush()
			for _, line := range strings.Split(para, "\n") {
				if cur.Len()+len(line)+1 > max {
					flush()
				}
				if len(line) > max {
					// Single monster line: hard-clip on rune boundaries.
					rest := line
					for len(rest) > max {
						out = append(out, textutil.Clip(rest, max))
						cut := len(textutil.Clip(rest, max))
						rest = rest[cut:]
					}
					if strings.TrimSpace(rest) != "" {
						cur.WriteString(rest)
						cur.WriteByte('\n')
					}
					continue
				}
				cur.WriteString(line)
				cur.WriteByte('\n')
			}
			continue
		}
		if cur.Len()+len(para)+2 > max {
			flush()
		}
		cur.WriteString(para)
		cur.WriteString("\n\n")
	}
	flush()
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
