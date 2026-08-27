// Package vcs connects a run to the forge: an issue on the way in, a pull
// request on the way out.
//
// It is a thin shell around the `gh` CLI, deliberately. A Go GitHub client
// would mean a new dependency, a new auth story, a new token to store and a new
// place for that token to leak; `gh` is already installed on the machines that
// would use this, already authenticated, and already the thing the user would
// have typed. The cost is that `gh` must be on PATH, which is a clear,
// actionable error rather than a silent capability gap.
package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/quality"
)

// MaxIssueBody caps how much of an issue becomes the query.
//
// An issue can be arbitrarily long, and the query it produces is packed into
// every specialist's prompt for the whole run. A 40KB issue would evict the
// repo map, the skills and the file excerpts that make the run work — on a
// local model, silently and completely.
const MaxIssueBody = 6000

// cmdTimeout bounds any single forge call.
const cmdTimeout = 30 * time.Second

// Issue is the part of a forge issue a run needs.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string
}

// Query renders the issue as a run query.
//
// The body is UNTRUSTED. It is written by whoever opened the issue — which on a
// public repository is anyone at all — and it flows straight into the prompt of
// every specialist in the run. Two things follow, and both are done here rather
// than left to callers:
//
//   - harness markers are defused. The gates in pkg/quality read plain markdown
//     (`## Deterministic smoke`, `PASSED`) back as ground truth, so an issue
//     body containing those strings could otherwise forge evidence for work
//     nobody did.
//   - the text is framed as a REPORT rather than as instructions. "Issue #12
//     says X" is a description of a request; pasting X raw makes the issue
//     author's words indistinguishable from the operator's.
func (i Issue) Query() string {
	title := clip(quality.DefuseHarnessMarkers(strings.TrimSpace(i.Title)), 300)
	body := clip(quality.DefuseHarnessMarkers(strings.TrimSpace(i.Body)), MaxIssueBody)

	var b strings.Builder
	fmt.Fprintf(&b, "Resolve issue #%d: %s", i.Number, title)
	if body != "" {
		b.WriteString("\n\nThe issue reports:\n")
		b.WriteString(body)
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "\n… (issue truncated)"
}

// issueRefRe matches the accepted issue references:
//
//	https://github.com/owner/repo/issues/123
//	owner/repo#123
//	#123
//	123
//
// Anything else is refused. The reference reaches `gh` as argv, and while argv
// is not a shell, a permissive parser here would still let a caller aim the
// command at an arbitrary repository — which is a data-exfiltration shape, not
// just a malformed input.
var issueRefRe = regexp.MustCompile(
	`^(?:https?://[^/]*github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)/?|([\w.-]+/[\w.-]+)#(\d+)|#?(\d+))$`)

// ParseIssueRef splits a reference into an optional repo and an issue number.
func ParseIssueRef(ref string) (repo string, number int, err error) {
	ref = strings.TrimSpace(ref)
	m := issueRefRe.FindStringSubmatch(ref)
	if m == nil {
		return "", 0, fmt.Errorf(
			"unrecognized issue reference %q — use a github.com issue URL, owner/repo#123, or 123", ref)
	}
	switch {
	case m[1] != "":
		repo = m[1]
		number, _ = strconv.Atoi(m[2])
	case m[3] != "":
		repo = m[3]
		number, _ = strconv.Atoi(m[4])
	default:
		number, _ = strconv.Atoi(m[5])
	}
	if number <= 0 {
		return "", 0, fmt.Errorf("issue number must be positive, got %d", number)
	}
	return repo, number, nil
}

// FetchIssue reads an issue through the `gh` CLI.
func FetchIssue(ctx context.Context, root, ref string) (Issue, error) {
	repo, number, err := ParseIssueRef(ref)
	if err != nil {
		return Issue{}, err
	}
	if err := RequireGH(); err != nil {
		return Issue{}, err
	}
	args := []string{"issue", "view", strconv.Itoa(number),
		"--json", "number,title,body,url,labels"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := run(ctx, root, "gh", args...)
	if err != nil {
		return Issue{}, fmt.Errorf("gh issue view %d: %w", number, err)
	}

	var payload struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return Issue{}, fmt.Errorf("parse gh issue JSON: %w", err)
	}
	iss := Issue{
		Number: payload.Number,
		Title:  payload.Title,
		Body:   payload.Body,
		URL:    payload.URL,
	}
	for _, l := range payload.Labels {
		if n := strings.TrimSpace(l.Name); n != "" {
			iss.Labels = append(iss.Labels, n)
		}
	}
	if iss.Number == 0 {
		iss.Number = number
	}
	return iss, nil
}

// RequireGH reports a clear, actionable error when the CLI is missing.
func RequireGH() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("the GitHub CLI (gh) is not on PATH — install it from https://cli.github.com " +
			"and run `gh auth login`")
	}
	return nil
}

// run executes an argv-only command in root and returns its stdout.
//
// argv-only, never a shell: every argument here is either a fixed literal or a
// value this package has already validated, and keeping the shell out means a
// value that slips validation still cannot become a second command.
func run(ctx context.Context, root, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- argv-only, validated arguments
	if root != "" {
		cmd.Dir = root
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", err, firstLine(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError) //nolint:errorlint // direct type check is the intent
	if ok {
		*target = ee
	}
	return ok
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
