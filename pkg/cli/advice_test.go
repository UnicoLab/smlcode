package cli

import "testing"

// The engine writes one event stream for three consumers and phrases its advice
// for the richest one. A `slmcode run` user must never be told to do something
// this binary cannot do.
func TestTranslateEngineAdviceRemovesStudioOnlyRemedies(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring that must appear
		gone string // substring that must NOT appear
	}{
		{
			name: "escalate advice",
			in:   "T1 needs human review — decide in Studio (or wait for timeout)",
			want: "slmcode task show",
			gone: "Studio",
		},
		{
			name: "resume slash command",
			in:   "interrupted at execute — board saved; /resume run-17875",
			want: "slmcode session resume",
			gone: "; /resume ",
		},
		{
			name: "task note",
			in:   "AWAITING: re-scope / precise fix in Studio",
			want: "slmcode task show",
			gone: "Studio",
		},
		{
			name: "result summary",
			in:   "done (escalated tasks need human review in Studio)",
			want: "slmcode task show",
			gone: "Studio",
		},
		{
			name: "quality monitor",
			in:   "task escalated — needs human review or precise fix in Studio",
			want: "slmcode task show",
			gone: "Studio",
		},
		{
			name: "wrong command name",
			in:   `unknown specialist "x" — use: slmcode agents / Studio → Agents`,
			want: "slmcode agent ",
			gone: "slmcode agents",
		},
		{
			name: "api advice still stripped",
			in:   "approve plan? 1 tasks — POST /api/plan/approve",
			want: "approve plan? 1 tasks",
			gone: "/api/",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TranslateEngineAdvice(c.in)
			if !contains(got, c.want) {
				t.Errorf("TranslateEngineAdvice(%q) = %q, want it to contain %q", c.in, got, c.want)
			}
			if c.gone != "" && contains(got, c.gone) {
				t.Errorf("TranslateEngineAdvice(%q) = %q, still contains %q", c.in, got, c.gone)
			}
		})
	}
}

func TestTranslateEngineAdviceLeavesOrdinaryTextAlone(t *testing.T) {
	for _, msg := range []string{
		"worker finished T1",
		"go test ./... -race passed",
		"",
	} {
		if got := TranslateEngineAdvice(msg); got != msg {
			t.Errorf("TranslateEngineAdvice(%q) = %q, want it unchanged", msg, got)
		}
	}
}

// The /resume rewrite opens a backtick that the run id has to close.
func TestCloseResumeQuoteBalancesTheBackticks(t *testing.T) {
	got := TranslateEngineAdvice("interrupted at execute — board saved; /resume run-17875 (2 tasks open)")
	if n := countByte(got, '`'); n%2 != 0 {
		t.Errorf("unbalanced backticks in %q", got)
	}
	if !contains(got, "`slmcode session resume run-17875`") {
		t.Errorf("got %q, want the run id inside the quoted command", got)
	}
}

func TestNormalizeBulletsCollapsesDoubledMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"- - The project is a tiny module", "- The project is a tiny module"},
		{"- The project is a tiny module", "- The project is a tiny module"},
		{"  - - nested", "  - nested"},
		{"- * mixed markers", "- mixed markers"},
		{"- - - three deep", "- three deep"},
		{"no bullet at all", "no bullet at all"},
		{"a - b - c", "a - b - c"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeBullets(c.in); got != c.want {
			t.Errorf("NormalizeBullets(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	multi := "## Facts\n\n- - one\n- two\n"
	if got := NormalizeBullets(multi); got != "## Facts\n\n- one\n- two\n" {
		t.Errorf("NormalizeBullets(multiline) = %q", got)
	}
}

func TestTrimBulletMarker(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"- The project is a tiny module", "The project is a tiny module"},
		{"* starred", "starred"},
		{"plain", "plain"},
		{"-nospace", "-nospace"},
	} {
		if got := TrimBulletMarker(c.in); got != c.want {
			t.Errorf("TrimBulletMarker(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The escalate gate is where most local-SLM runs end; its card has to name a
// command the reader can type, and say what forcing done actually means.
func TestEscalateGateOffersATerminalPath(t *testing.T) {
	g := EscalateGate("ask-1", "T1", "add Divide", "rejected by evidence gate", []string{"calc.go"})
	body := ""
	for _, line := range g.Body {
		body += line + "\n"
	}
	for _, want := range []string{"slmcode task show T1", "overrides the gate", "recorded as your override"} {
		if !contains(body, want) {
			t.Errorf("escalate gate body missing %q:\n%s", want, body)
		}
	}
	if contains(body, "Studio") {
		t.Errorf("escalate gate still mentions Studio:\n%s", body)
	}
}

func contains(hay, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func countByte(s string, b byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			n++
		}
	}
	return n
}
