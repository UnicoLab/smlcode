package quality

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// StaticIssue is a deterministic code-quality finding (no LLM).
type StaticIssue struct {
	Path   string
	Reason string
}

var (
	reNotImplemented = regexp.MustCompile(`(?i)\bNotImplementedError\b|\braise\s+NotImplemented\b`)
	reTodoOnly       = regexp.MustCompile(`(?m)^\s*(#|//|/\*)\s*(TODO|FIXME|XXX)\b`)
	reEllipsisBody   = regexp.MustCompile(`(?m)^\s*\.\.\.\s*$`)
	rePassStub       = regexp.MustCompile(`(?m)^\s*pass\s*(#.*)?$`)
	reTODOFunc       = regexp.MustCompile(`(?i)def\s+\w+\([^)]*\):\s*\n\s*(pass|\.\.\.|raise\s+NotImplemented)`)
	reJSTodo         = regexp.MustCompile(`(?i)throw new Error\(['\"]TODO`)
	reGoPanicTodo    = regexp.MustCompile(`(?i)panic\(["\']TODO`)
	// Catch explicit stub markers SLMs leave while claiming done (TestSLMs regression).
	rePlaceholder = regexp.MustCompile(`(?i)\b(your[_-]?code[_-]?here|implement me|fill[_-]?in|` +
		`lorem ipsum|placeholder\s+implementation|placeholder\s+code|` +
		`stub\s+implementation|not\s+yet\s+implemented|coming\s+soon)\b`)
	// Constant fake returns like return {"output": "run_result"} / "processed_result".
	reStubReturn = regexp.MustCompile(`(?i)return\s+\{[^}]{0,80}` +
		`["'](?:output|result|response|status)["']\s*:\s*["'][^"']{0,40}` +
		`(?:run_result|processed_result|placeholder|todo|stub|dummy|fake)`)
	// Hallucinated LangGraph import seen in TestSLMs garbage output.
	reBadLangGraphImport = regexp.MustCompile(`(?m)^\s*from\s+langgraph\s+import\s+Graph\b`)
)

// CheckStaticQuality scans focus / changed files for stub/placeholder code that
// frontier-model agents often leave while claiming "done". Returns issues.
func CheckStaticQuality(root string, t plan.Task) []StaticIssue {
	if root == "" {
		return nil
	}
	paths := collectQualityPaths(root, t)
	var issues []StaticIssue
	for _, rel := range paths {
		abs := filepath.Join(root, rel)
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			continue
		}
		text := string(data)
		ext := strings.ToLower(filepath.Ext(rel))
		if why := staticReason(text, ext); why != "" {
			issues = append(issues, StaticIssue{Path: rel, Reason: why})
		}
	}
	return issues
}

// FormatStaticSection embeds findings into worker/reviewer context.
func FormatStaticSection(issues []StaticIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Static quality gate\n")
	b.WriteString("FAILED — stub/placeholder code detected:\n")
	for _, is := range issues {
		b.WriteString(fmt.Sprintf("- %s: %s\n", is.Path, is.Reason))
	}
	b.WriteString("Corrector must replace stubs with real implementations before status=done.\n")
	return b.String()
}

// StaticFailedInOutput reports whether output already carries a failed static gate.
func StaticFailedInOutput(output string) bool {
	return strings.Contains(output, "## Static quality gate") &&
		strings.Contains(output, "FAILED")
}

func collectQualityPaths(root string, t plan.Task) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(strings.TrimPrefix(p, "./"))
		if p == "" || seen[p] || strings.Contains(p, "..") {
			return
		}
		ext := strings.ToLower(filepath.Ext(p))
		switch ext {
		case ".py", ".go", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java":
		default:
			return
		}
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, f := range t.Files {
		add(f)
	}
	for _, f := range parseFilesChangedLoose(t.Output) {
		add(f)
	}
	return out
}

func parseFilesChangedLoose(output string) []string {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "files_changed") {
		return nil
	}
	re := regexp.MustCompile(`"([^"]+\.(?:py|go|js|ts|tsx|jsx|rs|java))"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(output, -1) {
		if len(m) > 1 {
			out = append(out, m[1])
		}
	}
	return out
}

func staticReason(text, ext string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 8 {
		return "file is nearly empty"
	}
	if rePlaceholder.MatchString(text) {
		return "contains placeholder markers (Placeholder implementation / implement me)"
	}
	if reStubReturn.MatchString(text) {
		return "contains stub constant return (run_result / processed_result / placeholder)"
	}
	if reBadLangGraphImport.MatchString(text) {
		return "invalid LangGraph import (use langgraph.graph.StateGraph, not langgraph.Graph)"
	}
	switch ext {
	case ".py":
		if reNotImplemented.MatchString(text) {
			return "contains NotImplementedError"
		}
		if reTODOFunc.MatchString(text) {
			return "function body is only pass/…/NotImplemented"
		}
		// Whole-file stub: mostly pass / ellipsis / todos
		if stubHeavy(text, rePassStub, reEllipsisBody, reTodoOnly) {
			return "file is mostly stubs (pass / … / TODO)"
		}
		// Comment-only "implementation" bodies (common SLM dodge).
		if placeholderCommentHeavy(text) {
			return "methods are comment-only placeholders"
		}
	case ".js", ".ts", ".tsx", ".jsx":
		if reJSTodo.MatchString(text) {
			return "throws TODO Error stub"
		}
		if strings.Contains(text, "not implemented") && strings.Count(text, "\n") < 40 {
			return "looks like an unfinished stub"
		}
	case ".go":
		if reGoPanicTodo.MatchString(text) {
			return "contains panic(\"TODO\") stub"
		}
		if strings.Contains(text, "panic(\"not implemented\")") {
			return "contains panic(\"not implemented\")"
		}
	}
	return ""
}

// placeholderCommentHeavy detects files where ≥2 methods/funcs have only a
// "# Placeholder…" / "// Placeholder…" comment before a trivial return.
func placeholderCommentHeavy(text string) bool {
	re := regexp.MustCompile(`(?im)^\s*(?:#|//)\s*placeholder\b`)
	hits := re.FindAllStringIndex(text, -1)
	return len(hits) >= 2
}

func stubHeavy(text string, patterns ...*regexp.Regexp) bool {
	lines := strings.Split(text, "\n")
	codeLines := 0
	stubLines := 0
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, "'''") {
			continue
		}
		codeLines++
		for _, p := range patterns {
			if p.MatchString(ln) {
				stubLines++
				break
			}
		}
	}
	if codeLines < 6 {
		return stubLines >= 2
	}
	return stubLines*2 >= codeLines // ≥50% stubs
}
