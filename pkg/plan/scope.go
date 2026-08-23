package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// Clarify modes (Claude Code / pi-clarify style).
const (
	ClarifyAuto = "auto" // apply recommended options; never block
	ClarifyAsk  = "ask"  // pause for user answers; timeout → recommended
	ClarifyOff  = "off"  // skip interview
)

// ScopeOption is one discrete choice (AskUserQuestion / pi-ask-user pattern).
type ScopeOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// ScopeQuestion is a structured interview question with options + default.
type ScopeQuestion struct {
	ID            string        `json:"id,omitempty"`
	Header        string        `json:"header,omitempty"`
	Question      string        `json:"question"`
	Options       []ScopeOption `json:"options,omitempty"`
	MultiSelect   bool          `json:"multi_select,omitempty"`
	AllowFreeform bool          `json:"allow_freeform,omitempty"`
	Recommended   string        `json:"recommended,omitempty"` // label or freeform default
}

// ScopePRD is the locked product requirements used for plan + every task.
type ScopePRD struct {
	Summary     string   `json:"summary,omitempty"`
	Goals       []string `json:"goals,omitempty"`
	NonGoals    []string `json:"non_goals,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
	Language    string   `json:"language,omitempty"`
	Entrypoint  string   `json:"entrypoint,omitempty"`
}

// ScopeInterview is the clarifier/interviewer output (extended ClarifyResult).
type ScopeInterview struct {
	NeedsUser   bool            `json:"needs_user"`
	Questions   []ScopeQuestion `json:"questions"`
	Assumptions []string        `json:"assumptions"`
	Acceptance  []string        `json:"acceptance"`
	NonGoals    []string        `json:"non_goals"`
	Language    string          `json:"language"`
	Entrypoint  string          `json:"entrypoint"`
	PRD         ScopePRD        `json:"prd"`
	Raw         string          `json:"-"`
}

// ScopeAnswer is the user's (or auto) choice for one question.
type ScopeAnswer struct {
	QuestionID string   `json:"question_id"`
	Selected   []string `json:"selected"` // option labels
	Freeform   string   `json:"freeform,omitempty"`
	Comment    string   `json:"comment,omitempty"`
}

// ScopeAnswers is the full decision handshake payload.
type ScopeAnswers struct {
	AskID      string        `json:"ask_id,omitempty"`
	Answers    []ScopeAnswer `json:"answers"`
	UseAllRec  bool          `json:"use_all_recommended,omitempty"`
	Notes      string        `json:"notes,omitempty"`
	AnsweredAt string        `json:"answered_at,omitempty"`
}

// ScopeAsk is emitted to Studio/TUI/file when clarify_mode=ask.
type ScopeAsk struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind,omitempty"`
	Query     string          `json:"query"`
	Questions []ScopeQuestion `json:"questions"`
	PRDDraft  ScopePRD        `json:"prd_draft,omitempty"`
	TimeoutS  int             `json:"timeout_sec,omitempty"`
	OnTimeout string          `json:"on_timeout,omitempty"` // "use_recommended"
	CreatedAt string          `json:"created_at"`
}

// ScopeJudgeResult is the post-split PRD completeness check.
type ScopeJudgeResult struct {
	OK      bool     `json:"ok"`
	Issues  []string `json:"issues"`
	Hints   []string `json:"hints"`
	WeakIDs []string `json:"weak_task_ids"`
	Raw     string   `json:"-"`
}

// NormalizeClarifyMode maps config aliases.
func NormalizeClarifyMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", ClarifyAuto, "defaults", "recommend":
		return ClarifyAuto
	case ClarifyAsk, "interview", "hitl":
		return ClarifyAsk
	case ClarifyOff, "skip", "none", "false":
		return ClarifyOff
	default:
		return ClarifyAuto
	}
}

// ParseScopeInterview extracts a structured interview from model output.
// Accepts both the new questions[{options}] shape and legacy string questions.
func ParseScopeInterview(raw string) ScopeInterview {
	raw = strings.TrimSpace(raw)
	extracted := repairRole(extractJSON(raw), schema.RoleClarify)

	// Try rich shape first (questions as objects).
	var rich struct {
		NeedsUser   bool            `json:"needs_user"`
		Questions   json.RawMessage `json:"questions"`
		Assumptions []string        `json:"assumptions"`
		Acceptance  []string        `json:"acceptance"`
		NonGoals    []string        `json:"non_goals"`
		Language    string          `json:"language"`
		Entrypoint  string          `json:"entrypoint"`
		PRD         ScopePRD        `json:"prd"`
	}
	if err := json.Unmarshal([]byte(extracted), &rich); err != nil {
		// Fall back to legacy ClarifyResult.
		c := ParseClarifyJSON(raw)
		return interviewFromClarify(c)
	}

	out := ScopeInterview{
		NeedsUser:   rich.NeedsUser,
		Assumptions: rich.Assumptions,
		Acceptance:  rich.Acceptance,
		NonGoals:    rich.NonGoals,
		Language:    rich.Language,
		Entrypoint:  rich.Entrypoint,
		PRD:         rich.PRD,
		Raw:         raw,
	}
	out.Questions = parseQuestionsFlexible(rich.Questions)
	normalizeInterview(&out)
	return out
}

func interviewFromClarify(c ClarifyResult) ScopeInterview {
	out := ScopeInterview{
		NeedsUser:   c.NeedsUser,
		Assumptions: c.Assumptions,
		Acceptance:  c.Acceptance,
		Language:    c.Language,
		Entrypoint:  c.Entrypoint,
		Raw:         c.Raw,
	}
	for i, q := range c.Questions {
		out.Questions = append(out.Questions, ScopeQuestion{
			ID:       fmt.Sprintf("q%d", i+1),
			Question: q,
			Options: []ScopeOption{
				{Label: "Use recommended default", Description: "Continue with clarifier assumptions", Recommended: true},
				{Label: "I will specify later", Description: "Leave open (risky)"},
			},
			Recommended:   "Use recommended default",
			AllowFreeform: true,
		})
	}
	normalizeInterview(&out)
	return out
}

func parseQuestionsFlexible(raw json.RawMessage) []ScopeQuestion {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var objs []ScopeQuestion
	if err := json.Unmarshal(raw, &objs); err == nil && len(objs) > 0 {
		// Detect string-shaped decode failure: empty Question with empty Options
		// when the payload was actually strings.
		ok := false
		for _, q := range objs {
			if strings.TrimSpace(q.Question) != "" || len(q.Options) > 0 {
				ok = true
				break
			}
		}
		if ok {
			return objs
		}
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		var out []ScopeQuestion
		for i, s := range strs {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, ScopeQuestion{
				ID:       fmt.Sprintf("q%d", i+1),
				Question: s,
				Options: []ScopeOption{
					{Label: "Use recommended default", Recommended: true},
					{Label: "Block until I answer", Description: "Do not guess"},
				},
				Recommended:   "Use recommended default",
				AllowFreeform: true,
			})
		}
		return out
	}
	return objs
}

func normalizeInterview(in *ScopeInterview) {
	if in == nil {
		return
	}
	for i := range in.Questions {
		q := &in.Questions[i]
		if q.ID == "" {
			q.ID = fmt.Sprintf("q%d", i+1)
		}
		if q.Recommended == "" {
			for _, opt := range q.Options {
				if opt.Recommended {
					q.Recommended = opt.Label
					break
				}
			}
		}
		if q.Recommended == "" && len(q.Options) > 0 {
			q.Recommended = q.Options[0].Label
			q.Options[0].Recommended = true
		}
		if q.Header == "" && q.Question != "" {
			// Short header from first few words.
			words := strings.Fields(q.Question)
			if len(words) > 4 {
				q.Header = strings.Join(words[:4], " ")
			} else {
				q.Header = q.Question
			}
		}
	}
	// Fold top-level fields into PRD.
	if in.PRD.Language == "" {
		in.PRD.Language = in.Language
	}
	if in.PRD.Entrypoint == "" {
		in.PRD.Entrypoint = in.Entrypoint
	}
	if len(in.PRD.Acceptance) == 0 {
		in.PRD.Acceptance = append([]string{}, in.Acceptance...)
	}
	if len(in.PRD.NonGoals) == 0 {
		in.PRD.NonGoals = append([]string{}, in.NonGoals...)
	}
	if len(in.PRD.Constraints) == 0 && len(in.Assumptions) > 0 {
		in.PRD.Constraints = append([]string{}, in.Assumptions...)
	}
}

// NeedsUserDecision reports whether the interview has real forks to resolve.
func (in ScopeInterview) NeedsUserDecision() bool {
	if in.NeedsUser && len(in.Questions) > 0 {
		return true
	}
	for _, q := range in.Questions {
		if len(q.Options) >= 2 {
			return true
		}
		if strings.TrimSpace(q.Question) != "" && q.AllowFreeform {
			return true
		}
	}
	return false
}

// ResolveWithDefaults applies recommended options (auto / timeout path).
func ResolveWithDefaults(in ScopeInterview) ScopeAnswers {
	ans := ScopeAnswers{
		UseAllRec:  true,
		AnsweredAt: time.Now().UTC().Format(time.RFC3339),
		Notes:      "auto: applied recommended options",
	}
	for _, q := range in.Questions {
		sel := q.Recommended
		if sel == "" && len(q.Options) > 0 {
			sel = q.Options[0].Label
		}
		a := ScopeAnswer{QuestionID: q.ID}
		if sel != "" {
			a.Selected = []string{sel}
		}
		ans.Answers = append(ans.Answers, a)
	}
	return ans
}

// ApplyScopeAnswers locks decisions into assumptions + PRD.
func ApplyScopeAnswers(in ScopeInterview, ans ScopeAnswers) ScopeInterview {
	if ans.UseAllRec || len(ans.Answers) == 0 {
		ans = ResolveWithDefaults(in)
	}
	byID := map[string]ScopeAnswer{}
	for _, a := range ans.Answers {
		byID[a.QuestionID] = a
	}
	for _, q := range in.Questions {
		a, ok := byID[q.ID]
		if !ok {
			// Missing answer → recommended.
			a = ScopeAnswer{QuestionID: q.ID, Selected: []string{q.Recommended}}
		}
		choice := strings.TrimSpace(a.Freeform)
		if choice == "" && len(a.Selected) > 0 {
			choice = strings.Join(a.Selected, ", ")
		}
		if choice == "" {
			choice = q.Recommended
		}
		if choice == "" {
			continue
		}
		line := fmt.Sprintf("Decision [%s]: %s", firstNonEmpty(q.Header, q.ID), choice)
		if a.Comment != "" {
			line += " (" + strings.TrimSpace(a.Comment) + ")"
		}
		in.Assumptions = appendUnique(in.Assumptions, line)
		in.PRD.Constraints = appendUnique(in.PRD.Constraints, line)

		// Promote language/entrypoint from known headers (keep prior defaults if set).
		h := strings.ToLower(q.Header + " " + q.Question)
		if strings.Contains(h, "language") || strings.Contains(h, "runtime") || strings.Contains(h, "stack") {
			lang := strings.ToLower(firstToken(choice))
			if lang != "" {
				in.Language = lang
				in.PRD.Language = lang
			}
		}
		if strings.Contains(h, "entrypoint") || strings.Contains(h, "entry point") || strings.Contains(h, "main file") {
			ep := firstPathish(choice)
			if ep != "" {
				in.Entrypoint = ep
				in.PRD.Entrypoint = ep
			}
		}
	}
	if strings.TrimSpace(ans.Notes) != "" && !ans.UseAllRec {
		in.Assumptions = appendUnique(in.Assumptions, "User notes: "+strings.TrimSpace(ans.Notes))
	}
	if in.PRD.Summary == "" && len(in.Assumptions) > 0 {
		in.PRD.Summary = "Scoped from interview decisions"
	}
	// Clear open questions — resolved.
	in.NeedsUser = false
	in.Questions = nil
	return in
}

// ToClarifyResult adapts interview → legacy clarifier type for MergeClarifyIntoPlan.
func (in ScopeInterview) ToClarifyResult() ClarifyResult {
	return ClarifyResult{
		NeedsUser:   false,
		Assumptions: in.Assumptions,
		Acceptance:  firstNonEmptySlice(in.PRD.Acceptance, in.Acceptance),
		Language:    firstNonEmpty(in.PRD.Language, in.Language),
		Entrypoint:  firstNonEmpty(in.PRD.Entrypoint, in.Entrypoint),
		Raw:         in.Raw,
	}
}

// FormatPRDMarkdown renders a locked PRD for CONTEXT.md / plan prompt.
func FormatPRDMarkdown(prd ScopePRD, assumptions []string) string {
	var b strings.Builder
	b.WriteString("## Locked PRD\n\n")
	if prd.Summary != "" {
		b.WriteString(prd.Summary + "\n\n")
	}
	if prd.Language != "" {
		b.WriteString("- **Language:** " + prd.Language + "\n")
	}
	if prd.Entrypoint != "" {
		b.WriteString("- **Entrypoint:** " + prd.Entrypoint + "\n")
	}
	writeBulletSection(&b, "Goals", prd.Goals)
	writeBulletSection(&b, "Non-goals", prd.NonGoals)
	writeBulletSection(&b, "Acceptance", prd.Acceptance)
	writeBulletSection(&b, "Constraints / decisions", firstNonEmptySlice(prd.Constraints, assumptions))
	return b.String()
}

func writeBulletSection(b *strings.Builder, title string, items []string) {
	items = cleanStrings(items)
	if len(items) == 0 {
		return
	}
	b.WriteString("\n### " + title + "\n")
	for _, it := range items {
		b.WriteString("- " + it + "\n")
	}
}

// EnsureTaskPRDs seeds missing acceptance/checklist from the locked PRD so every
// worker task has verifiable scope (Claude Code spec-before-code pattern).
func EnsureTaskPRDs(tasks []Task, prd ScopePRD, query string) []Task {
	if len(tasks) == 0 {
		return tasks
	}
	globalAC := cleanStrings(prd.Acceptance)
	if len(globalAC) == 0 {
		globalAC = defaultAcceptanceFromQuery(query, prd)
	}
	for i := range tasks {
		t := &tasks[i]
		if strings.EqualFold(t.Role, RoleTester) {
			if strings.TrimSpace(t.Acceptance) == "" {
				t.Acceptance = "Verification commands exit 0; global PRD acceptance met"
			}
			continue
		}
		if strings.TrimSpace(t.Acceptance) == "" || isVagueAcceptance(t.Acceptance) {
			if len(t.Files) > 0 {
				t.Acceptance = fmt.Sprintf("%s exists and meets: %s",
					t.Files[0], firstLine(strings.Join(globalAC, "; ")))
			} else if t.Title != "" {
				t.Acceptance = fmt.Sprintf("%s done; %s", t.Title, firstLine(strings.Join(globalAC, "; ")))
			} else {
				t.Acceptance = firstLine(strings.Join(globalAC, "; "))
			}
		}
		// Seed checklist from PRD acceptance when empty (lightweight per-task PRD).
		if len(t.Checklist) == 0 && len(globalAC) > 0 && !strings.EqualFold(t.Role, RoleExplorer) {
			max := 3
			if len(globalAC) < max {
				max = len(globalAC)
			}
			for _, a := range globalAC[:max] {
				t.Checklist = append(t.Checklist, ChecklistItem{Text: a})
			}
		}
		// Append non-goals as notes so workers don't wander.
		if len(prd.NonGoals) > 0 && !strings.Contains(strings.ToLower(t.Notes), "non-goal") {
			t.Notes = strings.TrimSpace(t.Notes + "\nNon-goals: " + strings.Join(prd.NonGoals, "; "))
		}
		if prd.Language != "" && !strings.Contains(strings.ToLower(t.Description), "language") {
			t.Description = strings.TrimSpace(t.Description +
				"\n\nLocked language: " + prd.Language)
		}
		if prd.Entrypoint != "" && len(t.Files) == 0 &&
			(strings.Contains(strings.ToLower(t.Title), "main") ||
				strings.Contains(strings.ToLower(t.Title), "entrypoint") ||
				strings.Contains(strings.ToLower(t.Title), "cli")) {
			t.Files = []string{prd.Entrypoint}
		}
	}
	return tasks
}

func defaultAcceptanceFromQuery(query string, prd ScopePRD) []string {
	q := strings.TrimSpace(query)
	out := []string{}
	if prd.Entrypoint != "" {
		out = append(out, prd.Entrypoint+" runs successfully")
	}
	lower := strings.ToLower(q)
	if strings.Contains(lower, "langgraph") || strings.Contains(lower, "langchain") {
		ep := firstNonEmpty(prd.Entrypoint, "main.py")
		out = append(out,
			"Class-based LangGraph agent uses langgraph.graph.StateGraph (not invented Graph API)",
			ep+" invokes the compiled graph once and exits 0",
			"python -m pytest -q passes; no Placeholder stubs in agent modules",
			"requirements.txt lists langgraph + langchain-core + pytest",
		)
		return out
	}
	if q != "" {
		out = append(out, "Implements: "+firstLine(q))
	}
	return out
}

// JudgeTaskScopeHeuristics finds tasks with incomplete PRD/scope (no LLM).
func JudgeTaskScopeHeuristics(tasks []Task, prd ScopePRD) ScopeJudgeResult {
	res := ScopeJudgeResult{OK: true}
	// Note: an entirely absent PRD is fine for tiny concrete edits, so it is
	// deliberately NOT an issue here — only per-task worker gaps are flagged.
	for _, t := range tasks {
		if strings.EqualFold(t.Role, RoleExplorer) || strings.EqualFold(t.Role, "docs") {
			continue
		}
		weak := false
		title := strings.TrimSpace(t.Title)
		ac := strings.TrimSpace(t.Acceptance)
		desc := strings.TrimSpace(t.Description)
		if title == "" || isVagueTitle(title) {
			res.Issues = append(res.Issues, t.ID+": vague or empty title")
			weak = true
		}
		if ac == "" || isVagueAcceptance(ac) {
			res.Issues = append(res.Issues, t.ID+": missing concrete acceptance")
			weak = true
		}
		if !strings.EqualFold(t.Role, RoleTester) && len(t.Files) == 0 &&
			!strings.Contains(strings.ToLower(desc+title), "create") &&
			!strings.Contains(strings.ToLower(desc+title), "scaffold") {
			res.Issues = append(res.Issues, t.ID+": no focus files")
			weak = true
		}
		if len(desc) < 12 {
			res.Issues = append(res.Issues, t.ID+": description too thin for SLM worker")
			weak = true
		}
		if weak {
			res.WeakIDs = append(res.WeakIDs, t.ID)
			res.OK = false
		}
	}
	if !res.OK {
		res.Hints = append(res.Hints,
			"Add concrete acceptance (command or observable outcome)",
			"Pin real file paths",
			"Expand description with locked PRD constraints")
	}
	return res
}

// ParseScopeJudgeJSON parses LLM scope-judge output.
func ParseScopeJudgeJSON(raw string) ScopeJudgeResult {
	raw = strings.TrimSpace(raw)
	extracted := repairRole(extractJSON(raw), schema.RoleScopeJudge)
	var r ScopeJudgeResult
	if err := json.Unmarshal([]byte(extracted), &r); err != nil {
		lower := strings.ToLower(raw)
		r.OK = strings.Contains(lower, `"ok":true`) || strings.Contains(lower, `"ok": true`)
		if !r.OK && strings.TrimSpace(raw) != "" {
			r.Issues = []string{firstLine(raw)}
		}
		r.Raw = raw
		return r
	}
	r.Raw = raw
	return r
}

// MergePRDIntoPlan folds locked PRD into the plan document.
func MergePRDIntoPlan(pl Plan, prd ScopePRD) Plan {
	if prd.Summary != "" && strings.TrimSpace(pl.Summary) == "" {
		pl.Summary = prd.Summary
	}
	for _, g := range prd.Goals {
		pl.Goals = appendUnique(pl.Goals, g)
	}
	for _, a := range prd.Acceptance {
		pl.Assumptions = appendUnique(pl.Assumptions, "Acceptance: "+a)
	}
	for _, ng := range prd.NonGoals {
		pl.Risks = appendUnique(pl.Risks, "Non-goal: "+ng)
	}
	for _, c := range prd.Constraints {
		pl.Assumptions = appendUnique(pl.Assumptions, c)
	}
	if prd.Language != "" {
		pl.Assumptions = appendUnique(pl.Assumptions, "Language: "+prd.Language)
	}
	if prd.Entrypoint != "" {
		pl.Assumptions = appendUnique(pl.Assumptions, "Entrypoint: "+prd.Entrypoint)
	}
	return pl
}

func isVagueTitle(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "implement", "do it", "fix", "update", "change", "work", "task", "step":
		return true
	}
	return len(strings.Fields(lower)) <= 1
}

func isVagueAcceptance(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	vague := []string{
		"done", "ok", "works", "looks good", "complete", "finished",
		"query goals met", "step completed", "as expected",
		"step completed with tool evidence", "with tool evidence",
		"verified by qa_gate", "qa_gate green",
	}
	for _, v := range vague {
		if lower == v || strings.Contains(lower, v) && len(v) >= 20 {
			return true
		}
	}
	// Existence-only criteria are too weak for implement tasks.
	if strings.Contains(lower, "exist and contain") ||
		strings.Contains(lower, "exists and contain") ||
		strings.Contains(lower, "exist and are valid") ||
		strings.Contains(lower, "exists and are valid") ||
		(strings.Contains(lower, "exist") && strings.Contains(lower, "valid") &&
			!strings.Contains(lower, "pytest") && !strings.Contains(lower, "import") &&
			!strings.Contains(lower, "run") && !strings.Contains(lower, "exit")) {
		return true
	}
	if strings.Contains(lower, "pytest --collect-only") {
		return true
	}
	return len(lower) < 8
}

func appendUnique(list []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return list
	}
	low := strings.ToLower(item)
	for _, x := range list {
		if strings.ToLower(strings.TrimSpace(x)) == low {
			return list
		}
	}
	return append(list, item)
}

func cleanStrings(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmptySlice(a, b []string) []string {
	if len(cleanStrings(a)) > 0 {
		return cleanStrings(a)
	}
	return cleanStrings(b)
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ",;/|"); i >= 0 {
		s = s[:i]
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func firstPathish(s string) string {
	s = strings.TrimSpace(s)
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, ",;\"'")
		if strings.Contains(f, ".") || strings.Contains(f, "/") {
			return f
		}
	}
	return firstToken(s)
}

// MarshalAskJSON is a helper for SSE/file payloads.
func MarshalAskJSON(ask ScopeAsk) string {
	b, err := json.MarshalIndent(ask, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
