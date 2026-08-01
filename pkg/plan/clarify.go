package plan

import (
	"encoding/json"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/repair"
)

// ClarifyResult is the structured output of the pre-plan clarifier.
type ClarifyResult struct {
	NeedsUser   bool     `json:"needs_user"`
	Questions   []string `json:"questions"`
	Assumptions []string `json:"assumptions"`
	Acceptance  []string `json:"acceptance"`
	Language    string   `json:"language"`
	Entrypoint  string   `json:"entrypoint"`
	Raw         string   `json:"-"`
}

// NeedsClarification reports whether a query is too vague for a reliable first
// implementation without explicit assumptions.
func NeedsClarification(query string) bool {
	q := strings.TrimSpace(query)
	if q == "" {
		return false
	}
	lower := strings.ToLower(q)
	words := strings.Fields(q)
	// Already concrete: path refs, @file, explicit stack, acceptance-ish language.
	concreteHints := []string{
		".py", ".go", ".ts", ".js", ".rs",
		"@file:", "pytest", "unittest", "fastapi", "flask", "django",
		"langgraph", "requirements.txt", "go.mod", "package.json",
		"cli", "http", "endpoint", "api", "class ", "function ",
		"must ", "should ", "acceptance", "test that",
	}
	for _, h := range concreteHints {
		if strings.Contains(lower, h) {
			return false
		}
	}
	// Short / vague product asks ("build an agent", "make a script").
	vague := []string{
		"build", "create", "make", "write", "implement", "add", "generate",
		"scaffold", "setup", "set up",
	}
	hasVague := false
	for _, v := range vague {
		if strings.Contains(lower, v) {
			hasVague = true
			break
		}
	}
	if hasVague && len(words) <= 18 {
		return true
	}
	if len(words) <= 8 {
		return true
	}
	return false
}

// ParseClarifyJSON extracts ClarifyResult from model output.
func ParseClarifyJSON(raw string) ClarifyResult {
	raw = strings.TrimSpace(raw)
	extracted := extractJSON(raw)
	if fixed, err := repair.RepairJSON(extracted); err == nil {
		extracted = fixed
	}
	var c ClarifyResult
	if err := json.Unmarshal([]byte(extracted), &c); err != nil {
		return ClarifyResult{
			Assumptions: []string{firstLine(raw)},
			Raw:         raw,
		}
	}
	c.Raw = raw
	return c
}

// MergeClarifyIntoPlan folds clarifier assumptions into an existing plan.
func MergeClarifyIntoPlan(pl Plan, c ClarifyResult) Plan {
	seen := map[string]bool{}
	for _, a := range pl.Assumptions {
		seen[strings.ToLower(strings.TrimSpace(a))] = true
	}
	for _, a := range c.Assumptions {
		a = strings.TrimSpace(a)
		if a == "" || seen[strings.ToLower(a)] {
			continue
		}
		pl.Assumptions = append(pl.Assumptions, a)
		seen[strings.ToLower(a)] = true
	}
	if c.Language != "" {
		line := "Language: " + strings.TrimSpace(c.Language)
		if !seen[strings.ToLower(line)] {
			pl.Assumptions = append(pl.Assumptions, line)
		}
	}
	if c.Entrypoint != "" {
		line := "Entrypoint: " + strings.TrimSpace(c.Entrypoint)
		if !seen[strings.ToLower(line)] {
			pl.Assumptions = append(pl.Assumptions, line)
		}
	}
	for _, q := range c.Questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		risk := "Open question: " + q
		pl.Risks = append(pl.Risks, risk)
	}
	return pl
}
