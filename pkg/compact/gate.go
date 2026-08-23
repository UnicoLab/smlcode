package compact

import (
	"regexp"
	"strings"
)

// Acceptance gate thresholds for LLM/auto document compaction.
//
// The summarizer here is a LOCAL SMALL MODEL. A 7B asked to "compress this
// context" will happily answer "Sure! Here is the compressed context:" and
// stop, or return three bullet points for a 30 KB document. The caller then
// overwrites CONTEXT.md permanently. Every compacted result must clear these
// gates before it is allowed to replace real project memory.
const (
	// MinRetentionDivisor requires the output to be at least before/10 bytes.
	MinRetentionDivisor = 10
	// MinPathRetentionPercent requires this share of `path/like.go` tokens
	// present in the input to survive into the output.
	MinPathRetentionPercent = 60
)

var (
	preambleRe = regexp.MustCompile(`(?i)^(sure|here is|here's|okay|ok|certainly|of course|absolutely)[^\n]*\n`)
	// Backtick-quoted things that look like file paths: contain a dot or slash.
	pathTokenRe = regexp.MustCompile("`([A-Za-z0-9_./\\-]+\\.[A-Za-z0-9_]+|[A-Za-z0-9_.\\-]+/[A-Za-z0-9_./\\-]+)`")
)

// GateFailure names the acceptance gate a compaction result failed.
type GateFailure string

// Gate failure reasons.
const (
	GateOK          GateFailure = ""
	GateEmpty       GateFailure = "empty output"
	GateTooShort    GateFailure = "output under 1/10 of input"
	GateNoHeading   GateFailure = "no '## ' heading"
	GateLostPaths   GateFailure = "dropped too many file paths"
	GateNotSmaller  GateFailure = "output not smaller than input"
	GateNoSummarize GateFailure = "no summarizer configured"
)

// StripPreamble removes a leading chat preamble line ("Sure! Here is …").
func StripPreamble(out string) string {
	out = strings.TrimSpace(out)
	for i := 0; i < 2; i++ {
		stripped := preambleRe.ReplaceAllString(out, "")
		if stripped == out {
			break
		}
		out = strings.TrimSpace(stripped)
	}
	// A fenced block wrapping the whole answer is also preamble noise.
	if strings.HasPrefix(out, "```") {
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			body := out[i+1:]
			if j := strings.LastIndex(body, "```"); j >= 0 {
				out = strings.TrimSpace(body[:j])
			}
		}
	}
	return out
}

// PathTokens returns the set of backtick-quoted path-like tokens in s.
func PathTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range pathTokenRe.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 && m[1] != "" {
			out[m[1]] = true
		}
	}
	return out
}

// AcceptCompaction reports whether a summarizer's output is safe to persist in
// place of the input. It returns GateOK when every gate passes.
func AcceptCompaction(input, output string) GateFailure {
	output = strings.TrimSpace(output)
	if output == "" {
		return GateEmpty
	}
	before := len(strings.TrimSpace(input))
	if before == 0 {
		return GateOK
	}
	if len(output) >= before {
		return GateNotSmaller
	}
	if len(output) < before/MinRetentionDivisor {
		return GateTooShort
	}
	if !strings.Contains(output, "## ") && !strings.HasPrefix(output, "## ") {
		return GateNoHeading
	}
	want := PathTokens(input)
	if len(want) > 0 {
		kept := 0
		for p := range want {
			if strings.Contains(output, p) {
				kept++
			}
		}
		if kept*100 < len(want)*MinPathRetentionPercent {
			return GateLostPaths
		}
	}
	return GateOK
}
