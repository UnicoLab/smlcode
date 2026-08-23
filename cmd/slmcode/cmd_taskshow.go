package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// `slmcode task show <id>` — the answer to "why did T1 stop?".
//
// "T1 needs human review" is the single most common terminal state of a local
// SLM run, and from the terminal there was no way to find out what that meant.
// The board printed the title. The run summary printed a count. The verdict the
// reviewer wrote, the gate that refused the task, and the diff of the files it
// was allowed to touch all existed on disk — in board.json and events.jsonl —
// and nothing rendered them. The only advice on offer came from the engine and
// said "decide in Studio", which is not a thing a terminal can do.
//
// This command renders, in the order a human needs them:
//
//	scope        what the task was asked to do, and where it was allowed to write
//	acceptance   how it was going to be judged
//	verdict      what the reviewer said, with its issues
//	gate         which gate refused it, and why
//	diff         what the focus files actually look like now
//	next         what can be done about it from this terminal

// reviewVerdict is the reviewer JSON contract, when the review parses as one.
type reviewVerdict struct {
	Approved bool     `json:"approved"`
	Score    int      `json:"score"`
	Summary  string   `json:"summary"`
	Issues   []string `json:"issues"`
}

// parseReview reads the reviewer's JSON verdict out of a task's Review field.
//
// The field is free text as often as not (the engine also writes prose markers
// like "human mark_done after escalate" into it), so a parse failure is normal
// and simply means "render it as text".
func parseReview(raw string) (reviewVerdict, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return reviewVerdict{}, false
	}
	var v reviewVerdict
	if json.Unmarshal([]byte(raw), &v) != nil {
		return reviewVerdict{}, false
	}
	return v, true
}

// workerOutput is the worker/corrector JSON contract.
type workerOutput struct {
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed"`
	Notes        string   `json:"notes"`
}

func parseWorkerOutput(raw string) (workerOutput, bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	if !strings.HasPrefix(raw, "{") {
		return workerOutput{}, false
	}
	// Agent output routinely carries trailing prose (smoke-test markers,
	// markdown) after the JSON object. Cut at the matching brace.
	if j := strings.LastIndex(raw, "}"); j > 0 {
		raw = raw[:j+1]
	}
	var w workerOutput
	if json.Unmarshal([]byte(raw), &w) != nil {
		return workerOutput{}, false
	}
	return w, true
}

// gateTrace is what the event log remembers about one task being refused.
type gateTrace struct {
	Gate    string   // the harness message that named the gate
	Reason  string   // the detail attached to it
	Asked   string   // the escalate question the user was (or was not) asked
	Answer  string   // what answered it
	Repeats int      // how many times the same gate fired
	Extra   []string // other interventions worth showing, deduplicated
}

// maxGateExtras bounds the "other interventions" list.
const maxGateExtras = 4

// traceTaskGates scans the most recent runs' event logs for one task.
//
// Events are the only record of WHICH gate refused a task: the board keeps the
// verdict but not the gate that acted on it. The newest run that mentions the
// task wins — an older run's escalation is not what the user just watched.
func traceTaskGates(slmDir, taskID string) gateTrace {
	var tr gateTrace
	turns, err := session.ListQueries(slmDir)
	if err != nil {
		return tr
	}
	for _, turn := range turns {
		events, err := session.ReadEvents(slmDir, turn.ID, 4000)
		if err != nil || len(events) == 0 {
			continue
		}
		found := false
		seen := map[string]bool{}
		for _, e := range events {
			if e.TaskID != taskID {
				continue
			}
			msg := strings.TrimSpace(e.Message)
			if msg == "" {
				continue
			}
			found = true
			switch e.Kind {
			case "ask":
				tr.Asked = cli.TranslateEngineAdvice(msg)
			case "output":
				if strings.Contains(strings.ToLower(msg), "escalate answer") {
					tr.Answer = strings.TrimSpace(msg + " — " + firstLine(e.Output))
				}
			case "intervention":
				if isGateMessage(msg) {
					if tr.Gate == "" {
						tr.Gate = cli.TranslateEngineAdvice(msg)
						tr.Reason = firstLine(e.Output)
					}
					if tr.Gate == cli.TranslateEngineAdvice(msg) {
						tr.Repeats++
					}
					continue
				}
				key := cli.Clip(msg, 90)
				if !seen[key] && len(tr.Extra) < maxGateExtras {
					seen[key] = true
					tr.Extra = append(tr.Extra, key)
				}
			}
		}
		if found {
			return tr
		}
	}
	return tr
}

// isGateMessage recognizes the harness messages that mean "a gate acted".
func isGateMessage(msg string) bool {
	l := strings.ToLower(msg)
	for _, marker := range []string{
		"needs human review", "call budget", "evidence gate",
		"escalating", "rejected by", "hit its",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// taskShowCmd renders everything known about one task.
func taskShowCmd() *cobra.Command {
	var (
		asJSON  bool
		noDiff  bool
		context int
	)
	c := &cobra.Command{
		Use:   "show [id]",
		Short: "Everything known about one task: scope, verdict, the gate that blocked it, and its diff",
		Long: `Explain one task.

This is where an escalated task stops being a dead end. It renders the task's
scope and acceptance criteria, the agent's last output, the reviewer's verdict
and its issues, the gate that refused the task, and the diff of the files the
task was allowed to touch — then lists what you can do about it from here.`,
		Example: "  slmcode task show T1\n  slmcode task show T1 --no-diff\n  slmcode task show T1 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			_ = ws.Board.Load()
			t, ok := ws.Board.GetTask(args[0])
			if !ok {
				return taskNotFound(ws.Board.Snapshot(), args[0])
			}
			trace := traceTaskGates(ws.Config.SlmDir(), t.ID)

			if asJSON {
				verdict, parsed := parseReview(t.Review)
				payload := map[string]any{
					"task":         t,
					"forced_done":  humanForcedDone(t),
					"gate":         trace.Gate,
					"gate_reason":  trace.Reason,
					"gate_repeats": trace.Repeats,
					"asked":        trace.Asked,
					"answered":     trace.Answer,
				}
				if parsed {
					payload["verdict"] = verdict
				}
				return emitJSON(payload)
			}
			renderTask(ws.Config.Root, ws.Config.SlmDir(), t, trace, !noDiff, context)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	c.Flags().BoolVar(&noDiff, "no-diff", false, "skip the diff of the task's focus files")
	c.Flags().IntVar(&context, "context", 3, "diff context lines")
	return c
}

// taskNotFound turns "task T9 not found" into something a user can act on.
func taskNotFound(b plan.Board, id string) error {
	if len(b.Tasks) == 0 {
		return failf(2, "no tasks on the board yet — `slmcode run \"…\"` creates them")
	}
	ids := make([]string, 0, len(b.Tasks))
	for _, t := range b.Tasks {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return failf(2, "task %s is not on the board — this board has: %s", id, strings.Join(ids, ", "))
}

func renderTask(root, slmDir string, t plan.Task, trace gateTrace, withDiff bool, context int) {
	cli.Header(t.ID + " — " + t.Title)

	cli.KeyVal("column", columnWithVerdict(t))
	cli.KeyVal("role", orDash("@"+strings.TrimPrefix(t.Role, "@")))
	if t.Retries > 0 {
		cli.KeyVal("retries", fmt.Sprintf("%d", t.Retries))
	}
	if len(t.Files) > 0 {
		cli.KeyVal("focus files", strings.Join(t.Files, ", "))
	}
	if len(t.DependsOn) > 0 {
		cli.KeyVal("depends on", strings.Join(t.DependsOn, ", "))
	}

	if body := strings.TrimSpace(t.Description); body != "" {
		section("Scope")
		fmt.Println(indent(body))
	}
	if acc := strings.TrimSpace(t.Acceptance); acc != "" {
		section("Acceptance criteria")
		fmt.Println(indent(acc))
	}
	if len(t.Checklist) > 0 {
		section("Checklist")
		for _, item := range t.Checklist {
			mark := cli.Dim("[ ]")
			if item.Done {
				mark = cli.Green("[x]")
			}
			fmt.Printf("  %s %s\n", mark, item.Text)
		}
	}

	renderTaskOutput(t)
	renderTaskVerdict(t)
	renderTaskGate(t, trace)

	renderTaskNotes(t)

	if withDiff {
		renderTaskDiff(root, slmDir, t, context)
	}
	renderTaskNextSteps(slmDir, t, withDiff)
}

// maxNoteLines bounds the notes block.
const maxNoteLines = 12

// renderTaskNotes prints the task's notes, collapsed.
//
// The engine APPENDS to Task.Notes on every escalate round, so a task that
// bounced through 200 retry waves carries the same four-line block 200 times.
// Printing it verbatim buried the one human-written line at the bottom of a
// 600-line wall. Identical lines collapse to one with a count.
func renderTaskNotes(t plan.Task) {
	notes := strings.TrimSpace(t.Notes)
	if notes == "" {
		return
	}
	section("Notes")
	lines, repeats, order := map[string]int{}, map[string]int{}, []string{}
	for _, raw := range strings.Split(notes, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if _, seen := lines[line]; !seen {
			lines[line] = len(order)
			order = append(order, line)
		}
		repeats[line]++
	}
	shown := 0
	for _, line := range order {
		if shown >= maxNoteLines {
			fmt.Println(indent(cli.Dim(fmt.Sprintf("… +%d more distinct note line(s) in board.json",
				len(order)-shown))))
			break
		}
		shown++
		out := cli.TranslateEngineAdvice(line)
		if n := repeats[line]; n > 1 {
			out += cli.Dim(fmt.Sprintf("  ×%d", n))
		}
		fmt.Println(indent(out))
	}
}

// columnWithVerdict labels the column and, when a human overrode the gate, says so.
func columnWithVerdict(t plan.Task) string {
	label := plan.ColumnLabel(t.Column) + cli.Dim(" ("+t.Column+")")
	if humanForcedDone(t) {
		return label + "  " + cli.Yellow("← forced done by a human; the evidence gate had refused it")
	}
	return label
}

func renderTaskOutput(t plan.Task) {
	out := strings.TrimSpace(t.Output)
	if out == "" {
		section("Last output")
		fmt.Println(indent(cli.Dim("(none — no agent has produced output for this task)")))
		return
	}
	section("Last output")
	if w, ok := parseWorkerOutput(out); ok {
		cli.KeyVal("  status", orDash(w.Status))
		if w.Summary != "" {
			cli.KeyVal("  summary", w.Summary)
		}
		if len(w.FilesChanged) > 0 {
			// The agent's CLAIM, deliberately labeled as such: the whole
			// point of the evidence gate is that this list can be fiction.
			cli.KeyVal("  claimed", strings.Join(w.FilesChanged, ", ")+
				cli.Dim("  (claimed by the agent — the diff below is the truth)"))
		}
		if w.Notes != "" {
			cli.KeyVal("  notes", w.Notes)
		}
		return
	}
	fmt.Println(indent(cli.Clip(out, 1200)))
}

func renderTaskVerdict(t plan.Task) {
	review := strings.TrimSpace(t.Review)
	if review == "" {
		return
	}
	section("Review verdict")
	v, ok := parseReview(review)
	if !ok {
		fmt.Println(indent(cli.TranslateEngineAdvice(cli.Clip(review, 800))))
		return
	}
	mark := cli.Red("rejected")
	if v.Approved {
		mark = cli.Green("approved")
	}
	cli.KeyVal("  verdict", mark+cli.Dim(fmt.Sprintf("  score %d/100", v.Score)))
	if v.Summary != "" {
		cli.KeyVal("  summary", v.Summary)
	}
	for _, issue := range v.Issues {
		fmt.Println("    " + cli.Yellow("• ") + issue)
	}
}

func renderTaskGate(t plan.Task, trace gateTrace) {
	err := strings.TrimSpace(t.Error)
	if err == "" && trace.Gate == "" {
		return
	}
	section("Gate")
	if trace.Gate != "" {
		line := trace.Gate
		if trace.Repeats > 1 {
			line += cli.Dim(fmt.Sprintf("  ×%d", trace.Repeats))
		}
		fmt.Println("  " + cli.Yellow(line))
		if trace.Reason != "" {
			fmt.Println(indent(cli.Dim(cli.Clip(trace.Reason, 400))))
		}
	}
	if err != "" {
		cli.KeyVal("  error", cli.TranslateEngineAdvice(cli.Clip(err, 400)))
	}
	if trace.Asked != "" {
		cli.KeyVal("  asked", trace.Asked)
	}
	if trace.Answer != "" {
		cli.KeyVal("  answered", trace.Answer)
	}
	for _, extra := range trace.Extra {
		fmt.Println(indent(cli.Dim("· " + cli.TranslateEngineAdvice(extra))))
	}
}

// renderTaskDiff shows what the task's focus files actually look like.
//
// This is the half of the story the board never told: the reviewer rejected
// something, and until now there was no way to see what.
func renderTaskDiff(root, slmDir string, t plan.Task, context int) {
	section("Diff of focus files")
	if len(t.Files) == 0 {
		fmt.Println(indent(cli.Dim("(the task declared no focus files)")))
		return
	}
	diffs, err := collectWorkspaceDiffs(root, t.Files, context, true)
	if err != nil || len(diffs) == 0 {
		fmt.Println(indent(cli.Warn("no change on disk in " + strings.Join(t.Files, ", "))))
		// "Nothing on disk" and "nothing was produced" are different answers,
		// and in `permission: review` the second one would be a lie.
		if held := pendingForFiles(slmDir, t.Files); len(held) > 0 {
			fmt.Println(indent(cli.Dim(fmt.Sprintf(
				"%d proposed edit(s) for these files are held for review — `slmcode apply`",
				len(held)))))
			for _, p := range held {
				fmt.Println()
				fmt.Print(cli.RenderDiff(p.diff(root), cli.DefaultDiffRender(termWidth())))
			}
			return
		}
		fmt.Println(indent(cli.Dim("the files this task was scoped to are byte-for-byte what they were")))
		return
	}
	for _, fd := range diffs {
		fmt.Println()
		fmt.Print(cli.RenderDiff(fd, cli.DefaultDiffRender(termWidth())))
	}
}

// termWidth is the render width for a diff block.
func termWidth() int {
	w, _ := cli.TermSize()
	return w
}

// pendingForFiles returns the staged proposals that target any of files.
func pendingForFiles(slmDir string, files []string) []pendingPatch {
	all, err := loadPending(slmDir)
	if err != nil || len(all) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, f := range files {
		want[filepath.ToSlash(strings.TrimPrefix(f, "./"))] = true
	}
	var out []pendingPatch
	for _, p := range all {
		if want[filepath.ToSlash(p.Path)] {
			out = append(out, p)
		}
	}
	return out
}

func renderTaskNextSteps(slmDir string, t plan.Task, showedDiff bool) {
	fmt.Println()
	var steps []nextStep
	// A staged proposal outranks everything else: until it is applied, none of
	// the "check the change" advice below points at anything real.
	if n := len(pendingForFiles(slmDir, t.Files)); n > 0 {
		steps = append(steps, nextStep{"slmcode apply",
			fmt.Sprintf("write the %d held edit(s) to disk", n)})
	}
	switch t.Column {
	case plan.ColDone:
		steps = append(steps, nextStep{"slmcode diff", "check the change before you keep it"})
		steps = append(steps, nextStep{"slmcode commit -m \"…\"", "keep it"})
	case plan.ColBlocked:
		steps = append(steps, nextStep{"slmcode task move " + t.ID + " ready_to_dev", "un-block and let agents retry"})
		steps = append(steps, nextStep{"slmcode task edit " + t.ID + " --notes \"…\"", "tell the agent what it got wrong"})
	default:
		steps = append(steps,
			nextStep{"slmcode task edit " + t.ID + " --acceptance \"…\"", "sharpen what \"done\" means"},
			nextStep{"slmcode task move " + t.ID + " ready_to_dev", "put it back in the queue"},
			nextStep{"slmcode run \"…\"", "run again — agents pick the board up where it is"})
	}
	if !showedDiff && len(t.Files) > 0 {
		steps = append(steps, nextStep{"slmcode diff " + strings.Join(t.Files, " "), "what the focus files look like now"})
	}
	if t.Column != plan.ColDone {
		steps = append(steps, nextStep{"slmcode task move " + t.ID + " done", "you fixed it yourself; close it out"})
	}
	printNextSteps(steps)
}

// section prints a blank line and a bold heading.
func section(title string) {
	fmt.Println()
	fmt.Println(cli.Bold(title))
}

// indent prefixes every line of a block with two spaces.
func indent(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
