package workspace

import (
	"strings"
	"testing"
)

// Every one of these was a VERIFIED bypass of the previous guard: the command
// mutated the workspace (or escaped analysis entirely) while IsSafeBash
// reported "safe".
func TestKnownShellBypassesAreBlocked(t *testing.T) {
	cases := []struct {
		name    string
		command string
		reason  string // substring the refusal must contain
	}{
		{"sed in place", `sed -i 's/a/b/' main.py`, "ws_edit"},
		{"sed in place with suffix", `sed -i.bak 's/a/b/' main.py`, "ws_edit"},
		{"cp exfiltration", "cp /etc/passwd ./leak.txt", "ws_edit"},
		{"mv out of tree", "mv important.go /tmp/", "ws_edit"},
		{"python inline write", `python -c "open('x','w').write('boom')"`, "arbitrary code"},
		{"python3 inline write", `python3 -c "print(1)"`, "arbitrary code"},
		{"node eval", `node -e "require('fs').writeFileSync('x','y')"`, "arbitrary code"},
		{"perl system", `perl -e 'system("rm -rf /tmp/z")'`, "arbitrary code"},
		{"ruby", `ruby -e 'puts 1'`, "arbitrary code"},
		{"backtick substitution", "echo `rm -rf /tmp/z`", "command substitution"},
		{"dollar substitution", "ls $(rm -rf /tmp/z)", "command substitution"},
		{"test with substitution", "[[ $(rm -rf /tmp/z) ]]", "command substitution"},
		{"process substitution in", "diff <(cat a) <(cat b)", "command substitution"},
		{"process substitution out", "tee >(cat) < a", "command substitution"},
		{"bare ampersand chain", "ls & rm -rf /tmp/x", "&"},
		{"npx", "npx some-tool", "arbitrary code"},
		{"make install", "make install", "arbitrary code"},
		{"go run", "go run ./cmd/tool", "arbitrary code"},
		{"cargo run", "cargo run", "arbitrary code"},
		{"rm", "rm -rf build", "ws_edit"},
		{"truncate", "truncate -s 0 main.go", "ws_edit"},
		{"install", "install -m644 a.go b.go", "ws_edit"},
		{"rsync", "rsync -a src/ dst/", "ws_edit"},
		{"tee append", "echo x | tee -a main.py", "ws_write"},
		{"chained rm after safe", "go test ./... && rm -rf pkg", "ws_edit"},
		{"eval", "eval 'rm -rf /tmp/x'", "arbitrary code"},
		{"bash -c nested", `bash -c 'rm -rf /tmp/x'`, "arbitrary code"},
		{"xargs", "ls | xargs rm", "arbitrary code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsSafeBash(tc.command, BuiltinSafePrefixes) {
				t.Fatalf("IsSafeBash must reject %q", tc.command)
			}
			msg, blocked := GuardShellWhitelist(tc.command, nil)
			if !blocked {
				t.Fatalf("GuardShellWhitelist must block %q", tc.command)
			}
			if !strings.Contains(msg, tc.reason) {
				t.Fatalf("refusal for %q should mention %q, got:\n%s", tc.command, tc.reason, msg)
			}
		})
	}
}

func TestSafeCommandsStillPass(t *testing.T) {
	cases := []string{
		"go test ./pkg -short",
		"go vet ./...",
		"go build ./...",
		"gofmt -l .",
		"python -m pytest -q",
		"python -m py_compile main.py",
		"node --check app.js",
		"npm test",
		"cargo test",
		"ls && pwd",
		"git status --short",
		"grep -rn TODO pkg/",
		"echo hi 2>/dev/null",
		"cat go.mod | head -5",
		"go test ./pkg/foo -run TestBar -short",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			if !IsSafeBash(cmd, BuiltinSafePrefixes) {
				t.Fatalf("%q must remain allowed", cmd)
			}
			if _, blocked := GuardShellWhitelist(cmd, nil); blocked {
				t.Fatalf("%q must not be blocked", cmd)
			}
		})
	}
}

func TestExplicitAllowListUnlocksExecutors(t *testing.T) {
	cmd := "python script.py"
	if _, blocked := GuardShellWhitelist(cmd, nil); !blocked {
		t.Fatal("executors must be blocked by default")
	}
	if _, blocked := GuardShellWhitelist(cmd, []string{"python "}); blocked {
		t.Fatal("an explicit allow entry must unlock the executor")
	}
}

func TestUnsafeShellSyntax(t *testing.T) {
	cases := []struct {
		name    string
		command string
		unsafe  bool
	}{
		{"dollar paren", "echo $(id)", true},
		{"backtick", "echo `id`", true},
		{"process substitution", "cat <(id)", true},
		{"process substitution out", "cat >(id)", true},
		{"bare ampersand", "sleep 1 & echo hi", true},
		{"logical and", "a && b", false},
		{"pipe", "a | b", false},
		{"fd dup 2>&1", "go test ./... 2>&1", false},
		{"fd dup &>", "go test ./... &>out.log", false},
		{"single-quoted dollar paren is literal", `echo '$(id)'`, false},
		{"double-quoted dollar paren still expands", `echo "$(id)"`, true},
		{"heredoc body ignored", "cat <<'EOF'\n$(id)\nEOF", false},
		{"plain", "go test ./...", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, unsafe := UnsafeShellSyntax(tc.command)
			if unsafe != tc.unsafe {
				t.Fatalf("UnsafeShellSyntax(%q)=%v want %v (%s)", tc.command, unsafe, tc.unsafe, reason)
			}
			if unsafe && reason == "" {
				t.Fatal("unsafe syntax must come with an explanation")
			}
		})
	}
}

func TestSplitCommandChainHandlesBareAmpersand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"bare ampersand splits", "ls & rm -rf x", []string{"ls", "rm -rf x"}},
		{"logical and does not double-split", "ls && pwd", []string{"ls", "pwd"}},
		{"fd dup stays together", "go test ./... 2>&1", []string{"go test ./... 2>&1"}},
		{"pipe", "cat a | grep b", []string{"cat a", "grep b"}},
		{"semicolon", "a; b", []string{"a", "b"}},
		{"quoted ampersand is literal", `echo "a & b"`, []string{`echo "a & b"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitCommandChain(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %q want %q", got, tc.want)
				}
			}
		})
	}
}

func TestDetectWriteTargetsMutatingCommands(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"redirect", "echo x > a.txt", []string{"a.txt"}},
		{"append", "echo x >> a.txt", []string{"a.txt"}},
		{"tee", "echo x | tee out.txt", []string{"out.txt"}},
		{"tee append", "echo x | tee -a out.txt", []string{"out.txt"}},
		{"dd", "dd if=/dev/zero of=blob.bin", []string{"blob.bin"}},
		{"sed in place", "sed -i 's/a/b/' main.py", []string{"main.py"}},
		{"sed in place suffix", "sed -i.bak 's/a/b/' main.py", []string{"main.py"}},
		{"sed in place with -e", "sed -i -e s/a/b/ main.py", []string{"main.py"}},
		{"sed without -i is a read", "sed 's/a/b/' main.py", nil},
		{"cp destination", "cp a.txt b.txt", []string{"b.txt"}},
		{"mv destination", "mv a.go /tmp/a.go", []string{"/tmp/a.go"}},
		{"install", "install -m644 a b", []string{"b"}},
		{"truncate", "truncate -s 0 main.go", []string{"main.go"}},
		{"rsync", "rsync -a src/ dst/", []string{"dst/"}},
		{"ln", "ln -s a b", []string{"b"}},
		{"dev null ignored", "echo x 2>/dev/null", nil},
		{"leading assignment", "FOO=1 cp a b", []string{"b"}},
		{"plain read", "cat a.txt", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectWriteTargets(tc.command)
			if len(got) != len(tc.want) {
				t.Fatalf("got %#v want %v", got, tc.want)
			}
			for i := range got {
				if got[i].Path != tc.want[i] {
					t.Fatalf("got %#v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestGuardShellWritesBlocksSubstitution(t *testing.T) {
	root := t.TempDir()
	err := GuardShellWrites(root, "echo $(cat /etc/passwd) > /dev/null")
	if err == nil {
		t.Fatal("substitution must be refused before redirect analysis")
	}
	if !strings.Contains(err.Error(), "command substitution") {
		t.Fatalf("got %v", err)
	}
}

func TestClassifySegment(t *testing.T) {
	cases := []struct{ seg, want string }{
		{"python -c x", "executor"},
		{"/usr/bin/python3 x", "executor"},
		{"make install", "executor"},
		{"sed -i s/a/b/ f", "mutator"},
		{"cp a b", "mutator"},
		{"git checkout .", "mutator"},
		{"go test ./...", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.seg, func(t *testing.T) {
			if got := ClassifySegment(tc.seg); got != tc.want {
				t.Fatalf("ClassifySegment(%q)=%s want %s", tc.seg, got, tc.want)
			}
		})
	}
}
