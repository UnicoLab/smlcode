package plan

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ── Correction tickets ───────────────────────────────────────────────────
//
// What a failing reviewer or tester used to produce was a red notification and
// a generic "Fix tester failures" task assigned to `worker` with the failure
// lines pasted into its description.
//
// Three things were wrong with that, and all three are why the failures felt
// like noise rather than progress:
//
//   1. The GENERIC ROLE. A TypeScript compile error handed to the plain worker
//      gets a plain worker's guess. The repo ships go-worker, react-worker,
//      python-worker and the rest; the failing file's extension says which one
//      should have it.
//   2. The MISSING EVIDENCE. "test failed" is not a bug report. The command
//      that ran, the output it produced and the file it implicates are what
//      turn a retry into a fix, and they were all available at the moment the
//      ticket was written.
//   3. The NOISE. Every failure surfaced as a red event even when the harness
//      had already routed it to somebody. A defect that has an owner and a
//      plan is a ticket, not an alarm.

// CorrectionSource says which gate raised the defect. It changes the wording of
// the ticket, not its handling.
type CorrectionSource string

const (
	// SourceTester is the acceptance/test phase.
	SourceTester CorrectionSource = "tester"
	// SourceReviewer is the per-task review verdict.
	SourceReviewer CorrectionSource = "reviewer"
	// SourceQAGate is the end-of-run quality gate.
	SourceQAGate CorrectionSource = "qa_gate"
	// SourceIntegration is the cross-squad join step.
	SourceIntegration CorrectionSource = "integration"
)

// CorrectionInput is everything known about a defect at the moment it is found.
type CorrectionInput struct {
	Source CorrectionSource
	// Failures are the specific findings, most important first.
	Failures []string
	// Summary is the gate's one-line verdict.
	Summary string
	// Command is what was run to find it, when a command was run.
	Command string
	// Output is the raw tail of that command. Clipped when the ticket is built.
	Output string
	// Files are the implicated paths.
	Files []string
	// Squad routes the ticket to a virtual team, when squads are active.
	Squad string
	// Origin is the task that produced the defective work, when known.
	Origin string
	// Attempt is how many corrections have already been tried for this defect.
	Attempt int
}

// maxTicketOutput bounds the pasted command output.
//
// The tail, not the head: a test runner prints its summary and the actual
// assertion last, while the first 2KB is setup noise the model does not need.
const maxTicketOutput = 1600

// SpecialistFor picks the agent best suited to fix these files.
//
// `available` reports whether an agent id is actually registered — the language
// packs are optional, and naming an agent that does not exist is worse than
// naming the generic worker, because the run fails to dispatch rather than
// doing something slightly less well.
func SpecialistFor(files []string, available func(string) bool) string {
	if available == nil {
		available = func(string) bool { return false }
	}
	// Count by language so a ticket touching four .tsx files and one .go file
	// goes to the frontend specialist rather than to whichever came first.
	counts := map[string]int{}
	for _, f := range files {
		if lang := langOf(f); lang != "" {
			counts[lang]++
		}
	}
	if len(counts) == 0 {
		return RoleWorker
	}
	langs := make([]string, 0, len(counts))
	for l := range counts {
		langs = append(langs, l)
	}
	// Deterministic: most files wins, ties broken by name so the same defect
	// always routes to the same specialist.
	sort.Slice(langs, func(i, j int) bool {
		if counts[langs[i]] != counts[langs[j]] {
			return counts[langs[i]] > counts[langs[j]]
		}
		return langs[i] < langs[j]
	})
	for _, l := range langs {
		if id := l + "-worker"; available(id) {
			return id
		}
	}
	return RoleWorker
}

// langOf maps a path to a language-pack prefix, "" when it does not matter.
func langOf(path string) string {
	if lang := manifestLang(path); lang != "" {
		return lang
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".go":
		return "go"
	case ".tsx", ".jsx":
		return "react"
	case ".ts", ".mts", ".cts":
		return "ts"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".cs":
		return "dotnet"
	case ".c", ".h", ".cc", ".cpp", ".hpp":
		return "cpp"
	case ".sh", ".bash":
		return "shell"
	case ".html", ".css", ".scss":
		return "web"
	}
	return ""
}

// manifestLang maps a project manifest to its language by NAME.
//
// An extension cannot: `requirements.txt` is a .txt, `Gemfile` has none at all,
// and both were routed to whatever the run picked as its default specialist —
// which in a mixed repo means a Go worker editing a Python dependency list.
// Manifests come up constantly in real builds ("add the dependency", every
// greenfield scaffold), so getting them wrong is not an edge case.
//
// Deliberately excluded: `package.json` and `tsconfig.json`. Both are genuinely
// ambiguous between a TypeScript and a React lane, and the file-language rung
// OUTRANKS the squad rung in RouteTask — so claiming them here would override
// the frontend team's own choice of worker with a guess. Leaving them unmapped
// lets the better signal win.
func manifestLang(path string) string {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(path))) {
	case "go.mod", "go.sum", "go.work":
		return "go"
	case "requirements.txt", "requirements-dev.txt", "pyproject.toml",
		"setup.py", "setup.cfg", "pipfile", "conftest.py", "tox.ini":
		return "python"
	case "cargo.toml", "cargo.lock":
		return "rust"
	case "gemfile", "gemfile.lock":
		return "ruby"
	case "composer.json":
		return "php"
	case "pom.xml", "build.gradle", "build.gradle.kts":
		return "java"
	}
	return ""
}

// NewCorrectionTicket turns a defect into a task assigned to a specialist.
//
// The description is written for the agent that will read it, in the order it
// needs: what broke, how to see it break, what the evidence was, and what
// "fixed" means. A ticket whose acceptance is "the tester passes" is not
// actionable; one that names the command is.
func NewCorrectionTicket(in CorrectionInput, available func(string) bool) Task {
	files := dedupeFiles(in.Files)
	role := SpecialistFor(files, available)

	headline := firstNonBlank(in.Failures...)
	if headline == "" {
		headline = strings.TrimSpace(in.Summary)
	}
	if headline == "" {
		headline = string(in.Source) + " reported a failure with no detail"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "The %s gate rejected this work. Fix the cause, not the symptom.\n\n", in.Source)

	b.WriteString("## What failed\n")
	if len(in.Failures) > 0 {
		for _, f := range trimList(in.Failures, 6) {
			b.WriteString("- " + strings.TrimSpace(f) + "\n")
		}
	} else {
		b.WriteString("- " + headline + "\n")
	}
	if s := strings.TrimSpace(in.Summary); s != "" && s != headline {
		b.WriteString("\n" + s + "\n")
	}

	if cmd := strings.TrimSpace(in.Command); cmd != "" {
		b.WriteString("\n## Reproduce it\n```\n" + cmd + "\n```\n")
	}
	if out := strings.TrimSpace(in.Output); out != "" {
		// The tail: a runner prints the assertion that failed last.
		b.WriteString("\n## What it printed (tail)\n```\n" + tailOf(out, maxTicketOutput) + "\n```\n")
	}
	if len(files) > 0 {
		b.WriteString("\n## Implicated files\n")
		for _, f := range trimList(files, 8) {
			b.WriteString("- " + f + "\n")
		}
	}
	if in.Origin != "" {
		b.WriteString("\nThis is a correction of task " + in.Origin + ".\n")
	}
	if in.Attempt > 0 {
		fmt.Fprintf(&b, "\nCorrection attempt %d. Earlier attempts did not resolve it — "+
			"do something different from what the notes above already describe.\n", in.Attempt+1)
	}

	acceptance := strings.TrimSpace(in.Command)
	if acceptance != "" {
		acceptance = "`" + acceptance + "` passes"
	} else {
		acceptance = "the " + string(in.Source) + " findings above are resolved, with evidence of a real edit"
	}

	return Task{
		Title:       "Fix: " + clipLine(headline, 80),
		Description: b.String(),
		Role:        role,
		Column:      ColReadyToDev,
		Status:      StatusReady,
		Files:       files,
		Squad:       in.Squad,
		Acceptance:  acceptance,
		Notes: fmt.Sprintf("correction ticket from the %s gate; assigned to %s",
			in.Source, role),
	}
}

// CorrectionKey identifies a defect so the same one is not ticketed twice.
//
// Keyed on the failure text and the files, not on a counter: a gate that runs
// three times for one unresolved break must reopen the existing ticket rather
// than stack three identical ones, which is what made the board look like it
// was losing ground.
func CorrectionKey(in CorrectionInput) string {
	head := strings.ToLower(strings.TrimSpace(firstNonBlank(in.Failures...)))
	if head == "" {
		head = strings.ToLower(strings.TrimSpace(in.Summary))
	}
	files := dedupeFiles(in.Files)
	sort.Strings(files)
	return string(in.Source) + "|" + clipLine(head, 120) + "|" + strings.Join(files, ",")
}

// HasOpenCorrection reports whether an unfinished ticket already covers this
// defect.
func (b *Board) HasOpenCorrection(key string) bool {
	if b == nil || key == "" {
		return false
	}
	for _, t := range b.Tasks {
		if t.Column == ColDone || t.Status == StatusDone {
			continue
		}
		if CorrectionKeyOf(t) == key {
			return true
		}
	}
	return false
}

// correctionKeyMarker prefixes the dedupe key inside a ticket's notes.
const correctionKeyMarker = "\ncorrection-key: "

// StampCorrectionKey records the dedupe key on a ticket.
func StampCorrectionKey(t *Task, key string) {
	if t == nil || key == "" {
		return
	}
	t.Notes = strings.TrimRight(t.Notes, "\n") + correctionKeyMarker + key
}

// CorrectionKeyOf reads the dedupe key back off a ticket, "" when the task is
// not a correction.
func CorrectionKeyOf(t Task) string { return markerValue(t.Notes, correctionKeyMarker) }

// correctionAttemptMarker records which attempt at a defect a ticket is.
//
// The number cannot be derived from the board. A defect that is re-reported
// while its ticket is still open produces no second task — dedupe is doing its
// job — so counting tickets would call the third rejection of one defect a
// first attempt, and hand it straight back to the agent that has now failed at
// it twice.
const correctionAttemptMarker = "\ncorrection-attempt: "

// StampCorrectionAttempt records which attempt at a defect this ticket is,
// replacing any previous stamp.
func StampCorrectionAttempt(t *Task, n int) {
	if t == nil || n <= 0 {
		return
	}
	t.Notes = strings.TrimRight(stripMarker(t.Notes, correctionAttemptMarker), "\n") +
		correctionAttemptMarker + strconv.Itoa(n)
}

// CorrectionAttemptOf is which attempt at its defect this ticket is; 0 when the
// task is not a correction ticket at all.
func CorrectionAttemptOf(t Task) int {
	n, err := strconv.Atoi(markerValue(t.Notes, correctionAttemptMarker))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// NoteRepeatedRejection records that a defect was reported again while its
// ticket was still open, and returns the ticket's new attempt count.
//
// This is the moment a correction becomes a repeat: the gate found the same
// thing, the ticket that was supposed to fix it is still sitting there, and
// handing it back unchanged to the agent already holding it is the loop.
// Returns 0 when no open ticket covers the defect.
func (b *Board) NoteRepeatedRejection(key string) int {
	if b == nil || key == "" {
		return 0
	}
	for i := range b.Tasks {
		t := b.Tasks[i]
		if t.Column == ColDone || t.Status == StatusDone || CorrectionKeyOf(t) != key {
			continue
		}
		n := CorrectionAttemptOf(t) + 1
		StampCorrectionAttempt(&t, n)
		b.Tasks[i] = t
		return n
	}
	return 0
}

// markerValue reads a single-line marker's value out of a notes field.
//
// The markers carry a leading newline so they cannot match mid-line, but the
// notes they are written into are trimmed by several callers — and a stamp that
// landed at position 0 then loses that newline. Matching the first line as well
// as an interior one is what keeps a trim from silently un-ticketing a defect:
// dedupe would stop seeing it and the board would grow a second ticket for the
// same failure.
func markerValue(notes, marker string) string {
	head := strings.TrimPrefix(marker, "\n")
	rest := ""
	switch {
	case strings.HasPrefix(notes, head):
		rest = notes[len(head):]
	default:
		i := strings.Index(notes, marker)
		if i < 0 {
			return ""
		}
		rest = notes[i+len(marker):]
	}
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// stripMarker removes a marker line so a re-stamp replaces rather than stacks.
func stripMarker(notes, marker string) string {
	head := strings.TrimPrefix(marker, "\n")
	var kept []string
	for _, line := range strings.Split(notes, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), strings.TrimSpace(head)) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// CorrectionAttempts counts the tickets this board has raised for ONE defect,
// finished ones included.
//
// This is the number that says whether handing the ticket back to the same
// deterministic choice is a fix or a rerun. A board-wide count of corrections
// cannot: two unrelated failures make a first attempt at a third defect look
// like a third attempt, and the ticket then tells its worker that approaches it
// never tried have already been ruled out.
func (b *Board) CorrectionAttempts(key string) int {
	if b == nil || key == "" {
		return 0
	}
	n := 0
	for _, t := range b.Tasks {
		if CorrectionKeyOf(t) == key {
			n++
		}
	}
	return n
}

func dedupeFiles(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, f := range in {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func trimList(in []string, n int) []string {
	if n <= 0 || len(in) <= n {
		return in
	}
	return in[:n]
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func clipLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// tailOf keeps the LAST n bytes on a rune boundary.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	// Do not start mid-rune, and prefer starting at a line break.
	for len(cut) > 0 && !isUTF8Start(cut[0]) {
		cut = cut[1:]
	}
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < 200 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }
