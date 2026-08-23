package compact

import (
	"strings"
	"testing"
)

func TestBuildDigestExtractsMustPreserveState(t *testing.T) {
	msgs := []ChatMsg{
		{Role: RoleUser, Content: "add a flag"},
		asst("", ToolCallRef{ID: "1", Name: "ws_read", Arguments: `{"path":"pkg/cli/flags.go"}`}),
		tool("1", "ws_read", "package cli"),
		asst("", ToolCallRef{ID: "2", Name: "ws_edit", Arguments: `{"path":"pkg/cli/flags.go","old_str":"a"}`}),
		tool("2", "ws_edit", `{"error":"old_str did not match"}`),
		asst("I will retry with more context."),
		asst("", ToolCallRef{ID: "3", Name: "ws_shell", Arguments: `{"command":"go test ./..."}`}),
		tool("3", "ws_shell", "FAIL\nexit status 1"),
		asst("", ToolCallRef{ID: "4", Name: "ws_write", Arguments: `{"path":"pkg/cli/new.go"}`}),
		tool("4", "ws_write", "ok"),
		asst("Done, tests pass."),
	}
	d := BuildDigest(msgs)

	if !contains(strings.Join(d.FilesRead, ","), "pkg/cli/flags.go") {
		t.Fatalf("files read=%v", d.FilesRead)
	}
	if len(d.FilesEdited) != 2 {
		t.Fatalf("files edited=%v", d.FilesEdited)
	}
	if len(d.Commands) != 1 || d.Commands[0].Command != "go test ./..." {
		t.Fatalf("commands=%+v", d.Commands)
	}
	if d.Commands[0].Status != "exit 1" {
		t.Fatalf("status=%q", d.Commands[0].Status)
	}
	if len(d.Failures) < 2 {
		t.Fatalf("failures=%+v", d.Failures)
	}
	if d.Failures[0].Tool != "ws_edit" || d.Failures[0].Path != "pkg/cli/flags.go" {
		t.Fatalf("failure[0]=%+v", d.Failures[0])
	}
	if len(d.Decisions) != 2 {
		t.Fatalf("decisions=%v", d.Decisions)
	}
	if d.Empty() {
		t.Fatal("digest should not be empty")
	}

	out := d.Render(DefaultDigestBytes)
	for _, want := range []string{
		"Files read:", "Files edited:", "Commands run + exit status:",
		"Failed tool calls:", "Decisions:", "pkg/cli/flags.go", "go test ./...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	if len(out) > DefaultDigestBytes {
		t.Fatalf("render %d bytes over budget", len(out))
	}
}

func TestDigestRenderBudget(t *testing.T) {
	d := MustPreserve{}
	for i := 0; i < 50; i++ {
		d.Decisions = append(d.Decisions, strings.Repeat("x", 150))
	}
	tests := []int{0, 100, 400, 1200, 4000}
	for _, budget := range tests {
		out := d.Render(budget)
		want := budget
		if want <= 0 {
			want = DefaultDigestBytes
		}
		if len(out) > want {
			t.Fatalf("budget %d produced %d bytes", budget, len(out))
		}
	}
}

func TestDigestEmptyAndFallback(t *testing.T) {
	msgs := []ChatMsg{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleUser, Content: "world"},
	}
	d := BuildDigest(msgs)
	if !d.Empty() {
		t.Fatalf("expected empty digest, got %+v", d)
	}
	fb := DigestOrFallback(msgs, DefaultDigestBytes)
	if !strings.Contains(fb, "compacted 2 earlier messages") {
		t.Fatalf("fallback=%q", fb)
	}
}

func TestDigestExtraSection(t *testing.T) {
	d := MustPreserve{Extra: map[string][]string{"Open questions": {"is X needed?"}}}
	out := d.Render(500)
	if !strings.Contains(out, "Open questions:") || !strings.Contains(out, "is X needed?") {
		t.Fatalf("extra lost:\n%s", out)
	}
	if d.Empty() {
		t.Fatal("Extra should count as content")
	}
}

func TestArgPathVariants(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"json path", `{"path":"a/b.go"}`, "a/b.go"},
		{"json file_path", `{"file_path":"c.go"}`, "c.go"},
		{"json other", `{"query":"x"}`, ""},
		{"raw", `path="d/e.go", old="x"`, "d/e.go"},
		{"empty", "", ""},
		{"garbage", "!!!", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := argPath(tc.args); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestToolResultFailure(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"json error", `{"error":"boom"}`, true},
		{"plain error", "Error: no such file", true},
		{"panic", "panic: nil map", true},
		{"ok", "wrote 12 lines", false},
		{"empty", "  ", false},
		{"json ok false", `{"ok":false}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := toolResultFailure(tc.content)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestExitStatus(t *testing.T) {
	tests := []struct{ in, want string }{
		{"exit status 0", "ok"},
		{"exit status 2", "exit 2"},
		{"exit code 1", "exit 1"},
		{"all good", "ok"},
		{"panic: boom", "failed"},
	}
	for _, tc := range tests {
		if got := exitStatus(tc.in); got != tc.want {
			t.Errorf("exitStatus(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
