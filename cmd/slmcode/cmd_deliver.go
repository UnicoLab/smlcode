package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/vcs"
	"github.com/spf13/cobra"
)

// Issue intake and pull-request delivery for `slmcode run`.
//
// The two are deliberately asymmetric. Reading an issue is a read: it costs
// nothing, changes nothing, and the worst case is a query the operator does not
// like. Opening a pull request LEAVES THE MACHINE, and nothing else the harness
// does is like that — so it takes an explicit flag AND a confirmation, and the
// confirmation shows the exact branch, title and file list first.

// resolveIssueQuery turns --issue into a run query, or returns args unchanged.
func resolveIssueQuery(ctx context.Context, cmd *cobra.Command, root string, args []string) (string, *vcs.Issue, error) {
	ref, _ := cmd.Flags().GetString("issue")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return strings.Join(args, " "), nil, nil
	}
	iss, err := vcs.FetchIssue(ctx, root, ref)
	if err != nil {
		return "", nil, err
	}
	cli.KeyVal("issue", fmt.Sprintf("#%d %s", iss.Number, iss.Title))
	if iss.URL != "" {
		cli.KeyVal("url", iss.URL)
	}

	query := iss.Query()
	// Extra words on the command line REFINE the issue rather than replacing it
	// — "slmcode run --issue 42 focus on the parser only" is the shape an
	// operator reaches for, and dropping the tail would silently ignore them.
	if extra := strings.TrimSpace(strings.Join(args, " ")); extra != "" {
		query += "\n\nOperator note: " + extra
	}
	return query, &iss, nil
}

// maybeDeliverPR opens a pull request for a finished run, when asked.
//
// Reports whether a pull request was actually opened, which the isolation
// teardown needs: a delivered PR IS the delivery, and merging the same branch
// locally as well would pre-empt the review the PR exists to get.
//
// Every refusal is reported and non-fatal: a run that succeeded and could not
// be delivered still succeeded, and turning that into a failure exit code
// would be lying about the work.
func maybeDeliverPR(ctx context.Context, cmd *cobra.Command, root string, board *plan.Board, iss *vcs.Issue, success bool) (delivered bool) {
	mode, _ := cmd.Flags().GetString("deliver")
	if strings.TrimSpace(mode) == "" {
		return false
	}
	if mode != "pr" {
		cli.Warn(fmt.Sprintf("unknown --deliver mode %q — only 'pr' is supported", mode))
		return false
	}
	if !success {
		cli.Warn("run did not succeed — not opening a pull request")
		return false
	}

	title, body := deliveryText(board, iss)
	opt := vcs.DeliverOptions{
		Title: title,
		Body:  body,
		Files: changedFiles(board),
		Draft: flagBool(cmd, "draft"),
		Base:  flagString(cmd, "base"),
	}
	if b := flagString(cmd, "branch"); b != "" {
		opt.Branch = b
	}

	planned, err := vcs.Prepare(ctx, root, opt)
	if err != nil {
		cli.Warn("cannot open a pull request: " + err.Error())
		return false
	}

	fmt.Println()
	fmt.Println(cli.Bold("Pull request"))
	for _, line := range planned.Summary() {
		fmt.Println("  " + line)
	}
	if !confirmDelivery(cmd) {
		fmt.Println(cli.Dim("  skipped — nothing was pushed"))
		return false
	}

	url, err := vcs.Deliver(ctx, root, planned, body)
	if err != nil {
		cli.Warn("pull request failed: " + err.Error())
		return false
	}
	cli.KeyVal("pull request", url)
	return true
}

// confirmDelivery asks before anything leaves the machine.
//
// With no TTY the answer is NO unless --yes was passed. A prompt nobody can
// answer must never default to the irreversible option — the same rule the
// HITL gates follow, for the same reason.
func confirmDelivery(cmd *cobra.Command) bool {
	if flagBool(cmd, "yes") {
		return true
	}
	if !cli.IsInteractive() {
		cli.Warn("no terminal to confirm on — pass --yes to open the pull request unattended")
		return false
	}
	fmt.Print("  open this pull request? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// deliveryText derives the PR title and body from the run.
func deliveryText(board *plan.Board, iss *vcs.Issue) (title, body string) {
	switch {
	case iss != nil:
		title = fmt.Sprintf("Fix #%d: %s", iss.Number, iss.Title)
	case board != nil && strings.TrimSpace(board.Query) != "":
		title = firstLineOf(board.Query)
	default:
		title = "Automated change"
	}

	var b strings.Builder
	if board != nil && strings.TrimSpace(board.Query) != "" {
		b.WriteString(firstLineOf(board.Query))
		b.WriteString("\n\n")
	}
	if board != nil && len(board.Tasks) > 0 {
		b.WriteString("### Tasks\n")
		for _, t := range board.Tasks {
			mark := " "
			if t.Column == plan.ColDone {
				mark = "x"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", mark, firstLineOf(t.Title))
		}
		b.WriteString("\n")
	}
	// Acceptance criteria travel with the change: they are the definition of
	// done a reviewer would otherwise have to reconstruct from the diff, and
	// after P1 they are already structured and already verified.
	if crit := criteriaSection(board); crit != "" {
		b.WriteString(crit)
	}
	b.WriteString("\nGenerated by slmcode.\n")
	return title, b.String()
}

// criteriaSection renders every task's acceptance criteria as a checklist.
func criteriaSection(board *plan.Board) string {
	if board == nil {
		return ""
	}
	var b strings.Builder
	for _, t := range board.Tasks {
		for _, c := range t.Criteria {
			if b.Len() == 0 {
				b.WriteString("### Acceptance criteria\n")
			}
			fmt.Fprintf(&b, "- **%s** (%s) %s", c.ID, c.Priority, firstLineOf(c.Text))
			if c.Verify != "" {
				fmt.Fprintf(&b, " — `%s`", c.Verify)
			}
			b.WriteString("\n")
		}
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// changedFiles collects the focus files of every completed task.
//
// Task focus, not `git status`: the point of naming files explicitly is that a
// delivery ships what the RUN changed and leaves whatever else the operator had
// in flight exactly where it was.
func changedFiles(board *plan.Board) []string {
	if board == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range board.Tasks {
		if t.Column != plan.ColDone {
			continue
		}
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 160 {
		s = strings.TrimSpace(s[:160])
	}
	return s
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}

// registerDeliveryFlags adds the intake and delivery flags to `run`.
func registerDeliveryFlags(cmd *cobra.Command) {
	cmd.Flags().String("issue", "", "run against a GitHub issue (URL, owner/repo#123, or 123)")
	cmd.Flags().String("deliver", "", "deliver the result: 'pr' opens a pull request (asks first)")
	cmd.Flags().String("branch", "", "branch to deliver on (default: derived from the title)")
	cmd.Flags().String("base", "", "pull request base branch (default: the repository's)")
	cmd.Flags().Bool("draft", false, "open the pull request as a draft")
	cmd.Flags().Bool("yes", false, "skip the delivery confirmation (required with no TTY)")
}
