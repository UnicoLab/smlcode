package quality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// ClaimIssue is a hallucinated or unverifiable files_changed entry.
type ClaimIssue struct {
	Path   string
	Reason string
}

// CheckClaimedFiles verifies worker files_changed against disk.
// Giant LLMs often invent paths; this gate refuses "done" on fiction.
func CheckClaimedFiles(root string, t plan.Task) []ClaimIssue {
	if root == "" {
		return nil
	}
	// Only what the MODEL claimed. The harness appends its own stamped
	// sections to this same string before this gate runs, and the loose parser
	// matches any quoted source path anywhere in it — so without this the gate
	// convicts a worker of hallucinating a path the harness itself printed.
	claimed := extractClaimedPaths(StripHarnessSections(t.Output))
	if len(claimed) == 0 {
		return nil
	}
	var issues []ClaimIssue
	for _, p := range claimed {
		p = strings.TrimSpace(strings.TrimPrefix(p, "./"))
		if p == "" || strings.Contains(p, "..") {
			continue
		}
		abs := filepath.Join(root, p)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			issues = append(issues, ClaimIssue{
				Path:   p,
				Reason: "claimed in files_changed but missing on disk",
			})
			continue
		}
	}
	return issues
}

// FormatClaimsSection embeds claim failures for review/corrector.
func FormatClaimsSection(issues []ClaimIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n" + ClaimsSectionHeader + "\n")
	b.WriteString(SmokeFailedMarker + " — hallucinated or missing paths in files_changed:\n")
	for _, is := range issues {
		fmt.Fprintf(&b, "- %s: %s\n", is.Path, is.Reason)
	}
	b.WriteString("Fix: only list paths you actually wrote/edited; reconcile with disk.\n")
	return b.String()
}

// ClaimsFailedInOutput reports a failed claims section.
func ClaimsFailedInOutput(output string) bool {
	return strings.Contains(output, ClaimsSectionHeader) &&
		strings.Contains(output, SmokeFailedMarker)
}

func extractClaimedPaths(output string) []string {
	raw := extractJSONObjectLoose(output)
	if raw == "" {
		return parseFilesChangedLoose(output)
	}
	// Prefer JSON files_changed when parseable.
	var payload struct {
		FilesChanged []string `json:"files_changed"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err == nil && len(payload.FilesChanged) > 0 {
		return payload.FilesChanged
	}
	return parseFilesChangedLoose(output)
}

func extractJSONObjectLoose(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return ""
	}
	return s[i : j+1]
}
