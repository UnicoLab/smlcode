package compact

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// DefaultDigestBytes is the budget for a rendered Digest.
const DefaultDigestBytes = 1200

// MustPreserve is the documented, extensible schema of what a conversation
// compaction is REQUIRED to carry across the cut. ResumeMessage promises the
// model that "the summary above preserves the work done so far"; this type is
// what makes that promise true. Callers may extend it via Extra.
//
// The field order below is the render order and is deliberate: state the model
// can act on (files, commands) comes before narrative (decisions).
type MustPreserve struct {
	// FilesRead are paths the agent has already looked at — the model should
	// not spend turns re-reading them.
	FilesRead []string `json:"files_read,omitempty"`
	// FilesEdited are paths already modified. Losing these causes the classic
	// small-model failure of re-applying an edit that already landed.
	FilesEdited []string `json:"files_edited,omitempty"`
	// Commands are the last few shell invocations with their exit status.
	Commands []CommandRecord `json:"commands,omitempty"`
	// Failures are the last few failed tool calls: tool, path, first error line.
	Failures []FailedCall `json:"failures,omitempty"`
	// Decisions are the last few assistant messages that made a claim without
	// calling a tool — the reasoning that would otherwise be lost entirely.
	Decisions []string `json:"decisions,omitempty"`
	// Extra lets a caller append its own must-preserve lines under a heading.
	Extra map[string][]string `json:"extra,omitempty"`
	// DroppedMessages is the count of messages the digest stands in for.
	DroppedMessages int `json:"dropped_messages,omitempty"`
}

// Digest is an alias kept short for call sites.
type Digest = MustPreserve

// CommandRecord is one shell command and how it ended.
type CommandRecord struct {
	Command string `json:"command"`
	Status  string `json:"status,omitempty"` // "ok", "exit 1", "unknown"
}

// FailedCall is one failed tool invocation.
type FailedCall struct {
	Tool  string `json:"tool"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error"`
}

// Digest extraction limits (per the must-preserve schema).
const (
	MaxDigestFiles     = 12
	MaxDigestCommands  = 5
	MaxDigestFailures  = 3
	MaxDigestDecisions = 5
)

var readTools = map[string]bool{"ws_read": true, "ws_list": true, "ws_glob": true, "ws_grep": true}
var editTools = map[string]bool{"ws_edit": true, "ws_write": true, "ws_patch": true, "ws_delete": true, "ws_mv": true}
var shellTools = map[string]bool{"ws_shell": true, "bash": true, "shell": true, "run_command": true}

// BuildDigest extracts the must-preserve state from the messages a compaction
// is about to drop.
func BuildDigest(dropped []ChatMsg) MustPreserve {
	d := MustPreserve{DroppedMessages: len(dropped)}
	callByID := map[string]ToolCallRef{}
	var reads, edits []string

	for _, m := range dropped {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				callByID[tc.ID] = tc
			}
			name := strings.ToLower(tc.Name)
			path := argPath(tc.Arguments)
			switch {
			case readTools[name] && path != "":
				reads = append(reads, path)
			case editTools[name] && path != "":
				edits = append(edits, path)
			case shellTools[name]:
				cmd := argString(tc.Arguments, "command", "cmd", "script")
				if cmd != "" {
					d.Commands = append(d.Commands, CommandRecord{
						Command: textutil.FirstLine(cmd, 120), Status: "unknown",
					})
				}
			}
		}
		if role == RoleTool {
			call := callByID[m.ToolCallID]
			name := strings.ToLower(call.Name)
			if name == "" {
				name = strings.ToLower(m.Name)
			}
			if failed, errLine := toolResultFailure(m.Content); failed {
				d.Failures = append(d.Failures, FailedCall{
					Tool: orUnknown(name), Path: argPath(call.Arguments),
					Error: textutil.FirstLine(errLine, 140),
				})
			}
			if shellTools[name] && len(d.Commands) > 0 {
				d.Commands[len(d.Commands)-1].Status = exitStatus(m.Content)
			}
			continue
		}
		if role == RoleAssistant && len(m.ToolCalls) == 0 {
			if line := textutil.FirstLine(m.Content, 160); line != "" {
				d.Decisions = append(d.Decisions, line)
			}
		}
	}

	d.FilesRead = lastN(sortedUnique(reads), MaxDigestFiles)
	d.FilesEdited = lastN(sortedUnique(edits), MaxDigestFiles)
	d.Commands = lastNCommands(d.Commands, MaxDigestCommands)
	d.Failures = lastNFailures(d.Failures, MaxDigestFailures)
	d.Decisions = lastN(d.Decisions, MaxDigestDecisions)
	return d
}

// Empty reports whether extraction found nothing worth preserving.
func (d MustPreserve) Empty() bool {
	return len(d.FilesRead) == 0 && len(d.FilesEdited) == 0 && len(d.Commands) == 0 &&
		len(d.Failures) == 0 && len(d.Decisions) == 0 && len(d.Extra) == 0
}

// Render emits the digest as a terse, list-shaped block under maxBytes.
// List-shaped context outperforms prose narration for small models, and the
// explicit headings give the model stable anchors to look things up under.
func (d MustPreserve) Render(maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultDigestBytes
	}
	var b strings.Builder
	b.WriteString("## Compacted session state\n\n")
	if d.DroppedMessages > 0 {
		fmt.Fprintf(&b, "_%d earlier messages compacted. The facts below are preserved verbatim._\n\n", d.DroppedMessages)
	}
	writeList := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, it := range items {
			b.WriteString("- " + it + "\n")
		}
		b.WriteString("\n")
	}
	writeList("Files read:", d.FilesRead)
	writeList("Files edited:", d.FilesEdited)
	if len(d.Commands) > 0 {
		b.WriteString("Commands run + exit status:\n")
		for _, c := range d.Commands {
			fmt.Fprintf(&b, "- `%s` → %s\n", c.Command, orUnknown(c.Status))
		}
		b.WriteString("\n")
	}
	if len(d.Failures) > 0 {
		b.WriteString("Failed tool calls:\n")
		for _, f := range d.Failures {
			if f.Path != "" {
				fmt.Fprintf(&b, "- %s %s: %s\n", f.Tool, f.Path, f.Error)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", f.Tool, f.Error)
			}
		}
		b.WriteString("\n")
	}
	writeList("Decisions:", d.Decisions)
	for _, k := range sortedUnique(keysOf(d.Extra)) {
		writeList(k+":", d.Extra[k])
	}
	return textutil.Truncate(strings.TrimRight(b.String(), "\n")+"\n", maxBytes, "\n…[digest truncated]\n")
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func lastN(in []string, n int) []string {
	if n <= 0 || len(in) <= n {
		return in
	}
	return in[len(in)-n:]
}

func lastNCommands(in []CommandRecord, n int) []CommandRecord {
	if n <= 0 || len(in) <= n {
		return in
	}
	return in[len(in)-n:]
}

func lastNFailures(in []FailedCall, n int) []FailedCall {
	if n <= 0 || len(in) <= n {
		return in
	}
	return in[len(in)-n:]
}

// argPath pulls a file path out of a tool-call argument blob (JSON or raw).
func argPath(args string) string {
	return argString(args, "path", "file", "file_path", "filename", "target")
}

func argString(args string, keys ...string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err == nil {
		for _, k := range keys {
			if v, ok := parsed[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	// Non-JSON arguments: look for key="value" / key: value.
	for _, k := range keys {
		if i := strings.Index(args, k); i >= 0 {
			rest := args[i+len(k):]
			rest = strings.TrimLeft(rest, " \t:=\"'")
			if j := strings.IndexAny(rest, "\"',\n}"); j >= 0 {
				rest = rest[:j]
			}
			if s := strings.TrimSpace(rest); s != "" {
				return s
			}
		}
	}
	return ""
}

var failureNeedles = []string{
	"error:", "error ", "failed", "failure", "exception", "traceback",
	"no such file", "not found", "permission denied", "panic:",
	"exit status 1", "exit code 1", "did not match", "no match",
}

func toolResultFailure(content string) (bool, string) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false, ""
	}
	lower := strings.ToLower(trimmed)
	// Structured {"error": "..."} results.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		if v, ok := parsed["error"]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return true, s
			}
		}
		if v, ok := parsed["ok"]; ok {
			if b, ok := v.(bool); ok && !b {
				return true, textutil.FirstLine(trimmed, 140)
			}
		}
	}
	for _, needle := range failureNeedles {
		if strings.Contains(lower, needle) {
			// Report the first line that actually contains the needle.
			for _, line := range strings.Split(trimmed, "\n") {
				if strings.Contains(strings.ToLower(line), needle) {
					return true, strings.TrimSpace(line)
				}
			}
			return true, textutil.FirstLine(trimmed, 140)
		}
	}
	return false, ""
}

func exitStatus(content string) string {
	lower := strings.ToLower(content)
	for _, marker := range []string{"exit status ", "exit code ", "exitcode="} {
		if i := strings.Index(lower, marker); i >= 0 {
			rest := strings.TrimSpace(content[i+len(marker):])
			end := 0
			for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
				end++
			}
			if end > 0 {
				if rest[:end] == "0" {
					return "ok"
				}
				return "exit " + rest[:end]
			}
		}
	}
	if failed, _ := toolResultFailure(content); failed {
		return "failed"
	}
	return "ok"
}
