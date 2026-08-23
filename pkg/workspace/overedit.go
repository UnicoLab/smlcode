package workspace

import (
	"fmt"
	"strings"
)

// OverEditThreshold is the max fraction of a file an edit may replace.
const OverEditThreshold = 0.72

// MinOverEditBytes only enforces the ratio on files larger than this.
const MinOverEditBytes = 400

// MinOverEditLines exempts genuinely small files. A 20-line helper whose one
// function IS 80% of the file has no legal "smaller surgical edit" — the old
// guard simply had no legal move there and models looped against it forever.
const MinOverEditLines = 40

// AssessOverEdit refuses whole-file-style edits that destroy surgical quality.
// Returns "" when the edit is acceptable.
func AssessOverEdit(fileText, oldStr, newStr string) string {
	return assessOverEdit(fileText, oldStr, newStr, "ws_edit")
}

// AssessOverEditFor is AssessOverEdit with the calling tool named in the
// refusal, so ws_patch gets patch-shaped advice. ws_patch used to sail past
// this guard entirely, which made "rewrite the file as a diff" the path of
// least resistance.
func AssessOverEditFor(tool, fileText, oldStr, newStr string) string {
	return assessOverEdit(fileText, oldStr, newStr, tool)
}

func assessOverEdit(fileText, oldStr, newStr, tool string) string {
	if fileText == "" {
		return ""
	}
	if strings.TrimSpace(oldStr) == "" {
		// An empty / whitespace-only search span is not an over-edit, it is a
		// malformed edit. editFile rejects it with a specific message; return
		// "" here so the specific message is the one the model sees.
		return ""
	}
	if len(fileText) < MinOverEditBytes {
		return ""
	}
	if strings.Count(fileText, "\n")+1 <= MinOverEditLines {
		return ""
	}
	if !strings.Contains(fileText, oldStr) {
		// The exact span is not present; the fallback ladder may still match a
		// slightly different span. Measure conservatively on length alone.
		if float64(len(oldStr))/float64(len(fileText)) < OverEditThreshold {
			return ""
		}
	}
	ratio := float64(len(oldStr)) / float64(len(fileText))
	if ratio < OverEditThreshold {
		return ""
	}
	// Allow replacing a stub-heavy span with much richer code up to 90%.
	if ratio < 0.90 && len(newStr) > len(oldStr)*2 && looksStubHeavy(oldStr) {
		return ""
	}
	alt := "ws_edit a unique 2–10 line span"
	if tool == "ws_patch" {
		alt = "a single-hunk unified diff covering only the lines that change"
	}
	return fmt.Sprintf(
		"Over-edit refused — the replaced span covers %.0f%% of %s (%d/%d bytes). "+
			"Whole-file rewrites collapse quality on small models.\n\n"+
			"DO THIS INSTEAD:\n"+
			"1. ws_read the file with offset/limit to see just the region you need\n"+
			"2. Pick the SMALLEST span that must change (one function, one block)\n"+
			"3. Use %s, including 2–3 unchanged context lines for uniqueness\n"+
			"If the change really does span the whole file, make it as 3–4 separate "+
			"smaller edits instead of one. Do NOT retry the same span.",
		ratio*100, tool, len(oldStr), len(fileText), alt,
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
