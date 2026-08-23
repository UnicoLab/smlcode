package evolve

import (
	"strings"
	"testing"
)

func TestClassifyRealisticFixtures(t *testing.T) {
	tests := []struct {
		name string
		sig  Signal
		want Class
	}{
		{
			name: "ws_edit old_str missing",
			sig:  Signal{Tool: "ws_edit", Message: "old_str not found in pkg/loop/runner.go.\n\nClosest text already in the file — copy one of these blocks VERBATIM into old_str."},
			want: ClassEditNotFound,
		},
		{
			name: "ws_edit ambiguous",
			sig:  Signal{Tool: "ws_edit", Message: "old_str found 3 times in pkg/plan/tasks.go — pass replace_all:true, or include more surrounding context."},
			want: ClassEditAmbiguous,
		},
		{
			name: "ws_edit line number prefix",
			sig:  Signal{Tool: "ws_edit", Message: "Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`)."},
			want: ClassEditLineNumbers,
		},
		{
			name: "ws_edit empty old_str",
			sig:  Signal{Tool: "ws_edit", Message: "Edit refused — old_str is empty (or only whitespace). An empty search matches nothing."},
			want: ClassEditEmptyOldStr,
		},
		{
			name: "ws_edit no-op",
			sig:  Signal{Tool: "ws_edit", Message: "No-op edit refused — old_str and new_str are identical."},
			want: ClassEditNoOp,
		},
		{
			name: "file must be read first",
			sig:  Signal{Tool: "ws_edit", Message: "File must be read first before edit — pkg/a/b.go has not been read in this session."},
			want: ClassFileNotRead,
		},
		{
			name: "patch hunk failed",
			sig:  Signal{Tool: "ws_patch", Message: "hunk #2 FAILED at 118 -- saving rejects to file pkg/x.go.rej"},
			want: ClassPatchFailed,
		},
		{
			name: "truncated by max_tokens",
			sig:  Signal{Message: `response stopped early: finish_reason: length (max_tokens=2048)`},
			want: ClassTruncatedOutput,
		},
		{
			name: "malformed json tool args",
			sig:  Signal{Tool: "ws_edit", Message: `failed to parse tool arguments: unexpected end of JSON input`},
			want: ClassMalformedJSON,
		},
		{
			name: "context overflow",
			sig:  Signal{Message: "This model's maximum context length is 32768 tokens, however you requested 41022 tokens."},
			want: ClassContextOverflow,
		},
		{
			name: "go compile error",
			sig:  Signal{Tool: "ws_shell", Language: "go", Message: "./pkg/loop/runner.go:412:9: undefined: waveTimeout"},
			want: ClassCompileError,
		},
		{
			name: "go vet unused",
			sig:  Signal{Tool: "ws_shell", Language: "go", Message: "./main.go:12:2: declared and not used: cfg"},
			want: ClassCompileError,
		},
		{
			name: "go test failure",
			sig:  Signal{Tool: "ws_shell", Language: "go", Message: "--- FAIL: TestRunnerWave (0.03s)\n    runner_test.go:88: got 2 waves, want 1\nFAIL"},
			want: ClassTestFailure,
		},
		{
			name: "pytest failure",
			sig:  Signal{Tool: "ws_shell", Language: "python", Message: "FAILED tests/test_api.py::test_login - AssertionError: assert 401 == 200"},
			want: ClassTestFailure,
		},
		{
			name: "python import missing",
			sig:  Signal{Tool: "ws_shell", Language: "python", Message: "ModuleNotFoundError: No module named 'httpx'"},
			want: ClassDependency,
		},
		{
			name: "shell command missing",
			sig:  Signal{Tool: "ws_shell", Message: "bash: line 1: pnpm: command not found"},
			want: ClassDependency,
		},
		{
			name: "http 429",
			sig:  Signal{Message: "429 Too Many Requests: rate limit reached for gpt-4o-mini"},
			want: ClassRateLimit,
		},
		{
			name: "http 503",
			sig:  Signal{Message: "POST /v1/chat/completions: 503 Service Unavailable"},
			want: ClassProviderError,
		},
		{
			name: "timeout",
			sig:  Signal{Message: "Post \"http://127.0.0.1:11434/v1/chat/completions\": context deadline exceeded (Client.Timeout exceeded)"},
			want: ClassTimeout,
		},
		{
			name: "loop detected",
			sig:  Signal{Tool: "ws_read", Message: "Repeated identical tool call refused — you already called this tool with the same arguments."},
			want: ClassNoProgress,
		},
		{
			name: "permission denied",
			sig:  Signal{Tool: "ws_shell", Message: "command is not allowed by the current permission mode: rm -rf /"},
			want: ClassPermissionDenied,
		},
		{
			name: "file not found",
			sig:  Signal{Tool: "ws_read", Message: "open pkg/nope/x.go: no such file or directory"},
			want: ClassFileNotFound,
		},
		{
			name: "review rejection",
			sig:  Signal{Message: "task T3 failed: max retries exceeded (review rejected 3 times)"},
			want: ClassReviewRejected,
		},
		{
			name: "empty message",
			sig:  Signal{Tool: "ws_edit"},
			want: ClassUnknown,
		},
		{
			name: "gibberish",
			sig:  Signal{Message: "the flux capacitor is misaligned"},
			want: ClassUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.sig); got != tc.want {
				t.Errorf("Classify = %q, want %q\n  message: %s", got, tc.want, tc.sig.Message)
			}
		})
	}
}

// The core contract: superficially different messages describing the same
// underlying problem must collapse to one fingerprint.
func TestFingerprintCollapsesEquivalentFailures(t *testing.T) {
	groups := []struct {
		name    string
		signals []Signal
	}{
		{
			name: "old_str not found, different files and text",
			signals: []Signal{
				{Tool: "ws_edit", Language: "go", Model: "qwen2.5-coder:14b", Message: "old_str not found in pkg/loop/runner.go."},
				{Tool: "ws_edit", Language: "go", Model: "qwen2.5-coder:14b", Message: "old_str not found in cmd/slmcode/root.go.\nClosest text: func main() {"},
				{Tool: "ws_edit", Language: "golang", Model: "qwen2.5-coder-14b-instruct", Message: "OLD_STR NOT FOUND in a/b/c.go at 2026-01-02T03:04:05Z"},
			},
		},
		{
			name: "ambiguous match, different counts",
			signals: []Signal{
				{Tool: "ws_edit", Language: "go", Message: "old_str found 3 times in x.go — pass replace_all:true"},
				{Tool: "ws_edit", Language: "go", Message: "old_str found 17 times in totally/other.go — pass replace_all:true"},
			},
		},
		{
			name: "context overflow, different numbers and providers",
			signals: []Signal{
				{Language: "go", Message: "This model's maximum context length is 32768 tokens, however you requested 41022 tokens."},
				{Language: "go", Message: "maximum context length is 8192 tokens, however you requested 9001 tokens (took 3.2s)"},
			},
		},
		{
			name: "go compile error, same cause different file/line",
			signals: []Signal{
				{Tool: "ws_shell", Language: "go", Message: "./pkg/a/b.go:412:9: undefined: waveTimeout"},
				{Tool: "ws_shell", Language: "go", Message: "./cmd/x/main.go:7:2: undefined: waveTimeout"},
			},
		},
		{
			name: "pytest assertion, different tests and values",
			signals: []Signal{
				{Tool: "ws_shell", Language: "python", Message: "FAILED tests/test_api.py::test_login - AssertionError: assert 401 == 200"},
				{Tool: "ws_shell", Language: "python", Message: "FAILED tests/other/test_x.py::test_thing - AssertionError: assert 12 == 13"},
			},
		},
		{
			name: "provider timeout at different addresses",
			signals: []Signal{
				{Message: `Post "http://127.0.0.1:11434/v1/chat": context deadline exceeded (took 30s)`},
				{Message: `Post "http://10.0.0.4:8080/v1/chat": context deadline exceeded (took 120.5s)`},
			},
		},
		{
			name: "truncated json with different byte offsets",
			signals: []Signal{
				{Tool: "ws_edit", Message: "failed to parse tool arguments: unexpected end of JSON input at byte 2048"},
				{Tool: "ws_edit", Message: "failed to parse tool arguments: unexpected end of JSON input at byte 4096"},
			},
		},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			first := Analyze(g.signals[0])
			if first.Zero() {
				t.Fatalf("no fingerprint produced for %q", g.signals[0].Message)
			}
			for i, sig := range g.signals[1:] {
				got := Analyze(sig)
				if got.ID != first.ID {
					t.Errorf("signal %d fingerprinted as %s (class %s, salient %q), want %s (class %s, salient %q)",
						i+1, got.ID, got.Class, got.Salient, first.ID, first.Class, first.Salient)
				}
			}
		})
	}
}

// The mirror-image contract: genuinely different problems must NOT collapse.
func TestFingerprintSeparatesDifferentFailures(t *testing.T) {
	distinct := []Signal{
		{Tool: "ws_edit", Language: "go", Message: "old_str not found in a.go"},
		{Tool: "ws_edit", Language: "go", Message: "old_str found 3 times in a.go"},
		{Tool: "ws_edit", Language: "go", Message: "File must be read first before edit — a.go"},
		{Tool: "ws_shell", Language: "go", Message: "./a.go:1:1: undefined: alpha"},
		{Tool: "ws_shell", Language: "go", Message: "./a.go:1:1: undefined: beta"},
		{Tool: "ws_shell", Language: "python", Message: "ModuleNotFoundError: No module named 'httpx'"},
		{Message: "429 Too Many Requests"},
		{Message: "maximum context length is 32768 tokens"},
	}
	seen := map[string]int{}
	for i, sig := range distinct {
		fp := Analyze(sig)
		if prev, dup := seen[fp.ID]; dup {
			t.Errorf("signals %d and %d collapsed to %s:\n  %q\n  %q",
				prev, i, fp.ID, distinct[prev].Message, sig.Message)
		}
		seen[fp.ID] = i
	}
}

// Language and model family are part of identity: the same message on a
// different stack is a different problem to repair.
func TestFingerprintNamespacedByLanguageAndModel(t *testing.T) {
	base := Signal{Tool: "ws_shell", Message: "./a.go:1:1: undefined: alpha", Language: "go", Model: "qwen2.5-coder:14b"}
	otherLang := base
	otherLang.Language = "python"
	otherModel := base
	otherModel.Model = "gpt-4o-mini"

	a, b, c := Analyze(base), Analyze(otherLang), Analyze(otherModel)
	if a.ID == b.ID {
		t.Error("language is not part of the fingerprint")
	}
	if a.ID == c.ID {
		t.Error("model family is not part of the fingerprint")
	}
	if a.ModelFamily != "qwen2.5-coder" {
		t.Errorf("model family = %q", a.ModelFamily)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		contains []string
		absent   []string
	}{
		{
			name:     "paths and line numbers",
			in:       "./pkg/loop/runner.go:412:9: undefined: waveTimeout",
			contains: []string{"undefined: wavetimeout", "<path>"},
			absent:   []string{"412", "runner.go:412"},
		},
		{
			name:     "timestamps and durations",
			in:       "2026-01-02T03:04:05Z request failed after 12.5s",
			contains: []string{"<ts>", "<dur>"},
			absent:   []string{"2026", "12.5s"},
		},
		{
			name:     "hex and addresses",
			in:       "panic at 0xc000123456 commit deadbeef1234 on 127.0.0.1:8080",
			contains: []string{"<addr>", "<hash>"},
			absent:   []string{"0xc000123456", "deadbeef1234", "127.0.0.1"},
		},
		{
			name:     "quoted payloads",
			in:       `old_str "func Foo() {\n  return nil\n}" not found`,
			contains: []string{"<str>", "not found"},
			absent:   []string{"func Foo"},
		},
		{
			name:     "stack trace is cut",
			in:       "runtime error: index out of range\ngoroutine 17 [running]:\nmain.main()\n\t/x/y.go:9 +0x1d",
			contains: []string{"index out of range"},
			absent:   []string{"goroutine", "main.main"},
		},
		{
			name:     "ansi colors",
			in:       "\x1b[31mFAILED\x1b[0m tests/test_a.py",
			contains: []string{"failed"},
			absent:   []string{"\x1b["},
		},
		{name: "empty", in: "   ", contains: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("Normalize(%q) = %q, missing %q", tc.in, got, want)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(got, bad) {
					t.Errorf("Normalize(%q) = %q, still contains %q", tc.in, got, bad)
				}
			}
			if len(got) > MaxNormLen {
				t.Errorf("normalized message is %d bytes, cap is %d", len(got), MaxNormLen)
			}
		})
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	inputs := []string{
		"./pkg/a/b.go:12:3: cannot use x (variable of type int) as string value",
		"FAILED tests/test_api.py::test_login - AssertionError: assert 401 == 200",
		"old_str not found in a/b/c.go",
	}
	for _, in := range inputs {
		once := Normalize(in)
		twice := Normalize(once)
		if once != twice {
			t.Errorf("Normalize not idempotent:\n  once:  %q\n  twice: %q", once, twice)
		}
	}
}

func TestAnalyzeEmptySignal(t *testing.T) {
	fp := Analyze(Signal{})
	if !fp.Zero() {
		t.Errorf("empty signal produced fingerprint %+v", fp)
	}
	if fp.Class != ClassUnknown {
		t.Errorf("class = %q", fp.Class)
	}
}

func TestDescribeCoversEveryClass(t *testing.T) {
	for _, c := range AllClasses {
		if d := Describe(c); d == "" {
			t.Errorf("class %q has no description", c)
		}
	}
}
