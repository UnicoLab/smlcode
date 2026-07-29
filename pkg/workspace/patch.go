package workspace

import (
	"fmt"
	"strings"
)

// ApplyPatch applies a SEARCH/REPLACE block or a simplified unified diff to content.
// Supported forms:
//  1. <<<<<<< SEARCH ... ======= ... >>>>>>> REPLACE
//  2. @@ hunks with leading -/+ lines (context lines optional with space prefix)
//  3. Plain old→new via first "---\n+++\n" style when only one hunk of +/- lines
func ApplyPatch(content, patch string) (next string, summary string, err error) {
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	patch = strings.TrimSpace(patch)
	if patch == "" {
		return "", "", fmt.Errorf("empty patch")
	}

	if strings.Contains(patch, "<<<<<<<") && strings.Contains(patch, ">>>>>>>") {
		return applySearchReplace(content, patch)
	}
	if strings.Contains(patch, "=======") && (strings.Contains(patch, "SEARCH") || strings.Contains(patch, "REPLACE")) {
		return applySearchReplace(content, patch)
	}
	// SEARCH / REPLACE without git conflict markers
	if i := strings.Index(patch, "\n=======\n"); i >= 0 {
		oldPart := strings.TrimPrefix(patch, "SEARCH\n")
		if j := strings.Index(oldPart, "\n=======\n"); j >= 0 {
			oldStr := strings.TrimPrefix(oldPart[:j], "SEARCH\n")
			rest := oldPart[j+len("\n=======\n"):]
			rest = strings.TrimPrefix(rest, "REPLACE\n")
			rest = strings.TrimSuffix(rest, "\n>>>>>>> REPLACE")
			rest = strings.TrimSuffix(rest, ">>>>>>> REPLACE")
			return applyExact(content, strings.TrimRight(oldStr, "\n"), strings.TrimRight(rest, "\n"))
		}
	}
	if strings.Contains(patch, "@@") || strings.HasPrefix(patch, "---") || looksLikeDiff(patch) {
		return applyUnifiedHunks(content, patch)
	}
	return "", "", fmt.Errorf("unrecognized patch format — use SEARCH/REPLACE or unified diff hunks")
}

func applySearchReplace(content, patch string) (string, string, error) {
	start := strings.Index(patch, "<<<<<<<")
	mid := strings.Index(patch, "=======")
	end := strings.Index(patch, ">>>>>>>")
	if start < 0 || mid < 0 || end < 0 || mid < start || end < mid {
		return "", "", fmt.Errorf("malformed SEARCH/REPLACE markers")
	}
	oldBlock := patch[start:mid]
	// drop marker line
	if i := strings.Index(oldBlock, "\n"); i >= 0 {
		oldBlock = oldBlock[i+1:]
	} else {
		oldBlock = ""
	}
	newBlock := patch[mid:end]
	if i := strings.Index(newBlock, "\n"); i >= 0 {
		newBlock = newBlock[i+1:]
	} else {
		newBlock = ""
	}
	oldBlock = strings.TrimRight(oldBlock, "\n")
	newBlock = strings.TrimRight(newBlock, "\n")
	return applyExact(content, oldBlock, newBlock)
}

func applyExact(content, oldStr, newStr string) (string, string, error) {
	if oldStr == "" {
		if content != "" {
			return "", "", fmt.Errorf("empty SEARCH only valid for new empty files")
		}
		return newStr, fmt.Sprintf("create %d bytes", len(newStr)), nil
	}
	if !strings.Contains(content, oldStr) {
		return "", "", fmt.Errorf("SEARCH block not found in file")
	}
	next := strings.Replace(content, oldStr, newStr, 1)
	return next, diffSnippet(oldStr, newStr), nil
}

func looksLikeDiff(patch string) bool {
	hasMinus, hasPlus := false, false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			hasPlus = true
		}
	}
	return hasMinus || hasPlus
}

func applyUnifiedHunks(content, patch string) (string, string, error) {
	var oldParts, newParts []string
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "@@"), line == "\\ No newline at end of file":
			continue
		case strings.HasPrefix(line, "-"):
			oldParts = append(oldParts, line[1:])
		case strings.HasPrefix(line, "+"):
			newParts = append(newParts, line[1:])
		case strings.HasPrefix(line, " "):
			oldParts = append(oldParts, line[1:])
			newParts = append(newParts, line[1:])
		default:
			// bare context
			if line != "" {
				oldParts = append(oldParts, line)
				newParts = append(newParts, line)
			}
		}
	}
	oldStr := strings.Join(oldParts, "\n")
	newStr := strings.Join(newParts, "\n")
	if oldStr == "" && newStr == "" {
		return "", "", fmt.Errorf("diff hunk produced empty change")
	}
	if oldStr == "" {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + newStr, fmt.Sprintf("+%d lines", len(newParts)), nil
	}
	if !strings.Contains(content, oldStr) {
		// try without requiring full context match — unique minus-only block
		minusOnly := []string{}
		for _, line := range strings.Split(patch, "\n") {
			if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				minusOnly = append(minusOnly, line[1:])
			}
		}
		oldStr = strings.Join(minusOnly, "\n")
		plusOnly := []string{}
		for _, line := range strings.Split(patch, "\n") {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				plusOnly = append(plusOnly, line[1:])
			}
		}
		newStr = strings.Join(plusOnly, "\n")
		if oldStr == "" || !strings.Contains(content, oldStr) {
			return "", "", fmt.Errorf("diff context not found in file")
		}
	}
	return applyExact(content, oldStr, newStr)
}

func truncateSnippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func diffSnippet(oldStr, newStr string) string {
	o := truncateSnippet(oldStr, 160)
	n := truncateSnippet(newStr, 160)
	if o == "" {
		return "+ " + n
	}
	return "- " + o + "\n+ " + n
}
