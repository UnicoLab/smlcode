package workspace

import (
	"fmt"
	"strings"
)

// OverEditThreshold is the max fraction of a file an ws_edit may replace.
const OverEditThreshold = 0.72

// MinOverEditBytes only enforces the ratio on files larger than this.
const MinOverEditBytes = 400

// AssessOverEdit refuses whole-file-style edits that destroy surgical quality.
func AssessOverEdit(fileText, oldStr, newStr string) string {
	if fileText == "" || oldStr == "" {
		return ""
	}
	if len(fileText) < MinOverEditBytes {
		return ""
	}
	if !strings.Contains(fileText, oldStr) {
		return ""
	}
	ratio := float64(len(oldStr)) / float64(len(fileText))
	if ratio < OverEditThreshold {
		return ""
	}
	// Allow replacing a stub-heavy span with much richer code up to 90%.
	if ratio < 0.90 && len(newStr) > len(oldStr)*2 && looksStubHeavy(oldStr) {
		return ""
	}
	return fmt.Sprintf(
		"Over-edit refused — old_str covers %.0f%% of the file (%d/%d bytes). "+
			"Make a SMALLER surgical edit (one function / few lines). "+
			"ws_read with offset/limit, then ws_edit a unique 2–10 line span. "+
			"Whole-file rewrites collapse quality — do not retry the same span.",
		ratio*100, len(oldStr), len(fileText),
	)
}

func looksStubHeavy(s string) bool {
	lower := strings.ToLower(s)
	stubs := 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		tl := strings.ToLower(t)
		if t == "pass" || t == "..." || strings.HasPrefix(tl, "pass ") ||
			strings.Contains(tl, "notimplemented") || strings.Contains(tl, "todo") {
			stubs++
		}
	}
	lines := strings.Count(s, "\n") + 1
	return stubs >= 2 || (stubs > 0 && lines <= 8) || strings.Contains(lower, "notimplemented")
}
