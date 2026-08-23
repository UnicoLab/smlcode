package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// TokenCounter is re-exported so callers do not have to import pkg/context
// just to pass a counter into Options.
type TokenCounter = contextstore.TokenCounter

// bytesPerToken is the conversion used to turn a token budget into a byte
// budget before a precise (and more expensive) token check.
const bytesPerToken = contextstore.FallbackCharsPerToken

func countTokens(count TokenCounter, s string) int {
	if count == nil {
		count = contextstore.DefaultTokenCounter
	}
	return count(s)
}

// fitToTokens trims s so that it costs at most budget tokens. It converts to a
// byte budget first (cheap) and only then verifies with the counter, shrinking
// proportionally at most a few times. Returns "" for a non-positive budget.
func fitToTokens(s string, budget int, count TokenCounter) string {
	if budget <= 0 || strings.TrimSpace(s) == "" {
		return ""
	}
	out := s
	if len(out) > budget*bytesPerToken*2 {
		out = textutil.Truncate(out, budget*bytesPerToken*2, "\n…\n")
	}
	for i := 0; i < 4; i++ {
		got := countTokens(count, out)
		if got <= budget {
			return out
		}
		ratio := float64(budget) / float64(got)
		next := int(float64(len(out)) * ratio * 0.92)
		if next < 16 {
			return ""
		}
		out = textutil.Truncate(out, next, "\n…\n")
	}
	return out
}

// clip shortens s to at most n bytes without splitting a rune.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return textutil.Clip(s, n)
}

// firstLine returns the first meaningful line of s, capped at n bytes.
func firstLine(s string, n int) string { return textutil.FirstLine(s, n) }

// hashID returns a short stable id for the joined parts.
func hashID(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

// dedupe returns in with blanks and duplicates removed, order preserved, and
// at most max entries (the FIRST max are kept — callers pass most-salient
// first).
func dedupe(in []string, max int) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// tailStrings keeps the last max entries of in.
func tailStrings(in []string, max int) []string {
	if max <= 0 || len(in) <= max {
		return in
	}
	return in[len(in)-max:]
}

// recency is a gentle 1/(1+age) decay used to rank older memories lower
// without ever zeroing them out. halfLife is in days.
func recency(at, now time.Time, halfLifeDays float64) float64 {
	if at.IsZero() || now.IsZero() || halfLifeDays <= 0 {
		return 1
	}
	age := now.Sub(at).Hours() / 24
	if age <= 0 {
		return 1
	}
	return 1 / (1 + age/halfLifeDays)
}

// sortedKeys returns map keys in deterministic order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
