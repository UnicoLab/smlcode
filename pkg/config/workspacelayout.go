package config

import (
	"fmt"
	"strings"
)

// Which parts of .slmcode/ must never reach a commit.
//
// This list is the single source of truth for BOTH the `.slmcode/.gitignore`
// that `slmcode init` writes and the `slmcode doctor` probe that checks it. It
// lives here rather than in cmd/slmcode because the CLI is not the authority
// on the workspace layout: every one of these paths is created by a package
// under pkg/, and a list maintained next to the `init` command drifts silently
// the moment a package starts writing somewhere new. `slmcode commit` runs
// `git add -A`, so a gap here is not a tidiness problem — it is run content,
// file excerpts and provider metadata landing in the user's history and,
// through a push, in their remote.
//
// What is deliberately NOT ignored: config.yaml, board.json, skills/, agents/,
// blocks/ and hooks.json. Those are the parts of .slmcode/ a team is meant to
// share and review.

// SlmIgnoreEntry is one line of `.slmcode/.gitignore` plus the material needed
// to verify it.
type SlmIgnoreEntry struct {
	// Pattern is the gitignore line, relative to .slmcode/. Directory patterns
	// end in "/" so they cannot match a file of the same name.
	Pattern string
	// Probe is a repo-relative path that the pattern must ignore. Probing a
	// representative CHILD makes the answer correct whether or not the
	// directory exists yet — `git check-ignore` on a directory pattern with no
	// directory present answers nothing useful.
	Probe string
	// What names the content, for the comment in the generated file and for a
	// human reading a doctor failure.
	What string
}

// SlmIgnoreEntries is the authoritative ignore list for .slmcode/.
//
// Keep it sorted by risk: credentials first, then run content, then caches.
var SlmIgnoreEntries = []SlmIgnoreEntry{
	{"auth.json", ".slmcode/auth.json", "provider API keys"},
	{"credentials.json", ".slmcode/credentials.json", "provider API keys (alternate name)"},
	{"sessions/", ".slmcode/sessions/x.json", "per-session transcripts and tool results"},
	{"queries/", ".slmcode/queries/x/events.jsonl", "full event streams for every run"},
	{"memory/", ".slmcode/memory/episodes.jsonl", "episodic memory: run content, file excerpts, reflections"},
	{"summaries/", ".slmcode/summaries/INDEX.md", "model-written summaries of the repository"},
	{"archives/", ".slmcode/archives/x.json", "archived boards and their task history"},
	{"errors/", ".slmcode/errors/errors.md", "captured failures, including command output"},
	{"pending/", ".slmcode/pending/x.patch.json", "review queue: unapplied diffs"},
	{"checkpoints/", ".slmcode/checkpoints/s/x", "pre-write file snapshots"},
	{"waves/", ".slmcode/waves/x/x", "per-wave rewind snapshots of the tree"},
	{"scratch/", ".slmcode/scratch/x", "agent scratch space"},
	{"clarify/", ".slmcode/clarify/ask.json", "HITL clarify handshake"},
	{"plan/", ".slmcode/plan/ask.json", "HITL plan-approval handshake"},
	{"shell/", ".slmcode/shell/ask.json", "HITL shell-approval handshake"},
	{"continue/", ".slmcode/continue/ask.json", "HITL continue handshake"},
	{"escalate/", ".slmcode/escalate/ask.json", "HITL escalation handshake"},
	{"evolve/", ".slmcode/evolve/policy.json", "learned policy, rules and stored regressions"},
	{"autoresearch/", ".slmcode/autoresearch/trials.jsonl", "self-tuning trial log and pre-run file snapshots"},
	{"metrics/", ".slmcode/metrics/runs.jsonl", "per-run metrics, including query text"},
	{"skills/learned/", ".slmcode/skills/learned/SKILL.md", "skills the harness wrote about this repo"},
	{"capabilities.json", ".slmcode/capabilities.json", "probed backend capabilities"},
	{"throughput.json", ".slmcode/throughput.json", "measured tokens/sec per model"},
	{"repomap.json", ".slmcode/repomap.json", "repository map cache"},
	{"eval-report.json", ".slmcode/eval-report.json", "eval harness output"},
	{"CONTEXT.md.bak", ".slmcode/CONTEXT.md.bak", "backup of a rewritten CONTEXT.md"},
	{"*.log", ".slmcode/x.log", "harness logs"},
}

// SlmGitignoreHeader is the first line of the generated file.
const SlmGitignoreHeader = "# Written by slmcode init — keeps secrets and run content out of git."

// RenderSlmGitignore renders `.slmcode/.gitignore` from SlmIgnoreEntries.
//
// Every pattern is preceded by its own comment line: the operator reviewing
// this file should be able to tell what they would start committing if they
// deleted a line. The comment goes ABOVE the pattern, not after it — gitignore
// only treats `#` as a comment at the start of a line, so a trailing comment
// silently becomes part of the pattern and the rule matches nothing.
func RenderSlmGitignore() string {
	var b strings.Builder
	b.WriteString(SlmGitignoreHeader + "\n")
	b.WriteString("# The authoritative list lives in pkg/config (SlmIgnoreEntries) — edit it there.\n")
	b.WriteString("# Not ignored on purpose: config.yaml, board.json, hooks.json, skills/, agents/, blocks/.\n")
	for _, e := range SlmIgnoreEntries {
		fmt.Fprintf(&b, "\n# %s\n%s\n", e.What, e.Pattern)
	}
	return b.String()
}

// SlmIgnoreProbes returns name → repo-relative probe path for every entry, for
// `slmcode doctor`. The name is the pattern without its trailing separator, so
// a doctor report reads like the gitignore file it is checking.
func SlmIgnoreProbes() map[string]string {
	out := make(map[string]string, len(SlmIgnoreEntries))
	for _, e := range SlmIgnoreEntries {
		out[strings.TrimSuffix(e.Pattern, "/")] = e.Probe
	}
	return out
}
