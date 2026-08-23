package evolve

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/repair"
)

// RepairKind is the discriminator of the repair union. Modeling repairs as a
// small typed union rather than free text is what lets the harness *apply* a
// remembered fix instead of merely describing it to a model and hoping.
type RepairKind string

const (
	// RepairGuidance injects text into the next prompt. The weakest repair:
	// it still costs an LLM round-trip, but a targeted one.
	RepairGuidance RepairKind = "guidance"
	// RepairTransformArgs rewrites the failed tool call's arguments with a
	// named deterministic transform and retries. No LLM round-trip at all.
	RepairTransformArgs RepairKind = "transform_args"
	// RepairSwitchTool retries with a different tool.
	RepairSwitchTool RepairKind = "switch_tool"
	// RepairEditFormat switches the edit format for the retry.
	RepairEditFormat RepairKind = "edit_format"
	// RepairConfig changes a config knob for the retry.
	RepairConfig RepairKind = "config"
	// RepairShell runs a fixup command (subject to the permission system —
	// evolve never executes anything itself).
	RepairShell RepairKind = "shell"
	// RepairAction asks the harness to perform a known recovery action
	// (re-read a file, compact the context, shorten the response) and retry.
	RepairAction RepairKind = "action"
)

// Named recovery actions for RepairAction.
const (
	ActionRereadFile      = "reread_file"
	ActionCompactContext  = "compact_context"
	ActionShortenResponse = "shorten_response"
	ActionRaiseMaxTokens  = "raise_max_tokens"
	ActionSplitTask       = "split_task"
	ActionForceDifferent  = "force_different_action"
	ActionEscalateModel   = "escalate_model"
	ActionBackoffRetry    = "backoff_retry"
)

// Named argument transforms for RepairTransformArgs.
const (
	TransformStripLineNumbers = "strip_line_number_prefix"
	TransformSetReplaceAll    = "set_replace_all"
	TransformTrimTrailingWS   = "trim_trailing_whitespace"
	TransformUnfence          = "unfence_code"
	TransformRepairJSON       = "repair_json"
	TransformShrinkOldStr     = "shrink_old_str"
)

// AllTransforms lists every transform name, for docs and validation.
var AllTransforms = []string{
	TransformStripLineNumbers, TransformSetReplaceAll, TransformTrimTrailingWS,
	TransformUnfence, TransformRepairJSON, TransformShrinkOldStr,
}

// Repair is the typed union of things the harness can do about a failure.
// Exactly one kind is active; the fields that kind needs must be set, which
// Validate enforces.
type Repair struct {
	Kind RepairKind `json:"kind"`
	// Guidance is ALWAYS populated: even a deterministic repair carries a
	// human-readable explanation, for REFLECTION.md and for the prompt when
	// the deterministic path is unavailable.
	Guidance string `json:"guidance"`

	Transform   string `json:"transform,omitempty"`
	Tool        string `json:"tool,omitempty"`
	EditFormat  string `json:"edit_format,omitempty"`
	ConfigKey   string `json:"config_key,omitempty"`
	ConfigValue string `json:"config_value,omitempty"`
	Command     string `json:"command,omitempty"`
	Action      string `json:"action,omitempty"`
	// Retry says whether the harness should immediately retry the failed
	// operation after applying the repair.
	Retry bool `json:"retry,omitempty"`
}

// ErrInvalidRepair is returned by Validate.
var ErrInvalidRepair = errors.New("evolve: invalid repair")

// Validate enforces the union's invariants.
func (r Repair) Validate() error {
	if strings.TrimSpace(r.Guidance) == "" {
		return errors.Join(ErrInvalidRepair, errors.New("guidance is required for every repair kind"))
	}
	switch r.Kind {
	case RepairGuidance:
		return nil
	case RepairTransformArgs:
		if !validTransform(r.Transform) {
			return errors.Join(ErrInvalidRepair, errors.New("unknown transform "+r.Transform))
		}
	case RepairSwitchTool:
		if strings.TrimSpace(r.Tool) == "" {
			return errors.Join(ErrInvalidRepair, errors.New("switch_tool needs a tool"))
		}
	case RepairEditFormat:
		if strings.TrimSpace(r.EditFormat) == "" {
			return errors.Join(ErrInvalidRepair, errors.New("edit_format needs a format"))
		}
	case RepairConfig:
		if strings.TrimSpace(r.ConfigKey) == "" {
			return errors.Join(ErrInvalidRepair, errors.New("config needs a key"))
		}
	case RepairShell:
		if strings.TrimSpace(r.Command) == "" {
			return errors.Join(ErrInvalidRepair, errors.New("shell needs a command"))
		}
	case RepairAction:
		if strings.TrimSpace(r.Action) == "" {
			return errors.Join(ErrInvalidRepair, errors.New("action needs an action name"))
		}
	default:
		return errors.Join(ErrInvalidRepair, errors.New("unknown kind "+string(r.Kind)))
	}
	return nil
}

// Deterministic reports whether applying this repair needs no model call.
func (r Repair) Deterministic() bool {
	switch r.Kind {
	case RepairTransformArgs, RepairEditFormat, RepairConfig, RepairAction, RepairShell, RepairSwitchTool:
		return true
	default:
		return false
	}
}

// String is a one-line summary for logs and REFLECTION.md.
func (r Repair) String() string {
	switch r.Kind {
	case RepairTransformArgs:
		return "transform args: " + r.Transform
	case RepairSwitchTool:
		return "switch tool: " + r.Tool
	case RepairEditFormat:
		return "edit format: " + r.EditFormat
	case RepairConfig:
		return "set " + r.ConfigKey + "=" + r.ConfigValue
	case RepairShell:
		return "run: " + r.Command
	case RepairAction:
		return "action: " + r.Action
	default:
		return "guidance"
	}
}

func validTransform(name string) bool {
	for _, t := range AllTransforms {
		if t == name {
			return true
		}
	}
	return false
}

// ApplyTransform runs a named deterministic transform over a tool call's raw
// JSON arguments. It returns the new arguments and whether anything changed.
// An unknown transform, unparsable arguments or a no-op all return ok=false —
// the caller then falls back to the guidance text.
func ApplyTransform(name, args string) (string, bool) {
	switch name {
	case TransformRepairJSON:
		fixed, err := repair.RepairToolArgs(args)
		if err != nil || strings.TrimSpace(fixed) == "" || fixed == args {
			return args, false
		}
		return fixed, true
	case TransformStripLineNumbers:
		return stripLineNumbersInArgs(args)
	case TransformTrimTrailingWS:
		return mutateStringFields(args, []string{"old_str", "new_str"}, trimTrailingWS)
	case TransformUnfence:
		return mutateStringFields(args, []string{"old_str", "new_str", "content", "body"}, unfence)
	case TransformShrinkOldStr:
		return mutateStringFields(args, []string{"old_str"}, shrinkToAnchor)
	case TransformSetReplaceAll:
		return setBoolField(args, "replace_all", true)
	default:
		return args, false
	}
}

var reLineNumPrefix = regexp.MustCompile(`(?m)^[ \t]*\d+[ \t]*\|[ \t]?`)

// stripLineNumbersInArgs applies the gutter strip only where a ws_read gutter
// can legitimately BE.
//
// It used to run over `old_str`, `new_str`, `content` and `body` alike — i.e.
// over the FILE THE HARNESS WAS ABOUT TO WRITE. `^[ \t]*\d+[ \t]*\|` is not
// only a ws_read gutter; it is an ordinary line of pipe-delimited data. A
// markdown table row `1 | Alpha`, a CSV dump, an ASCII table, a changelog
// column — every one of them lost its first field, silently, in the file that
// reached disk, and nothing downstream could tell that from the model having
// written it that way.
//
// What remains:
//
//   - old_str and patch/diff hunks: text the model COPIED out of a ws_read, so
//     a gutter there is provably transcription, not content;
//   - new_str, but ONLY when old_str in the same call also carries a gutter.
//     That is the evidence that this model is mirroring a ws_read block into
//     both halves of the edit. Without that evidence a pipe-prefixed new_str is
//     just a table row being inserted, and stripping it is the corruption.
//
// content/body — whole-file writes — are never touched.
func stripLineNumbersInArgs(args string) (string, bool) {
	fields := []string{"old_str", "patch", "diff", "hunk"}
	if oldStr, ok := stringField(args, "old_str"); ok && reLineNumPrefix.MatchString(oldStr) {
		fields = append(fields, "new_str")
	}
	return mutateStringFields(args, fields, stripLineNumberPrefix)
}

// stringField reads one string field out of a tool call's raw JSON arguments.
func stringField(args, name string) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", false
	}
	s, ok := parsed[name].(string)
	return s, ok
}

// stripLineNumberPrefix removes ws_read's "   42|" gutter, the single most
// common reason an old_str can never match.
func stripLineNumberPrefix(s string) string {
	if !reLineNumPrefix.MatchString(s) {
		return s
	}
	return reLineNumPrefix.ReplaceAllString(s, "")
}

func trimTrailingWS(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	return strings.Join(lines, "\n")
}

var reFence = regexp.MustCompile("(?s)^\\s*```[a-zA-Z0-9_+-]*\\s*\n(.*?)\n?\\s*```\\s*$")

func unfence(s string) string {
	if m := reFence.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return s
}

// shrinkToAnchor reduces a sprawling old_str to its first non-blank line.
// A long old_str is fragile: every intervening line is another chance for the
// model's copy to drift from the file. One well-chosen line usually still
// anchors uniquely, and when it does not the ambiguity repair takes over.
func shrinkToAnchor(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 2 {
		return s
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return s
}

func mutateStringFields(args string, fields []string, fn func(string) string) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return args, false
	}
	changed := false
	for _, f := range fields {
		v, ok := parsed[f]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out := fn(s)
		if out != s {
			parsed[f] = out
			changed = true
		}
	}
	if !changed {
		return args, false
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		return args, false
	}
	return string(data), true
}

func setBoolField(args, field string, value bool) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return args, false
	}
	if cur, ok := parsed[field].(bool); ok && cur == value {
		return args, false
	}
	parsed[field] = value
	data, err := json.Marshal(parsed)
	if err != nil {
		return args, false
	}
	return string(data), true
}
