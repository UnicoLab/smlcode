// Package evolve is slmcode's self-improvement subsystem: fingerprint a
// failure, look up (or learn) a repair for it, choose harness knobs with a
// bandit that learns from real outcomes, and reflect on each run.
//
// The organizing principle is "fail once". Every failure is reduced to a
// stable fingerprint; a fingerprint that has been seen before maps to a stored
// repair that is applied immediately, with no LLM round-trip. A fingerprint
// that has not been seen before produces a candidate rule once the failure is
// resolved, so the *next* occurrence is cheap.
//
// Everything is deterministic and works with zero LLM calls. Everything is
// bounded, prunable, and persisted as human-readable JSON under .slmcode/evolve
// and ~/.slmcode/evolve.
package evolve

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Class is the coarse category of a failure. The class alone determines the
// repair strategy for structural failures (a missing old_str is always fixed
// the same way); for content failures (a compile error, a test failure) the
// normalized message participates in identity too.
type Class string

const (
	ClassUnknown          Class = "unknown"
	ClassToolArgs         Class = "tool_args"
	ClassEditNotFound     Class = "edit_not_found"
	ClassEditAmbiguous    Class = "edit_ambiguous"
	ClassEditLineNumbers  Class = "edit_line_numbers"
	ClassEditEmptyOldStr  Class = "edit_empty_old_str"
	ClassEditNoOp         Class = "edit_noop"
	ClassFileNotRead      Class = "file_not_read"
	ClassFileNotFound     Class = "file_not_found"
	ClassPatchFailed      Class = "patch_failed"
	ClassMalformedJSON    Class = "malformed_json"
	ClassTruncatedOutput  Class = "truncated_output"
	ClassCompileError     Class = "compile_error"
	ClassTestFailure      Class = "test_failure"
	ClassLintError        Class = "lint_error"
	ClassTimeout          Class = "timeout"
	ClassContextOverflow  Class = "context_overflow"
	ClassProviderError    Class = "provider_error"
	ClassRateLimit        Class = "rate_limit"
	ClassNoProgress       Class = "no_progress"
	ClassPermissionDenied Class = "permission_denied"
	ClassDependency       Class = "dependency_missing"
	ClassReviewRejected   Class = "review_rejected"
)

// AllClasses is every class, in a stable order (docs, UIs, tests).
var AllClasses = []Class{
	ClassToolArgs, ClassEditNotFound, ClassEditAmbiguous, ClassEditLineNumbers,
	ClassEditEmptyOldStr, ClassEditNoOp, ClassFileNotRead, ClassFileNotFound,
	ClassPatchFailed, ClassMalformedJSON, ClassTruncatedOutput, ClassCompileError,
	ClassTestFailure, ClassLintError, ClassTimeout, ClassContextOverflow,
	ClassProviderError, ClassRateLimit, ClassNoProgress, ClassPermissionDenied,
	ClassDependency, ClassReviewRejected, ClassUnknown,
}

// structuralClasses are failures whose identity is fully determined by class
// and tool: every "old_str not found" is the same problem no matter which file
// or which text missed, so their normalized messages are deliberately excluded
// from the hash. This is what makes two superficially different messages
// collapse to one fingerprint.
var structuralClasses = map[Class]bool{
	ClassEditNotFound:    true,
	ClassEditAmbiguous:   true,
	ClassEditLineNumbers: true,
	ClassEditEmptyOldStr: true,
	ClassEditNoOp:        true,
	ClassFileNotRead:     true,
	ClassMalformedJSON:   true,
	ClassTruncatedOutput: true,
	ClassContextOverflow: true,
	ClassTimeout:         true,
	ClassNoProgress:      true,
	ClassRateLimit:       true,
	ClassPatchFailed:     true,
	ClassToolArgs:        true,
	ClassReviewRejected:  true,
}

// Signal is everything known about a failure at the moment it happens.
type Signal struct {
	// Tool is the tool that failed ("ws_edit", "ws_shell", ""). Lowercased.
	Tool string
	// Message is the raw error text — a tool result, stderr, an HTTP body.
	Message string
	// Language is the project language ("go", "python"…).
	Language string
	// Model is the concrete model id; the family is derived from it.
	Model string
	// ModelFamily overrides the family derived from Model.
	ModelFamily string
	// Path, Command, ExitCode, Phase and Role are optional context.
	Path     string
	Command  string
	ExitCode int
	Phase    string
	Role     string
}

// Fingerprint is the stable identity of a failure.
type Fingerprint struct {
	ID          string `json:"id"`
	Class       Class  `json:"class"`
	Tool        string `json:"tool,omitempty"`
	Language    string `json:"language,omitempty"`
	ModelFamily string `json:"model_family,omitempty"`
	// Norm is the normalized message, kept for humans and for rule evidence.
	Norm string `json:"norm,omitempty"`
	// Salient is the substring that actually participated in the hash.
	Salient string `json:"salient,omitempty"`
}

// String returns the fingerprint id.
func (f Fingerprint) String() string { return f.ID }

// Zero reports whether the fingerprint is empty.
func (f Fingerprint) Zero() bool { return f.ID == "" }

// MaxNormLen bounds the normalized message. A stack trace is not identity.
const MaxNormLen = 300

// Analyze turns a raw failure into a stable fingerprint.
//
// The contract: two occurrences of the same underlying problem produce the
// same ID, even when the messages differ in paths, line numbers, hashes,
// timings, addresses, quoted content and counts.
func Analyze(sig Signal) Fingerprint {
	tool := strings.ToLower(strings.TrimSpace(sig.Tool))
	lang := memory.NormalizeLanguage(sig.Language)
	family := strings.TrimSpace(sig.ModelFamily)
	if family == "" {
		family = memory.ModelFamily(sig.Model)
	}
	norm := Normalize(sig.Message)
	class := Classify(sig)

	salient := norm
	if structuralClasses[class] {
		// Identity is class + tool only: the message is presentational.
		salient = ""
	}
	fp := Fingerprint{
		Class: class, Tool: tool, Language: lang, ModelFamily: family,
		Norm: norm, Salient: salient,
	}
	if class == ClassUnknown && norm == "" && tool == "" {
		return Fingerprint{Class: ClassUnknown}
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(class), tool, lang, family, salient,
	}, "\x00")))
	fp.ID = "fp_" + hex.EncodeToString(sum[:])[:12]
	return fp
}

// Normalization patterns. Order matters: the more specific pattern wins.
var (
	reANSI      = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	reTimestamp = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	reClock     = regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}\b`)
	reDuration  = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|us|ms|s|m|h)\b`)
	reHexAddr   = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	reLongHex   = regexp.MustCompile(`\b[0-9a-f]{7,}\b`)
	reIPPort    = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)
	reURL       = regexp.MustCompile(`\bhttps?://[^\s"')]+`)
	reWinPath   = regexp.MustCompile(`\b[A-Za-z]:\\[^\s"':]+`)
	rePosixPath = regexp.MustCompile(`(\.{0,2}/)?(?:[\w.@+-]+/)+[\w.@+-]+(\.\w+)?`)
	reLineCol   = regexp.MustCompile(`:\d+(:\d+)?\b`)
	reLineWord  = regexp.MustCompile(`(?i)\b(line|lines|col|column|offset|byte|position|pos)\s+#?\d+`)
	reQuoted    = regexp.MustCompile("(\"[^\"\\n]{0,200}\"|'[^'\\n]{0,200}'|`[^`\\n]{0,200}`)")
	reNumber    = regexp.MustCompile(`\b\d+([.,]\d+)*\b`)
	reWS        = regexp.MustCompile(`\s+`)
	reTestID    = regexp.MustCompile(`(?i)::[\w\[\]<>.-]+`)
	reGoTestRun = regexp.MustCompile(`(?i)---\s+(fail|pass|skip):\s+\S+`)
	rePyFile    = regexp.MustCompile(`(?i)file "<path>", <line>`)
)

// stackCutMarkers end the interesting part of a message: everything after is
// a stack trace or a goroutine dump, which is per-occurrence noise.
var stackCutMarkers = []string{
	"\ngoroutine ", "\n\tat ", "\nTraceback (most recent call last)",
	"\n  File \"", "\nStack trace:", "\npanic: runtime error",
}

// Normalize reduces an error message to a stable, comparable form: no paths,
// no line numbers, no hashes, no timestamps, no addresses, no durations, no
// quoted payloads and no bare numbers.
func Normalize(msg string) string {
	s := strings.TrimSpace(msg)
	if s == "" {
		return ""
	}
	s = reANSI.ReplaceAllString(s, "")
	for _, marker := range stackCutMarkers {
		if i := strings.Index(s, marker); i > 0 {
			s = s[:i]
		}
	}
	s = reTimestamp.ReplaceAllString(s, "<ts>")
	s = reClock.ReplaceAllString(s, "<ts>")
	s = reDuration.ReplaceAllString(s, "<dur>")
	s = reURL.ReplaceAllString(s, "<url>")
	s = reIPPort.ReplaceAllString(s, "<addr>")
	s = reHexAddr.ReplaceAllString(s, "<addr>")
	s = reLongHex.ReplaceAllString(s, "<hash>")
	s = reWinPath.ReplaceAllString(s, "<path>")
	s = rePosixPath.ReplaceAllString(s, "<path>")
	s = reGoTestRun.ReplaceAllString(s, "--- fail: <test>")
	s = reTestID.ReplaceAllString(s, "::<test>")
	s = reLineWord.ReplaceAllString(s, "<line>")
	s = reLineCol.ReplaceAllString(s, ":<line>")
	s = reQuoted.ReplaceAllString(s, "<str>")
	s = reNumber.ReplaceAllString(s, "<n>")
	s = rePyFile.ReplaceAllString(s, "file <path>, <line>")
	s = strings.ToLower(s)
	s = reWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > MaxNormLen {
		s = strings.TrimSpace(s[:MaxNormLen])
	}
	return s
}

// classifier is one message→class rule.
type classifier struct {
	class Class
	// all needles must be present (after lowercasing) for the rule to fire.
	all []string
	// any of these needles is enough.
	any []string
	// tools restricts the rule; empty means any tool.
	tools []string
}

// classifiers are evaluated in order; the first match wins, so the most
// specific patterns come first.
var classifiers = []classifier{
	{class: ClassEditLineNumbers, any: []string{"line-number prefix", "line number prefix", "still contains ws_read's line-number"}},
	{class: ClassEditEmptyOldStr, any: []string{"old_str is empty", "empty old_str", "old_str is empty (or only whitespace)"}},
	{class: ClassEditNoOp, any: []string{"no-op edit refused", "old_str and new_str are identical"}},
	{class: ClassEditAmbiguous, any: []string{"found 3 times", "occurrences", "found multiple times", "is not unique", "matches multiple", "replace_all"}},
	{class: ClassEditAmbiguous, all: []string{"old_str", "times in"}},
	{class: ClassEditNotFound, all: []string{"old_str", "not found"}},
	{class: ClassEditNotFound, any: []string{"search block not found", "searchreplace block", "did not match anything", "no match found for"}},
	{class: ClassFileNotRead, any: []string{"must be read first", "has not been read in this session", "read the file first"}},
	{class: ClassPatchFailed, any: []string{"hunk", "patch does not apply", "malformed patch", "corrupt patch", "can't find file to patch"}},
	{class: ClassTruncatedOutput, any: []string{"max_tokens", "finish_reason: length", "response was truncated", "truncated by the token limit", "length limit reached"}},
	{class: ClassMalformedJSON, any: []string{"unexpected end of json", "invalid character", "cannot unmarshal", "json parse error", "expecting ',' delimiter", "unterminated string", "failed to parse tool arguments", "invalid json"}},
	{class: ClassContextOverflow, any: []string{"context length exceeded", "maximum context length", "context_length_exceeded", "prompt is too long", "too many tokens", "reduce the length of the messages"}},
	{class: ClassRateLimit, any: []string{"rate limit", "429", "too many requests", "quota exceeded"}},
	{class: ClassTimeout, any: []string{"context deadline exceeded", "timed out", "timeout", "deadline exceeded", "i/o timeout"}},
	{class: ClassNoProgress, any: []string{"repeated identical tool call", "same tool call", "no progress", "loop detected", "already called this tool with the same arguments"}},
	{class: ClassPermissionDenied, any: []string{"permission denied", "not permitted", "command is not allowed", "blocked by permission", "operation not permitted", "refused by the permission"}},
	{class: ClassDependency, any: []string{"command not found", "no such command", "modulenotfounderror", "cannot find module", "missing go.sum entry", "package not found", "executable file not found"}},
	{class: ClassCompileError, any: []string{"syntax error", "undefined:", "undeclared name", "cannot use", "declared and not used", "expected declaration", "compilation failed", "compile error", "cannot find package", "type error", "does not implement"}},
	{class: ClassReviewRejected, any: []string{"review rejected", "max retries", "reviewer rejected"}},
	{class: ClassTestFailure, any: []string{"--- fail", "=== fail", "assertionerror", "test failed", "tests failed", "failed tests/", "assert ", "e   assert"}},
	{class: ClassLintError, any: []string{"golangci-lint", "ruff", "eslint", "flake8", "lint error", "vet:"}},
	{class: ClassFileNotFound, any: []string{"no such file or directory", "file does not exist", "cannot find the file", "enoent"}},
	{class: ClassProviderError, any: []string{"internal server error", "502 bad gateway", "503 service unavailable", "connection refused", "eof", "upstream error", "api error", "unauthorized", "invalid api key"}},
	{class: ClassToolArgs, any: []string{"is required", "missing required", "unknown argument", "invalid argument", "unexpected field"}},
}

// Classify assigns a class to a failure signal.
func Classify(sig Signal) Class {
	msg := strings.ToLower(sig.Message)
	if strings.TrimSpace(msg) == "" {
		if sig.ExitCode != 0 {
			return ClassUnknown
		}
		return ClassUnknown
	}
	tool := strings.ToLower(strings.TrimSpace(sig.Tool))
	for _, c := range classifiers {
		if len(c.tools) > 0 && !containsFold(c.tools, tool) {
			continue
		}
		if len(c.all) > 0 && allPresent(msg, c.all) {
			return c.class
		}
		if len(c.any) > 0 && anyPresent(msg, c.any) {
			return c.class
		}
	}
	return ClassUnknown
}

func allPresent(msg string, needles []string) bool {
	for _, n := range needles {
		if !hasNeedle(msg, n) {
			return false
		}
	}
	return true
}

func anyPresent(msg string, needles []string) bool {
	for _, n := range needles {
		if hasNeedle(msg, n) {
			return true
		}
	}
	return false
}

// hasNeedle is Contains with word boundaries for bare single-word needles.
// Without it "timeout" matches the identifier "waveTimeout" and a Go compile
// error gets classified as a network timeout — a real misfire that would send
// the wrong repair to a real failure.
func hasNeedle(msg, needle string) bool {
	if needle == "" {
		return false
	}
	if !isBareWord(needle) {
		return strings.Contains(msg, needle)
	}
	for i := 0; ; {
		j := strings.Index(msg[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		if !isWordByte(byteAt(msg, start-1)) && !isWordByte(byteAt(msg, end)) {
			return true
		}
		i = start + 1
		if i >= len(msg) {
			return false
		}
	}
}

func isBareWord(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	return true
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// Describe returns a one-line human summary of a class.
func Describe(c Class) string {
	switch c {
	case ClassToolArgs:
		return "the tool was called with bad arguments"
	case ClassEditNotFound:
		return "old_str did not match the file"
	case ClassEditAmbiguous:
		return "old_str matched more than once"
	case ClassEditLineNumbers:
		return "old_str still carried ws_read's line-number prefix"
	case ClassEditEmptyOldStr:
		return "old_str was empty"
	case ClassEditNoOp:
		return "old_str and new_str were identical"
	case ClassFileNotRead:
		return "the file was edited before it was read"
	case ClassFileNotFound:
		return "the path does not exist"
	case ClassPatchFailed:
		return "a diff hunk did not apply"
	case ClassMalformedJSON:
		return "the model emitted invalid JSON"
	case ClassTruncatedOutput:
		return "the response hit the token limit and was cut off"
	case ClassCompileError:
		return "the code does not compile"
	case ClassTestFailure:
		return "a test failed"
	case ClassLintError:
		return "a linter rejected the change"
	case ClassTimeout:
		return "the operation timed out"
	case ClassContextOverflow:
		return "the prompt exceeded the model's context window"
	case ClassProviderError:
		return "the model provider returned an error"
	case ClassRateLimit:
		return "the provider rate-limited us"
	case ClassNoProgress:
		return "the same call was repeated with no progress"
	case ClassPermissionDenied:
		return "the command or path is not permitted"
	case ClassDependency:
		return "a required tool or module is missing"
	case ClassReviewRejected:
		return "the reviewer rejected the change repeatedly"
	default:
		return "an unclassified failure"
	}
}
